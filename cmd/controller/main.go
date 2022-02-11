package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/vishvananda/netlink"
	"net"
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
	pubIfaceName := os.Getenv("PUB_IFACE")
	if pubIfaceName == "" {
		return errors.New("PUB_IFACE not specified")
	}
	pubAddrStr := os.Getenv("PUB_ADDR")
	if pubAddrStr == "" {
		return errors.New("PUB_ADDR not specified")
	}

	//privIfaceName := os.Getenv("PRIV_IFACE")
	//if privIfaceName == "" {
	//	return errors.New("PRIV_IFACE not specified")
	//}

	pubAddr := net.ParseIP(pubAddrStr)
	if pubAddr == nil {
		return errors.New("PUB_ADDR is not a valid IPv4 address")
	}
	pubIface, err := netlink.LinkByName(pubIfaceName)
	if err != nil {
		return fmt.Errorf("failed to lookup PUB_IFACE: %w", err)
	}

	if err := useNAT(pubIface, pubAddr); err != nil {
		return err
	}

	return nil

	var config *rest.Config
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

func becomeNAT() {

}

func useNAT(pubIface netlink.Link, floatingIP net.IP) error {
	if err := metadataSvcRoute(pubIface); err != nil {
		return fmt.Errorf("error setting metadata route: %w", err)
	}

	if err := unassignFloatingIp(pubIface, floatingIP); err != nil {
		return fmt.Errorf("unassign floating IP failed: %w", err)
	}

	return nil
}

// Remove floating IP from public interface, if assigned.
// `ip addr del <ip> dev <iface>`
func unassignFloatingIp(pubIface netlink.Link, floatingIp net.IP) error {
	addrs, err := netlink.AddrList(pubIface, netlink.FAMILY_V4)
	if err != nil {
		return err
	}
	for _, addr := range addrs {
		if !addr.IPNet.IP.Equal(floatingIp) {
			continue
		}
		_, bits := addr.Mask.Size()
		if bits != 32 {
			continue
		}
		return netlink.AddrDel(pubIface, &addr)
	}
	return nil
}

// Ensure metadata service goes via public interface
// Analogous to `ip route replace 169.254.169.254 scope link`
func metadataSvcRoute(pubIface netlink.Link) error {
	metadataIP, err := netlink.ParseIPNet("169.254.169.254/32")
	if err != nil {
		return err
	}
	return netlink.RouteReplace(&netlink.Route{
		LinkIndex: pubIface.Attrs().Index,
		Dst:       metadataIP,
		Scope:     netlink.SCOPE_LINK,
	})
}
