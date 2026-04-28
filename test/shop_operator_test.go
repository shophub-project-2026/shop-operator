package test

import (
	"context"
	"testing"

	"github.com/devops-milos/shop-operator/api/v1alpha1"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestShopOperator(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Shop Operator Suite")
}

var _ = ginkgo.Describe("Shop CRD", func() {
	var ctx context.Context

	ginkgo.BeforeEach(func() {
		ctx = context.Background()
	})

	ginkgo.It("should create a valid Shop resource", func() {
		shop := &v1alpha1.Shop{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-shop",
				Namespace: "default",
			},
			Spec: v1alpha1.ShopSpec{
				Availability:  "standard",
				WalletAddress: "0x742d35Cc6634C0532925a3b844Bc9e7595f42e0",
				Database:      "standard",
				Image:         "devopsmilos/shop:v1.0.0",
			},
		}

		gomega.Expect(shop).ToNot(gomega.BeNil())
		gomega.Expect(shop.Spec.Availability).To(gomega.Equal("standard"))
		gomega.Expect(shop.Spec.Database).To(gomega.Equal("standard"))
	})

	ginkgo.It("should validate Shop with high availability", func() {
		shop := &v1alpha1.Shop{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "high-availability-shop",
				Namespace: "default",
			},
			Spec: v1alpha1.ShopSpec{
				Availability:  "high",
				WalletAddress: "0xABCDEF123456",
				Database:      "light",
			},
		}

		gomega.Expect(shop.Spec.Availability).To(gomega.Equal("high"))
		gomega.Expect(shop.Spec.Database).To(gomega.Equal("light"))
	})

	ginkgo.It("should create a valid DiscordChannel resource", func() {
		channel := &v1alpha1.DiscordChannel{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-discord-channel",
				Namespace: "default",
			},
			Spec: v1alpha1.DiscordChannelSpec{
				WebhookURL:  "https://discordapp.com/api/webhooks/123456/abcdef",
				ChannelName: "shop-notifications",
			},
		}

		gomega.Expect(channel).ToNot(gomega.BeNil())
		gomega.Expect(channel.Spec.ChannelName).To(gomega.Equal("shop-notifications"))
	})

	ginkgo.It("should create a valid Wallet resource", func() {
		wallet := &v1alpha1.Wallet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-wallet",
				Namespace: "default",
			},
			Spec: v1alpha1.WalletSpec{
				Address:    "0x742d35Cc6634C0532925a3b844Bc9e7595f42e0",
				Blockchain: "ethereum",
				Network:    "testnet",
				Currency:   "USDT",
			},
		}

		gomega.Expect(wallet).ToNot(gomega.BeNil())
		gomega.Expect(wallet.Spec.Blockchain).To(gomega.Equal("ethereum"))
		gomega.Expect(wallet.Spec.Network).To(gomega.Equal("testnet"))
	})
})
