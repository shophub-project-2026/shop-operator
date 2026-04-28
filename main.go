package main

import (
	"github.com/devops-milos/shop-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var setupLog = logf.Log.WithName("setup")

var scheme = runtime.NewScheme()

func init() {
	_ = v1alpha1.AddToScheme(scheme)
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

	setupLog.Info("manager created successfully")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
