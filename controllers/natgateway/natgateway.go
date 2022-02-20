package natgateway

import (
	"context"
	"fmt"
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
	"the-nat-controller/api/v1alpha1"
)

// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch
// +kubebuilder:rbac:namespace="{{.Release.Namespace}}",groups="batch",resources=jobs,verbs=get;list;watch;create;update;patch;delete

var log = logf.Log.WithName("natgateway")

type Options struct {
	Namespace       string
	FIPAssignLabel  string
	SetupAnnotation string
	SetupJobTTL     int32
}

type Reconciler struct {
	client.Client
	v1alpha1.Config
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log.V(1).Info("starting reconcile", "request", req)

	node := corev1.Node{}
	err := r.Get(ctx, req.NamespacedName, &node)
	if err != nil {
		return reconcile.Result{}, err
	}

	assignedFIP, isAssigned := node.Labels[r.Config.FloatingIP.AssignmentLabel]
	configuredFIP, isConfigured := node.Annotations[r.Config.NATGateway.SetupAnnotation]

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
			err := r.Delete(ctx, setupJob, client.PropagationPolicy(metav1.DeletePropagationForeground))
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("failed to delete setup job: %w", err)
			}
			log.Info("deleted stale setup job", "node", node.Name, "fip", configuredFIP)
		}

		if teardownJob == nil {
			err := r.createJob(ctx, "teardown", configuredFIP, &node, &r.NATGateway.TeardownJob)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("failed to create teardown job: %w", err)
			}
			return reconcile.Result{}, nil
		}

		if teardownJob.Status.Succeeded > 0 {
			patch := client.MergeFrom(node.DeepCopy())
			delete(node.Annotations, r.NATGateway.SetupAnnotation)
			err = r.Patch(ctx, &node, patch)
			if err != nil {
				return reconcile.Result{}, err
			}
			log.Info("teardown complete, removing Node annotation", "node", node.Name, "ip", assignedFIP)
			// TODO: Add Event here for Node?
			return reconcile.Result{}, err
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
			err := r.Delete(ctx, teardownJob, client.PropagationPolicy(metav1.DeletePropagationForeground))
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("failed to delete teardown job: %w", err)
			}
			log.Info("deleted stale teardown job", "node", node.Name, "fip", assignedFIP)
		}

		if setupJob == nil {
			err := r.createJob(ctx, "setup", assignedFIP, &node, &r.NATGateway.SetupJob)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("failed to create setup job: %w", err)
			}
			return reconcile.Result{}, nil
		}

		if setupJob.Status.Succeeded > 0 {
			patch := client.MergeFrom(node.DeepCopy())
			node.Annotations[r.NATGateway.SetupAnnotation] = assignedFIP
			err = r.Patch(ctx, &node, patch)
			if err != nil {
				return reconcile.Result{}, err
			}
			log.Info("setup complete, annotated Node", "node", node.Name, "ip", assignedFIP)
			// TODO: Add Event here for Node?
		}
	}

	return reconcile.Result{}, nil
}

func (r *Reconciler) getJob(ctx context.Context, jobType string, node *corev1.Node, fip string) (*batchv1.Job, error) {
	jobName := fmt.Sprintf("nat-%s-%s-%s", jobType, fip, node.Name)

	job := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Namespace: r.CacheNamespace, Name: jobName}, job)
	if errors.IsNotFound(err) {
		err = nil
		job = nil
	}
	return job, err
}

// Ensures that a Job is created to set up a given Node and IP. On success, the Node is annotated.
// If Jobs of the same type, but for other IPs exist for the Node, they are deleted.
// Returns true if the desired Job has completed.
func (r *Reconciler) createJob(ctx context.Context, jobType string, fip string, node *corev1.Node, jobConfig *v1alpha1.Job) error {
	privileged := true
	hostPathType := corev1.HostPathType("Directory")
	job := batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.CacheNamespace,
			Name:      fmt.Sprintf("nat-%s-%s-%s", jobType, fip, node.Name),
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  jobType,
							Image: jobConfig.Image,
							Command: []string{
								"/bin/chroot", "/host", "/bin/bash", "-c", jobConfig.Script,
							},
							Env: []corev1.EnvVar{
								{
									Name:  "JOB_TYPE",
									Value: jobType,
								},
								{
									Name:  "FLOATING_IP",
									Value: fip,
								},
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged: &privileged,
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "host",
									MountPath: "/host",
								},
							},
						},
					},
					HostNetwork:   true,
					NodeName:      node.Name,
					RestartPolicy: corev1.RestartPolicyOnFailure,
					Volumes: []corev1.Volume{
						{
							Name: "host",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Type: &hostPathType,
									Path: "/",
								},
							},
						},
					},
				},
			},
		},
	}
	if jobConfig.TTL > 0 {
		job.Spec.TTLSecondsAfterFinished = &jobConfig.TTL
	}
	err := ctrl.SetControllerReference(node, &job, r.Scheme())
	if err != nil {
		return err
	}
	err = r.Create(ctx, &job)
	if err != nil {
		return err
	}
	log.Info("created Job", "node", node.Name, "type", jobType, "ip", fip)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return builder.ControllerManagedBy(mgr).
		For(&corev1.Node{}, builder.WithPredicates(predicate.Or(
			predicate.AnnotationChangedPredicate{},
			predicate.LabelChangedPredicate{},
		))).
		Owns(&batchv1.Job{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
			job := object.(*batchv1.Job)
			return job.Status.Succeeded > 0
		}))).
		Complete(r)
}
