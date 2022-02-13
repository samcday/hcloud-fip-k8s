package hcloudfip

import (
	"context"
	"errors"
	"fmt"
	"k8s.io/klog/v2"
	"strconv"
	"strings"
	"time"

	"github.com/hetznercloud/hcloud-go/hcloud"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type FloatingIPReconciler struct {
	client.Client
	HCloudClient    *hcloud.Client
	IPLabelSelector string
	AssignmentLabel string

	fipLastUpdate time.Time
	fipsByAddr    map[string]int
	serversByFIP  map[int]int
}

func (r *FloatingIPReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	r.refreshFloatingIPs(ctx)

	node := corev1.Node{}
	err := r.Get(ctx, req.NamespacedName, &node)
	if err != nil {
		return reconcile.Result{}, err
	}

	serverID := 0
	if strings.HasPrefix(node.Spec.ProviderID, "hcloud://") {
		serverIDStr := strings.TrimPrefix(node.Spec.ProviderID, "hcloud://")
		serverID, _ = strconv.Atoi(serverIDStr)
	}
	if serverID == 0 {
		return reconcile.Result{}, errors.New("error resolving server ID of node")
	}

	requestedFIP, ok := node.Labels[r.AssignmentLabel]
	if ok {
		fipID, ok := r.fipsByAddr[requestedFIP]
		if !ok {
			// Maybe generate a warning Event here?
			return reconcile.Result{}, errors.New("requested floating IP not found")
		}
		if r.serversByFIP[fipID] == serverID {
			return reconcile.Result{}, nil
		}
		assign, _, err := r.HCloudClient.FloatingIP.Assign(ctx, &hcloud.FloatingIP{ID: fipID}, &hcloud.Server{ID: serverID})
		if err == nil {
			_, errCh := r.HCloudClient.Action.WatchProgress(ctx, assign)
			err = <-errCh
		}
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("error assigning floating IP: %w", err)
		}

		r.serversByFIP[fipID] = serverID

		klog.Infof("assigned floating IP %s to node %s", requestedFIP, node.Name)
	} else {
		for fipID, fipServer := range r.serversByFIP {
			if fipServer == serverID {
				assign, _, err := r.HCloudClient.FloatingIP.Unassign(ctx, &hcloud.FloatingIP{ID: fipID})
				if err == nil {
					_, errCh := r.HCloudClient.Action.WatchProgress(ctx, assign)
					err = <-errCh
				}
				if err != nil {
					return reconcile.Result{}, fmt.Errorf("error assigning floating IP: %w", err)
				}
				klog.Infof("unassigned floating IP %s from node %s", requestedFIP, node.Name)

				delete(r.serversByFIP, fipID)

				break
			}
		}
	}

	return reconcile.Result{}, nil
}

// Rechecks floating IP status from hcloud API if it's too stale.
func (r *FloatingIPReconciler) refreshFloatingIPs(ctx context.Context) error {
	if time.Now().Sub(r.fipLastUpdate) < 15*time.Second {
		return nil
	}

	fips, _, err := r.HCloudClient.FloatingIP.List(ctx,
		hcloud.FloatingIPListOpts{ListOpts: hcloud.ListOpts{LabelSelector: r.IPLabelSelector}})
	if err != nil {
		return fmt.Errorf("error listing floating IPs: %w", err)
	}

	r.fipsByAddr = map[string]int{}
	r.serversByFIP = map[int]int{}

	for _, fip := range fips {
		r.fipsByAddr[fip.IP.String()] = fip.ID
		if server := fip.Server; server != nil {
			r.serversByFIP[fip.ID] = server.ID
		}
	}

	r.fipLastUpdate = time.Now()
	return nil
}

func (r *FloatingIPReconciler) InjectClient(c client.Client) error {
	r.Client = c
	return nil
}
