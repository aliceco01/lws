/*
Copyright 2026 The Kubernetes Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	leaderworkerset "sigs.k8s.io/lws/api/leaderworkerset/v1"
	"sigs.k8s.io/lws/test/wrappers"
)

// classLabel is set on every generated LeaderWorkerSet, to the name of its class.
const classLabel = "performance.lws.x-k8s.io/class"

// Config describes the workload a run generates and the phases it times.
type Config struct {
	// Namespace the LeaderWorkerSets are created in. The runner creates it and
	// deletes it when the run ends.
	Namespace string `json:"namespace"`
	// Timeout bounds each phase.
	Timeout metav1.Duration `json:"timeout"`
	// NodeSelector and Tolerations are set on every pod, so that the pods land
	// on the simulated nodes.
	NodeSelector map[string]string   `json:"nodeSelector,omitempty"`
	Tolerations  []corev1.Toleration `json:"tolerations,omitempty"`
	// Classes are the workload shapes, for example many small LeaderWorkerSets
	// and a few large ones.
	Classes []Class `json:"classes"`
	// Scale lists the scaling phases, run in order after creation.
	Scale []ScalePhase `json:"scale,omitempty"`
	// DRA configures the simulated DRA driver. It is required when a class
	// uses ResourceClaims.
	DRA *DRAConfig `json:"dra,omitempty"`
}

// DRAConfig configures the DRA driver the runner simulates, since the
// simulated nodes run no driver. See dra.go.
type DRAConfig struct {
	// DevicesPerNode is the number of devices the runner publishes in
	// ResourceSlices for each node that matches nodeSelector. Each
	// ResourceClaim is allocated one device, so it bounds the pods with claims
	// per node.
	DevicesPerNode int `json:"devicesPerNode"`
}

// Class is a set of identical LeaderWorkerSets.
type Class struct {
	Name string `json:"name"`
	// Count is the number of LeaderWorkerSets of the class.
	Count int `json:"count"`
	// Replicas is the number of groups of each LeaderWorkerSet, and Size the
	// number of pods in a group.
	Replicas int32 `json:"replicas"`
	Size     int32 `json:"size"`
	// GroupIdentity is Ordinal when unset.
	GroupIdentity leaderworkerset.GroupIdentityType `json:"groupIdentity,omitempty"`
	// ResourceClaimTemplate makes every pod reference a ResourceClaimTemplate
	// of the class, so that each pod gets its own ResourceClaim.
	ResourceClaimTemplate bool `json:"resourceClaimTemplate,omitempty"`
	// AutoComputeDomain enables auto-compute-domain (KEP-1259) on the
	// LeaderWorkerSets: the controller manages a ComputeDomain per group and
	// injects a ResourceClaim for it into every pod. It needs the
	// AutoComputeDomain feature gate and the ComputeDomain CRD.
	AutoComputeDomain bool `json:"autoComputeDomain,omitempty"`
}

// claimsPerPod is the number of ResourceClaims each pod of the class has.
func (c Class) claimsPerPod() int {
	n := 0
	if c.ResourceClaimTemplate {
		n++
	}
	if c.AutoComputeDomain {
		n++
	}
	return n
}

// usesDRA reports whether any class gives its pods ResourceClaims.
func (c *Config) usesDRA() bool {
	return slices.ContainsFunc(c.Classes, func(class Class) bool { return class.claimsPerPod() > 0 })
}

// usesAutoComputeDomain reports whether any class enables auto-compute-domain.
func (c *Config) usesAutoComputeDomain() bool {
	return slices.ContainsFunc(c.Classes, func(class Class) bool { return class.AutoComputeDomain })
}

// ScalePhase sets the replicas of every LeaderWorkerSet of the listed classes.
type ScalePhase struct {
	Name     string           `json:"name"`
	Replicas map[string]int32 `json:"replicas"`
}

func loadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := yaml.UnmarshalStrict(raw, cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.Namespace == "" || c.Timeout.Duration <= 0 || len(c.Classes) == 0 {
		return fmt.Errorf("namespace, a positive timeout and at least one class are required")
	}
	classes := map[string]bool{}
	for _, class := range c.Classes {
		if class.Name == "" || classes[class.Name] {
			return fmt.Errorf("class names must be set and unique, got %q", class.Name)
		}
		classes[class.Name] = true
		// The API server validates the rest of the LeaderWorkerSet.
		if class.Count < 1 {
			return fmt.Errorf("class %s: count must be at least 1", class.Name)
		}
	}
	if c.usesDRA() && (c.DRA == nil || c.DRA.DevicesPerNode < 1) {
		return fmt.Errorf("classes with ResourceClaims need dra.devicesPerNode of at least 1")
	}
	phases := map[string]bool{phaseCreate: true}
	for _, phase := range c.Scale {
		if phase.Name == "" || phases[phase.Name] {
			return fmt.Errorf("scale phase names must be set, unique and not %q, got %q", phaseCreate, phase.Name)
		}
		phases[phase.Name] = true
		if len(phase.Replicas) == 0 {
			return fmt.Errorf("scale phase %s changes no class", phase.Name)
		}
		for class, replicas := range phase.Replicas {
			if !classes[class] || replicas < 0 {
				return fmt.Errorf("scale phase %s: unknown class %q or negative replicas", phase.Name, class)
			}
		}
	}
	return nil
}

func leaderWorkerSetName(class Class, index int) string {
	return fmt.Sprintf("%s-%d", class.Name, index)
}

// buildLeaderWorkerSet generates the LeaderWorkerSet with the given index in
// its class, from the wrapper the integration and e2e tests use.
func (c *Config) buildLeaderWorkerSet(class Class, index int) *leaderworkerset.LeaderWorkerSet {
	lws := wrappers.BuildLeaderWorkerSet(c.Namespace).
		Name(leaderWorkerSetName(class, index)).
		Labels(map[string]string{classLabel: class.Name}).
		Replica(int(class.Replicas)).
		Size(int(class.Size)).
		Obj()
	if class.GroupIdentity != "" {
		lws.Spec.GroupIdentity = class.GroupIdentity
	}
	if class.AutoComputeDomain {
		lws.Annotations = map[string]string{leaderworkerset.AutoComputeDomainAnnotationKey: leaderworkerset.AutoComputeDomainEnabled}
	}
	// The images are never pulled: the simulated nodes report the pods Ready.
	template := &lws.Spec.LeaderWorkerTemplate
	for _, pod := range []*corev1.PodTemplateSpec{template.LeaderTemplate, &template.WorkerTemplate} {
		pod.Spec.NodeSelector = c.NodeSelector
		pod.Spec.Tolerations = c.Tolerations
		if class.ResourceClaimTemplate {
			pod.Spec.ResourceClaims = append(pod.Spec.ResourceClaims, corev1.PodResourceClaim{
				Name:                      deviceClaimName,
				ResourceClaimTemplateName: ptr.To(resourceClaimTemplateName(class)),
			})
			for i := range pod.Spec.Containers {
				claims := &pod.Spec.Containers[i].Resources.Claims
				*claims = append(*claims, corev1.ResourceClaim{Name: deviceClaimName})
			}
		}
		if class.AutoComputeDomain {
			// The simulated nodes have no nvidia.com/gpu, so the containers that
			// get the ComputeDomain channel are named instead of detected.
			names := make([]string, 0, len(pod.Spec.Containers))
			for _, container := range pod.Spec.Containers {
				names = append(names, container.Name)
			}
			pod.Annotations = map[string]string{leaderworkerset.ComputeDomainContainersAnnotationKey: strings.Join(names, ",")}
		}
	}
	return lws
}

// resourceClaimTemplateName is the name of the ResourceClaimTemplate the pods
// of a class reference.
func resourceClaimTemplateName(class Class) string {
	return "class-" + class.Name
}
