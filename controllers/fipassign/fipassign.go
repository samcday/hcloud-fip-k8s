package fipassign

import (
	"context"
	"errors"
	"fmt"
	"github.com/samcday/hcloud-fip-k8s/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/flowcontrol"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"strconv"
	"strings"
	"time"

	"github.com/hetznercloud/hcloud-go/hcloud"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch

var log = logf.Log.WithName("fipassign")

type Reconciler struct {
	client.Client
	v1alpha1.Config
	HCloud *hcloud.Client

	nodeSelector labels.Selector

	// Writing to this channel "wakes up" the floating IP reconciler loop for an iteration.
	// It's written to whenever a Node in the cluster changes in an interesting way, and on a regular basis to detect
	// changes in hcloud API.
	work chan int
	// Outbound API calls to hcloud API are rate limited.
	limit    flowcontrol.RateLimiter
	recorder record.EventRecorder
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	r.work <- 0
	return reconcile.Result{}, nil
}

func (r *Reconciler) reconcileFloatingIPs(ctx context.Context) error {
	fips, err := r.getFloatingIPs(ctx)
	if err != nil {
		return err
	}

	for _, fip := range fips {
		addr := fip.IP.String()
		var chosenNode *corev1.Node
		// The floating IP is automatically assumed to need assignment, if it's not already assigned.
		needsAssign := fip.Server == nil
		if fip.Server != nil {
			var nodes corev1.NodeList
			if err := r.List(ctx, &nodes, client.MatchingFields{"spec.hcloudID": strconv.Itoa(fip.Server.ID)}); err != nil {
				return err
			}
			if len(nodes.Items) > 0 {
				node := nodes.Items[0]
				if node.Spec.Unschedulable {
					// Node is currently unschedulable. We should reassign the floating IP elsewhere.
					needsAssign = true
				}
				if val, hasLabel := node.Labels[r.FloatingIP.Label]; hasLabel && val != addr {
					// Node's label is pointing at a different IP. We should reassign the floating IP elsewhere.
					needsAssign = true
				} else if !hasLabel && !node.Spec.Unschedulable {
					// Node doesn't have the label, but should.
					chosenNode = &node
				}
			}
		}
		// If this floating IP needs to be reassigned, and we don't already have a chosen Node, then we search for one.
		if needsAssign && chosenNode == nil {
			log.V(1).Info("searching for candidates for floating IP assignment", "ip", addr)
			var nodes corev1.NodeList
			var opts []client.ListOption
			if r.nodeSelector != nil {
				opts = append(opts, client.MatchingLabelsSelector{Selector: r.nodeSelector})
			}
			if err := r.List(ctx, &nodes, opts...); err != nil {
				return err
			}
			var candidates []*corev1.Node
			for idx, node := range nodes.Items {
				if node.Spec.Unschedulable {
					continue
				}
				if node.Labels[r.FloatingIP.Label] == addr {
					// We've found a schedulable Node that is already requesting this floating IP. Automatic winner.
					chosenNode = &nodes.Items[idx]
					break
				}
				if _, ok := node.Labels[r.FloatingIP.Label]; !ok {
					// This Node doesn't already have a label, it's a candidate.
					log.V(1).Info("found candidate", "ip", addr, "node", node.Name)
					candidates = append(candidates, &nodes.Items[idx])
				}
			}
			if len(candidates) > 0 {
				if chosenNode == nil {
					// If there's multiple candidates, just pick the first one arbitrarily.
					chosenNode = candidates[0]
				}
			} else {
				log.Info("no Nodes found for floating IP assignment", "ip", addr)
			}
		}
		if needsAssign && chosenNode != nil {
			// Floating IP needs reassignment, and a suitable Node for assignment was found. Do assignment now.
			err := r.assignFloatingIP(ctx, chosenNode, fip)
			if err != nil {
				return err
			}
		}
		if chosenNode != nil {
			// Make sure chosen Node has the label.
			if _, ok := chosenNode.Labels[r.FloatingIP.Label]; !ok {
				err := r.setNodeLabel(ctx, chosenNode, addr)
				if err != nil {
					return err
				}
			}

			// Make sure no other Node has the label.
			var nodes corev1.NodeList
			err := r.List(ctx, &nodes, client.MatchingLabels{r.FloatingIP.Label: addr})
			if err != nil {
				return err
			}
			for _, node := range nodes.Items {
				matches := node.Name != chosenNode.Name
				if matches && r.nodeSelector != nil {
					matches = r.nodeSelector.Matches(labels.Set(node.Labels))
				}
				if !matches {
					continue
				}
				err := r.setNodeLabel(ctx, &node, "")
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (r *Reconciler) setNodeLabel(ctx context.Context, node *corev1.Node, fip string) error {
	patch := client.MergeFrom(node.DeepCopy())
	if fip != "" {
		log.Info("updating Node label", "node", node.Name, "label", r.Config.FloatingIP.Label, "value", fip)
		node.Labels[r.FloatingIP.Label] = fip
	} else {
		log.Info("removing Node label", "node", node.Name, "label", r.Config.FloatingIP.Label)
		delete(node.Labels, r.Config.FloatingIP.Label)
	}
	return r.Patch(ctx, node, patch)
}

func (r *Reconciler) getFloatingIPs(ctx context.Context) ([]*hcloud.FloatingIP, error) {
	var fips []*hcloud.FloatingIP
	page := 1
	lastPage := 2
	for page < lastPage {
		if err := r.limit.Wait(ctx); err != nil {
			return nil, err
		}

		fipPage, resp, err := r.HCloud.FloatingIP.List(ctx,
			hcloud.FloatingIPListOpts{ListOpts: hcloud.ListOpts{Page: page, LabelSelector: r.FloatingIP.Selector}})
		if err != nil {
			log.Error(err, "error listing floating IPs")
			return nil, err
		}

		for _, fip := range fipPage {
			fips = append(fips, fip)
		}
		page = resp.Meta.Pagination.Page + 1
		lastPage = resp.Meta.Pagination.LastPage
	}
	log.V(1).Info("refreshed floating IP list", "selector", r.FloatingIP.Selector, "count", len(fips))
	return fips, nil
}

func (r *Reconciler) assignFloatingIP(ctx context.Context, node *corev1.Node, fip *hcloud.FloatingIP) error {
	serverID, err := getNodeServerID(node)

	if err := r.limit.Wait(ctx); err != nil {
		return err
	}
	action, _, err := r.HCloud.FloatingIP.Assign(ctx, fip, &hcloud.Server{ID: serverID})
	err = r.waitForAction(ctx, action, err)
	if err != nil {
		return fmt.Errorf("error assigning floating IP: %w", err)
	}

	addr := fip.IP.String()
	log.Info("assigned floating IP to node", "floating-ip", addr, "node", node.Name)
	r.recorder.Eventf(node, corev1.EventTypeNormal, "FloatingIPAssigned", "%s assigned to Node", addr)
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
	r.work = make(chan int)
	r.limit = flowcontrol.NewTokenBucketRateLimiter(1, 3)
	r.recorder = mgr.GetEventRecorderFor("fipassign")

	preds := []predicate.Predicate{
		predicate.Or(
			predicate.LabelChangedPredicate{},
			predicate.GenerationChangedPredicate{},
		),
	}

	if r.FloatingIP.Selector == "" {
		return errors.New("floating IP selector not specified, refusing to start")
	} else {
		log.Info("watching floating IPs with selector", "selector", r.FloatingIP.Selector)
	}

	if r.FloatingIP.NodeSelector != nil {
		log.Info("watching Nodes with selector", "selector", metav1.FormatLabelSelector(r.FloatingIP.NodeSelector))
		var err error
		r.nodeSelector, err = metav1.LabelSelectorAsSelector(r.FloatingIP.NodeSelector)
		if err != nil {
			return err
		}
		pred, err := predicate.LabelSelectorPredicate(*r.FloatingIP.NodeSelector)
		if err != nil {
			return err
		}
		preds = append(preds, pred)
	}

	// Init the reconciliation loop for hcloud floating IPs.
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		for {
			select {
			case <-r.work:
			case <-ctx.Done():
				return nil
			}
			// Consume any redundant work requests.
			done := false
			for !done {
				select {
				case <-r.work:
				case <-ctx.Done():
					return nil
				default:
					done = true
				}
			}
			err := r.reconcileFloatingIPs(ctx)
			if err != nil {
				log.Error(err, "error reconciling floating IPs")
			}
		}
	})); err != nil {
		return err
	}
	// This pumps the floating IP reconcile loop on a regular basis.
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		wait.JitterUntilWithContext(ctx, func(ctx context.Context) {
			r.work <- 0
		}, 15*time.Second, 0.5, true)
		return nil
	})); err != nil {
		return err
	}

	// Index Nodes by their parsed providerID (set by hccm), so we can conveniently lookup cached Nodes by server ID.
	err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Node{}, "spec.hcloudID", func(object client.Object) []string {
		if serverID, err := getNodeServerID(object.(*corev1.Node)); serverID != 0 {
			return []string{strconv.Itoa(serverID)}
		} else if err != nil {
			log.Error(err, "failed to index hcloudID", "node", object.GetName())
		}
		return []string{}
	})
	if err != nil {
		return err
	}

	return ctrl.
		NewControllerManagedBy(mgr).
		For(&corev1.Node{}, builder.WithPredicates(preds...)).
		Complete(r)
}

// getNodeServerID returns the int server ID for a Node, or err if no valid ID could be determined.
// The Node's spec.providerID field is checked, it is set by hcloud-cloud-controller-manager.
func getNodeServerID(node *corev1.Node) (int, error) {
	if strings.HasPrefix(node.Spec.ProviderID, "hcloud://") {
		str := strings.TrimPrefix(node.Spec.ProviderID, "hcloud://")
		if str != "" {
			if id, err := strconv.Atoi(str); err == nil {
				return id, nil
			}
		}
	}
	return 0, fmt.Errorf("node %s has invalid hcloud provider ID", node.Name)
}
