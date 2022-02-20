package main

import (
	"flag"
	"github.com/hetznercloud/hcloud-go/hcloud"
	"k8s.io/apimachinery/pkg/runtime"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"the-nat-controller/api/v1alpha1"
	"the-nat-controller/controllers/hcloudfip"
	"the-nat-controller/controllers/natgateway"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// leaderelection needs this RBAC
// +kubebuilder:rbac:namespace="{{.Release.Namespace}}",groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:namespace="{{.Release.Namespace}}",groups="coordination.k8s.io",resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:namespace="{{.Release.Namespace}}",groups="",resources=events,verbs=create;patch

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

func main() {
	var configFile string
	flag.StringVar(&configFile, "config", "",
		"The controller will load its initial configuration from this file. "+
			"Omit this flag to use the default configuration values. "+
			"Command-line flags override configuration from this file.")
	logOpts := zap.Options{
		Development: true,
	}
	logOpts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&logOpts)))

	var err error
	ctrlConfig := v1alpha1.Config{}
	options := ctrl.Options{Scheme: scheme}
	if configFile != "" {
		options, err = options.AndFrom(ctrl.ConfigFile().AtPath(configFile).OfKind(&ctrlConfig))
		if err != nil {
			setupLog.Error(err, "unable to load the config file")
			os.Exit(1)
		}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), options)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	hcloudToken := ctrlConfig.HCloud.Token
	if fromEnv := os.Getenv("HCLOUD_TOKEN"); fromEnv != "" {
		hcloudToken = fromEnv
	}
	if hcloudToken == "" {
		setupLog.Error(nil, "no hcloud token provided")
		os.Exit(1)
	}
	opts := []hcloud.ClientOption{
		// TODO: figure out how to mush the controller runtime metrics Registry into hcloud-go?
		//hcloud.WithInstrumentation(metrics.Registry),
		hcloud.WithToken(hcloudToken),
	}

	if ctrlConfig.HCloud.Endpoint != "" {
		setupLog.Info("using custom hcloud endpoint")
		opts = append(opts, hcloud.WithEndpoint(ctrlConfig.HCloud.Endpoint))
	}
	if ctrlConfig.HCloud.PollInterval > 0 {
		setupLog.Info("using custom hcloud poll interval", "interval", ctrlConfig.HCloud.PollInterval)
		opts = append(opts, hcloud.WithPollInterval(time.Duration(ctrlConfig.HCloud.PollInterval)*time.Millisecond))
	}

	hcloudClient := hcloud.NewClient(opts...)

	if err = (&hcloudfip.Reconciler{
		Client: mgr.GetClient(),
		Config: ctrlConfig,
		HCloud: hcloudClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "hcloudfip")
		os.Exit(1)
	}

	if err = (&natgateway.Reconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "natgateway")
		os.Exit(1)
	}

	if err = (&natgateway.Reconciler{
		Client: mgr.GetClient(),
		Config: ctrlConfig,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "could not create natgateway controller")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}

	//logf.SetLogger(zap.New(zap.UseDevMode(true), zap.Level(zapcore.DebugLevel)))
	//var log = logf.Log.WithName("nat-controller")
	//
	//
	//
	//podName := os.Getenv("POD_NAME")
	//if podName == "" {
	//	log.Error(nil, "POD_NAME missing")
	//	os.Exit(1)
	//}
	//podNamespace := os.Getenv("POD_NAMESPACE")
	//if podNamespace == "" {
	//	log.Error(nil, "POD_NAMESPACE missing")
	//	os.Exit(1)
	//}

}
