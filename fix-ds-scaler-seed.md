**Title:** Seed External role scalers from retained capacity only

#### What type of PR is this?
/kind bug

#### What this PR does / why we need it
**Bug.** When a role switches from `scaling.mode: Static` to `External`, `seedForRole` creates its `DisaggregatedSetRoleScaler` with `spec.replicas` set to the role's current LWS replica count. The scaler is then the role's only source of truth, and the controller scales the role to it in the same reconcile. The seed sums every owned LWS of the role, so it overcounts in two cases:

1. **A slice being removed.** External scaling requires `spec.slices <= 1`, so a multi-slice DisaggregatedSet has to lower `slices` as part of the switch. `cleanupRemovedSlices` deletes the removed slice's LWS in the foreground, and they stay in the cache while their Pods drain. With 3 `prefill` replicas per slice and 2 slices, the scaler is created at **6**, and slice 0's prefill LWS is scaled from 3 to **6**.
2. **A rolling update.** `scaling` is not part of the revision hash, so switching mid-rollout keeps the rollout going. The old and target Specs overlap by up to `maxSurge`, and they were added together. With 3 old replicas and 1 surge replica, the seed is **4** instead of 3.

Without an autoscaler, the extra replica groups (often multi-GPU) stay indefinitely. With HPA, they stay at least until its scale-down stabilization window has passed.

**Root cause.** `seedForRole` ranged over `ListAll` without filtering out workloads that are leaving, and it summed the Specs across revisions within one (slice, role).

**Fix.**
- Skip LWS that are terminating, or whose slice index is outside `spec.slices`.
- Within one (slice, role), when the target revision has started next to a running old revision, count the size the rollout converges to once. That size is the target LWS's initial-replicas annotation, or its initial sub-role counts, which `syncTargetInitialReplicas` keeps equal to the desired count.
- When the target hasn't started yet, the existing rule still applies and the seed is the old revision's running size. Steady state, fresh roles, drained roles and bootstrap intent are unchanged.

#### Which issue(s) this PR fixes
None filed. This description is the bug report.

#### Special notes for your reviewer
- The `"other slice"` case of `TestScalerSeedDistinguishesBootstrapFromDrainedCapacity` put the old LWS in slice 1 of a single-slice DisaggregatedSet. Its expected 6 therefore relied on counting a slice that doesn't exist. The fixture now sets `slices: 2`, so the case still checks what it was written for: a running revision in another slice does not suppress this slice's bootstrap intent.
- **Tests:** new stages `removed slice`, `terminating old` and `started target` (Ordinal and Hash, with and without sub-roles), plus `TestExternalScalerSeedIgnoresRemovedSlice` and `TestExternalScalerSeedDoesNotCountRolloutSurge`, which run through `Reconcile`. All 14 fail on main and pass with this change.

Verification: `go test ./pkg/...`, `test/integration/...` (envtest 1.37), `gofmt`, `go vet`, `golangci-lint`.

#### Does this PR introduce a user-facing change?
```release-note
DisaggregatedSet: switching a role to External scaling no longer seeds its DisaggregatedSetRoleScaler, and scales the role, above its running size while slices are being removed or a rolling update is in progress.
```
