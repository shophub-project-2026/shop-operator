package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/devops-milos/shop-operator/api/v1alpha1"
)

type ShopReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *ShopReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Shop{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
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

	if err := r.reconcileDeployment(ctx, shop); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile deployment: %w", err)
	}

	if err := r.reconcileService(ctx, shop); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile service: %w", err)
	}

	if err := r.reconcileDatabase(ctx, shop); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile database: %w", err)
	}

	if err := r.syncStatus(ctx, shop); err != nil {
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
		image = "nginx:stable-alpine"
	}

	labels := shopLabels(shop.Name)

	desired := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      shop.Name,
			Namespace: shop.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "shop",
							Image: image,
							Ports: []corev1.ContainerPort{
								{ContainerPort: 8080, Protocol: corev1.ProtocolTCP},
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

	existing.Spec.Replicas = desired.Spec.Replicas
	existing.Spec.Template = desired.Spec.Template
	return r.Update(ctx, existing)
}

func (r *ShopReconciler) reconcileService(ctx context.Context, shop *v1alpha1.Shop) error {
	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      shop.Name,
			Namespace: shop.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": shop.Name},
			Type:     corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       80,
					TargetPort: intstr.FromInt(8080),
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

	existing.Spec.Selector = desired.Spec.Selector
	existing.Spec.Ports = desired.Spec.Ports
	return r.Update(ctx, existing)
}

func (r *ShopReconciler) reconcileDatabase(ctx context.Context, shop *v1alpha1.Shop) error {
	if shop.Spec.Database == "light" {
		return r.reconcileRedisDB(ctx, shop)
	}
	return r.reconcilePostgresCluster(ctx, shop)
}

// reconcilePostgresCluster creates a CNPG Cluster resource for the standard database tier.
// Uses unstructured to avoid importing the CNPG Go client as a direct dependency.
func (r *ShopReconciler) reconcilePostgresCluster(ctx context.Context, shop *v1alpha1.Shop) error {
	gvk := schema.GroupVersionKind{
		Group:   "postgresql.cnpg.io",
		Version: "v1",
		Kind:    "Cluster",
	}

	ownerRef := metav1.NewControllerRef(shop, v1alpha1.SchemeGroupVersion.WithKind("Shop"))
	desired := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "postgresql.cnpg.io/v1",
			"kind":       "Cluster",
			"metadata": map[string]interface{}{
				"name":            shop.Name + "-db",
				"namespace":       shop.Namespace,
				"ownerReferences": []interface{}{ownerRefToMap(ownerRef)},
			},
			"spec": map[string]interface{}{
				"instances": int64(1),
				"storage": map[string]interface{}{
					"size": "1Gi",
				},
			},
		},
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(gvk)
	err := r.Get(ctx, types.NamespacedName{
		Name:      shop.Name + "-db",
		Namespace: shop.Namespace,
	}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	return err
}

// reconcileRedisDB creates a Redis Enterprise Database resource for the light database tier.
// Uses unstructured to avoid importing the REDB operator Go client as a direct dependency.
func (r *ShopReconciler) reconcileRedisDB(ctx context.Context, shop *v1alpha1.Shop) error {
	gvk := schema.GroupVersionKind{
		Group:   "app.redislabs.com",
		Version: "v1alpha1",
		Kind:    "RedisEnterpriseDatabase",
	}

	ownerRef := metav1.NewControllerRef(shop, v1alpha1.SchemeGroupVersion.WithKind("Shop"))
	desired := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "app.redislabs.com/v1alpha1",
			"kind":       "RedisEnterpriseDatabase",
			"metadata": map[string]interface{}{
				"name":            shop.Name + "-redis",
				"namespace":       shop.Namespace,
				"ownerReferences": []interface{}{ownerRefToMap(ownerRef)},
			},
			"spec": map[string]interface{}{
				"memorySize": "100MB",
			},
		},
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(gvk)
	err := r.Get(ctx, types.NamespacedName{
		Name:      shop.Name + "-redis",
		Namespace: shop.Namespace,
	}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	return err
}

// syncStatus re-fetches the owned Deployment and mirrors its replica
// counts into Shop.Status. A fresh Get is used to avoid resource-version
// conflicts when both spec and status are updated in the same pass.
func (r *ShopReconciler) syncStatus(ctx context.Context, shop *v1alpha1.Shop) error {
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
	latest.Status.ServiceURL = fmt.Sprintf("%s.%s.svc.cluster.local", shop.Name, shop.Namespace)
	latest.Status.DatabaseReady = true

	if deploy.Status.ReadyReplicas > 0 && deploy.Status.ReadyReplicas == deploy.Status.Replicas {
		latest.Status.Phase = "Running"
	} else {
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
		"app":        name,
		"managed-by": "shop-operator",
	}
}
