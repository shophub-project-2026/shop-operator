package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/devops-milos/shop-operator/api/v1alpha1"
)

type DiscordChannelReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	HTTPClient *http.Client
}

func (r *DiscordChannelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.HTTPClient == nil {
		r.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.DiscordChannel{}).
		Complete(r)
}

func (r *DiscordChannelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	dc := &v1alpha1.DiscordChannel{}
	if err := r.Get(ctx, req.NamespacedName, dc); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("Reconciling DiscordChannel", "name", dc.Name, "namespace", dc.Namespace)

	if dc.Spec.WebhookURL == "" {
		return ctrl.Result{}, r.setDiscordStatus(ctx, dc, "Error", "webhookUrl must not be empty")
	}

	if err := r.validateWebhook(dc.Spec.WebhookURL, dc.Spec.ChannelName); err != nil {
		logger.Error(err, "webhook validation failed")
		return ctrl.Result{RequeueAfter: 30 * time.Second},
			r.setDiscordStatus(ctx, dc, "Error", err.Error())
	}

	return ctrl.Result{}, r.setDiscordStatus(ctx, dc, "Ready", "webhook validated successfully")
}

// validateWebhook sends a silent ping to the Discord webhook to confirm it is
// reachable. The message uses allowed_mentions with no mentions so it does not
// notify anyone; content is kept minimal to avoid flooding the channel.
func (r *DiscordChannelReconciler) validateWebhook(webhookURL, channelName string) error {
	channel := channelName
	if channel == "" {
		channel = "unknown"
	}

	payload := map[string]interface{}{
		"content":          fmt.Sprintf("✅ Shop operator connected to channel **%s**", channel),
		"allowed_mentions": map[string]interface{}{"parse": []interface{}{}},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	resp, err := r.HTTPClient.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("POST to webhook: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (r *DiscordChannelReconciler) setDiscordStatus(
	ctx context.Context,
	dc *v1alpha1.DiscordChannel,
	status, message string,
) error {
	latest := &v1alpha1.DiscordChannel{}
	if err := r.Get(ctx, types.NamespacedName{Name: dc.Name, Namespace: dc.Namespace}, latest); err != nil {
		return err
	}
	latest.Status.Status = status
	latest.Status.Message = message
	return r.Status().Update(ctx, latest)
}
