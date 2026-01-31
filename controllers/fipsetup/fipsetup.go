package fipsetup

import (
	"context"
	"crypto/md5"
	"fmt"

	"github.com/samcday/hcloud-fip-k8s/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch
// +kubebuilder:rbac:namespace="{{tpl .Values.namespace .}}",groups="batch",resources=jobs,verbs=get;list;watch;create;update;patch;delete

var log = logf.Log.WithName("fipsetup")

type Reconciler struct {
	client.Client
	v1alpha1.Config
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	node := corev1.Node{}
	err := r.Get(ctx, req.NamespacedName, &node)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to lookup Node: %w", err)
	}

	assignedFIP, isAssigned := node.Labels[r.Config.FloatingIP.Label]
	configuredFIP, isConfigured := node.Annotations[r.Config.FloatingIP.SetupAnnotation]

	if isConfigured && (!isAssigned || assignedFIP != configuredFIP) {
		setupJob, err := r.getJob(ctx, "setup", &node, configuredFIP)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to get setup job: %w", err)
		}
		teardownJob, err := r.getJob(ctx, "teardown", &node, configuredFIP)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to get teardown job: %w", err)
		}
		if setupJob != nil {
			if err := r.deleteJob(ctx, setupJob); err != nil {
				return reconcile.Result{}, fmt.Errorf("failed to delete setup job: %w", err)
			}
		}

		if teardownJob == nil {
			if err := r.createJob(ctx, "teardown", configuredFIP, &node, &r.FloatingIP.TeardownJob); err != nil {
				return reconcile.Result{}, fmt.Errorf("failed to create teardown job: %w", err)
			}
			return reconcile.Result{}, nil
		}

		if teardownJob.Status.Succeeded > 0 {
			if err := r.setAnnotation(ctx, node, ""); err != nil {
				return reconcile.Result{}, err
			}
		}
	}

	if isAssigned && !isConfigured {
		setupJob, err := r.getJob(ctx, "setup", &node, assignedFIP)
		if err != nil {
			return reconcile.Result{}, err
		}
		teardownJob, err := r.getJob(ctx, "teardown", &node, assignedFIP)
		if err != nil {
			return reconcile.Result{}, err
		}

		if teardownJob != nil {
			if err := r.deleteJob(ctx, teardownJob); err != nil {
				return reconcile.Result{}, fmt.Errorf("failed to delete teardown job: %w", err)
			}
		}

		if setupJob == nil {
			return reconcile.Result{}, r.createJob(ctx, "setup", assignedFIP, &node, &r.FloatingIP.SetupJob)
		}

		if setupJob.Status.Succeeded > 0 {
			if err := r.setAnnotation(ctx, node, assignedFIP); err != nil {
				return reconcile.Result{}, err
			}
		}
	}

	return reconcile.Result{}, nil
}

func (r *Reconciler) hashJobIdentity(jobType string, fip string, nodeName string) string {
	hash := md5.Sum([]byte(fmt.Sprintf("%s-%s-%s", jobType, fip, nodeName)))
	return fmt.Sprintf("%x", hash)[:8]
}

func (r *Reconciler) getJobName(jobType string, fip string, node *corev1.Node) string {
	name := fmt.Sprintf("fip-%s-%s", jobType, r.hashJobIdentity(jobType, fip, node.Name))
	if len(name) > 63 {
		return name[:63]
	}
	return name
}

func (r *Reconciler) getJob(ctx context.Context, jobType string, node *corev1.Node, fip string) (*batchv1.Job, error) {
	jobName := r.getJobName(jobType, fip, node)

	job := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Namespace: r.FloatingIP.JobNamespace, Name: jobName}, job)
	if errors.IsNotFound(err) {
		err = nil
		job = nil
	}
	return job, err
}

func (r *Reconciler) createJob(ctx context.Context, jobType string, fip string, node *corev1.Node, jobSpec *batchv1.JobSpec) error {
	job := batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.FloatingIP.JobNamespace,
			Name:      r.getJobName(jobType, fip, node),
		},
		Spec: *jobSpec,
	}
	job.Spec.Template.Spec.NodeName = node.Name
	job.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyOnFailure

	for i := range job.Spec.Template.Spec.Containers {
		job.Spec.Template.Spec.Containers[i].Env = append(job.Spec.Template.Spec.Containers[i].Env,
			corev1.EnvVar{
				Name:  "JOB_TYPE",
				Value: jobType,
			}, corev1.EnvVar{
				Name:  "FLOATING_IP",
				Value: fip,
			})
	}
	for i := range jobSpec.Template.Spec.InitContainers {
		jobSpec.Template.Spec.InitContainers[i].Env = append(jobSpec.Template.Spec.InitContainers[i].Env,
			corev1.EnvVar{
				Name:  "JOB_TYPE",
				Value: jobType,
			}, corev1.EnvVar{
				Name:  "FLOATING_IP",
				Value: fip,
			})
	}

	err := ctrl.SetControllerReference(node, &job, r.Scheme())
	if err != nil {
		return err
	}
	err = r.Create(ctx, &job)
	if err != nil {
		return err
	}
	log.V(1).Info(fmt.Sprintf("created %s Job", jobType), "node", node.Name, "ip", fip)
	return nil
}

func (r *Reconciler) deleteJob(ctx context.Context, job *batchv1.Job) error {
	err := r.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationForeground))
	if errors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	log.V(1).Info("deleted job", "job", job.Name)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	preds := []predicate.Predicate{
		predicate.Or(
			predicate.AnnotationChangedPredicate{},
			predicate.LabelChangedPredicate{},
		),
	}

	if r.FloatingIP.NodeSelector != nil {
		log.Info("watching Nodes with selector", "selector", metav1.FormatLabelSelector(r.FloatingIP.NodeSelector))
		pred, err := predicate.LabelSelectorPredicate(*r.FloatingIP.NodeSelector)
		if err != nil {
			return err
		}
		preds = append(preds, pred)
	}

	return builder.ControllerManagedBy(mgr).
		For(&corev1.Node{}, builder.WithPredicates(predicate.And(preds...))).
		Owns(&batchv1.Job{}).
		Complete(r)
}

func (r *Reconciler) setAnnotation(ctx context.Context, node corev1.Node, fip string) error {
	patch := client.MergeFrom(node.DeepCopy())
	if fip != "" {
		node.Annotations[r.FloatingIP.SetupAnnotation] = fip
	} else {
		delete(node.Annotations, r.FloatingIP.SetupAnnotation)
	}
	err := r.Patch(ctx, &node, patch)
	if err != nil {
		return err
	}
	if fip != "" {
		log.Info("setup complete, annotated Node", "node", node.Name, "ip", fip)
	} else {
		log.Info("teardown complete, removed annotation from Node", "node", node.Name)
	}
	return nil
}
