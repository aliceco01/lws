**Title:** Report foreign PodGroup owners as ErrUnexpectedPodGroupOwner

#### What type of PR is this?
/kind bug

#### What this PR does / why we need it
**Bug.** `SchedulerProvider.CreatePodGroupIfNotExists` documents that it returns an error wrapping `ErrUnexpectedPodGroupOwner` when an existing PodGroup has a missing or conflicting controller owner. The pod controller emits an `UnexpectedPodGroupOwner` warning event for that error, and the Volcano provider follows the contract. The Kubernetes provider did not. A gated leader blocked by a PodGroup that another object owns therefore got the message `PodGroup ... has immutable scheduling configuration drift` and no event. Examples are a PodGroup left by a deleted LeaderWorkerSet of the same name that GC hasn't removed yet, or one created by hand.

**Root cause.** `updateMutablePodGroupFields` combined the owner check and the spec comparison into one drift error.

**Fix.** An owner mismatch now returns `ErrUnexpectedPodGroupOwner: PodGroup <ns>/<name> has controller owner <current>; expected <expected>`, in the same style as the Volcano provider. The `ReconcileError` reason is unchanged, so retry behavior is the same, and a correctly owned PodGroup with a different spec is still drift.

#### Which issue(s) this PR fixes
None filed.

#### Special notes for your reviewer
`TestKubernetesCreatePodGroupReportsUnexpectedOwner` covers three cases:
- A missing controller owner.
- An owner from a previous LeaderWorkerSet UID.
- A matching owner with a different spec, which must stay drift.

The first two fail on main. This PR and "Report Workload fields that kube-apiserver drops instead of drift" each add a test at the end of `kubernetes_provider_test.go`. Whichever merges second needs a trivial rebase.

Verification: `go test ./pkg/...`, `test/integration/...` (envtest 1.37), `gofmt`, `go vet`, `golangci-lint`.

#### Does this PR introduce a user-facing change?
```release-note
The Kubernetes scheduler provider now reports a PodGroup owned by another object as an unexpected PodGroup owner, with an UnexpectedPodGroupOwner event, instead of as scheduling configuration drift.
```
