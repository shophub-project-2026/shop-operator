package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// SchemeGroupVersion is group version used to register these objects
var SchemeGroupVersion = schema.GroupVersion{Group: "shop.devops.io", Version: "v1alpha1"}

var SchemeBuilder runtime.SchemeBuilder
var AddToScheme = SchemeBuilder.AddToScheme

func init() {
	SchemeBuilder.Register(registerShop, registerDiscordChannel, registerWallet)
}

func registerShop(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion, &Shop{}, &ShopList{})
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

func registerDiscordChannel(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion, &DiscordChannel{}, &DiscordChannelList{})
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

func registerWallet(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion, &Wallet{}, &WalletList{})
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.status.replicas`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`

type Shop struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ShopSpec   `json:"spec,omitempty"`
	Status            ShopStatus `json:"status,omitempty"`
}

type ShopSpec struct {
	// Availability tier: standard runs 2 replicas, high runs 3 (§1.2/§3.1).
	// +kubebuilder:validation:Enum=standard;high
	// +kubebuilder:default=standard
	Availability string `json:"availability,omitempty"`
	// WalletAddress is the merchant's Ethereum address that customer
	// payments are sent to. Must be a 0x-prefixed 20-byte hex string
	// (EIP-55 mixed case is accepted; checksum is not enforced here).
	// +kubebuilder:validation:Pattern=`^0x[a-fA-F0-9]{40}$`
	WalletAddress string `json:"walletAddress"`
	// Database tier: standard deploys PostgreSQL (CNPG operator), light
	// deploys Redis (Redis operator) — §1.2.
	// +kubebuilder:validation:Enum=standard;light
	// +kubebuilder:default=standard
	Database string `json:"database,omitempty"`
	Image         string `json:"image,omitempty"`
	// NotificationWebhook is an optional Discord webhook URL. When set, the
	// operator provisions a DiscordChannel plus an Alertmanager route so that
	// this shop's alerts are delivered to its own Discord channel.
	NotificationWebhook string `json:"notificationWebhook,omitempty"`
}

type ShopStatus struct {
	Phase         string `json:"phase,omitempty"`
	Replicas      int32  `json:"replicas,omitempty"`
	ReadyReplicas int32  `json:"readyReplicas,omitempty"`
	DatabaseReady bool   `json:"databaseReady,omitempty"`
	ServiceURL    string `json:"serviceUrl,omitempty"`
}

func (s *Shop) DeepCopyInto(out *Shop) {
	*out = *s
	s.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = s.Spec
	out.Status = s.Status
}

func (s *Shop) DeepCopy() *Shop {
	if s == nil {
		return nil
	}
	out := new(Shop)
	s.DeepCopyInto(out)
	return out
}

func (s *Shop) DeepCopyObject() runtime.Object {
	return s.DeepCopy()
}

// +kubebuilder:object:root=true

type ShopList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Shop `json:"items"`
}

func (sl *ShopList) DeepCopyInto(out *ShopList) {
	*out = *sl
	sl.ListMeta.DeepCopyInto(&out.ListMeta)
	if sl.Items != nil {
		in, out := &sl.Items, &out.Items
		*out = make([]Shop, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

func (sl *ShopList) DeepCopy() *ShopList {
	if sl == nil {
		return nil
	}
	out := new(ShopList)
	sl.DeepCopyInto(out)
	return out
}

func (sl *ShopList) DeepCopyObject() runtime.Object {
	return sl.DeepCopy()
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`

type DiscordChannel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DiscordChannelSpec   `json:"spec,omitempty"`
	Status            DiscordChannelStatus `json:"status,omitempty"`
}

type DiscordChannelSpec struct {
	// ChannelName is the name of the Discord text channel the operator creates
	// in the guild for this shop's notifications.
	ChannelName string `json:"channelName,omitempty"`
	// GuildID overrides the operator's default guild for this channel. When
	// empty, the operator's configured DISCORD_GUILD_ID is used.
	GuildID string `json:"guildId,omitempty"`
	// WebhookURL is an optional manual override. When set, the operator skips
	// bot-driven channel/webhook creation and routes to this URL directly,
	// which lets a shop reuse a pre-existing Discord channel.
	WebhookURL string `json:"webhookUrl,omitempty"`
}

type DiscordChannelStatus struct {
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	// ChannelID is the Discord channel the operator created (empty when a manual
	// WebhookURL override is used).
	ChannelID string `json:"channelId,omitempty"`
	// WebhookID is the Discord webhook created on the channel.
	WebhookID string `json:"webhookId,omitempty"`
	// WebhookURL is the resolved webhook alerts are delivered to — bot-created
	// or taken from the spec override. Downstream resources (the per-shop Secret
	// and AlertmanagerConfig) consume this value.
	WebhookURL string `json:"webhookUrl,omitempty"`
}

func (dc *DiscordChannel) DeepCopyInto(out *DiscordChannel) {
	*out = *dc
	dc.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
}

func (dc *DiscordChannel) DeepCopy() *DiscordChannel {
	if dc == nil {
		return nil
	}
	out := new(DiscordChannel)
	dc.DeepCopyInto(out)
	return out
}

func (dc *DiscordChannel) DeepCopyObject() runtime.Object {
	return dc.DeepCopy()
}

// +kubebuilder:object:root=true

type DiscordChannelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DiscordChannel `json:"items"`
}

func (dcl *DiscordChannelList) DeepCopyInto(out *DiscordChannelList) {
	*out = *dcl
	dcl.ListMeta.DeepCopyInto(&out.ListMeta)
	if dcl.Items != nil {
		in, out := &dcl.Items, &out.Items
		*out = make([]DiscordChannel, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

func (dcl *DiscordChannelList) DeepCopy() *DiscordChannelList {
	if dcl == nil {
		return nil
	}
	out := new(DiscordChannelList)
	dcl.DeepCopyInto(out)
	return out
}

func (dcl *DiscordChannelList) DeepCopyObject() runtime.Object {
	return dcl.DeepCopy()
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="Blockchain",type=string,JSONPath=`.spec.blockchain`
// +kubebuilder:printcolumn:name="Address",type=string,JSONPath=`.status.address`

type Wallet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              WalletSpec   `json:"spec,omitempty"`
	Status            WalletStatus `json:"status,omitempty"`
}

type WalletSpec struct {
	// Address is the blockchain account to adopt. When empty the operator
	// CREATES a new account on the chain: it generates a keypair, stores it in
	// the Secret <wallet-name>-keys and publishes the derived address in
	// status.address (spec §3.1 — "Wallet: Kreira account na blockchain-u").
	Address    string `json:"address,omitempty"`
	Blockchain string `json:"blockchain"`
	Network    string `json:"network,omitempty"`
	Currency   string `json:"currency,omitempty"`
}

type WalletStatus struct {
	Status string `json:"status,omitempty"`
	// Address is the account customers pay into: either adopted from
	// spec.address or derived from the operator-generated keypair.
	Address string `json:"address,omitempty"`
	Balance string `json:"balance,omitempty"`
	Message string `json:"message,omitempty"`
}

func (w *Wallet) DeepCopyInto(out *Wallet) {
	*out = *w
	w.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
}

func (w *Wallet) DeepCopy() *Wallet {
	if w == nil {
		return nil
	}
	out := new(Wallet)
	w.DeepCopyInto(out)
	return out
}

func (w *Wallet) DeepCopyObject() runtime.Object {
	return w.DeepCopy()
}

// +kubebuilder:object:root=true

type WalletList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Wallet `json:"items"`
}

func (wl *WalletList) DeepCopyInto(out *WalletList) {
	*out = *wl
	wl.ListMeta.DeepCopyInto(&out.ListMeta)
	if wl.Items != nil {
		in, out := &wl.Items, &out.Items
		*out = make([]Wallet, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

func (wl *WalletList) DeepCopy() *WalletList {
	if wl == nil {
		return nil
	}
	out := new(WalletList)
	wl.DeepCopyInto(out)
	return out
}

func (wl *WalletList) DeepCopyObject() runtime.Object {
	return wl.DeepCopy()
}
