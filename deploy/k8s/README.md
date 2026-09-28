# Kubernetes manifests

**Validated, not the primary runtime.** Every reported number comes from the
Compose deployment on one RTX 5090 ([../compose/](../compose/)). These
manifests are the same engine and the same sweep expressed for a cluster. CI
checks them against the Kubernetes schemas with `kubeconform -strict`
(`make lint-k8s`). They have not been applied to a cluster, so nothing here is
a result.

## What is here

| Path | Kind | Notes |
|---|---|---|
| `engine/namespace.yaml` | Namespace | `vllm-serve-bench`; everything else lives in it |
| `engine/model-cache-pvc.yaml` | PersistentVolumeClaim | HF cache (subPath `huggingface`, read-only in the pod) and vLLM's compile cache (subPath `vllm`) |
| `engine/vllm-deployment.yaml` | Deployment | the pinned image digest and the baseline flags from `deploy/compose/`, `Recreate` strategy, one `nvidia.com/gpu`, startup/readiness/liveness probes, 8 GiB `/dev/shm` |
| `engine/vllm-service.yaml` | Service | ClusterIP `vllm:8000` |
| `engine/vllm-networkpolicy.yaml` | NetworkPolicy | only bench pods reach the engine, because dev mode is on |
| `bench/bench-engine-configmap.yaml` | ConfigMap | engine config name, model, image and argv that the bench records |
| `bench/results-pvc.yaml` | PersistentVolumeClaim | run directories |
| `bench/bench-job.yaml` | Job | both profiles in sequence, `scripts/sweep.sh`'s flags |

All objects carry `app.kubernetes.io/part-of: vllm-serve-bench`, with
`name`/`component` of `vllm`/`engine` or `bench`/`loadgen`.

## Assumptions about the cluster

- GPU nodes are tainted `nvidia.com/gpu:NoSchedule` and labelled
  `nvidia.com/gpu.present=true` (NVIDIA GPU feature discovery, as the GPU
  Operator installs it). The NVIDIA device plugin advertises `nvidia.com/gpu`.
- A default StorageClass exists; neither PVC names one.
- The engine gets a whole node's single GPU. The bench does not request a GPU,
  so no `nvidia-smi` is injected into it and it runs with `--gpu-sampler=false`.
  In Compose the CDI device gives the bench `nvidia-smi` without a second
  allocation; on a cluster, GPU telemetry would come from dcgm-exporter
  (design, not built).
- The bench Job is pinned to the engine's node with pod affinity and tolerates
  the GPU taint, so TTFT does not include a node-to-node hop. It is the one CPU
  workload allowed on the GPU node.

## Running it

```sh
kubectl apply -f deploy/k8s/engine/
```

The engine runs with `HF_HUB_OFFLINE=1`, so the `model-cache` claim must
already hold `Qwen/Qwen2.5-7B-Instruct` under `huggingface/hub/` (for example,
a one-off pod that mounts the claim and runs `huggingface-cli download`). Then
record the engine's **actual** argv, not the one the manifest declares
(AGENTS.md, "Record the engine's actual argv"), and start the sweep:

```sh
argv=$(kubectl -n vllm-serve-bench exec deploy/vllm -- cat /proc/1/cmdline | tr '\0' ' ' | sed 's/ $//')
kubectl -n vllm-serve-bench create configmap bench-engine \
  --from-literal=ENGINE_CONFIG=baseline \
  --from-literal=MODEL=Qwen/Qwen2.5-7B-Instruct \
  --from-literal=ENGINE_IMAGE="$(sed -n 's/^VLLM_IMAGE=//p' deploy/compose/.env)" \
  --from-literal=ENGINE_ARGV="$argv" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n vllm-serve-bench delete job bench-sweep --ignore-not-found
kubectl apply -f deploy/k8s/bench/results-pvc.yaml -f deploy/k8s/bench/bench-job.yaml
```

The committed ConfigMap holds the argv the Deployment declares under the
image's entrypoint. That is a fallback only; a run that will be reported uses
the one read from the pod.

The image is distroless (no shell, no `tar`), so `kubectl cp` cannot read the
results out of the bench pod. Mount `bench-results` in a throwaway pod with a
shell and copy from there.

## EKS (design, not result)

The intended cluster is EKS with a g6e.xlarge node group (one L40S, 48 GB)
tainted as above ([docs/02-architecture.md](../../docs/02-architecture.md#kubernetes-validated-not-the-primary-runtime)).
It has not been run. If it is, it is a second hardware point reported on its
own, not merged into the 5090 tables. The explicit `--max-num-seqs` and
`--max-num-batched-tokens` keep the scheduler budgets the same on a card
whose vLLM defaults could differ.
