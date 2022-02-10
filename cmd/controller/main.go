package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

func start(ctx context.Context) error {
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		return errors.New("POD_NAME not specified")
	}
	podNamespace := os.Getenv("POD_NAMESPACE")
	if podNamespace == "" {
		return errors.New("POD_NAMESPACE not specified")
	}

	var config *rest.Config
	var err error
	if kubeconfigPath := os.Getenv("KUBECONFIG"); kubeconfigPath != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	} else {
		config, err = rest.InClusterConfig()
	}

	var clientset *kubernetes.Clientset
	if err == nil {
		clientset, err = kubernetes.NewForConfig(config)
	}

	if err != nil {
		return fmt.Errorf("kubernetes client init failed: %w", err)
	}

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock: &resourcelock.LeaseLock{
			LeaseMeta: metav1.ObjectMeta{
				Name:      "the-nat-controller",
				Namespace: podNamespace,
			},
			Client: clientset.CoordinationV1(),
			LockConfig: resourcelock.ResourceLockConfig{
				Identity: podName,
			}},
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				fmt.Println("I'm the boss.")
			},
			OnStoppedLeading: func() {
				fmt.Println("no longer boss sad")
			},
			OnNewLeader: func(leader string) {
				fmt.Printf("Boss is %s\n", leader)
			},
		},
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
	})

	return nil
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := start(ctx); err != nil {
		panic(err)
	}
}
