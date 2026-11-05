# Posting checklist (updated 2026-11-05)

Run this from a session on Alice's Mac where `gh auth status` shows `aliceco01`.
Post each item with `gh`, exactly as written. Post everything in section A
without asking. Section B needs Alice's yes first.

Before posting on a PR, check it for newer comments. If someone has already
answered, or the state changed (merged, closed, CI green), skip the item and
report it.

## A. Post now

### A1. kubernetes-sigs/lws#1165: rerun CI

Skip this if the jobs on the PR head (4860757 or later) are already green. The
earlier failures on `24d1163` (integration, e2e-1.37) died on a proxy.golang.org
network error while downloading `k8s.io/api`, not on the PR code.

```bash
gh pr comment 1165 --repo kubernetes-sigs/lws --body "/retest"
```

### A2. kubernetes-sigs/lws#1165: reply to ahg-g

```bash
gh pr comment 1165 --repo kubernetes-sigs/lws --body-file - <<'EOF'
@ahg-g thanks for both reviews. I rebased on main and pushed the fixes.

From the Nov 2 review:
- I now re-record a revision without the `placement-recorded` marker under the same key, so later annotation changes create a new revision and roll under `maxUnavailable`/`maxSurge` (5eccf20).
- I do this as soon as the controller reconciles the LWS, before it compares revisions, so the one-step annotation→field move on a pre-upgrade LWS also keeps the revision. I take the placement from the leader template rather than the live annotation, so an annotation changed during the controller upgrade still rolls out (24d1163).
- I added the controller integration cases you asked for, plus the one-step move on a pre-upgrade LWS, and updated the KEP's Update Semantics and test plan.

From the Nov 5 review (4860757):
- `validatePlacement` again rejects role exclusive placement on any topology key when the DisaggregatedSet `placementPolicy` is not `None`, for the annotations and for the role's `spec.placementPolicy`, using the code you suggested.
- `ComputeRevisionV1` now includes the role's normalized `spec.placementPolicy` with `omitempty`. It is nil when a role has no exclusive placement, so the pinned v1 hash is unchanged; setting, changing or clearing it changes the revision.
- I updated KEP-1064's "Interaction With DisaggregatedSet" and the topology-placement docs to say the combination is rejected and why.

I also fixed the e2e placement checks Copilot flagged (117ea65). The red jobs on the earlier run failed on a proxy.golang.org download error before compiling.
EOF
```

### A3. kubernetes-sigs/lws#1165: resolve the addressed review threads

List the threads with GraphQL, then resolve only these, and only when unresolved:

- ahg-g, Nov 2: `revision_utils.go` "Re-record a pre-upgrade revision under the same key…" (fixed in 5eccf20 and 24d1163)
- ahg-g, Nov 2: integration test thread "Add controller integration cases for the rollout claims…" (fixed in 5eccf20 and 24d1163)
- ahg-g, Oct 26: "In `ApplyRevision`, clear `restoredLws.Spec.PlacementPolicy` whenever `!placementInData`…" (fixed in 0d30db4)
- ahg-g, Oct 26: "In `SetMatchesRevision` … include `GetRevisionKey(proposedRevision)` … in `revisionEqualityCacheKey`" (fixed in 0d30db4)
- ahg-g, Oct 26: "In `TestRevisionPlacementChanges` and `TestApplyRevisionRestoresPlacement`, test adding `spec.placementPolicy` against a pre-upgrade revision…". Resolve it only if `pkg/utils/revision/revision_placement_test.go` on the PR head has a pre-upgrade "field added" case with `wantKeep: false`. Otherwise leave it and report it.
- yindia, Oct 25: "This duplicates revisionRecordsPlacement()…" (fixed in 9a862fc)
- yindia, Oct 25: "KEP validation table says both levels set must use different keys…" (fixed in 4d3c0be)

- ahg-g, Nov 5: `disaggregatedset_webhook.go` "When `spec.placementPolicy.type` is not `None`, reject any role exclusive placement…" (fixed in 4860757)
- ahg-g, Nov 5: KEP "Update `Interaction With DisaggregatedSet`…" (fixed in 4860757)
- ahg-g, Nov 5: docs "Replace this note…" (fixed in 4860757)
- ahg-g, Nov 5: `leaderworkerset_types.go` "In `pkg/utils/disaggregatedset/utils.go`, include a normalized `PlacementPolicy`…" (fixed in 4860757)

Do not resolve the Copilot threads.

```bash
gh api graphql -f query='query{repository(owner:"kubernetes-sigs",name:"lws"){pullRequest(number:1165){reviewThreads(first:100){nodes{id isResolved path comments(first:1){nodes{author{login} body}}}}}}}'
gh api graphql -f query='mutation($id:ID!){resolveReviewThread(input:{threadId:$id}){thread{isResolved}}}' -f id=THREAD_ID
```

### A4. kubernetes-sigs/lws#1216: resolve the addressed threads

Resolve these only:
- Copilot "KWOK pod-ready stage ignores LWS readiness gates" (Alice answered on Oct 25; fixed by the "Keep the group-ready gate to the controller on KWOK nodes" commit)
- yindia, Oct 25, `test/performance/runner/main.go`: "The poll loop starts only after the submission loop returns…" (fixed by "Observe completions while the scalability test is still writing")

Use the same GraphQL queries as A3, with `number:1216`.

### A5. llm-d/llm-d: membership request issue

```bash
gh issue create --repo llm-d/llm-d --title "Request: llm-d org membership for @aliceco01" --body-file - <<'EOF'
Hi, I'd like to request membership in the llm-d GitHub org. @ahg-g, as an llm-d approver, has agreed to sponsor me.

I'm starting a project on scaling coordination in llm-d. I also work upstream on LeaderWorkerSet, which llm-d's wide-EP and P/D guides run on. I'm a member of the Kubernetes org, and I'm the author of KEP-1064 (LWS placement policy) and its implementation in kubernetes-sigs/lws#1165.

My llm-d contributions so far:

PRs
- llm-d/llm-d-router#3625: ci: build images from the checked-out tree (merged)
- llm-d/llm-d-inference-sim#674: fix: reject empty messages array on /v1/chat/completions (merged)

Issues
- llm-d/llm-d#2854: safe-rollouts guide: singleton prefill rollout can return 503 on current LWS

Reviews
- llm-d/llm-d#2857, llm-d/llm-d-router#3456, llm-d/llm-d-autoscaling#1577

Discussions
- llm-d/llm-d-autoscaling#1525, llm-d/llm-d-router#2280

Thank you!
EOF
```

If Alice confirms 2FA is on, add the line "I have 2FA enabled on my GitHub account." before "Thank you!".

### A6. kubernetes-sigs/jobset#1367: answer andreyvelich (asked Oct 26)

Reply in the review thread under Alice's Oct 24 inline comment. If replying in the thread fails, post a PR comment instead.

```text
@andreyvelich no, LWS doesn't read `scheduling.k8s.io/workload-name`. Delegation goes through `scheduling.k8s.io/group-template-name` on the LWS: the LWS controller walks its controller-owner chain, finds the Workload whose `spec.controllerRef` matches the owner, and creates runtime PodGroups from that template (kubernetes-sigs/lws#1144, kubernetes-sigs/lws#1162). It rejects a delegated LWS without a controller owner, and more than one matching Workload is an error. `scheduling.k8s.io/parent-compositepodgroup` is declared but not honored yet.
```

## B. Ask Alice first

- llm-d/llm-d#2857: approve. larainema addressed her review, and all 4 nightly P/D jobs passed at 61530b7.
- kueue#17500, llm-d-router#3456: the review drafts in `drafts/` on `aliceco01/lws:status-notes`.
- llm-d-autoscaling#1525: reply to mamy-CS's "coordinate with #1598 owner". The design draft is on `status-notes` under `drafts/llm-d-autoscaling-1525/`.

## C. Slack (no gh), for Alice to send to ahg-g

> I pushed fixes for both of your reviews on the LWS placementPolicy PR (#1165): the pre-upgrade revision handling with the integration cases, and the two DisaggregatedSet points (role exclusive placement rejected on any key, and placement in ComputeRevisionV1). CLA is green. Ready for another look when you have time.

## Report back

For each item, report one line: posted (with link), skipped (with reason), or failed (with error).
