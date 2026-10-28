# Cross-role scaling for wide-EP on DisaggregatedSet: a comparison

Status: Draft, not yet run on a cluster. Every behavior below is read from the
LWS code and KEPs at kubernetes-sigs/lws main `4f2bfdc`. Rows marked
**unverified** need a Kind run before this doc goes out.
Author: @aliceco01
Tracking: llm-d/llm-d-autoscaling#1525 (scope item 1)

## Summary

A wide-EP deployment on a DisaggregatedSet (DS) has prefill and decode as
separate roles. Each role is a LeaderWorkerSet (LWS) per revision and slice.
[KEP-849](https://github.com/kubernetes-sigs/lws/tree/main/keps/849-DisaggregatedSet-HPA)
lets an autoscaler drive each role through a `DisaggregatedSetRoleScaler`
(DSRS), and it leaves cross-role coordination out of scope (non-goal 2).

This doc compares three ways to scale the same DS:

1. **Independent per-role ScaledObjects on the DSRS** (today's path)
2. **One signal driving both roles**
3. **Scaling whole slices** (`spec.slices`)

For each one it walks through scale-out, scale-in and a rollout in progress.
At each step it records what DS and LWS do: which role moves first, how long a
slice runs with only one role ready, and whether scale-in drains the right
groups.

#1598 found no P/D oscillation in its benchmarks, so ratio drift is a minor
axis here. The comparison focuses on scale-in and slice semantics.

## Background: what DS and LWS do today

- **DSRS write path.** KEDA or HPA writes `/scale` on `<ds>-<role>`. The DS
  controller `Owns()` the DSRS, so each write enqueues the DS. One reconcile
  then sets that role's LWS replicas. Each role's scaler is read separately
  (KEP-849, Controller Wiring).
- **HPA view.** `status.replicas` counts LWS groups across all revisions.
  `status.selector` matches only leaders (`worker-index=0`), so per-pod metric
  averaging divides by the group count (KEP-849, Scale Subresource Status
  Fields).
- **Slices.** `spec.slices` replicates the whole role topology. Each slice
  rolls out on its own clock, and slices are left out of the revision hash
  (KEP-846).
  - Today the webhook rejects `spec.slices > 1` while any role has
    `scaling.mode: External`
    (`pkg/webhooks/disaggregatedset/disaggregatedset_webhook.go:155-160`).
  - DS has no `/scale` for slices. KEP-849 Alternative 2 rejected `/scale` on
    the DS itself.
- **Scale-in inside a role.** Lowering a role's replicas removes the
  highest-ordinal LWS groups, which is StatefulSet ordinal semantics. DS
  orchestrates no drain. Lowering `spec.slices` deletes the LWS objects whose
  slice index is `>= N`, with no drain either (KEP-846, Scale-Down).
- **Rollouts.**
  - The scaler value becomes the target revision's desired count.
  - Under the default `scaleDownDuringRollout: Deferred`, a lower scaler value
    has no effect while old replicas of that role remain: effective target =
    max(scaler, target Spec).
  - `Immediate` (KEP-1022) allows reductions down to
    `desired - maxUnavailable`.
  - The planner keeps the roles on a shared fractional curve, for example
    `(1,1), (2,1), (3,2)` (KEP-1022, Keep roles coordinated).

## Setup 1: independent per-role ScaledObjects on the DSRS

Two ScaledObjects, one per DSRS, each with its own trigger. For example,
prefill scales on `num_requests_waiting` and decode on KV-cache utilization.

| Phase | Which role moves first | One role ready per slice | Scale-in drains the right groups |
|---|---|---|---|
| Scale-out | Whichever trigger crosses its threshold first. No ordering between roles. | Not applicable with `slices: 1`: the slice always has both roles. The cost is ratio skew until the other trigger fires. | n/a |
| Scale-in | Each role on its own trigger and stabilization window. | n/a | No. The highest ordinals go, whatever their load, with no drain or preStop sequence. **Unverified:** in-flight request loss at removal. |
| Rollout in progress | The planner's shared curve, not the HPA. A scale-up raises the target revision's desired count. A scale-down is deferred (`Deferred`) or floored at `desired - maxUnavailable` (`Immediate`). | Bounded by the planner. The smaller role may be one rounded step ahead, but no role runs arbitrarily ahead of another (KEP-1022). | Old-revision groups drain first, per planner preference. |

Notes:
- This is the only setup that works with External scaling today, and only with
  `slices: 1`.
- Ratio skew is bounded by how far apart the two triggers fire. #1598 saw no
  oscillation, so the open question is scale-in, not drift.

## Setup 2: one signal driving both roles

The same metric drives both DSRS. There are two ways to build it:
- **2a.** Two ScaledObjects on the same trigger, with targets chosen so that
  decode ≈ k × prefill.
- **2b.** A small external controller writes both DSRS from one computed value.

| Phase | Which role moves first | One role ready per slice | Scale-in drains the right groups |
|---|---|---|---|
| Scale-out | 2a: each HPA evaluates on its own sync, so either can land first. 2b: the two `/scale` writes are separate API calls, so DS can reconcile after the first and before the second. **Unverified:** the size of that window, expected to be one reconcile hop. | Same as Setup 1 with `slices: 1`. | n/a |
| Scale-in | Same as scale-out. | n/a | Same as Setup 1: highest ordinals, no drain. The ratio holds except for rounding. |
| Rollout in progress | Same as Setup 1. The planner overrides the ratio during the rollout. | Same as Setup 1. | Same as Setup 1. |

Notes:
- 2a needs no new code but holds the ratio only approximately, because each
  HPA rounds and stabilizes on its own.
- 2b holds the ratio exactly, but it is a custom controller, which the
  KEDA-only direction of this repo tries to avoid.
- Neither changes scale-in semantics. Both still remove the highest-ordinal
  groups of each role.

## Setup 3: scaling whole slices

Each slice is a complete prefill and decode set, and scaling changes
`spec.slices`.

Not reachable with KEDA today:
- `spec.slices` has no `/scale` subresource.
- `slices > 1` is rejected while any role is External.

So this setup needs Static role replicas and something that writes
`spec.slices`: a patch from a custom controller, or a future scale target from
the KEP-849 multi-slice follow-up.

| Phase | Which role moves first | One role ready per slice | Scale-in drains the right groups |
|---|---|---|---|
| Scale-out | Both. One reconcile creates the new slice's LWS for every role at the current target revision (KEP-846, Per-Slice Reconciliation). | The slice has one role ready for the gap between the roles' startup times, for example decode loading weights later than prefill. **Unverified:** measure it on Kind with the simulator's startup delay. | n/a |
| Scale-in | Both roles of the removed slice together. | None. The slice goes away whole. | Partly. It removes the highest slice index, whatever its load or rollout state, with no orchestrated drain (KEP-846, Scale-Down). It never leaves one role of a slice behind. |
| Rollout in progress | Each slice rolls on its own clock. Changing `slices` does not restart the rollout, because slices are left out of the hash. **Unverified:** whether a slice added mid-rollout starts directly at the target revision. | Same as scale-out for a new slice. | A slice removed mid-rollout takes both its old and new revisions with it. **Unverified:** how the controller accounts for a removed slice that was mid-drain. |

Notes:
- This is the only setup where scale-in can never strand one role, and the
  only one where a scaling step is a whole serving unit. That matches the
  wide-EP case where a slice is one rack or NVL domain.
- The step size is a whole slice, so it is coarse for small deployments.

## Comparison

| | Setup 1 | Setup 2 | Setup 3 |
|---|---|---|---|
| Works with KEDA today | Yes (`slices: 1`) | 2a yes, 2b needs a controller | No |
| P/D ratio held | No, per trigger | 2a approximately, 2b yes | Yes, inside each slice |
| Scale-in unit | Highest-ordinal group per role | Highest-ordinal group per role | Highest-index slice |
| Drain on scale-in | None | None | None |
| Rollout interaction | Planner curve, Deferred or Immediate | Same | Per-slice rollout |

None of the three drains on scale-in. That is the gap #1598 did not cover.
It is likely the main finding for scope item 3, whatever setup is picked.

## How the cases will be run

Kind, CPU only, inference simulator backends, using the staging scenario
`benchmark/config/scenarios/staging/lws-pd-disaggregation.yaml` adapted from
LWS to a DisaggregatedSet. For each cell above, record:

- the timeline of per-role Ready groups, from DS status and the LWS objects
- the time a slice has only one role Ready
- which groups or slices are removed on scale-in, and their load at the time
- client errors and aborted streams during scale-in
- for the rollout cases, the planner's step sequence from DS events

Results replace the **unverified** marks above.

## Open questions

- Should scale-in pick the least-loaded group, and is that LWS's job or the
  router's (drain first, then lower `/scale`)?
- For Setup 3, which scaler shape fits wide-EP: aggregate, per-slice, or
  per-role applied per slice (KEP-849, Interaction with DisaggregatedSet
  Slices)?
