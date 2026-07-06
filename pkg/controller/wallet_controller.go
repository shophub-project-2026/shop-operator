package controller

import (
	"context"
	"fmt"
	"regexp"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/devops-milos/shop-operator/api/v1alpha1"
)

// ethAddressRE matches a hex Ethereum address with optional 0x prefix.
var ethAddressRE = regexp.MustCompile(`(?i)^(0x)?[0-9a-f]{40}$`)

// solanaAddressRE matches a base58-encoded Solana public key (32–44 chars).
var solanaAddressRE = regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{32,44}$`)

// balanceRefreshInterval is how often a Ready wallet re-reads its on-chain
// balance so status.balance stays current without hammering the RPC endpoint.
const balanceRefreshInterval = 10 * time.Minute

// walletKeysSecret names the Secret holding a generated account's keypair.
func walletKeysSecret(wallet string) string { return wallet + "-keys" }

type WalletReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Balances reads on-chain balances. May be nil (e.g. in unit tests or when
	// no RPC endpoint is configured); balance reporting is then skipped.
	Balances BalanceFetcher
}

func (r *WalletReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Wallet{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}

// Reconcile implements spec §3.1 — "Wallet: Kreira account na blockchain-u na
// kojem korisnici vrše uplatu":
//   - spec.address empty  → CREATE an account: generate a secp256k1 keypair,
//     persist it in the Secret <name>-keys and publish the derived address.
//   - spec.address set    → adopt the existing account after validating it.
//
// In both cases the resolved account lands in status.address and, when an RPC
// endpoint is configured, its live balance in status.balance.
func (r *WalletReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	wallet := &v1alpha1.Wallet{}
	if err := r.Get(ctx, req.NamespacedName, wallet); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("Reconciling Wallet",
		"name", wallet.Name,
		"blockchain", wallet.Spec.Blockchain,
		"network", wallet.Spec.Network,
	)

	address, created, err := r.resolveAccount(ctx, wallet)
	if err != nil {
		logger.Error(err, "resolve wallet account")
		return ctrl.Result{}, r.setWalletStatus(ctx, wallet, "Error", err.Error(), "", "")
	}

	verb := "adopted existing"
	if created {
		verb = "created"
	}
	msg := fmt.Sprintf("%s account %s on %s/%s",
		verb, address, wallet.Spec.Blockchain, networkOrDefault(wallet))

	balance := ""
	if r.Balances != nil && wallet.Spec.Blockchain == "ethereum" {
		wei, balErr := r.Balances.Balance(ctx, address)
		if balErr != nil {
			// A flaky RPC endpoint must not flip a valid wallet to Error; keep
			// the wallet Ready and retry on the next periodic refresh.
			logger.Info("balance fetch failed, will retry", "error", balErr.Error())
			msg += " (balance unavailable: RPC error)"
		} else {
			balance = formatEth(wei)
		}
	}

	if err := r.setWalletStatus(ctx, wallet, "Ready", msg, balance, address); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: balanceRefreshInterval}, nil
}

// resolveAccount returns the account address for the wallet, creating a new
// on-chain account (keypair + Secret) when spec.address is empty. The bool
// reports whether this reconciler created the account.
func (r *WalletReconciler) resolveAccount(ctx context.Context, wallet *v1alpha1.Wallet) (string, bool, error) {
	if wallet.Spec.Blockchain == "" {
		return "", false, fmt.Errorf("spec.blockchain must not be empty")
	}

	if wallet.Spec.Address != "" {
		if err := validateAddress(wallet.Spec.Blockchain, wallet.Spec.Address); err != nil {
			return "", false, err
		}
		return wallet.Spec.Address, false, nil
	}

	if wallet.Spec.Blockchain != "ethereum" {
		return "", false, fmt.Errorf(
			"account creation is only supported for blockchain \"ethereum\"; provide spec.address for %q",
			wallet.Spec.Blockchain)
	}
	return r.ensureEthAccount(ctx, wallet)
}

// ensureEthAccount creates the account keypair Secret on first reconcile and
// re-reads it afterwards, so the account survives operator restarts and the
// key is never regenerated for an existing wallet.
func (r *WalletReconciler) ensureEthAccount(ctx context.Context, wallet *v1alpha1.Wallet) (string, bool, error) {
	secretName := walletKeysSecret(wallet.Name)

	existing := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: wallet.Namespace}, existing)
	if err == nil {
		address := string(existing.Data["address"])
		if address == "" {
			return "", false, fmt.Errorf("keys secret %s exists but has no address key", secretName)
		}
		return address, false, nil
	}
	if !errors.IsNotFound(err) {
		return "", false, err
	}

	account, err := generateEthAccount()
	if err != nil {
		return "", false, fmt.Errorf("create ethereum account: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: wallet.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "shop-operator",
				"shop.devops.io/wallet":        wallet.Name,
			},
		},
		Type: corev1.SecretTypeOpaque,
		// Data (not StringData) so reads see identical bytes on every client,
		// including the fake client in unit tests which skips API-server
		// StringData conversion.
		Data: map[string][]byte{
			"address":     []byte(account.Address),
			"private-key": []byte(account.PrivateKey),
		},
	}
	if err := ctrl.SetControllerReference(wallet, secret, r.Scheme); err != nil {
		return "", false, fmt.Errorf("set owner ref on keys secret: %w", err)
	}
	if err := r.Create(ctx, secret); err != nil {
		return "", false, fmt.Errorf("create keys secret: %w", err)
	}
	return account.Address, true, nil
}

func validateAddress(blockchain, address string) error {
	switch blockchain {
	case "ethereum":
		if !ethAddressRE.MatchString(address) {
			return fmt.Errorf("address %q is not a valid Ethereum address", address)
		}
	case "solana":
		if !solanaAddressRE.MatchString(address) {
			return fmt.Errorf("address %q is not a valid Solana address", address)
		}
	default:
		// Accept unknown blockchains without address validation.
	}
	return nil
}

func networkOrDefault(wallet *v1alpha1.Wallet) string {
	if wallet.Spec.Network != "" {
		return wallet.Spec.Network
	}
	return "sepolia"
}

func (r *WalletReconciler) setWalletStatus(
	ctx context.Context,
	wallet *v1alpha1.Wallet,
	status, message, balance, address string,
) error {
	latest := &v1alpha1.Wallet{}
	if err := r.Get(ctx, types.NamespacedName{Name: wallet.Name, Namespace: wallet.Namespace}, latest); err != nil {
		return err
	}
	latest.Status.Status = status
	latest.Status.Message = message
	latest.Status.Address = address
	// Keep the last known balance when a refresh temporarily fails.
	if balance != "" || status != "Ready" {
		latest.Status.Balance = balance
	}
	return r.Status().Update(ctx, latest)
}
