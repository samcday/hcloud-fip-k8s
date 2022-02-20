package natgateway

import (
	"context"
	"fmt"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"the-nat-controller/api/v1alpha1"
)

// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups="batch",resources=jobs,verbs=get;list;watch
// +kubebuilder:rbac:namespace="{{.Release.Namespace}}",groups="batch",resources=jobs,verbs=create;update;patch;delete

var jobLabelPrefix = "thenatcontroller/"

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
	_, isSetup := node.Annotations[r.Config.NATGateway.SetupAnnotation]

	if isAssigned && !isSetup {
		// Node has an IP assigned, but it's not setup to perform NAT for that IP yet.
		return r.setup(ctx, node, assignedFIP)
	}

	return reconcile.Result{}, nil
}

// Ensures that a Job is created to set up a given Node and IP. On success, the Node is annotated.
// If setup Jobs for other IPs exist for the Node, they are deleted.
func (r *Reconciler) setup(ctx context.Context, node corev1.Node, assignedFIP string) (reconcile.Result, error) {
	jobList := batchv1.JobList{}
	err := r.List(ctx, &jobList, client.MatchingLabels(map[string]string{
		jobLabelPrefix + "type": "setup",
		jobLabelPrefix + "node": node.Name,
	}))
	if err != nil {
		return reconcile.Result{}, err
	}
	setupExists := false
	setupDone := false
	for _, job := range jobList.Items {
		if jobIP, _ := job.Labels[jobLabelPrefix+"ip"]; jobIP != assignedFIP {
			// A setup Job already exists for this Node, but for a different IP. Delete it.
			err := r.Delete(ctx, &job)
			if err != nil {
				return reconcile.Result{}, err
			}
			log.Info("created setup Job", "job", job.Name)
		} else {
			setupExists = true
			setupDone = job.Status.Succeeded > 0
			break
		}
	}
	if !setupExists {
		jobLabels := map[string]string{
			jobLabelPrefix + "type": "setup",
			jobLabelPrefix + "node": node.Name,
			jobLabelPrefix + "ip":   assignedFIP,
		}

		jobTTL := r.NATGateway.SetupJob.TTL
		if jobTTL == 0 {
			jobTTL = 3600
		}

		privileged := true
		hostPathType := corev1.HostPathType("Directory")
		job := batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      fmt.Sprintf("nat-setup-%s-%s", node.Name, assignedFIP),
				Labels:    jobLabels,
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "setup",
								Image: r.NATGateway.SetupJob.Image,
								Command: []string{
									"/bin/chroot", "/host", "/bin/bash", "-c", r.NATGateway.SetupJob.Script,
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
				TTLSecondsAfterFinished: &jobTTL,
			},
		}
		err := controllerruntime.SetControllerReference(&node, &job, r.Scheme())
		if err != nil {
			return reconcile.Result{}, err
		}
		err = r.Create(ctx, &job)
		if err != nil {
			return reconcile.Result{}, err
		}
		log.Info("created setup Job", "node", node.Name, "ip", assignedFIP)
	}
	if setupDone {
		patch := client.MergeFrom(node.DeepCopy())
		node.Annotations[r.NATGateway.SetupAnnotation] = assignedFIP
		err := r.Patch(ctx, &node, patch)
		if err != nil {
			return reconcile.Result{}, err
		}
		log.Info("setup Job complete, annotated Node", "node", node.Name, "ip", assignedFIP)
	}
	return reconcile.Result{}, nil
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
