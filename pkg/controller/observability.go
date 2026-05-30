package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/devops-milos/shop-operator/api/v1alpha1"
)

// reconcileObservability provisions the per-shop observability resources
// required by §4.1 of the specification:
//   - a PrometheusRule with alerts scoped to this shop (label shop=<name>),
//   - a Grafana dashboard (ConfigMap picked up by the Grafana sidecar),
//   - when a notification webhook is configured: a DiscordChannel, a Secret
//     holding the webhook, and an AlertmanagerConfig that routes this shop's
//     alerts to its own Discord channel.
//
// All resources are owned by the Shop so they are garbage-collected with it.
func (r *ShopReconciler) reconcileObservability(ctx context.Context, shop *v1alpha1.Shop) error {
	if err := r.reconcilePrometheusRule(ctx, shop); err != nil {
		return fmt.Errorf("prometheus rule: %w", err)
	}
	if err := r.reconcileDashboard(ctx, shop); err != nil {
		return fmt.Errorf("dashboard: %w", err)
	}
	if shop.Spec.NotificationWebhook != "" {
		if err := r.reconcileDiscordChannel(ctx, shop); err != nil {
			return fmt.Errorf("discord channel: %w", err)
		}
		if err := r.reconcileAlertRouting(ctx, shop); err != nil {
			return fmt.Errorf("alert routing: %w", err)
		}
	}
	return nil
}

func prometheusRuleName(shop string) string   { return shop + "-alerts" }
func dashboardCMName(shop string) string      { return shop + "-dashboard" }
func discordWebhookSecret(shop string) string { return shop + "-discord-webhook" }

// reconcilePrometheusRule creates per-shop alerting rules. Every alert carries
// the shop=<name> label so the matching AlertmanagerConfig can route it to the
// shop's Discord channel.
func (r *ShopReconciler) reconcilePrometheusRule(ctx context.Context, shop *v1alpha1.Shop) error {
	gvk := schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "PrometheusRule"}
	name := prometheusRuleName(shop.Name)
	sel := fmt.Sprintf(`shop="%s"`, shop.Name)

	rules := []interface{}{
		map[string]interface{}{
			"alert": "ShopInstanceDown",
			"expr":  fmt.Sprintf(`max(up{%s}) == 0`, sel),
			"for":   "2m",
			"labels": map[string]interface{}{
				"severity": "critical", "shop": shop.Name,
			},
			"annotations": map[string]interface{}{
				"summary":     fmt.Sprintf("Shop %s is down", shop.Name),
				"description": fmt.Sprintf("No shop instance for %s has been scrapeable for 2 minutes.", shop.Name),
			},
		},
		map[string]interface{}{
			"alert": "ShopHighErrorRate",
			"expr": fmt.Sprintf(
				`sum(rate(shop_http_requests_total{%s,status=~"5.."}[5m]))`+
					` / clamp_min(sum(rate(shop_http_requests_total{%s}[5m])), 1) * 100 > 5`,
				sel, sel),
			"for": "5m",
			"labels": map[string]interface{}{
				"severity": "warning", "shop": shop.Name,
			},
			"annotations": map[string]interface{}{
				"summary":     fmt.Sprintf("Shop %s 5xx error rate above 5%%", shop.Name),
				"description": "More than 5% of requests are returning 5xx over the last 5 minutes.",
			},
		},
		map[string]interface{}{
			"alert": "ShopHighLatency",
			"expr": fmt.Sprintf(
				`histogram_quantile(0.99, sum(rate(shop_http_request_duration_seconds_bucket{%s}[5m])) by (le)) > 1`,
				sel),
			"for": "5m",
			"labels": map[string]interface{}{
				"severity": "warning", "shop": shop.Name,
			},
			"annotations": map[string]interface{}{
				"summary":     fmt.Sprintf("Shop %s p99 latency above 1s", shop.Name),
				"description": "The 99th percentile request latency exceeds 1 second.",
			},
		},
		map[string]interface{}{
			"alert": "ShopElevated404Rate",
			"expr": fmt.Sprintf(
				`sum(rate(shop_http_requests_total{%s,status="404"}[10m])) > 1`,
				sel),
			"for": "10m",
			"labels": map[string]interface{}{
				"severity": "info", "shop": shop.Name,
			},
			"annotations": map[string]interface{}{
				"summary":     fmt.Sprintf("Shop %s elevated 404 rate", shop.Name),
				"description": "More than 1 request/s is returning 404 over the last 10 minutes.",
			},
		},
	}

	spec := map[string]interface{}{
		"groups": []interface{}{
			map[string]interface{}{
				"name":     shop.Name + ".rules",
				"interval": "30s",
				"rules":    rules,
			},
		},
	}
	return r.applyOwnedUnstructured(ctx, shop, gvk, name, shop.Namespace,
		map[string]interface{}{"release": "kube-prometheus-stack"}, spec)
}

// reconcileDashboard creates a Grafana dashboard ConfigMap for the shop. The
// kube-prometheus-stack Grafana sidecar imports any ConfigMap labelled
// grafana_dashboard=1 from any namespace.
func (r *ShopReconciler) reconcileDashboard(ctx context.Context, shop *v1alpha1.Shop) error {
	name := dashboardCMName(shop.Name)
	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: shop.Namespace,
			Labels: map[string]string{
				"grafana_dashboard":            "1",
				"app.kubernetes.io/managed-by": "shop-operator",
			},
		},
		Data: map[string]string{
			shop.Name + "-dashboard.json": shopDashboardJSON(shop.Name, shop.Namespace),
		},
	}
	if err := ctrl.SetControllerReference(shop, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner ref: %w", err)
	}
	existing := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: shop.Namespace}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	existing.Labels = desired.Labels
	existing.Data = desired.Data
	return r.Update(ctx, existing)
}

// reconcileDiscordChannel creates a DiscordChannel CR so the existing
// reconciler validates the webhook and reflects its status.
func (r *ShopReconciler) reconcileDiscordChannel(ctx context.Context, shop *v1alpha1.Shop) error {
	desired := &v1alpha1.DiscordChannel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      shop.Name,
			Namespace: shop.Namespace,
			Labels:    shopLabels(shop.Name),
		},
		Spec: v1alpha1.DiscordChannelSpec{
			WebhookURL:  shop.Spec.NotificationWebhook,
			ChannelName: shop.Name,
		},
	}
	if err := ctrl.SetControllerReference(shop, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner ref: %w", err)
	}
	existing := &v1alpha1.DiscordChannel{}
	err := r.Get(ctx, types.NamespacedName{Name: shop.Name, Namespace: shop.Namespace}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	existing.Labels = desired.Labels
	existing.Spec = desired.Spec
	return r.Update(ctx, existing)
}

// reconcileAlertRouting creates the Secret holding the Discord webhook and an
// AlertmanagerConfig that routes alerts labelled shop=<name> to it.
func (r *ShopReconciler) reconcileAlertRouting(ctx context.Context, shop *v1alpha1.Shop) error {
	// 1. Secret with the webhook URL (referenced by the AlertmanagerConfig).
	secretName := discordWebhookSecret(shop.Name)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: shop.Namespace,
			Labels:    shopLabels(shop.Name),
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"webhook-url": shop.Spec.NotificationWebhook},
	}
	if err := ctrl.SetControllerReference(shop, secret, r.Scheme); err != nil {
		return fmt.Errorf("set owner ref on secret: %w", err)
	}
	existingSecret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: shop.Namespace}, existingSecret)
	switch {
	case errors.IsNotFound(err):
		if err := r.Create(ctx, secret); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		existingSecret.StringData = secret.StringData
		existingSecret.Labels = secret.Labels
		if err := r.Update(ctx, existingSecret); err != nil {
			return err
		}
	}

	// 2. AlertmanagerConfig routing shop=<name> alerts to the Discord receiver.
	gvk := schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1alpha1", Kind: "AlertmanagerConfig"}
	spec := map[string]interface{}{
		"route": map[string]interface{}{
			"receiver":       "discord",
			"groupBy":        []interface{}{"alertname", "shop", "severity"},
			"groupWait":      "30s",
			"groupInterval":  "5m",
			"repeatInterval": "4h",
			"matchers": []interface{}{
				map[string]interface{}{"name": "shop", "matchType": "=", "value": shop.Name},
			},
		},
		"receivers": []interface{}{
			map[string]interface{}{
				"name": "discord",
				"discordConfigs": []interface{}{
					map[string]interface{}{
						"apiURL": map[string]interface{}{
							"key":  "webhook-url",
							"name": secretName,
						},
						"title": fmt.Sprintf("[{{ .Status | toUpper }}] %s — {{ .CommonLabels.alertname }}", shop.Name),
						"message": "{{ range .Alerts }}**Alert:** {{ .Annotations.summary }}\n" +
							"**Severity:** {{ .Labels.severity }}\n" +
							"**Description:** {{ .Annotations.description }}\n{{ end }}",
					},
				},
			},
		},
	}
	return r.applyOwnedUnstructured(ctx, shop, gvk, shop.Name, shop.Namespace,
		map[string]interface{}{"release": "kube-prometheus-stack"}, spec)
}

// applyOwnedUnstructured creates or updates an unstructured CR with the given
// GVK, owned by the shop, replacing only the spec and labels.
func (r *ShopReconciler) applyOwnedUnstructured(
	ctx context.Context, shop *v1alpha1.Shop, gvk schema.GroupVersionKind,
	name, namespace string, labels, spec map[string]interface{},
) error {
	ownerRef := metav1.NewControllerRef(shop, v1alpha1.SchemeGroupVersion.WithKind("Shop"))
	desired := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": gvk.GroupVersion().String(),
		"kind":       gvk.Kind,
		"metadata": map[string]interface{}{
			"name":            name,
			"namespace":       namespace,
			"labels":          labels,
			"ownerReferences": []interface{}{ownerRefToMap(ownerRef)},
		},
		"spec": spec,
	}}
	desired.SetGroupVersionKind(gvk)

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(gvk)
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	existing.Object["spec"] = spec
	if meta, ok := existing.Object["metadata"].(map[string]interface{}); ok {
		meta["labels"] = labels
	}
	return r.Update(ctx, existing)
}

// shopDashboardJSON returns a Grafana dashboard definition for a single shop.
// Placeholders are substituted rather than using fmt to avoid clashing with
// the many braces in the embedded JSON and PromQL.
func shopDashboardJSON(shop, namespace string) string {
	s := shopDashboardTemplate
	s = strings.ReplaceAll(s, "__SHOP__", shop)
	s = strings.ReplaceAll(s, "__NS__", namespace)
	return s
}
