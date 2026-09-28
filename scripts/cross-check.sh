#!/usr/bin/env bash
# Cross-check the harness against vLLM's own client (docs/02-architecture.md,
# "Cross-check"). Runs on the GPU host, from the deploy directory (bin/bench
# and compose/ side by side), with the baseline engine already up and warm.
#
#   scripts/cross-check.sh <out-dir> [concurrency]
#
# Order: ours, vLLM's, ours again. The repeat measures run-to-run drift on
# this engine, so a gap between the two clients can be read against it. The
# interactive profile's shape is matched: ~300 shared prefix tokens, ~100
# unique, output length uniform over 96-160 tokens (the same rule on both
# sides) with ignore_eos, temperature 0, closed loop at a
# fixed concurrency.
set -euo pipefail

out=${1:?usage: $0 <out-dir> [concurrency]}
c=${2:-8}
model=Qwen/Qwen2.5-7B-Instruct
base=http://127.0.0.1:8000
here=$(cd "$(dirname "$0")/.." && pwd)
bench=$here/bin/bench
compose=$here/compose

mkdir -p "$out"
argv=$("$compose/engine.sh" argv)
image=$(sed -n 's/^VLLM_IMAGE=//p' "$compose/.env")

ours() {
	"$bench" run --profile interactive --concurrency "$c" --warmup 10s --duration 60s \
		--base-url "$base" --model "$model" --out "$out/$1" \
		--engine-config baseline --engine-argv "$argv" --engine-image "$image"
}

vllm_args=(
	vllm bench serve --backend openai-chat --endpoint /v1/chat/completions
	--base-url "$base" --model "$model"
	--dataset-name random --random-prefix-len 300 --random-input-len 100
	--random-output-len 128 --random-range-ratio '{"input": 0, "output": 0.25}' --ignore-eos --temperature 0
	--max-concurrency "$c" --num-prompts 400 --num-warmups 16 --seed 1
	--percentile-metrics ttft,tpot,itl,e2el --metric-percentiles 50,95,99
	--save-result --save-detailed --result-dir /tmp/vsb-verify --result-filename vllm-bench.json
)

ours a

mkdir -p "$out/vllm"
printf '%s\n' "${vllm_args[*]}" >"$out/vllm/command.txt"
"$bench" sample --base-url "$base" --out "$out/vllm" &
sampler=$!
docker exec vsb-vllm rm -rf /tmp/vsb-verify
docker exec vsb-vllm "${vllm_args[@]}" 2>&1 | tee "$out/vllm/stdout.txt"
kill -INT "$sampler"
wait "$sampler" || true
docker cp vsb-vllm:/tmp/vsb-verify/vllm-bench.json "$out/vllm/vllm-bench.json"

ours b
echo "done: $out"
