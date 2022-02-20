package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	cfg "sigs.k8s.io/controller-runtime/pkg/config/v1alpha1"
)

// +kubebuilder:object:root=true

// Config is the configuration for the-nat-controller
type Config struct {
	metav1.TypeMeta                        `json:",inline"`
	cfg.ControllerManagerConfigurationSpec `json:",inline"`

	HCloud     HCloud     `json:"hcloud,omitempty"`
	FloatingIP FloatingIP `json:"floatingIP,omitempty"`
	NATGateway NATGateway `json:"natGateway,omitempty"`
}

type HCloud struct {
	// Token is a token for the Hetzner Cloud API, HCLOUD_TOKEN overrides this, if set
	Token    string `json:"token,omitempty""`
	Endpoint string `json:"endpoint,omitempty"`
	// PollInterval is the interval in milliseconds to poll the hcloud API for action progress
	// see https://pkg.go.dev/github.com/hetznercloud/hcloud-go/hcloud?#WithPollInterval
	PollInterval int64 `json:"pollInterval,omitempty"`
}

type FloatingIP struct {
	Selector          string `json:"selector,omitempty"`
	AssignmentLabel   string `json:"assignmentLabel,omitempty"`
	RequestAnnotation string `json:"requestAnnotation,omitempty"`
}

type NATGateway struct {
	Selector        string `json:"selector,omitempty"`
	SetupAnnotation string `json:"setupAnnotation,omitempty"`
	SetupJob        Job    `json:"setupJob,omitempty"`
	TeardownJob     Job    `json:"teardownJob,omitempty"`
}

type Job struct {
	// TTL indicates how long the Job should remain in the cluster, in seconds, before being cleaned up.
	// Defaults to 1 hour.
	// See https://kubernetes.io/docs/concepts/workloads/controllers/ttlafterfinished/
	TTL    int32  `json:"ttl,omitempty"`
	Image  string `json:"image,omitempty"`
	Script string `json:"script,omitempty"`
}

type NATSetup struct {
}

func init() {
	SchemeBuilder.Register(&Config{})
}
