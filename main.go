package main

import (
	"os"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/devops-milos/shop-operator/api/v1alpha1"
	"github.com/devops-milos/shop-operator/pkg/controller"
)

var (
	setupLog = logf.Log.WithName("setup")
	scheme   = runtime.NewScheme()
)

func init() {
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(networkingv1.AddToScheme(scheme))
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	mgr, err := manager.New(ctrl.GetConfigOrDie(), manager.Options{
		Scheme: scheme,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controller.ShopReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create ShopReconciler")
		os.Exit(1)
	}

	// Discord bot is optional: without a token the operator still runs and the
	// manual webhookUrl override path works; only bot-driven channel creation
	// is disabled (DiscordChannel CRs then report a configuration error).
	var discordClient controller.DiscordClient
	if token := os.Getenv("DISCORD_BOT_TOKEN"); token != "" {
		discordClient, err = controller.NewDiscordGoClient(token)
		if err != nil {
			setupLog.Error(err, "unable to create Discord client")
			os.Exit(1)
		}
	} else {
		setupLog.Info("DISCORD_BOT_TOKEN not set; bot channel creation disabled (manual webhookUrl override still works)")
	}

	if err = (&controller.DiscordChannelReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		Discord:        discordClient,
		DefaultGuildID: os.Getenv("DISCORD_GUILD_ID"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create DiscordChannelReconciler")
		os.Exit(1)
	}

	// Wallet accounts live on Ethereum Sepolia by default; ETH_RPC_URL lets a
	// deployment point balance lookups at a different JSON-RPC endpoint.
	rpcURL := os.Getenv("ETH_RPC_URL")
	if rpcURL == "" {
		rpcURL = "https://ethereum-sepolia-rpc.publicnode.com"
	}
	if err = (&controller.WalletReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Balances: controller.NewEthRPCClient(rpcURL),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create WalletReconciler")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
