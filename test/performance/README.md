# Scalability test

Measures how the LWS controller handles creating and scaling many
LeaderWorkerSets, to give a performance baseline and catch regressions
(kubernetes-sigs/lws#1146). The scope is the controller and the control plane,
not inference throughput.

## How it works

- **Cluster**: `make test-performance` uses the e2e setup in
  `hack/e2e-test.sh`, a kind cluster running the controller built from the
  checkout, and adds simulated nodes from [KWOK](https://kwok.sigs.k8s.io). The
  API server, scheduler and LWS controller are real. KWOK reports the pods on
  its nodes Running and Ready, so thousands of pods need no kubelets and no
  images. `kwok/pod-stages.yaml` replaces KWOK's pod-ready stage so that, as
  with a kubelet, a pod is Ready only while its readiness gates are True. The
  group-ready gate of Hash identity is then set by the controller, not by KWOK.
- **Runner** (`runner/`): generates LeaderWorkerSets from a generator config,
  runs the phases below, reads the controller metrics, and writes
  `summary.yaml`. With a range spec it fails when a measurement is above its
  limit.
- **Configs** (`configs/`): `baseline` is the workload the limits are set for:
  many small LeaderWorkerSets and a few large ones, in both group identity
  modes. `smoke` has 20 small ones, for checking the harness itself. `dra`
  and `dra-smoke` use DRA, see [DRA workloads](#dra-workloads).

| Phase | Operation | A LeaderWorkerSet is done when |
| --- | --- | --- |
| `create` | Create every LeaderWorkerSet. | Its status for the current generation shows all replicas ready and `Available`. |
| each entry of `scale` | Set `spec.replicas` of the listed classes. | The same, with the new number of replicas and none left over. |

After each phase the runner checks that every LeaderWorkerSet has exactly
`replicas * size` pods, all Running and Ready, and that there are no others.
With DRA it also checks that the numbers of ResourceClaims and ComputeDomains
in the namespace match the targets, terminating ones included, so that the check
waits until the objects removed by a scale-down are gone.

## Results

`summary.yaml` has an entry per phase with:

- `leaderWorkerSets`, `groups` and `pods`: what the phase created or removed.
- `wallTimeMs` and `groupsPerSecond`: from the first write until the last
  LeaderWorkerSet was done.
- `p50Ms`, `p90Ms`, `p99Ms` and `maxMs`: latency over the LeaderWorkerSets, each
  from the moment the API server stored its write, so the runner's own
  client-side throttling is not counted. The runner polls an informer cache
  every 100ms, which is the resolution, and adds no load to the API server.
  Polling starts with the first write, so a LeaderWorkerSet that is done
  before the last write is not timed until then.
- `settleMs`: from the moment every LeaderWorkerSet was done until the check
  above passed, for example until the pods, ResourceClaims and ComputeDomains
  of removed groups were gone. Same 100ms resolution.
- `resourceClaims` and `computeDomains`: how many there were at the end of the
  phase, for configs that use them.
- `controller`: CPU seconds, peak resident memory and API requests by method of
  the controller pods during the phase, from `process_cpu_seconds_total`,
  `process_resident_memory_bytes` and `rest_client_requests_total`. The runner
  reads the metrics endpoint of each pod through `kubectl port-forward`, with a
  token of the controller service account, like the e2e metrics test.

The range spec sets upper bounds per phase, by the names above, for example
`wallTimeMs`, `p90Ms` or `controller.apiRequests`. A bound on a phase or a
measurement the run did not produce fails the check as well. The numbers depend
on the machine, so compare runs from the same environment.

## Usage

```bash
make test-performance
```

creates a kind cluster with its own kubeconfig, deploys KWOK and the
controller, runs the baseline workload, checks it against
`configs/baseline/rangespec.yaml`, and deletes the cluster. `summary.yaml` and
the cluster logs are written to `bin/performance/`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `PERFORMANCE_GENERATOR_CONFIG` | `configs/baseline/generator.yaml` | Workload to generate. |
| `PERFORMANCE_RANGESPEC` | `configs/baseline/rangespec.yaml` | Limits to check. Empty skips the check. |
| `KWOK_NODES` | `20` | Simulated nodes, with room for 250 pods each. |
| `KWOK_VERSION` | `v0.8.0` | KWOK release to install. |
| `PERFORMANCE_AUTO_COMPUTE_DOMAIN` | `false` | `true` installs the ComputeDomain CRD and enables the `AutoComputeDomain` feature gate. Needed by configs with `autoComputeDomain`. |
| `USE_EXISTING_CLUSTER` | `false` | `true` uses the current kubeconfig context and keeps the cluster. |

`make test-performance-dra` runs the `dra` workload in the same way, with the
ComputeDomain CRD and the `AutoComputeDomain` feature gate, and checks it
against `configs/dra/rangespec.yaml`. It sets `PERFORMANCE_AUTO_COMPUTE_DOMAIN`,
which `make test-performance` also takes.

To check the harness quickly:

```bash
PERFORMANCE_GENERATOR_CONFIG=$PWD/test/performance/configs/smoke/generator.yaml make test-performance
PERFORMANCE_GENERATOR_CONFIG=$PWD/test/performance/configs/dra-smoke/generator.yaml PERFORMANCE_RANGESPEC= make test-performance-dra
```

The runner also works on its own against a cluster that runs the controller
and has KWOK nodes, or real nodes once `nodeSelector` and `tolerations` are
removed from the config:

```bash
go run ./test/performance/runner --config test/performance/configs/smoke/generator.yaml --output-dir /tmp/lws-performance
```

## Generator config

```yaml
namespace: lws-performance   # created by the runner and deleted at the end
timeout: 15m                 # per phase, writes included
nodeSelector: {type: kwok}   # set on every pod, with the tolerations below
tolerations:
- {key: kwok.x-k8s.io/node, operator: Exists, effect: NoSchedule}
classes:                     # workload shapes
- name: small-hash
  count: 80                  # LeaderWorkerSets of this class
  replicas: 2                # groups in each
  size: 2                    # pods in each group
  groupIdentity: Hash        # Ordinal when unset
  resourceClaimTemplate: false # true: a ResourceClaim per pod, see below
  autoComputeDomain: false   # true: auto-compute-domain, see below
dra:                         # required when a class uses either of the above
  devicesPerNode: 250        # simulated devices published for each node
scale:                       # scaling phases, run in order after creation
- name: scale-up
  replicas: {small-hash: 4}
```

## DRA workloads

Two kinds of classes give their pods ResourceClaims
(kubernetes-sigs/lws#1350):

- `resourceClaimTemplate: true`: the runner creates a ResourceClaimTemplate
  for the class, and the leader and worker templates reference it, so that
  kube-controller-manager creates a ResourceClaim for every pod.
- `autoComputeDomain: true`: the LeaderWorkerSets have the
  `leaderworkerset.sigs.k8s.io/auto-compute-domain: enabled` annotation of
  [KEP-1259](../../keps/1259-auto-compute-domain/README.md). The controller
  creates a ComputeDomain for every group and injects a ResourceClaim for its
  channel into every pod, and removes the ComputeDomains of removed groups.
  The simulated nodes have no `nvidia.com/gpu`, so the templates name their
  containers in `leaderworkerset.sigs.k8s.io/compute-domain-containers`.

A class can set both, and its pods then have two claims each.

The simulated nodes run no DRA driver, so the runner plays its part, and the
rest is the real kube-controller-manager and scheduler:

- It creates a DeviceClass for the driver `performance.lws.x-k8s.io` and, for
  every node that matches `nodeSelector`, ResourceSlices with
  `dra.devicesPerNode` devices. Each claim asks for one device, so the
  scheduler allocates every claim before it binds the pod. The DeviceClass and
  ResourceSlices are removed at the end of the run.
- In place of the NVIDIA ComputeDomain controller, it creates the
  ResourceClaimTemplate named in each ComputeDomain, owned by the
  ComputeDomain so that it is removed with it. It finds new ComputeDomains by
  polling an informer cache every 100ms, which can add up to that much to a
  latency. It publishes no IMEX daemons and sets no ComputeDomain status, which
  the LWS controller does not read.

The ComputeDomain CRD comes from `test/integration/crd`, and
`config/kustomization.yaml` adds the ComputeDomain RBAC to the controller.

fake-gpu-operator, which the fake GPU e2e tests use, also has KWOK components
that publish ResourceSlices. The runner does not use them: they would put the
operator's own controllers and images in the measurement, and they select nodes
by the operator's node pool labels. The simulation above needs nothing outside
the cluster.

## Not covered yet

- Rolling updates, group recreation after a member failure, and deletion.
  With auto-compute-domain these also replace ComputeDomains.
- The `resourceClaims` of the leader and worker PodGroups in
  `spec.leaderWorkerTemplate.scheduling`, which need workload-aware scheduling.
- DisaggregatedSets, including their per-slice ComputeDomains.
- A periodic CI job. The limits in `configs/baseline/rangespec.yaml` stay loose
  until that job has produced enough runs to set them from data.
