package main

import (
	"github.com/hetznercloud/hcloud-go/hcloud"
	"go.uber.org/zap/zapcore"
	"hcloud-the-nat-controller/pkg/controllers/hcloudfip"
	"hcloud-the-nat-controller/pkg/controllers/natgateway"
	"k8s.io/apimachinery/pkg/labels"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

func main() {
	logf.SetLogger(zap.New(zap.UseDevMode(true), zap.Level(zapcore.DebugLevel)))
	var log = logf.Log.WithName("nat-controller")

	token := os.Getenv("HCLOUD_TOKEN")
	if token == "" {
		log.Error(nil, "HCLOUD_TOKEN missing")
		os.Exit(1)
	}
	hcloudClient := hcloud.NewClient(hcloud.WithToken(token))

	fipSelector := os.Getenv("FIP_SELECTOR")
	if fipSelector == "" {
		log.Error(nil, "FIP_SELECTOR missing")
		os.Exit(1)
	}

	fipReqAnnotation := os.Getenv("REQUEST_ANNOTATION")
	if fipReqAnnotation == "" {
		log.Error(nil, "REQUEST_ANNOTATION missing")
		os.Exit(1)
	}

	fipLabel := os.Getenv("FIP_ASSIGNMENT_LABEL")
	if fipLabel == "" {
		log.Error(nil, "FIP_ASSIGNMENT_LABEL missing")
		os.Exit(1)
	}

	nodeSelector := labels.Everything()
	if str := os.Getenv("NODE_SELECTOR"); str != "" {
		var err error
		nodeSelector, err = labels.Parse(str)
		if err != nil {
			log.Error(err, "failed to parse NODE_SELECTOR")
			os.Exit(1)
		}
	}

	podName := os.Getenv("POD_NAME")
	if podName == "" {
		log.Error(nil, "POD_NAME missing")
		os.Exit(1)
	}
	podNamespace := os.Getenv("POD_NAMESPACE")
	if podNamespace == "" {
		log.Error(nil, "POD_NAMESPACE missing")
		os.Exit(1)
	}

	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{
		LeaderElection:                true,
		LeaderElectionNamespace:       podNamespace,
		LeaderElectionID:              "the-nat-controller",
		LeaderElectionReleaseOnCancel: true,
		MetricsBindAddress:            os.Getenv("METRICS_LISTEN"),
	})
	if err != nil {
		log.Error(err, "could not create manager")
		os.Exit(1)
	}

	err = hcloudfip.Run(mgr, hcloudClient, fipSelector, nodeSelector, fipLabel, fipReqAnnotation)
	if err != nil {
		log.Error(err, "could not create hcloudfip controller")
		os.Exit(1)
	}

	err = natgateway.Run(mgr, natgateway.Options{
		FIPAssignLabel:  fipLabel,
		Namespace:       podNamespace,
		SetupAnnotation: "setup",
		SetupJobTTL:     60,
	})
	if err != nil {
		log.Error(err, "could not create natgateway controller")
		os.Exit(1)
	}

	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		log.Error(err, "could not start manager")
		os.Exit(1)
	}
}
