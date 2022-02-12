package cmd

import (
	"errors"
	"fmt"
	"github.com/hetznercloud/hcloud-go/hcloud"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"os"
	"time"
)

func LeaderElectorConfig(name string, clientset *kubernetes.Clientset) (*leaderelection.LeaderElectionConfig, error) {
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		return nil, errors.New("POD_NAME missing")
	}
	podNamespace := os.Getenv("POD_NAMESPACE")
	if podNamespace == "" {
		return nil, errors.New("POD_NAMESPACE missing")
	}
	return &leaderelection.LeaderElectionConfig{
		Lock: &resourcelock.LeaseLock{
			LeaseMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: podNamespace,
			},
			Client: clientset.CoordinationV1(),
			LockConfig: resourcelock.ResourceLockConfig{
				Identity: podName,
			}},
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
	}, nil
}

func KubeClient() (*kubernetes.Clientset, error) {
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
		return nil, fmt.Errorf("kubernetes client init failed: %w", err)
	}
	return clientset, nil
}

func HCloudClient() (*hcloud.Client, error) {
	token := os.Getenv("HCLOUD_TOKEN")
	if token == "" {
		return nil, errors.New("HCLOUD_TOKEN missing")
	}
	return hcloud.NewClient(hcloud.WithToken(token)), nil
}
