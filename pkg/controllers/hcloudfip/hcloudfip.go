package hcloudfip

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hetznercloud/hcloud-go/hcloud"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/klog/v2"
)

type Controller struct {
	Client            *kubernetes.Clientset
	HClient           *hcloud.Client
	NodeLabelSelector string
	IPLabelSelector   string
	AssignmentLabel   string
}

// Start begins the reconciliation loop of Hetzner Cloud Floating IPs to Node assignments.
func (c *Controller) Start(ctx context.Context, lec *leaderelection.LeaderElectionConfig, group *sync.WaitGroup) error {
	lec.Callbacks = leaderelection.LeaderCallbacks{
		OnStartedLeading: func(ctx context.Context) {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			wait.Until(func() {
				err := c.runLeader(ctx)
				if err != nil {
					klog.Warning("reconciler encountered error: %v", err)
				}
			}, 10*time.Second, ctx.Done())
		},
		OnStoppedLeading: func() {},
		OnNewLeader:      func(leader string) {},
	}

	f, err := leaderelection.NewLeaderElector(*lec)
	if err != nil {
		return fmt.Errorf("hcloudfip election error: %w", err)
	}
	go func() {
		f.Run(ctx)
		group.Done()
	}()
	return nil
}

func (c *Controller) runLeader(ctx context.Context) error {
	fipsByAddr, serversByFIP, err := c.floatingIPs(ctx)
	if err != nil {
		return err
	}

	informerFactory := informers.NewSharedInformerFactoryWithOptions(c.Client, time.Minute, informers.WithTweakListOptions(func(options *metav1.ListOptions) {
		options.LabelSelector = c.NodeLabelSelector
	}))
	informerFactory.Start(wait.NeverStop)
	informerFactory.WaitForCacheSync(wait.NeverStop)

	nodeInformer := informerFactory.Core().V1().Nodes().Informer()

	nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			node := obj.(*v1.Node)
			if err := c.reconcileNode(ctx, node, fipsByAddr, serversByFIP); err != nil {
				klog.Warning("error reconciling node: %v", err)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldNode := oldObj.(*v1.Node)
			newNode := newObj.(*v1.Node)

			oldLabel, oldOk := oldNode.Labels[c.AssignmentLabel]
			newLabel, newOk := newNode.Labels[c.AssignmentLabel]

			if oldOk != newOk || oldLabel != newLabel {
				if err := c.reconcileNode(ctx, newNode, fipsByAddr, serversByFIP); err != nil {
					klog.Warning("error reconciling node: %v", err)
				}
			}
		},
	})

	nodeInformer.Run(ctx.Done())

	return nil
}

func (c *Controller) reconcileNode(ctx context.Context, node *v1.Node, fipsByAddr map[string]int, serversByFIP map[int]int) error {
	serverID := nodeServerID(node)
	if serverID == 0 {
		return errors.New("couldn't determine hcloud server from provider ID")
	}

	requestedFIP, ok := node.Labels[c.AssignmentLabel]
	if ok {
		fipID, ok := fipsByAddr[requestedFIP]
		if !ok {
			// Maybe generate a warning Event here?
			return errors.New("requested floating IP not found")
		}
		if serversByFIP[fipID] == serverID {
			return nil
		}
		assign, _, err := c.HClient.FloatingIP.Assign(ctx, &hcloud.FloatingIP{ID: fipID}, &hcloud.Server{ID: serverID})
		if err == nil {
			_, errCh := c.HClient.Action.WatchProgress(ctx, assign)
			err = <-errCh
		}
		if err != nil {
			return fmt.Errorf("error assigning floating IP: %w", err)
		}

		klog.Infof("assigned floating IP %s to node %s", requestedFIP, node.Name)
	} else {
		for fip, fipServer := range serversByFIP {
			if fipServer == serverID {
				assign, _, err := c.HClient.FloatingIP.Unassign(ctx, &hcloud.FloatingIP{ID: fip})
				if err == nil {
					_, errCh := c.HClient.Action.WatchProgress(ctx, assign)
					err = <-errCh
				}
				if err != nil {
					return fmt.Errorf("error assigning floating IP: %w", err)
				}
				klog.Infof("unassigned floating IP %s from node %s", requestedFIP, node.Name)
				break
			}
		}
	}

	return nil
}

// Fetches floating IPs matching given selector from the hcloud API.
// A map of floating IP addresses to IDs is returned, as well as a map of floating IP IDs to assigned server IDs.
func (c *Controller) floatingIPs(ctx context.Context) (map[string]int, map[int]int, error) {
	fipsByAddr := make(map[string]int)
	serversByFIP := make(map[int]int)

	fips, _, err := c.HClient.FloatingIP.List(ctx,
		hcloud.FloatingIPListOpts{ListOpts: hcloud.ListOpts{LabelSelector: c.IPLabelSelector}})
	if err != nil {
		return nil, nil, fmt.Errorf("error listing floating IPs: %w", err)
	}

	for _, fip := range fips {
		fipsByAddr[fip.IP.String()] = fip.ID
		if server := fip.Server; server != nil {
			serversByFIP[fip.ID] = server.ID
		}
	}

	return fipsByAddr, serversByFIP, nil
}

func nodeServerID(node *v1.Node) int {
	if !strings.HasPrefix(node.Spec.ProviderID, "hcloud://") {
		return 0
	}
	hcloudIDStr := strings.TrimPrefix(node.Spec.ProviderID, "hcloud://")
	hcloudID, err := strconv.Atoi(hcloudIDStr)
	if err != nil {
		return 0
	}

	return hcloudID
}
