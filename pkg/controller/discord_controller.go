package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/devops-milos/shop-operator/api/v1alpha1"
)

// discordFinalizer guards the bot-created Discord channel so it is removed from
// the server when the DiscordChannel CR is deleted, preventing orphaned
// channels from accumulating in the guild.
const discordFinalizer = "shop.devops.io/discord-channel"

type DiscordChannelReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Discord performs the channel/webhook operations. May be nil when no bot
	// token is configured; in that case only the manual webhookUrl override is
	// supported and bot-driven creation reports a configuration error.
	Discord DiscordClient
	// DefaultGuildID is the guild used when a DiscordChannel does not set
	// spec.guildId. Sourced from the operator's DISCORD_GUILD_ID.
	DefaultGuildID string
}

func (r *DiscordChannelReconciler) SetupWithManager(mgr ctrl.Manager) error {
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

	// Handle deletion: tear down the Discord channel, then drop the finalizer.
	if !dc.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(dc, discordFinalizer) {
			if err := r.finalize(ctx, dc); err != nil {
				logger.Error(err, "finalize discord channel")
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(dc, discordFinalizer)
			if err := r.Update(ctx, dc); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if controllerutil.AddFinalizer(dc, discordFinalizer) {
		if err := r.Update(ctx, dc); err != nil {
			return ctrl.Result{}, err
		}
	}

	logger.Info("Reconciling DiscordChannel", "name", dc.Name, "namespace", dc.Namespace)

	// Manual override: the user supplied a webhook URL, so skip bot-driven
	// creation and route straight to it. Guarded so we don't rewrite status
	// (and retrigger reconcile) once it already reflects the override.
	if dc.Spec.WebhookURL != "" {
		if dc.Status.WebhookURL == dc.Spec.WebhookURL && dc.Status.Status == "Ready" {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, r.setResolved(ctx, dc, "", "", dc.Spec.WebhookURL,
			"Ready", "using manual webhook override")
	}

	// Already resolved on a previous reconcile — nothing to do.
	if dc.Status.ChannelID != "" && dc.Status.WebhookURL != "" {
		return ctrl.Result{}, nil
	}

	guildID := dc.Spec.GuildID
	if guildID == "" {
		guildID = r.DefaultGuildID
	}
	if r.Discord == nil || guildID == "" {
		return ctrl.Result{}, r.setStatus(ctx, dc, "Error",
			"discord bot not configured (set DISCORD_BOT_TOKEN and DISCORD_GUILD_ID) and no webhookUrl override provided")
	}

	channelName := dc.Spec.ChannelName
	if channelName == "" {
		channelName = dc.Name
	}
	channelName = "shop-" + channelName

	channelID, err := r.Discord.EnsureChannel(guildID, channelName)
	if err != nil {
		logger.Error(err, "ensure channel")
		return ctrl.Result{}, r.setStatus(ctx, dc, "Error", fmt.Sprintf("create channel: %v", err))
	}

	webhookID, url, err := r.Discord.EnsureWebhook(channelID, "shop-alerts")
	if err != nil {
		logger.Error(err, "ensure webhook")
		return ctrl.Result{}, r.setStatus(ctx, dc, "Error", fmt.Sprintf("create webhook: %v", err))
	}

	return ctrl.Result{}, r.setResolved(ctx, dc, channelID, webhookID, url,
		"Ready", fmt.Sprintf("channel %s provisioned", channelName))
}

// finalize deletes the bot-created channel. Manual-override channels (no
// ChannelID recorded) are left untouched since the operator did not create them.
func (r *DiscordChannelReconciler) finalize(ctx context.Context, dc *v1alpha1.DiscordChannel) error {
	if dc.Status.ChannelID == "" || r.Discord == nil {
		return nil
	}
	return r.Discord.DeleteChannel(dc.Status.ChannelID)
}

func (r *DiscordChannelReconciler) setStatus(
	ctx context.Context, dc *v1alpha1.DiscordChannel, status, message string,
) error {
	return r.patchStatus(ctx, dc, func(latest *v1alpha1.DiscordChannel) {
		latest.Status.Status = status
		latest.Status.Message = message
	})
}

func (r *DiscordChannelReconciler) setResolved(
	ctx context.Context, dc *v1alpha1.DiscordChannel,
	channelID, webhookID, webhookURL, status, message string,
) error {
	return r.patchStatus(ctx, dc, func(latest *v1alpha1.DiscordChannel) {
		latest.Status.ChannelID = channelID
		latest.Status.WebhookID = webhookID
		latest.Status.WebhookURL = webhookURL
		latest.Status.Status = status
		latest.Status.Message = message
	})
}

func (r *DiscordChannelReconciler) patchStatus(
	ctx context.Context, dc *v1alpha1.DiscordChannel, mutate func(*v1alpha1.DiscordChannel),
) error {
	latest := &v1alpha1.DiscordChannel{}
	if err := r.Get(ctx, types.NamespacedName{Name: dc.Name, Namespace: dc.Namespace}, latest); err != nil {
		return err
	}
	mutate(latest)
	return r.Status().Update(ctx, latest)
}
