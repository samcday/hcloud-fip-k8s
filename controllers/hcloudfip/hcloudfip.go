package hcloudfip

import (
	"context"
	"errors"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"strconv"
	"strings"
	"the-nat-controller/api/v1alpha1"
	"time"

	"github.com/hetznercloud/hcloud-go/hcloud"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch

var log = logf.Log.WithName("hcloudfip")

type Reconciler struct {
	client.Client
	v1alpha1.Config
	HCloud *hcloud.Client

	fipLastUpdate time.Time
	fipsByAddr    map[string]int
	serversByFIP  map[int]int
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	node := &corev1.Node{}
	err := r.Get(ctx, req.NamespacedName, node)
	if err != nil {
		return reconcile.Result{}, err
	}
	log.V(1).Info("starting reconcile", "request", req)

	err = r.refreshFloatingIPs(ctx)
	if err != nil {
		return reconcile.Result{RequeueAfter: 5 * time.Second}, err
	}

	serverID := 0
	if strings.HasPrefix(node.Spec.ProviderID, "hcloud://") {
		serverIDStr := strings.TrimPrefix(node.Spec.ProviderID, "hcloud://")
		serverID, _ = strconv.Atoi(serverIDStr)
	}
	if serverID == 0 {
		return reconcile.Result{}, errors.New("error resolving server ID of node")
	}

	labelValue, labelPresent := node.Labels[r.Config.FloatingIP.AssignmentLabel]

	requestedFIP, ok := node.Annotations[r.Config.FloatingIP.RequestAnnotation]
	if ok {
		// Annotation requesting floating IP is present, ensure IP is assigned.
		fipID, ok := r.fipsByAddr[requestedFIP]
		if !ok {
			// IP not found. Maybe generate a warning Event here?
			return reconcile.Result{}, errors.New("requested floating IP not found")
		}

		if r.serversByFIP[fipID] != serverID {
			action, _, err := r.HCloud.FloatingIP.Assign(ctx, &hcloud.FloatingIP{ID: fipID}, &hcloud.Server{ID: serverID})
			err = r.waitForAction(ctx, action, err)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("error assigning floating IP: %w", err)
			}

			log.Info("assigned floating IP to node", "floating-ip", requestedFIP, "node", node.Name)
			r.serversByFIP[fipID] = serverID
		}
		if labelValue != requestedFIP {
			patch := client.MergeFrom(node.DeepCopy())
			node.Labels[r.Config.FloatingIP.AssignmentLabel] = requestedFIP
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
		action, _, err := r.HCloud.FloatingIP.Unassign(ctx, &hcloud.FloatingIP{ID: fipID})
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
	delete(node.Labels, r.Config.FloatingIP.AssignmentLabel)
	err = r.Patch(ctx, node, patch)

	// No floating IP assigned, no requeue requested here.
	return reconcile.Result{}, err
}

// Rechecks floating IP status from hcloud API if it's too stale.
func (r *Reconciler) refreshFloatingIPs(ctx context.Context) error {
	if time.Now().Sub(r.fipLastUpdate) < 15*time.Second {
		if r.fipsByAddr == nil {
			return errors.New("hcloud data not fetched yet")
		}
		return nil
	}

	r.fipLastUpdate = time.Now()

	page := 1
	count := 0
	for page > 0 {
		// Need to do some conservative retries here (max 2-3 over 5s period).
		fips, resp, err := r.HCloud.FloatingIP.List(ctx,
			hcloud.FloatingIPListOpts{ListOpts: hcloud.ListOpts{Page: page, LabelSelector: r.Config.FloatingIP.Selector}})
		if err != nil {
			return fmt.Errorf("error listing floating IPs: %w", err)
		}

		r.fipsByAddr = map[string]int{}
		r.serversByFIP = map[int]int{}

		for _, fip := range fips {
			count += 1
			r.fipsByAddr[fip.IP.String()] = fip.ID
			if server := fip.Server; server != nil {
				r.serversByFIP[fip.ID] = server.ID
			}
		}

		page = resp.Meta.Pagination.NextPage
	}

	log.V(1).Info("refreshed hcloud floating IP list", "total-addresses", count)

	return nil
}

func (r *Reconciler) waitForAction(ctx context.Context, action *hcloud.Action, err error) error {
	if err == nil {
		_, errCh := r.HCloud.Action.WatchProgress(ctx, action)
		select {
		case <-ctx.Done():
		case err = <-errCh:
		}
	}
	return err
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	preds := []predicate.Predicate{
		predicate.Or(
			predicate.AnnotationChangedPredicate{},
			predicate.LabelChangedPredicate{},
		),
	}

	if r.NATGateway.Selector != nil {
		log.Info("watching Nodes with selector", "selector", metav1.FormatLabelSelector(r.NATGateway.Selector))
		pred, err := predicate.LabelSelectorPredicate(*r.NATGateway.Selector)
		if err != nil {
			return err
		}
		preds = append(preds, pred)
	}

	return ctrl.
		NewControllerManagedBy(mgr).
		For(&corev1.Node{}, builder.WithPredicates(predicate.And(preds...))).
		Complete(r)
}
