package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

func shopLabels(name string) map[string]string {
	return map[string]string{
		"app":        name,
		"managed-by": "shop-operator",
	}
}
