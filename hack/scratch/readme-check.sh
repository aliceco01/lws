#!/usr/bin/env bash
# Scratch check, not for upstream: runs what test/performance/README.md
# documents but the baseline run does not exercise, on a cluster set up with
# `make test-performance USE_EXISTING_CLUSTER=true KWOK_NODES=5`.
set -uo pipefail
KUBECTL=${KUBECTL:-kubectl}
SMOKE=test/performance/configs/smoke/generator.yaml
out=${ARTIFACTS:-.}/readme-check
mkdir -p "$out"
fail=0
ok()  { echo "PASS: $*"; }
bad() { echo "FAIL: $*"; fail=1; }
# Each run deletes its namespace on exit; the next run needs it gone.
wait_ns() { $KUBECTL wait --for=delete namespace/lws-performance --timeout=5m >/dev/null 2>&1 || true; }

echo "== KWOK_NODES=5 gives 5 simulated nodes"
n=$($KUBECTL get nodes -l type=kwok --no-headers | wc -l)
[ "$n" = 5 ] && ok "KWOK_NODES: $n nodes" || bad "KWOK_NODES: want 5 nodes, got $n"

echo "== the runner on its own, as in the README"
rm -rf /tmp/lws-performance
wait_ns
if go run ./test/performance/runner --config $SMOKE --output-dir /tmp/lws-performance > "$out/standalone.log" 2>&1 \
   && [ -s /tmp/lws-performance/summary.yaml ]; then
  ok "standalone runner wrote summary.yaml with $(grep -c '^- controller:' /tmp/lws-performance/summary.yaml) phases"
else
  bad "standalone runner"; tail -20 "$out/standalone.log"
fi

echo "== a limit above the measurement fails the run and still writes the summary"
printf 'phases:\n  create:\n    wallTimeMs: 1\n' > "$out/tight-rangespec.yaml"
rm -rf "$out/tight"; mkdir -p "$out/tight"
wait_ns
go run ./test/performance/runner --config $SMOKE --rangespec "$out/tight-rangespec.yaml" --output-dir "$out/tight" > "$out/tight.log" 2>&1
rc=$?
if [ $rc -ne 0 ] && grep -q "limits of .* exceeded" "$out/tight.log" && grep -q "wallTimeMs is .* above the limit of 1" "$out/tight.log" && [ -s "$out/tight/summary.yaml" ]; then
  ok "range spec failure: exit $rc, reported, summary.yaml written"
else
  bad "range spec failure (exit $rc)"; tail -20 "$out/tight.log"
fi

echo "== a later phase failing keeps the earlier phases in the summary"
cat > "$out/stuck.yaml" <<YAML
namespace: lws-performance
timeout: 2m
nodeSelector: {type: kwok}
tolerations:
- {key: kwok.x-k8s.io/node, operator: Exists, effect: NoSchedule}
classes:
- {name: ordinal, count: 5, replicas: 2, size: 2}
scale:
# 5 nodes hold 1250 pods, so 2000 pods can never be Ready.
- {name: too-big, replicas: {ordinal: 200}}
YAML
rm -rf "$out/stuck"; mkdir -p "$out/stuck"
wait_ns
go run ./test/performance/runner --config "$out/stuck.yaml" --output-dir "$out/stuck" > "$out/stuck.log" 2>&1
rc=$?
if [ $rc -ne 0 ] && grep -q "phase too-big" "$out/stuck.log" && grep -q "name: create" "$out/stuck/summary.yaml" \
   && ! grep -q "name: too-big" "$out/stuck/summary.yaml"; then
  ok "later phase failure: exit $rc, summary.yaml keeps create only"
else
  bad "later phase failure (exit $rc)"; tail -20 "$out/stuck.log"; cat "$out/stuck/summary.yaml" 2>/dev/null
fi
exit $fail
