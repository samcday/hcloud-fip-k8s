package natgateway

import (
	"context"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var jobLabelPrefix = "thenatcontroller/"

var log = logf.Log.WithName("natgateway")

type Options struct {
	FIPAssignLabel  string
	SetupAnnotation string
}

type Reconciler struct {
	client.Client
	opts Options
}

func Run(mgr manager.Manager, opts Options) error {
	rec := Reconciler{opts: opts}
	return builder.ControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Owns(&batchv1.Job{}).
		Complete(&rec)
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	node := corev1.Node{}
	err := r.Get(ctx, req.NamespacedName, &node)
	if err != nil {
		return reconcile.Result{}, err
	}

	assignedFIP, isAssigned := node.Labels[r.opts.FIPAssignLabel]
	_, isSetup := node.Annotations[r.opts.SetupAnnotation]

	log.Info("oh", "ip", assignedFIP)
	if isAssigned && !isSetup {
		jobLabels := map[string]string{
			jobLabelPrefix + "type": "setup",
			jobLabelPrefix + "node": node.Name,
		}
		jobList := batchv1.JobList{}
		err := r.List(ctx, &jobList, client.MatchingLabels(jobLabels))
		if err != nil {
			return reconcile.Result{}, err
		}
		found := false
		for _, job := range jobList.Items {
			if job.Status.Succeeded > 0 {
				log.Info("found", "hmm", job.Name)
				found = true
				break
			}
		}
		if !found {
			log.Info("created setup Job", "node", node.Name, "ip", assignedFIP)
		}
	}

	log.V(1).Info("starting reconcile", "request", req)
	return reconcile.Result{}, nil
}

func (r *Reconciler) InjectClient(c client.Client) error {
	r.Client = c
	return nil
}
