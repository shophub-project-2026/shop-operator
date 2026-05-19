package controller

import (
	"context"
	"fmt"
	"regexp"

	"k8s.io/apimachinery/pkg/api/errors"
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

type WalletReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *WalletReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Wallet{}).
		Complete(r)
}

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

	if err := r.validateWallet(wallet); err != nil {
		logger.Error(err, "wallet validation failed")
		return ctrl.Result{}, r.setWalletStatus(ctx, wallet, "Error", err.Error(), "")
	}

	msg := fmt.Sprintf("wallet %s on %s/%s is configured",
		wallet.Spec.Address, wallet.Spec.Blockchain, wallet.Spec.Network)
	return ctrl.Result{}, r.setWalletStatus(ctx, wallet, "Ready", msg, "0")
}

func (r *WalletReconciler) validateWallet(wallet *v1alpha1.Wallet) error {
	if wallet.Spec.Address == "" {
		return fmt.Errorf("spec.address must not be empty")
	}
	if wallet.Spec.Blockchain == "" {
		return fmt.Errorf("spec.blockchain must not be empty")
	}

	switch wallet.Spec.Blockchain {
	case "ethereum":
		if !ethAddressRE.MatchString(wallet.Spec.Address) {
			return fmt.Errorf("address %q is not a valid Ethereum address", wallet.Spec.Address)
		}
	case "solana":
		if !solanaAddressRE.MatchString(wallet.Spec.Address) {
			return fmt.Errorf("address %q is not a valid Solana address", wallet.Spec.Address)
		}
	default:
		// Accept unknown blockchains without address validation.
	}

	return nil
}

func (r *WalletReconciler) setWalletStatus(
	ctx context.Context,
	wallet *v1alpha1.Wallet,
	status, message, balance string,
) error {
	latest := &v1alpha1.Wallet{}
	if err := r.Get(ctx, types.NamespacedName{Name: wallet.Name, Namespace: wallet.Namespace}, latest); err != nil {
		return err
	}
	latest.Status.Status = status
	latest.Status.Message = message
	latest.Status.Balance = balance
	return r.Status().Update(ctx, latest)
}
