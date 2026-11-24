**Title:** Skip unchanged DisaggregatedSetRoleScaler status patches

#### What type of PR is this?
/kind cleanup

#### What this PR does / why we need it
`ScalerManager.WriteStatus` patches the status of every External role's `DisaggregatedSetRoleScaler` on every DisaggregatedSet reconcile. When nothing changed, the merge patch is `{}`, but it is still a request to the API server: one per External role per reconcile. Reconciles are frequent while a rollout progresses.

This PR compares the desired status with the observed one and skips the patch when they are equal, the same way `status.go` skips unchanged `roleStatuses`. `SetStatusCondition` keeps `LastTransitionTime` for an unchanged condition, so an up-to-date scaler compares equal.

#### Which issue(s) this PR fixes
None.

#### Special notes for your reviewer
- `TestScalerManagerWriteStatusSkipsUnchangedStatus` counts status patches. The first write patches, an identical write does not (on main: 2 patches where 1 is expected), and a changed observed count patches again.
- `TestStatusPendingPreservesOtherWorkAndErrors` counted scaler patches to prove that a pending parent status doesn't suppress scaler writes. With this change the second reconcile has nothing to write, so the test now resets the scaler status first, and it still exercises the same guarantee.

Verification: `go test ./pkg/...`, `test/integration/...` (envtest 1.37), `gofmt`, `go vet`, `golangci-lint`.

#### Does this PR introduce a user-facing change?
```release-note
NONE
```
