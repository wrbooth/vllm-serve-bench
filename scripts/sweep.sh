#!/usr/bin/env bash
# One engine config's sweep: both profiles at their default concurrency lists
# (internal/prompts), into results/. Runs on the GPU host from the deploy
# directory (bin/bench and compose/ side by side), with the engine already up
# in the named config and warm.
#
#   scripts/sweep.sh <engine-config> <out-dir> [profile ...]
#
# bench run resets the prefix cache before every level and records the
# engine's argv, image digest, KV pool and host in each run's config.json.
#
# LEVELS_<PROFILE> (e.g. LEVELS_INTERACTIVE=32,64,128,256) overrides a
# profile's default concurrency list, for experiments that only need the
# levels around the baseline's knee.
set -euo pipefail

config=${1:?usage: $0 <engine-config> <out-dir> [profile ...]}
out=${2:?usage: $0 <engine-config> <out-dir> [profile ...]}
shift 2
profiles=("$@")
[[ ${#profiles[@]} -gt 0 ]] || profiles=(interactive throughput)

model=Qwen/Qwen2.5-7B-Instruct
here=$(cd "$(dirname "$0")/.." && pwd)
compose=$here/compose
argv=$("$compose/engine.sh" argv)
image=$(sed -n 's/^VLLM_IMAGE=//p' "$compose/.env")

for p in "${profiles[@]}"; do
	var=LEVELS_${p^^}
	levels=()
	[[ -n ${!var:-} ]] && levels=(--concurrency "${!var}")
	"$here/bin/bench" run --profile "$p" "${levels[@]}" --warmup 10s --duration 60s --seed 1 \
		--base-url http://127.0.0.1:8000 --model "$model" --out "$out" \
		--engine-config "$config" --engine-argv "$argv" --engine-image "$image"
done
