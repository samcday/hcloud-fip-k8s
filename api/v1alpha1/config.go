package v1alpha1

import (
	batchv1 "k8s.io/api/batch/v1"
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
	Selector        string                `json:"selector,omitempty"`
	Label           string                `json:"label,omitempty"`
	NodeSelector    *metav1.LabelSelector `json:"nodeSelector,omitempty"`
	SetupAnnotation string                `json:"setupAnnotation,omitempty"`
	SetupJob        batchv1.JobSpec       `json:"setupJob,omitempty"`
	TeardownJob     batchv1.JobSpec       `json:"teardownJob,omitempty"`
	Interval        int                   `json:"interval,omitempty"`
}

func init() {
	SchemeBuilder.Register(&Config{})
}
