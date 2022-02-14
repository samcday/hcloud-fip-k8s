package main

import (
	"github.com/hetznercloud/hcloud-go/hcloud"
	"hcloud-the-nat-controller/pkg/controllers/hcloudfip"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

func main() {
	logf.SetLogger(zap.New())
	var log = logf.Log.WithName("nat-controller")

	token := os.Getenv("HCLOUD_TOKEN")
	if token == "" {
		log.Error(nil, "HCLOUD_TOKEN missing")
		os.Exit(1)
	}
	hcloudClient := hcloud.NewClient(hcloud.WithToken(token))

	fipLabelSelector := os.Getenv("FIP_SELECTOR")
	if fipLabelSelector == "" {
		log.Error(nil, "FIP_SELECTOR missing")
		os.Exit(1)
	}

	requestAnnotation := os.Getenv("REQUEST_ANNOTATION")
	if requestAnnotation == "" {
		log.Error(nil, "REQUEST_ANNOTATION missing")
		os.Exit(1)
	}

	assignmentLabel := os.Getenv("ASSIGNMENT_LABEL")
	if assignmentLabel == "" {
		log.Error(nil, "ASSIGNMENT_LABEL missing")
		os.Exit(1)
	}

	nodeLabelSelector := labels.Everything()
	if str := os.Getenv("NODE_SELECTOR"); str != "" {
		var err error
		nodeLabelSelector, err = labels.Parse(str)
		if err != nil {
			log.Error(err, "failed to parse NODE_SELECTOR")
			os.Exit(1)
		}
	}

	floatingIPReconciler := hcloudfip.FloatingIPReconciler{
		HCloudClient:      hcloudClient,
		AssignmentLabel:   assignmentLabel,
		IPLabelSelector:   fipLabelSelector,
		RequestAnnotation: requestAnnotation,
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
	})
	if err != nil {
		log.Error(err, "could not create manager")
		os.Exit(1)
	}

	err = builder.
		ControllerManagedBy(mgr).
		For(&corev1.Node{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(o client.Object) bool {
			return nodeLabelSelector.Matches(labels.Set(o.GetLabels()))
		}))).
		WithEventFilter(predicate.AnnotationChangedPredicate{}).
		Complete(&floatingIPReconciler)
	if err != nil {
		log.Error(err, "could not create controller")
		os.Exit(1)
	}

	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		log.Error(err, "could not start manager")
		os.Exit(1)
	}
}
