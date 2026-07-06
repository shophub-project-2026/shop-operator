//go:build integration

// Package integration hosts the operator's end-to-end tests. The required
// infrastructure — a real Kubernetes API server — is provisioned with
// Testcontainers (k3s module), satisfying spec §5.2: integration tests whose
// infrastructure is created via Testcontainers.
package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/k3s"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/yaml"

	"github.com/devops-milos/shop-operator/api/v1alpha1"
	"github.com/devops-milos/shop-operator/pkg/controller"
)

const (
	k3sImage    = "rancher/k3s:v1.27.9-k3s1"
	testNS      = "default"
	waitTimeout = 90 * time.Second
	pollEvery   = 500 * time.Millisecond
)

var cnpgClusterGVK = schema.GroupVersionKind{
	Group: "postgresql.cnpg.io", Version: "v1", Kind: "Cluster",
}

// TestOperator_EndToEnd runs the operator's controllers against a real API
// server (k3s in a container) and verifies the §3.1 contract:
//   - Shop availability=standard → Deployment with 2 replicas,
//   - availability=high → 3 replicas,
//   - per-shop Service, Ingress, dashboard ConfigMap, PrometheusRule,
//     DiscordChannel and Wallet are provisioned,
//   - Wallet without spec.address gets a NEW on-chain account (keypair Secret).
func TestOperator_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	log.SetLogger(zap.New(zap.UseDevMode(true)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	k3sC, err := k3s.Run(ctx, k3sImage)
	if k3sC != nil {
		t.Cleanup(func() {
			if err := k3sC.Terminate(context.Background()); err != nil {
				t.Logf("terminate k3s container: %v", err)
			}
		})
	}
	if err != nil {
		t.Fatalf("start k3s: %v", err)
	}

	kubeconfig, err := k3sC.GetKubeConfig(ctx)
	if err != nil {
		t.Fatalf("kubeconfig: %v", err)
	}
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		t.Fatalf("rest config: %v", err)
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("crd scheme: %v", err)
	}
	if err := apiextensionsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("apiextensions scheme: %v", err)
	}

	cl, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	applyCRDs(t, ctx, cl, filepath.Join("..", "..", "config", "crd", "bases"))
	applyCRDs(t, ctx, cl, "testdata")
	waitForCRDsEstablished(t, ctx, cl)

	mgr, err := manager.New(restCfg, manager.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	if err := (&controller.ShopReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager(mgr); err != nil {
		t.Fatalf("setup shop reconciler: %v", err)
	}
	if err := (&controller.DiscordChannelReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager(mgr); err != nil {
		t.Fatalf("setup discord reconciler: %v", err)
	}
	if err := (&controller.WalletReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager(mgr); err != nil {
		t.Fatalf("setup wallet reconciler: %v", err)
	}
	go func() {
		if err := mgr.Start(ctx); err != nil {
			t.Errorf("manager exited: %v", err)
		}
	}()

	t.Run("standard shop gets 2 replicas and full resource set", func(t *testing.T) {
		shop := &v1alpha1.Shop{
			ObjectMeta: metav1.ObjectMeta{Name: "boutique", Namespace: testNS},
			Spec: v1alpha1.ShopSpec{
				Availability:  "standard",
				Database:      "standard",
				WalletAddress: "0x742d35Cc6634C0532925a3b844Bc9e7595f42e00",
			},
		}
		if err := cl.Create(ctx, shop); err != nil {
			t.Fatalf("create shop: %v", err)
		}

		simulateCNPGReady(t, ctx, cl, "boutique")

		deploy := &appsv1.Deployment{}
		waitFor(t, "deployment", func() error {
			return cl.Get(ctx, types.NamespacedName{Name: "boutique", Namespace: testNS}, deploy)
		})
		if got := *deploy.Spec.Replicas; got != 2 {
			t.Errorf("standard availability: replicas = %d, want 2", got)
		}

		waitFor(t, "service", func() error {
			return cl.Get(ctx, types.NamespacedName{Name: "boutique", Namespace: testNS}, &corev1.Service{})
		})
		waitFor(t, "ingress", func() error {
			return cl.Get(ctx, types.NamespacedName{Name: "boutique", Namespace: testNS}, &networkingv1.Ingress{})
		})
		waitFor(t, "dashboard configmap", func() error {
			return cl.Get(ctx, types.NamespacedName{Name: "boutique-dashboard", Namespace: testNS}, &corev1.ConfigMap{})
		})
		waitFor(t, "prometheus rule", func() error {
			rule := &unstructured.Unstructured{}
			rule.SetGroupVersionKind(schema.GroupVersionKind{
				Group: "monitoring.coreos.com", Version: "v1", Kind: "PrometheusRule",
			})
			return cl.Get(ctx, types.NamespacedName{Name: "boutique-alerts", Namespace: testNS}, rule)
		})
		waitFor(t, "discord channel", func() error {
			return cl.Get(ctx, types.NamespacedName{Name: "boutique", Namespace: testNS}, &v1alpha1.DiscordChannel{})
		})

		wallet := &v1alpha1.Wallet{}
		waitFor(t, "wallet ready", func() error {
			if err := cl.Get(ctx, types.NamespacedName{Name: "boutique", Namespace: testNS}, wallet); err != nil {
				return err
			}
			if wallet.Status.Status != "Ready" {
				return fmt.Errorf("wallet status %q (%s)", wallet.Status.Status, wallet.Status.Message)
			}
			return nil
		})
		if wallet.Status.Address != shop.Spec.WalletAddress {
			t.Errorf("wallet adopted address = %q, want %q", wallet.Status.Address, shop.Spec.WalletAddress)
		}
	})

	t.Run("high availability scales to 3 replicas", func(t *testing.T) {
		shop := &v1alpha1.Shop{}
		if err := cl.Get(ctx, types.NamespacedName{Name: "boutique", Namespace: testNS}, shop); err != nil {
			t.Fatalf("get shop: %v", err)
		}
		shop.Spec.Availability = "high"
		if err := cl.Update(ctx, shop); err != nil {
			t.Fatalf("update shop: %v", err)
		}

		waitFor(t, "3 replicas", func() error {
			deploy := &appsv1.Deployment{}
			if err := cl.Get(ctx, types.NamespacedName{Name: "boutique", Namespace: testNS}, deploy); err != nil {
				return err
			}
			if *deploy.Spec.Replicas != 3 {
				return fmt.Errorf("replicas = %d, want 3", *deploy.Spec.Replicas)
			}
			return nil
		})
	})

	t.Run("CRD admission rejects invalid availability", func(t *testing.T) {
		bad := &v1alpha1.Shop{
			ObjectMeta: metav1.ObjectMeta{Name: "bad-shop", Namespace: testNS},
			Spec: v1alpha1.ShopSpec{
				Availability:  "turbo",
				WalletAddress: "0x742d35Cc6634C0532925a3b844Bc9e7595f42e00",
			},
		}
		if err := cl.Create(ctx, bad); !apierrors.IsInvalid(err) {
			t.Errorf("create with availability=turbo: err = %v, want Invalid", err)
		}
	})

	t.Run("wallet without address creates a new on-chain account", func(t *testing.T) {
		wallet := &v1alpha1.Wallet{
			ObjectMeta: metav1.ObjectMeta{Name: "fresh", Namespace: testNS},
			Spec:       v1alpha1.WalletSpec{Blockchain: "ethereum", Network: "sepolia", Currency: "ETH"},
		}
		if err := cl.Create(ctx, wallet); err != nil {
			t.Fatalf("create wallet: %v", err)
		}

		waitFor(t, "generated account", func() error {
			if err := cl.Get(ctx, types.NamespacedName{Name: "fresh", Namespace: testNS}, wallet); err != nil {
				return err
			}
			if wallet.Status.Status != "Ready" || wallet.Status.Address == "" {
				return fmt.Errorf("wallet status %q address %q (%s)",
					wallet.Status.Status, wallet.Status.Address, wallet.Status.Message)
			}
			return nil
		})

		secret := &corev1.Secret{}
		if err := cl.Get(ctx, types.NamespacedName{Name: "fresh-keys", Namespace: testNS}, secret); err != nil {
			t.Fatalf("keys secret: %v", err)
		}
		if got := string(secret.Data["address"]); got != wallet.Status.Address {
			t.Errorf("secret address = %q, want %q", got, wallet.Status.Address)
		}
		if len(secret.Data["private-key"]) == 0 {
			t.Error("keys secret is missing the private key")
		}
	})

	t.Run("deleting the shop garbage-collects owned resources", func(t *testing.T) {
		shop := &v1alpha1.Shop{ObjectMeta: metav1.ObjectMeta{Name: "boutique", Namespace: testNS}}
		if err := cl.Delete(ctx, shop); err != nil {
			t.Fatalf("delete shop: %v", err)
		}
		waitFor(t, "deployment gone", func() error {
			err := cl.Get(ctx, types.NamespacedName{Name: "boutique", Namespace: testNS}, &appsv1.Deployment{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("deployment still present (err=%v)", err)
		})
	})
}

// simulateCNPGReady plays the CNPG operator's part: the shop-operator creates
// the Cluster CR and then waits for readyInstances >= 1 plus the app Secret —
// exactly what CNPG publishes when PostgreSQL is up.
func simulateCNPGReady(t *testing.T, ctx context.Context, cl client.Client, shop string) {
	t.Helper()

	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(cnpgClusterGVK)
	waitFor(t, "cnpg cluster created by operator", func() error {
		return cl.Get(ctx, types.NamespacedName{Name: shop + "-db", Namespace: testNS}, cluster)
	})

	cluster.Object["status"] = map[string]interface{}{"readyInstances": int64(1)}
	if err := cl.Status().Update(ctx, cluster); err != nil {
		t.Fatalf("update cnpg status: %v", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      shop + "-db-app",
			Namespace: testNS,
			Labels:    map[string]string{"cnpg.io/cluster": shop + "-db"},
		},
		Data: map[string][]byte{
			"host": []byte(shop + "-db-rw"), "port": []byte("5432"),
			"dbname": []byte("shop_db"), "username": []byte("shop_user"),
			"password": []byte("test-password"),
		},
	}
	if err := cl.Create(ctx, secret); err != nil {
		t.Fatalf("create db secret: %v", err)
	}
}

// applyCRDs installs every CRD found in dir (multi-document YAML supported).
func applyCRDs(t *testing.T, ctx context.Context, cl client.Client, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read crd dir %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, doc := range strings.Split(string(raw), "\n---") {
			if strings.TrimSpace(doc) == "" {
				continue
			}
			crd := &apiextensionsv1.CustomResourceDefinition{}
			if err := yaml.Unmarshal([]byte(doc), crd); err != nil {
				t.Fatalf("unmarshal CRD in %s: %v", e.Name(), err)
			}
			if crd.Name == "" {
				// Comment-only or empty YAML document (e.g. a license header
				// before the first ---separator).
				continue
			}
			if err := cl.Create(ctx, crd); err != nil && !apierrors.IsAlreadyExists(err) {
				t.Fatalf("create CRD %s: %v", crd.Name, err)
			}
		}
	}
}

// waitForCRDsEstablished blocks until every installed CRD is served, so the
// manager's informers don't race CRD registration.
func waitForCRDsEstablished(t *testing.T, ctx context.Context, cl client.Client) {
	t.Helper()
	waitFor(t, "CRDs established", func() error {
		list := &apiextensionsv1.CustomResourceDefinitionList{}
		if err := cl.List(ctx, list); err != nil {
			return err
		}
		for i := range list.Items {
			established := false
			for _, cond := range list.Items[i].Status.Conditions {
				if cond.Type == apiextensionsv1.Established && cond.Status == apiextensionsv1.ConditionTrue {
					established = true
				}
			}
			if !established {
				return fmt.Errorf("CRD %s not established", list.Items[i].Name)
			}
		}
		return nil
	})
}

// waitFor polls fn until it succeeds or the shared timeout elapses.
func waitFor(t *testing.T, what string, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if lastErr = fn(); lastErr == nil {
			return
		}
		time.Sleep(pollEvery)
	}
	t.Fatalf("timed out waiting for %s: %v", what, lastErr)
}
