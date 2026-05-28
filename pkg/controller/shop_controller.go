package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/devops-milos/shop-operator/api/v1alpha1"
)

const (
	defaultShopImage = "milos2002/shop:development"
	shopHTTPPort     = 8080
	dbStorageStd     = "1Gi"
	dbStorageLight   = "256Mi"
	ingressClassName = "nginx"
	// ingressHostSuffix uses nip.io's wildcard DNS so a freshly-created Shop
	// is reachable at http://<name>.127.0.0.1.nip.io with zero hosts-file
	// edits on the developer machine. nip.io resolves <anything>.<ip>.nip.io
	// back to <ip>, so any shop name routes to the local ingress on 127.0.0.1.
	ingressHostSuffix = ".127.0.0.1.nip.io"

	// sessionCookieMaxAge matches the in-memory cart TTL in the shop service
	// (cart.NewStore default of 30 minutes). Pinning a browser to the same
	// pod for that window keeps the wallet's cart visible across redirects.
	sessionCookieMaxAge = "1800"
	sessionCookieName   = "shop_route"
)

// ingressAffinityAnnotations pins each browser to a single shop pod via an
// nginx-ingress cookie. Without this, two-replica Shops lose cart contents
// after the 303 from POST /cart because the follow-up GET round-robins to
// the other pod, whose in-memory cart.Store is empty for that wallet.
func ingressAffinityAnnotations() map[string]string {
	return map[string]string{
		"nginx.ingress.kubernetes.io/affinity":                "cookie",
		"nginx.ingress.kubernetes.io/affinity-mode":           "persistent",
		"nginx.ingress.kubernetes.io/session-cookie-name":     sessionCookieName,
		"nginx.ingress.kubernetes.io/session-cookie-max-age":  sessionCookieMaxAge,
		"nginx.ingress.kubernetes.io/session-cookie-path":     "/",
		"nginx.ingress.kubernetes.io/session-cookie-samesite": "Lax",
	}
}

type ShopReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *ShopReconciler) SetupWithManager(mgr ctrl.Manager) error {
	cnpgSecretPred := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		_, has := obj.GetLabels()["cnpg.io/cluster"]
		return has
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Shop{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&networkingv1.Ingress{}).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.findShopsForSecret),
			builder.WithPredicates(cnpgSecretPred),
		).
		Complete(r)
}

func (r *ShopReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	shop := &v1alpha1.Shop{}
	if err := r.Get(ctx, req.NamespacedName, shop); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("Reconciling Shop", "name", shop.Name, "namespace", shop.Namespace)

	if err := r.reconcileDatabase(ctx, shop); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile database: %w", err)
	}

	dbReady, err := r.dbReady(ctx, shop)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("check db: %w", err)
	}
	if !dbReady {
		logger.Info("Database not ready yet, requeueing", "shop", shop.Name)
		if err := r.syncStatus(ctx, shop, false); err != nil {
			logger.Error(err, "sync status while waiting for db")
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	if err := r.reconcileDeployment(ctx, shop); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile deployment: %w", err)
	}

	if err := r.reconcileService(ctx, shop); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile service: %w", err)
	}

	if err := r.reconcileIngress(ctx, shop); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile ingress: %w", err)
	}

	if err := r.syncStatus(ctx, shop, true); err != nil {
		return ctrl.Result{}, fmt.Errorf("sync status: %w", err)
	}

	return ctrl.Result{}, nil
}

func (r *ShopReconciler) reconcileDeployment(ctx context.Context, shop *v1alpha1.Shop) error {
	replicas := int32(2)
	if shop.Spec.Availability == "high" {
		replicas = 3
	}

	image := shop.Spec.Image
	if image == "" {
		image = defaultShopImage
	}

	labels := shopLabels(shop.Name)
	dbSecret := dbSecretName(shop.Name)

	envFromSecret := func(name, key string) corev1.EnvVar {
		return corev1.EnvVar{
			Name: name,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: dbSecret},
					Key:                  key,
				},
			},
		}
	}

	desired := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      shop.Name,
			Namespace: shop.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
					Annotations: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/port":   fmt.Sprintf("%d", shopHTTPPort),
						"prometheus.io/path":   "/metrics",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "shop",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Ports: []corev1.ContainerPort{
								{Name: "http", ContainerPort: shopHTTPPort, Protocol: corev1.ProtocolTCP},
							},
							Env: []corev1.EnvVar{
								{Name: "SHOP_HTTP_ADDR", Value: fmt.Sprintf(":%d", shopHTTPPort)},
								{Name: "SHOP_ENV", Value: "production"},
								{Name: "SHOP_LOG_LEVEL", Value: "info"},
								envFromSecret("SHOP_DB_HOST", "host"),
								envFromSecret("SHOP_DB_PORT", "port"),
								envFromSecret("SHOP_DB_NAME", "dbname"),
								envFromSecret("SHOP_DB_USER", "username"),
								envFromSecret("SHOP_DB_PASSWORD", "password"),
								{Name: "SHOP_ADMIN_KEY", Value: "admin-" + shop.Name},
								{Name: "SHOP_ETH_WALLET", Value: shop.Spec.WalletAddress},
								// rpc.sepolia.org was decommissioned and now serves a static
								// Apache 404 page instead of JSON-RPC, which breaks payment
								// verification. PublicNode is a free, multi-region public RPC
								// for Sepolia that does not require an API key.
								{Name: "SHOP_ETH_RPC_URL", Value: "https://ethereum-sepolia-rpc.publicnode.com"},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt(shopHTTPPort),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt(shopHTTPPort),
									},
								},
								InitialDelaySeconds: 15,
								PeriodSeconds:       20,
							},
						},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(shop, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner reference on deployment: %w", err)
	}

	existing := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: shop.Name, Namespace: shop.Namespace}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	existing.Labels = desired.Labels
	existing.Spec.Replicas = desired.Spec.Replicas
	existing.Spec.Template = desired.Spec.Template
	return r.Update(ctx, existing)
}

func (r *ShopReconciler) reconcileService(ctx context.Context, shop *v1alpha1.Shop) error {
	labels := shopLabels(shop.Name)
	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      shop.Name,
			Namespace: shop.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Type:     corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       80,
					TargetPort: intstr.FromInt(shopHTTPPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(shop, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner reference on service: %w", err)
	}

	existing := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: shop.Name, Namespace: shop.Namespace}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	existing.Labels = desired.Labels
	existing.Spec.Selector = desired.Spec.Selector
	existing.Spec.Ports = desired.Spec.Ports
	return r.Update(ctx, existing)
}

func (r *ShopReconciler) reconcileIngress(ctx context.Context, shop *v1alpha1.Shop) error {
	className := ingressClassName
	pathType := networkingv1.PathTypePrefix
	host := shop.Name + ingressHostSuffix

	desired := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        shop.Name,
			Namespace:   shop.Namespace,
			Labels:      shopLabels(shop.Name),
			Annotations: ingressAffinityAnnotations(),
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{
				{
					Host: host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: shop.Name,
											Port: networkingv1.ServiceBackendPort{Number: 80},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(shop, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner reference on ingress: %w", err)
	}

	existing := &networkingv1.Ingress{}
	err := r.Get(ctx, types.NamespacedName{Name: shop.Name, Namespace: shop.Namespace}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	existing.Labels = desired.Labels
	if existing.Annotations == nil {
		existing.Annotations = map[string]string{}
	}
	// Merge — don't replace — so annotations added by other controllers
	// (cert-manager, external-dns, etc.) survive a reconcile.
	for k, v := range desired.Annotations {
		existing.Annotations[k] = v
	}
	existing.Spec = desired.Spec
	return r.Update(ctx, existing)
}

func (r *ShopReconciler) reconcileDatabase(ctx context.Context, shop *v1alpha1.Shop) error {
	return r.reconcilePostgresCluster(ctx, shop)
}

// reconcilePostgresCluster creates a CNPG Cluster with bootstrap so that CNPG
// creates an application database, role, and a Secret named <cluster>-app
// containing host/port/username/password/dbname keys.
func (r *ShopReconciler) reconcilePostgresCluster(ctx context.Context, shop *v1alpha1.Shop) error {
	gvk := schema.GroupVersionKind{
		Group:   "postgresql.cnpg.io",
		Version: "v1",
		Kind:    "Cluster",
	}

	storage := dbStorageStd
	if shop.Spec.Database == "light" {
		storage = dbStorageLight
	}

	ownerRef := metav1.NewControllerRef(shop, v1alpha1.SchemeGroupVersion.WithKind("Shop"))
	clusterName := dbClusterName(shop.Name)
	desired := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "postgresql.cnpg.io/v1",
			"kind":       "Cluster",
			"metadata": map[string]interface{}{
				"name":            clusterName,
				"namespace":       shop.Namespace,
				"ownerReferences": []interface{}{ownerRefToMap(ownerRef)},
				"labels":          shopLabels(shop.Name),
			},
			"spec": map[string]interface{}{
				"instances": int64(1),
				"bootstrap": map[string]interface{}{
					"initdb": map[string]interface{}{
						"database": "shop_db",
						"owner":    "shop_user",
					},
				},
				"storage": map[string]interface{}{
					"size": storage,
				},
			},
		},
	}
	desired.SetGroupVersionKind(gvk)

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(gvk)
	err := r.Get(ctx, types.NamespacedName{
		Name:      clusterName,
		Namespace: shop.Namespace,
	}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Patch only fields we own (instances, storage) so CNPG-managed status stays intact.
	spec, ok := existing.Object["spec"].(map[string]interface{})
	if !ok {
		spec = map[string]interface{}{}
	}
	spec["instances"] = int64(1)
	if storageSpec, ok := spec["storage"].(map[string]interface{}); ok {
		storageSpec["size"] = storage
	} else {
		spec["storage"] = map[string]interface{}{"size": storage}
	}
	existing.Object["spec"] = spec
	return r.Update(ctx, existing)
}

// dbReady returns true only when the CNPG cluster has at least one ready
// instance AND the application Secret exists. Checking only the Secret is
// insufficient: CNPG creates the Secret before the Postgres process is ready
// to accept connections, which causes the shop pods to crash-loop on startup.
func (r *ShopReconciler) dbReady(ctx context.Context, shop *v1alpha1.Shop) (bool, error) {
	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "postgresql.cnpg.io", Version: "v1", Kind: "Cluster",
	})
	if err := r.Get(ctx, types.NamespacedName{
		Name:      dbClusterName(shop.Name),
		Namespace: shop.Namespace,
	}, cluster); err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	status, _ := cluster.Object["status"].(map[string]interface{})
	readyInstances, _ := status["readyInstances"].(int64)
	if readyInstances < 1 {
		return false, nil
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      dbSecretName(shop.Name),
		Namespace: shop.Namespace,
	}, secret); err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// findShopsForSecret maps a CNPG-managed Secret change back to the owning Shop
// so the reconcile loop fires when database credentials are created/rotated.
func (r *ShopReconciler) findShopsForSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}

	clusterLabel, ok := secret.GetLabels()["cnpg.io/cluster"]
	if !ok {
		return nil
	}

	shopName, ok := shopNameFromClusterLabel(clusterLabel)
	if !ok {
		return nil
	}

	shop := &v1alpha1.Shop{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      shopName,
		Namespace: secret.Namespace,
	}, shop); err != nil {
		return nil
	}

	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Name: shop.Name, Namespace: shop.Namespace},
	}}
}

func (r *ShopReconciler) syncStatus(ctx context.Context, shop *v1alpha1.Shop, dbReady bool) error {
	latest := &v1alpha1.Shop{}
	if err := r.Get(ctx, types.NamespacedName{Name: shop.Name, Namespace: shop.Namespace}, latest); err != nil {
		return err
	}

	deploy := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: shop.Name, Namespace: shop.Namespace}, deploy)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	latest.Status.Replicas = deploy.Status.Replicas
	latest.Status.ReadyReplicas = deploy.Status.ReadyReplicas
	latest.Status.ServiceURL = "http://" + shop.Name + ingressHostSuffix
	latest.Status.DatabaseReady = dbReady

	switch {
	case !dbReady:
		latest.Status.Phase = "ProvisioningDB"
	case deploy.Status.ReadyReplicas > 0 && deploy.Status.ReadyReplicas == deploy.Status.Replicas:
		latest.Status.Phase = "Running"
	default:
		latest.Status.Phase = "Pending"
	}

	return r.Status().Update(ctx, latest)
}

func ownerRefToMap(ref *metav1.OwnerReference) map[string]interface{} {
	isController := ref.Controller != nil && *ref.Controller
	return map[string]interface{}{
		"apiVersion":         ref.APIVersion,
		"kind":               ref.Kind,
		"name":               ref.Name,
		"uid":                string(ref.UID),
		"controller":         isController,
		"blockOwnerDeletion": ref.BlockOwnerDeletion != nil && *ref.BlockOwnerDeletion,
	}
}

func shopLabels(name string) map[string]string {
	return map[string]string{
		"app":                          name,
		"app.kubernetes.io/name":       "shop",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/managed-by": "shop-operator",
	}
}

func dbClusterName(shopName string) string {
	return shopName + "-db"
}

func dbSecretName(shopName string) string {
	return dbClusterName(shopName) + "-app"
}

// shopNameFromClusterLabel reverses dbClusterName: "<shop>-db" -> "<shop>".
func shopNameFromClusterLabel(clusterLabel string) (string, bool) {
	const suffix = "-db"
	if len(clusterLabel) <= len(suffix) || clusterLabel[len(clusterLabel)-len(suffix):] != suffix {
		return "", false
	}
	return clusterLabel[:len(clusterLabel)-len(suffix)], true
}
