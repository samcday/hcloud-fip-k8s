package fipdistribute

import (
	"context"
	appsv1 "k8s.io/api/apps/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"the-nat-controller/api/v1alpha1"
)

// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch

var log = logf.Log.WithName("fipdistribute")

type Reconciler struct {
	client.Client
	v1alpha1.Config
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.DaemonSet{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		//Watches(&source.Kind{Type: &corev1.Node{}}, handler.EnqueueRequestsFromMapFunc(func(object client.Object) []reconcile.Request {
		//	node := object.(*corev1.Node)
		//})).
		Complete(r)
}
