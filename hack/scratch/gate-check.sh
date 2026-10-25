#!/usr/bin/env bash
# Scratch check, not for upstream. Runs on a cluster set up by
# `make test-performance USE_EXISTING_CLUSTER=true` with the KWOK pod stages.
# 1. A Hash leader pod carries the group-ready gate, and the gate condition
#    was written by the LWS controller, not by KWOK.
# 2. When the group breaks, group-ready goes False and KWOK takes the leader's
#    Ready to False (lws-pod-gates-unready). When it heals, both go back True.
set -euo pipefail
KUBECTL=${KUBECTL:-kubectl}
STAGES=${STAGES:-test/performance/kwok/pod-stages.yaml}
NS=gate-check
GATE=leaderworkerset.sigs.k8s.io/group-ready
out=${ARTIFACTS:-.}/gate-check
mkdir -p "$out"

$KUBECTL create namespace $NS
cat <<YAML | $KUBECTL apply -f -
apiVersion: leaderworkerset.x-k8s.io/v1
kind: LeaderWorkerSet
metadata: {name: hash-gate, namespace: $NS}
spec:
  replicas: 2
  groupIdentity: Hash
  leaderWorkerTemplate:
    size: 3
    restartPolicy: None
    workerTemplate:
      spec:
        nodeSelector: {type: kwok}
        tolerations:
        - {key: kwok.x-k8s.io/node, operator: Exists, effect: NoSchedule}
        containers:
        - {name: c, image: registry.k8s.io/pause:3.10}
YAML
$KUBECTL -n $NS wait lws/hash-gate --for=condition=Available --timeout=5m

leader=$($KUBECTL -n $NS get pods -l leaderworkerset.sigs.k8s.io/name=hash-gate,leaderworkerset.sigs.k8s.io/worker-index=0 -o name | head -1)
key=$($KUBECTL -n $NS get "$leader" -o jsonpath='{.metadata.labels.leaderworkerset\.sigs\.k8s\.io/group-key}')
echo "leader: $leader (group-key $key)"
cond() { $KUBECTL -n $NS get "$leader" -o jsonpath="{.status.conditions[?(@.type==\"$1\")].status}"; }

echo "== check 1: gate on the pod, and who wrote it"
gates=$($KUBECTL -n $NS get "$leader" -o jsonpath='{.spec.readinessGates[*].conditionType}')
echo "readinessGates: $gates"
[[ " $gates " == *" $GATE "* ]] || { echo "FAIL: leader has no $GATE gate"; exit 1; }
echo "group-ready=$(cond $GATE) Ready=$(cond Ready)"
[ "$(cond $GATE)" = True ] && [ "$(cond Ready)" = True ] || { echo "FAIL: leader not ready"; exit 1; }
$KUBECTL -n $NS get "$leader" -o json --show-managed-fields > "$out/leader-before.json"
writers=$(python3 - "$out/leader-before.json" "$GATE" <<'PY'
import json, sys
pod = json.load(open(sys.argv[1])); gate = sys.argv[2]
key = 'k:{"type":"%s"}' % gate
for mf in pod["metadata"].get("managedFields", []):
    conds = mf.get("fieldsV1", {}).get("f:status", {}).get("f:conditions", {})
    if key in conds:
        print(mf["manager"])
PY
)
echo "managers of the $GATE condition: ${writers:-none}"
[ -n "$writers" ] || { echo "FAIL: no manager owns the gate condition"; exit 1; }
if echo "$writers" | grep -qi kwok; then echo "FAIL: KWOK wrote the gate condition"; exit 1; fi

echo "== check 2: gate drops and comes back"
$KUBECTL -n $NS get "$leader" -w -o jsonpath="{.metadata.resourceVersion} ready={.status.conditions[?(@.type==\"Ready\")].status} gate={.status.conditions[?(@.type==\"$GATE\")].status}{\"\\n\"}" > "$out/leader-watch.log" &
watch_pid=$!
# Keep the replacement worker Pending, so the group stays broken until we heal it.
$KUBECTL delete stage lws-pod-running
$KUBECTL -n $NS delete pod -l "leaderworkerset.sigs.k8s.io/group-key=$key,leaderworkerset.sigs.k8s.io/worker-index=1" --wait=true
$KUBECTL -n $NS wait "$leader" --for=condition=$GATE=false --timeout=2m
$KUBECTL -n $NS wait "$leader" --for=condition=Ready=false --timeout=2m
echo "broken: group-ready=$(cond $GATE) Ready=$(cond Ready)"
$KUBECTL apply -f "$STAGES"
$KUBECTL -n $NS wait "$leader" --for=condition=$GATE=true --timeout=2m
$KUBECTL -n $NS wait "$leader" --for=condition=Ready=true --timeout=2m
echo "healed: group-ready=$(cond $GATE) Ready=$(cond Ready)"
sleep 2; kill $watch_pid || true
echo "== leader transitions"; uniq -f1 "$out/leader-watch.log" | cut -d' ' -f2-
echo "PASS"
