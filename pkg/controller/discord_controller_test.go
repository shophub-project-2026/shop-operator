package controller

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/devops-milos/shop-operator/api/v1alpha1"
)

// fakeDiscord is a test double for DiscordClient that records calls and hands
// back deterministic IDs so assertions don't depend on a live Discord server.
type fakeDiscord struct {
	createdChannels map[string]string // sanitized name -> channel ID
	createdWebhooks map[string]string // channel ID -> webhook ID
	deletedChannels []string
	failEnsureChan  bool
}

func newFakeDiscord() *fakeDiscord {
	return &fakeDiscord{
		createdChannels: map[string]string{},
		createdWebhooks: map[string]string{},
	}
}

func (f *fakeDiscord) EnsureChannel(guildID, name string) (string, error) {
	if f.failEnsureChan {
		return "", fmt.Errorf("boom")
	}
	if id, ok := f.createdChannels[name]; ok {
		return id, nil
	}
	id := "chan-" + name
	f.createdChannels[name] = id
	return id, nil
}

func (f *fakeDiscord) EnsureWebhook(channelID, name string) (string, string, error) {
	if id, ok := f.createdWebhooks[channelID]; ok {
		return id, webhookURL(id, "tok"), nil
	}
	id := "hook-" + channelID
	f.createdWebhooks[channelID] = id
	return id, webhookURL(id, "tok"), nil
}

func (f *fakeDiscord) DeleteChannel(channelID string) error {
	f.deletedChannels = append(f.deletedChannels, channelID)
	return nil
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add to scheme: %v", err)
	}
	return s
}

func reconcileDC(t *testing.T, r *DiscordChannelReconciler, name string) {
	t.Helper()
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

func TestDiscordChannel_BotCreatesChannelAndWebhook(t *testing.T) {
	scheme := testScheme(t)
	dc := &v1alpha1.DiscordChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "baki", Namespace: "default"},
		Spec:       v1alpha1.DiscordChannelSpec{ChannelName: "baki"},
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(dc).
		WithStatusSubresource(&v1alpha1.DiscordChannel{}).
		Build()

	disc := newFakeDiscord()
	r := &DiscordChannelReconciler{Client: cl, Scheme: scheme, Discord: disc, DefaultGuildID: "guild-1"}

	reconcileDC(t, r, "baki")

	got := &v1alpha1.DiscordChannel{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "baki", Namespace: "default"}, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Status != "Ready" {
		t.Errorf("status: want Ready, got %q (%s)", got.Status.Status, got.Status.Message)
	}
	if got.Status.ChannelID != "chan-shop-baki" {
		t.Errorf("channelId: got %q", got.Status.ChannelID)
	}
	want := webhookURL("hook-chan-shop-baki", "tok")
	if got.Status.WebhookURL != want {
		t.Errorf("webhookUrl: want %q, got %q", want, got.Status.WebhookURL)
	}
	if !controllerutil.ContainsFinalizer(got, discordFinalizer) {
		t.Error("finalizer not added")
	}
	if _, ok := disc.createdChannels["shop-baki"]; !ok {
		t.Error("channel shop-baki was not created on Discord")
	}
}

func TestDiscordChannel_ManualOverrideSkipsBot(t *testing.T) {
	scheme := testScheme(t)
	override := "https://discord.com/api/webhooks/123/abc"
	dc := &v1alpha1.DiscordChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "ovr", Namespace: "default"},
		Spec:       v1alpha1.DiscordChannelSpec{ChannelName: "ovr", WebhookURL: override},
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).WithObjects(dc).
		WithStatusSubresource(&v1alpha1.DiscordChannel{}).Build()

	disc := newFakeDiscord()
	r := &DiscordChannelReconciler{Client: cl, Scheme: scheme, Discord: disc, DefaultGuildID: "guild-1"}

	reconcileDC(t, r, "ovr")

	got := &v1alpha1.DiscordChannel{}
	_ = cl.Get(context.Background(), types.NamespacedName{Name: "ovr", Namespace: "default"}, got)
	if got.Status.WebhookURL != override {
		t.Errorf("override webhook: want %q, got %q", override, got.Status.WebhookURL)
	}
	if len(disc.createdChannels) != 0 {
		t.Errorf("bot should not create channels in override mode, created %v", disc.createdChannels)
	}
}

func TestDiscordChannel_NotConfiguredReportsError(t *testing.T) {
	scheme := testScheme(t)
	dc := &v1alpha1.DiscordChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "noconf", Namespace: "default"},
		Spec:       v1alpha1.DiscordChannelSpec{ChannelName: "noconf"},
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).WithObjects(dc).
		WithStatusSubresource(&v1alpha1.DiscordChannel{}).Build()

	// No Discord client and no guild configured.
	r := &DiscordChannelReconciler{Client: cl, Scheme: scheme}

	reconcileDC(t, r, "noconf")

	got := &v1alpha1.DiscordChannel{}
	_ = cl.Get(context.Background(), types.NamespacedName{Name: "noconf", Namespace: "default"}, got)
	if got.Status.Status != "Error" {
		t.Errorf("status: want Error, got %q", got.Status.Status)
	}
	if got.Status.WebhookURL != "" {
		t.Errorf("no webhook should be resolved, got %q", got.Status.WebhookURL)
	}
}

func TestDiscordChannel_FinalizerDeletesChannel(t *testing.T) {
	scheme := testScheme(t)
	now := metav1.Now()
	dc := &v1alpha1.DiscordChannel{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "gone",
			Namespace:         "default",
			Finalizers:        []string{discordFinalizer},
			DeletionTimestamp: &now,
		},
		Spec:   v1alpha1.DiscordChannelSpec{ChannelName: "gone"},
		Status: v1alpha1.DiscordChannelStatus{ChannelID: "chan-shop-gone"},
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).WithObjects(dc).
		WithStatusSubresource(&v1alpha1.DiscordChannel{}).Build()

	disc := newFakeDiscord()
	r := &DiscordChannelReconciler{Client: cl, Scheme: scheme, Discord: disc, DefaultGuildID: "guild-1"}

	reconcileDC(t, r, "gone")

	if len(disc.deletedChannels) != 1 || disc.deletedChannels[0] != "chan-shop-gone" {
		t.Errorf("expected channel chan-shop-gone deleted, got %v", disc.deletedChannels)
	}
}
