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
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/lws/pkg/computedomain"
)

// The simulated nodes run no DRA driver, so the runner plays its part: it
// publishes ResourceSlices with devices for every simulated node, which lets
// the scheduler allocate ResourceClaims and bind their pods, and it creates
// the ResourceClaimTemplate of every ComputeDomain, which the NVIDIA
// ComputeDomain controller would do. Everything else, from creating the
// ResourceClaims to allocating and releasing them, is done by the real
// kube-controller-manager and scheduler.
const (
	driverName      = "performance.lws.x-k8s.io"
	deviceClassName = "lws-performance"
	// deviceClaimName is the name of the claim in the pods of a class with a
	// ResourceClaimTemplate, and of the request in every simulated template.
	deviceClaimName = "device"
	// simulatedLabel marks the cluster-scoped objects the runner publishes, so
	// that it can remove them, including those a previous run left behind.
	simulatedLabel = "performance.lws.x-k8s.io/simulated"
)

// setUpDRA publishes the DeviceClass, the ResourceSlices of every node that
// matches the node selector, and the ResourceClaimTemplates of the classes
// that reference one. The returned function removes the cluster-scoped
// objects; the namespaced ones go with the namespace.
func (r *runner) setUpDRA(ctx context.Context) (func(), error) {
	cleanup := func() {
		// The run context may be cancelled already.
		if err := r.removeSimulatedDevices(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "removing the simulated devices:", err)
		}
	}
	if err := r.removeSimulatedDevices(ctx); err != nil {
		return nil, fmt.Errorf("removing the simulated devices of an earlier run: %w", err)
	}
	if err := r.client.Create(ctx, buildDeviceClass()); err != nil {
		return nil, fmt.Errorf("creating DeviceClass: %w", err)
	}
	nodes := &corev1.NodeList{}
	if err := r.client.List(ctx, nodes, client.MatchingLabels(r.cfg.NodeSelector)); err != nil {
		cleanup()
		return nil, fmt.Errorf("listing nodes: %w", err)
	}
	if len(nodes.Items) == 0 {
		cleanup()
		return nil, fmt.Errorf("no node matches %v to publish devices for", r.cfg.NodeSelector)
	}
	for _, node := range nodes.Items {
		for _, slice := range buildResourceSlices(node.Name, r.cfg.DRA.DevicesPerNode) {
			if err := r.client.Create(ctx, slice); err != nil {
				cleanup()
				return nil, fmt.Errorf("creating ResourceSlice: %w", err)
			}
		}
	}
	for _, class := range r.cfg.Classes {
		if !class.ResourceClaimTemplate {
			continue
		}
		template := buildResourceClaimTemplate(r.cfg.Namespace, resourceClaimTemplateName(class))
		if err := r.client.Create(ctx, template); err != nil {
			cleanup()
			return nil, fmt.Errorf("creating ResourceClaimTemplate: %w", err)
		}
	}
	fmt.Printf("published %d devices on each of %d nodes\n", r.cfg.DRA.DevicesPerNode, len(nodes.Items))
	return cleanup, nil
}

func (r *runner) removeSimulatedDevices(ctx context.Context) error {
	if err := r.client.DeleteAllOf(ctx, &resourcev1.ResourceSlice{}, client.HasLabels{simulatedLabel}); err != nil {
		return err
	}
	return client.IgnoreNotFound(r.client.Delete(ctx, buildDeviceClass()))
}

func buildDeviceClass() *resourcev1.DeviceClass {
	return &resourcev1.DeviceClass{
		ObjectMeta: metav1.ObjectMeta{Name: deviceClassName, Labels: map[string]string{simulatedLabel: "true"}},
		Spec: resourcev1.DeviceClassSpec{
			Selectors: []resourcev1.DeviceSelector{{
				CEL: &resourcev1.CELDeviceSelector{Expression: fmt.Sprintf("device.driver == %q", driverName)},
			}},
		},
	}
}

// buildResourceSlices returns the pool of devices of a node, in as many
// ResourceSlices as the limit on devices per slice needs.
func buildResourceSlices(node string, devices int) []*resourcev1.ResourceSlice {
	count := (devices + resourcev1.ResourceSliceMaxDevices - 1) / resourcev1.ResourceSliceMaxDevices
	slices := make([]*resourcev1.ResourceSlice, 0, count)
	for i := range count {
		slice := &resourcev1.ResourceSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:   fmt.Sprintf("lws-performance-%s-%d", node, i),
				Labels: map[string]string{simulatedLabel: "true"},
			},
			Spec: resourcev1.ResourceSliceSpec{
				Driver:   driverName,
				Pool:     resourcev1.ResourcePool{Name: node, Generation: 1, ResourceSliceCount: int64(count)},
				NodeName: ptr.To(node),
			},
		}
		for d := i * resourcev1.ResourceSliceMaxDevices; d < min(devices, (i+1)*resourcev1.ResourceSliceMaxDevices); d++ {
			slice.Spec.Devices = append(slice.Spec.Devices, resourcev1.Device{Name: fmt.Sprintf("device-%d", d)})
		}
		slices = append(slices, slice)
	}
	return slices
}

// buildResourceClaimTemplate returns a template whose claims each ask for one
// simulated device.
func buildResourceClaimTemplate(namespace, name string) *resourcev1.ResourceClaimTemplate {
	return &resourcev1.ResourceClaimTemplate{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: resourcev1.ResourceClaimTemplateSpec{
			Spec: resourcev1.ResourceClaimSpec{
				Devices: resourcev1.DeviceClaim{
					Requests: []resourcev1.DeviceRequest{{
						Name:    deviceClaimName,
						Exactly: &resourcev1.ExactDeviceRequest{DeviceClassName: deviceClassName},
					}},
				},
			},
		},
	}
}

// buildChannelTemplate returns the ResourceClaimTemplate of a ComputeDomain's
// channel, owned by the ComputeDomain so that it is removed with it, as the
// NVIDIA ComputeDomain controller does. Its claims ask for a simulated device
// instead of a channel of the NVIDIA driver.
func buildChannelTemplate(cd *computedomain.ComputeDomain) *resourcev1.ResourceClaimTemplate {
	template := buildResourceClaimTemplate(cd.Namespace, cd.Spec.Channel.ResourceClaimTemplate.Name)
	template.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: computedomain.GroupVersion.String(),
		Kind:       computedomain.Kind,
		Name:       cd.Name,
		UID:        cd.UID,
	}}
	return template
}

// runComputeDomainDriver creates the channel ResourceClaimTemplate of every
// ComputeDomain in the workload namespace until ctx is done. Like the latency
// measurement, it polls the informer cache.
func (r *runner) runComputeDomainDriver(ctx context.Context) {
	// done holds the UIDs of the ComputeDomains whose template exists.
	done := map[string]bool{}
	_ = wait.PollUntilContextCancel(ctx, pollInterval, true, func(ctx context.Context) (bool, error) {
		list := &computedomain.ComputeDomainList{}
		if err := r.cache.List(ctx, list, client.InNamespace(r.cfg.Namespace)); err != nil {
			fmt.Fprintln(os.Stderr, "listing ComputeDomains:", err)
			return false, nil
		}
		seen := make(map[string]bool, len(list.Items))
		for i := range list.Items {
			cd := &list.Items[i]
			uid := string(cd.UID)
			seen[uid] = true
			if done[uid] || !cd.DeletionTimestamp.IsZero() || cd.Spec.Channel == nil {
				continue
			}
			err := r.client.Create(ctx, buildChannelTemplate(cd))
			if err != nil && !apierrors.IsAlreadyExists(err) {
				if ctx.Err() == nil {
					fmt.Fprintf(os.Stderr, "creating the ResourceClaimTemplate of ComputeDomain %s: %v\n", cd.Name, err)
				}
				continue
			}
			done[uid] = true
		}
		// Forget the ComputeDomains that are gone, so the map does not grow
		// over a run.
		for uid := range done {
			if !seen[uid] {
				delete(done, uid)
			}
		}
		return false, nil
	})
}

// draObjects counts the DRA objects in the workload namespace, terminating
// ones included, so that a check waits until they are cleaned up.
type draObjects struct {
	resourceClaims int
	computeDomains int
}

func (r *runner) countDRAObjects(ctx context.Context) (draObjects, error) {
	var counts draObjects
	if !r.cfg.usesDRA() {
		return counts, nil
	}
	claims := &resourcev1.ResourceClaimList{}
	if err := r.cache.List(ctx, claims, client.InNamespace(r.cfg.Namespace)); err != nil {
		return counts, err
	}
	counts.resourceClaims = len(claims.Items)
	if r.cfg.usesAutoComputeDomain() {
		cds := &computedomain.ComputeDomainList{}
		if err := r.cache.List(ctx, cds, client.InNamespace(r.cfg.Namespace)); err != nil {
			return counts, err
		}
		counts.computeDomains = len(cds.Items)
	}
	return counts, nil
}

// wantDRAObjects returns how many DRA objects the current targets ask for: a
// ResourceClaim per claim of each pod and a ComputeDomain per group of each
// LeaderWorkerSet with auto-compute-domain.
func (r *runner) wantDRAObjects() draObjects {
	var want draObjects
	for _, class := range r.cfg.Classes {
		for i := range class.Count {
			replicas := int(r.replicas[leaderWorkerSetName(class, i)])
			want.resourceClaims += replicas * int(class.Size) * class.claimsPerPod()
			if class.AutoComputeDomain {
				want.computeDomains += replicas
			}
		}
	}
	return want
}

// draMismatch returns how got differs from want, or "" when they match.
func draMismatch(got, want draObjects) string {
	switch {
	case got.resourceClaims != want.resourceClaims:
		return fmt.Sprintf("%d ResourceClaims, want %d", got.resourceClaims, want.resourceClaims)
	case got.computeDomains != want.computeDomains:
		return fmt.Sprintf("%d ComputeDomains, want %d", got.computeDomains, want.computeDomains)
	}
	return ""
}
