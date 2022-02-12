package main

import (
	"context"
	"hcloud-the-nat-controller/pkg/cmd"
	"hcloud-the-nat-controller/pkg/controllers/hcloudfip"
	"os"
	"sync"
)

func start(ctx context.Context) error {
	//pubIfaceName := os.Getenv("PUB_IFACE")
	//if pubIfaceName == "" {
	//	return errors.New("PUB_IFACE not specified")
	//}
	//pubAddrStr := os.Getenv("PUB_ADDR")
	//if pubAddrStr == "" {
	//	return errors.New("PUB_ADDR not specified")
	//}
	//
	////privIfaceName := os.Getenv("PRIV_IFACE")
	////if privIfaceName == "" {
	////	return errors.New("PRIV_IFACE not specified")
	////}
	//
	//pubAddr := net.ParseIP(pubAddrStr)
	//if pubAddr == nil {
	//	return errors.New("PUB_ADDR is not a valid IPv4 address")
	//}
	//pubIface, err := netlink.LinkByName(pubIfaceName)
	//if err != nil {
	//	return fmt.Errorf("failed to lookup PUB_IFACE: %w", err)
	//}

	return nil
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientset, err := cmd.KubeClient()
	if err != nil {
		panic(err)
	}

	hcloudClient, err := cmd.HCloudClient()
	if err != nil {
		panic(err)
	}

	fipLabelSelector := os.Getenv("FIP_LABEL_SELECTOR")
	if fipLabelSelector == "" {
		panic("FIP_LABEL_SELECTOR missing")
	}

	assignmentLabel := os.Getenv("NODE_LABEL")
	if assignmentLabel == "" {
		panic("NODE_LABEL missing")
	}

	nodeLabelSelector := os.Getenv("NODE_LABEL_SELECTOR")

	lec, err := cmd.LeaderElectorConfig("the-nat-controller", clientset)
	if err != nil {
		panic(err)
	}

	wg := &sync.WaitGroup{}

	fip := hcloudfip.Controller{
		Client:            clientset,
		HClient:           hcloudClient,
		IPLabelSelector:   fipLabelSelector,
		NodeLabelSelector: nodeLabelSelector,
		AssignmentLabel:   assignmentLabel,
	}

	err = fip.Start(ctx, lec, wg)
	if err != nil {
		panic(err)
	}
	wg.Add(1)

	wg.Wait()
}
