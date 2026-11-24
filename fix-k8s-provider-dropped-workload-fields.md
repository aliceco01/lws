**Title:** Report Workload fields that kube-apiserver drops instead of drift

#### What type of PR is this?
/kind bug

#### What this PR does / why we need it
**Bug.** kube-apiserver silently drops fields that are served behind a disabled feature gate. With the configuration the installation guide gives (`GenericWorkload` plus `scheduling.k8s.io/v1beta1`, the same as the envtest suite), the PodGroup template `schedulingConstraints` in the Workload the Kubernetes provider creates is dropped (served by `TopologyAwareWorkloadScheduling`), and so is `resourceClaims`. Take an LWS that sets `spec.scheduling.replica.schedulingConstraints.topology`, or role-level `resourceClaims`:

1. The first reconcile succeeds, and the leader StatefulSet is created. The groups are scheduled **without** the topology or claims they asked for.
2. Every later reconcile compares the persisted Workload with the desired one and fails with `InvalidSchedulingConfiguration: ... immutable scheduling configuration drift in template "replica"`. That blocks scale-up and rollouts for good, and it names a change the user never made.

**Root cause.** The provider never checks that the stored Workload kept what it sent. A missing field then shows up later as a mismatch that looks like a user edit.

**Fix.** `droppedWorkloadFieldsError` compares the Workload returned by `Create`, and the persisted Workload before drift is reported, with the compiled one. It looks for PodGroup template fields (`schedulingConstraints`, `resourceClaims`, `disruptionMode`, `priorityClassName`) that the LWS sets but the server did not keep. When it finds one, it returns `UnsupportedProviderCapability: kube-apiserver did not persist <fields> of Workload <ns>/<name>; the feature gate that serves the field is likely disabled`.

A missing field can't be a user change: the LWS webhook forbids changing these fields after creation (`validateLeafItemImmutability` and the priority-class check in `phase_one.go`).

**Behavior change.**
- **Before:** an LWS whose Workload loses one of these fields is created and starts its pods without the constraints, then gets stuck on drift.
- **After:** the first reconcile stops before the leader StatefulSet exists. `WorkloadSchedulingCreated=False` names the dropped field, with reason `UnsupportedProviderCapability`, and no pods are created.

This follows the controller's rule that no member pod is created before its scheduling prerequisites exist. As with any `reconcileWorkloadScheduling` error, scale-down and status updates still run. An LWS whose fields are kept, or that doesn't set them, behaves as before.

#### Which issue(s) this PR fixes
None filed.

#### Special notes for your reviewer
- **Remediation after enabling the gate.** Workload templates are immutable, so enabling the gate later does not repair a Workload that was already stored without the field. The LWS keeps reporting the dropped field, which is accurate, until its Workload is recreated. That is the same remediation as today's drift state; only the message changes.
- **Gate attribution.** I confirmed `TopologyAwareWorkloadScheduling` for `schedulingConstraints` by enabling it on envtest's kube-apiserver, where the field is then kept. I didn't confirm which gate serves `resourceClaims`, so the message names only the field.
- **Tests:**
  - `TestKubernetesReconcileSchedulingReportsFieldsDroppedByAPIServer` uses a fake client that strips the field, and covers both the create and the persisted path. The dropped cases fail on main.
  - A new envtest spec in `workload_scheduling_test.go` runs against a real kube-apiserver that drops `schedulingConstraints`. It asserts the condition reason and message and that no leader StatefulSet is created. It fails on main (timeout waiting for the condition) and skips itself where the field is served.
- This PR and "Report foreign PodGroup owners as ErrUnexpectedPodGroupOwner" each add a test at the end of `kubernetes_provider_test.go`. Whichever merges second needs a trivial rebase.

Verification: `go test ./pkg/...`, `test/integration/...` (envtest 1.37), `gofmt`, `go vet`, `golangci-lint`.

#### Does this PR introduce a user-facing change?
```release-note
LeaderWorkerSet: when kube-apiserver drops Workload fields such as schedulingConstraints because their feature gate is disabled, the WorkloadSchedulingCreated condition now reports UnsupportedProviderCapability naming the field, and no pods are created. Previously the LeaderWorkerSet started without the constraints and then reported immutable scheduling configuration drift.
```
