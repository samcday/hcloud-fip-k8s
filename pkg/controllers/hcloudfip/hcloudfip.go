package hcloudfip

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hetznercloud/hcloud-go/hcloud"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var log = logf.Log.WithName("hcloudfip")

type FloatingIPReconciler struct {
	client.Client
	HCloudClient      *hcloud.Client
	IPLabelSelector   string
	RequestAnnotation string
	AssignmentLabel   string

	fipLastUpdate time.Time
	fipsByAddr    map[string]int
	serversByFIP  map[int]int
}

func (r *FloatingIPReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	node := &corev1.Node{}
	err := r.Get(ctx, req.NamespacedName, node)
	if err != nil {
		return reconcile.Result{}, err
	}
	log.V(1).Info("starting reconcile", "request", req)

	err = r.refreshFloatingIPs(ctx)
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

	labelValue, labelPresent := node.Labels[r.AssignmentLabel]

	requestedFIP, ok := node.Annotations[r.RequestAnnotation]
	if ok {
		// Annotation requesting floating IP is present, ensure IP is assigned.
		fipID, ok := r.fipsByAddr[requestedFIP]
		if !ok {
			// IP not found. Maybe generate a warning Event here?
			return reconcile.Result{}, errors.New("requested floating IP not found")
		}

		if r.serversByFIP[fipID] != serverID {
			action, _, err := r.HCloudClient.FloatingIP.Assign(ctx, &hcloud.FloatingIP{ID: fipID}, &hcloud.Server{ID: serverID})
			err = r.waitForAction(ctx, action, err)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("error assigning floating IP: %w", err)
			}

			log.Info("assigned floating IP to node", "floating-ip", requestedFIP, "node", node.Name)
			r.serversByFIP[fipID] = serverID
		}
		if labelValue != requestedFIP {
			patch := client.MergeFrom(node.DeepCopy())
			node.Labels[r.AssignmentLabel] = requestedFIP
			err := r.Patch(ctx, node, patch)
			if err != nil {
				return reconcile.Result{}, err
			}
		}

		// While a Node has a floating IP attached, queue regular reconciles to make sure it stays that way.
		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Annotation not present. If label is, then unassign floating IP and remove label.
	if !labelPresent {
		return reconcile.Result{}, nil
	}

	// Look up floating IP referenced by label. If it exists and is still assigned to the Node, unassign it.
	if fipID, ok := r.fipsByAddr[labelValue]; ok && r.serversByFIP[fipID] == serverID {
		action, _, err := r.HCloudClient.FloatingIP.Unassign(ctx, &hcloud.FloatingIP{ID: fipID})
		err = r.waitForAction(ctx, action, err)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("error unassigning floating IP: %w", err)
		}

		log.Info("unassigned floating IP from node", "floating-ip", requestedFIP, "node", node.Name)
		delete(r.serversByFIP, fipID)
	} else {
		// Should log a Warning event here.
	}

	// Whether we actually unassigned something or not, the label goes away now.
	patch := client.MergeFrom(node.DeepCopy())
	delete(node.Labels, r.AssignmentLabel)
	err = r.Patch(ctx, node, patch)

	// No floating IP assigned, no requeue requested here.
	return reconcile.Result{}, err
}

// Rechecks floating IP status from hcloud API if it's too stale.
func (r *FloatingIPReconciler) refreshFloatingIPs(ctx context.Context) error {
	if time.Now().Sub(r.fipLastUpdate) < 15*time.Second {
		return nil
	}

	page := 1

	for page > 0 {
		fips, resp, err := r.HCloudClient.FloatingIP.List(ctx,
			hcloud.FloatingIPListOpts{ListOpts: hcloud.ListOpts{Page: page, LabelSelector: r.IPLabelSelector}})
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

		page = resp.Meta.Pagination.NextPage
	}

	r.fipLastUpdate = time.Now()
	return nil
}

func (r *FloatingIPReconciler) InjectClient(c client.Client) error {
	r.Client = c
	return nil
}

func (r *FloatingIPReconciler) waitForAction(ctx context.Context, action *hcloud.Action, err error) error {
	if err == nil {
		_, errCh := r.HCloudClient.Action.WatchProgress(ctx, action)
		select {
		case <-ctx.Done():
		case err = <-errCh:
		}
	}
	return err
}
