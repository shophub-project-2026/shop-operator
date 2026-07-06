package controller

import (
	"context"
	"fmt"
	"math/big"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/devops-milos/shop-operator/api/v1alpha1"
)

// stubBalances is a BalanceFetcher test double.
type stubBalances struct {
	wei *big.Int
	err error
}

func (s *stubBalances) Balance(_ context.Context, _ string) (*big.Int, error) {
	return s.wei, s.err
}

func newWalletReconciler(t *testing.T, balances BalanceFetcher, objs ...*v1alpha1.Wallet) *WalletReconciler {
	t.Helper()
	scheme := testScheme(t)
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1 to scheme: %v", err)
	}
	builder := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.Wallet{})
	for _, o := range objs {
		builder = builder.WithObjects(o)
	}
	return &WalletReconciler{
		Client:   builder.Build(),
		Scheme:   scheme,
		Balances: balances,
	}
}

func reconcileWallet(t *testing.T, r *WalletReconciler, name string) {
	t.Helper()
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

func getWallet(t *testing.T, r *WalletReconciler, name string) *v1alpha1.Wallet {
	t.Helper()
	w := &v1alpha1.Wallet{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, w); err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	return w
}

func TestWallet_AdoptsExistingAddress(t *testing.T) {
	const addr = "0x742d35Cc6634C0532925a3b844Bc9e7595f42e00"
	r := newWalletReconciler(t, &stubBalances{wei: big.NewInt(2e18)}, &v1alpha1.Wallet{
		ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: "default"},
		Spec:       v1alpha1.WalletSpec{Address: addr, Blockchain: "ethereum"},
	})

	reconcileWallet(t, r, "w")

	w := getWallet(t, r, "w")
	if w.Status.Status != "Ready" {
		t.Fatalf("status = %q (%s), want Ready", w.Status.Status, w.Status.Message)
	}
	if w.Status.Address != addr {
		t.Errorf("status.address = %q, want %q", w.Status.Address, addr)
	}
	if w.Status.Balance != "2.000000 ETH" {
		t.Errorf("status.balance = %q, want 2.000000 ETH", w.Status.Balance)
	}

	// No keys Secret for adopted accounts — the operator did not create them.
	secret := &corev1.Secret{}
	err := r.Get(context.Background(), types.NamespacedName{Name: walletKeysSecret("w"), Namespace: "default"}, secret)
	if err == nil {
		t.Error("keys secret should not exist for an adopted address")
	}
}

func TestWallet_RejectsInvalidEthereumAddress(t *testing.T) {
	r := newWalletReconciler(t, nil, &v1alpha1.Wallet{
		ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: "default"},
		Spec:       v1alpha1.WalletSpec{Address: "not-an-address", Blockchain: "ethereum"},
	})

	reconcileWallet(t, r, "w")

	if w := getWallet(t, r, "w"); w.Status.Status != "Error" {
		t.Fatalf("status = %q, want Error", w.Status.Status)
	}
}

func TestWallet_CreatesAccountWhenAddressEmpty(t *testing.T) {
	r := newWalletReconciler(t, nil, &v1alpha1.Wallet{
		ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: "default"},
		Spec:       v1alpha1.WalletSpec{Blockchain: "ethereum", Network: "sepolia"},
	})

	reconcileWallet(t, r, "w")

	w := getWallet(t, r, "w")
	if w.Status.Status != "Ready" {
		t.Fatalf("status = %q (%s), want Ready", w.Status.Status, w.Status.Message)
	}
	if !ethAddressRE.MatchString(w.Status.Address) {
		t.Fatalf("status.address = %q is not a valid Ethereum address", w.Status.Address)
	}

	secret := &corev1.Secret{}
	if err := r.Get(context.Background(), types.NamespacedName{
		Name: walletKeysSecret("w"), Namespace: "default",
	}, secret); err != nil {
		t.Fatalf("keys secret not created: %v", err)
	}
	if got := string(secret.Data["address"]); got != w.Status.Address {
		t.Errorf("secret address = %q, want %q", got, w.Status.Address)
	}
	if len(secret.Data["private-key"]) != 64 {
		t.Errorf("private-key length = %d hex chars, want 64", len(secret.Data["private-key"]))
	}

	// Second reconcile must reuse the same account, never regenerate the key.
	firstAddr := w.Status.Address
	reconcileWallet(t, r, "w")
	if w = getWallet(t, r, "w"); w.Status.Address != firstAddr {
		t.Errorf("address changed across reconciles: %q -> %q", firstAddr, w.Status.Address)
	}
}

func TestWallet_CreationUnsupportedForOtherChains(t *testing.T) {
	r := newWalletReconciler(t, nil, &v1alpha1.Wallet{
		ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: "default"},
		Spec:       v1alpha1.WalletSpec{Blockchain: "solana"},
	})

	reconcileWallet(t, r, "w")

	if w := getWallet(t, r, "w"); w.Status.Status != "Error" {
		t.Fatalf("status = %q, want Error for unsupported account creation", w.Status.Status)
	}
}

func TestWallet_BalanceErrorKeepsWalletReady(t *testing.T) {
	const addr = "0x742d35Cc6634C0532925a3b844Bc9e7595f42e00"
	r := newWalletReconciler(t, &stubBalances{err: fmt.Errorf("rpc down")}, &v1alpha1.Wallet{
		ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: "default"},
		Spec:       v1alpha1.WalletSpec{Address: addr, Blockchain: "ethereum"},
	})

	reconcileWallet(t, r, "w")

	w := getWallet(t, r, "w")
	if w.Status.Status != "Ready" {
		t.Fatalf("status = %q, want Ready despite RPC failure", w.Status.Status)
	}
}

func TestShop_ReconcileWalletCreatesAndSyncs(t *testing.T) {
	scheme := testScheme(t)
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1 to scheme: %v", err)
	}
	shop := &v1alpha1.Shop{
		ObjectMeta: metav1.ObjectMeta{Name: "boutique", Namespace: "default", UID: "uid-1"},
		Spec: v1alpha1.ShopSpec{
			Availability:  "standard",
			WalletAddress: "0x742d35Cc6634C0532925a3b844Bc9e7595f42e00",
		},
	}
	r := &ShopReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(shop).Build(),
		Scheme: scheme,
	}

	if err := r.reconcileWallet(context.Background(), shop); err != nil {
		t.Fatalf("reconcileWallet: %v", err)
	}

	wallet := &v1alpha1.Wallet{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "boutique", Namespace: "default"}, wallet); err != nil {
		t.Fatalf("wallet not created: %v", err)
	}
	if wallet.Spec.Address != shop.Spec.WalletAddress {
		t.Errorf("wallet address = %q, want %q", wallet.Spec.Address, shop.Spec.WalletAddress)
	}
	if wallet.Spec.Blockchain != "ethereum" || wallet.Spec.Network != "sepolia" {
		t.Errorf("wallet chain/network = %s/%s, want ethereum/sepolia", wallet.Spec.Blockchain, wallet.Spec.Network)
	}
	if len(wallet.OwnerReferences) != 1 || wallet.OwnerReferences[0].Name != "boutique" {
		t.Error("wallet must be owned by the shop for garbage collection")
	}

	// Admin changes the shop's wallet address — the Wallet CR must follow.
	shop.Spec.WalletAddress = "0x0000000000000000000000000000000000000001"
	if err := r.reconcileWallet(context.Background(), shop); err != nil {
		t.Fatalf("reconcileWallet after update: %v", err)
	}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "boutique", Namespace: "default"}, wallet); err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	if wallet.Spec.Address != shop.Spec.WalletAddress {
		t.Errorf("wallet address not synced: %q", wallet.Spec.Address)
	}
}

func TestToEIP55_KnownVectors(t *testing.T) {
	// Official EIP-55 test vectors.
	vectors := map[string]string{
		"0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed": "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
		"0xfb6916095ca1df60bb79ce92ce3ea74c37c5d359": "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359",
		"0xdbf03b407c01e7cd3cbea99509d93f8dddc8c6fb": "0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB",
	}
	for in, want := range vectors {
		if got := toEIP55(in); got != want {
			t.Errorf("toEIP55(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestGenerateEthAccount_ProducesChecksummedAddress(t *testing.T) {
	a, err := generateEthAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !ethAddressRE.MatchString(a.Address) {
		t.Errorf("address %q is not a valid Ethereum address", a.Address)
	}
	if toEIP55(a.Address) != a.Address {
		t.Errorf("address %q is not EIP-55 checksummed", a.Address)
	}
	if len(a.PrivateKey) != 64 {
		t.Errorf("private key hex length = %d, want 64", len(a.PrivateKey))
	}

	b, err := generateEthAccount()
	if err != nil {
		t.Fatalf("generate second: %v", err)
	}
	if a.Address == b.Address {
		t.Error("two generated accounts must not collide")
	}
}
