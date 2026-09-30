#!/usr/bin/env bash
# Regenerate docs/charts/ from the committed run directories: the runs the
# README and docs/03 report on. Each SVG records the command that made it,
# and a test regenerates every committed chart from that command.
#
#   GOOS= make build && scripts/charts.sh
#
# --series fixes each engine config's name and color across every chart:
# the baseline is the reference (slot 0, neutral ink), B2 takes slot 1.
set -euo pipefail
cd "$(dirname "$0")/.."

bench=${BENCH:-bin/bench}
series=(
	--series 'baseline=Baseline (bf16)'
	--series 'a-mnbt8192@3=A: 8192-token steps'
	--series 'no-prefix-cache@2=B1: prefix cache off'
	--series 'b2-fp8@1=B2: FP8 weights'
)
i_base=results/baseline/interactive-baseline-20260928-015502
i_a=results/a-mnbt8192/interactive-a-mnbt8192-20260928-022649
i_b1=results/b1-no-prefix-cache/interactive-no-prefix-cache-20260928-232408
i_b2=results/b2-fp8/interactive-b2-fp8-20260928-224420
t_base=results/baseline/throughput-baseline-20260928-014014
t_a=results/a-mnbt8192/throughput-a-mnbt8192-20260928-023137
t_b1=results/b1-no-prefix-cache/throughput-no-prefix-cache-20260928-231559
t_b2=results/b2-fp8/throughput-b2-fp8-20260928-225015

"$bench" chart --kind latency --out docs/charts/interactive-saturation.svg "${series[@]}" \
	--title 'Interactive profile: where the baseline saturates' "$i_base"
"$bench" chart --kind latency --out docs/charts/throughput-saturation.svg "${series[@]}" \
	--title 'Throughput profile: where the baseline saturates' "$t_base"
"$bench" chart --kind latency --out docs/charts/interactive-experiments.svg "${series[@]}" \
	--title 'Interactive profile: experiments against the baseline' "$i_base" "$i_a" "$i_b1" "$i_b2"
"$bench" chart --kind latency --out docs/charts/throughput-experiments.svg "${series[@]}" \
	--title 'Throughput profile: experiments against the baseline' "$t_base" "$t_a" "$t_b1" "$t_b2"
"$bench" chart --kind engine --out docs/charts/throughput-engine.svg "${series[@]}" \
	--title 'Throughput profile: why the baseline saturates, and what FP8 changes' "$t_base" "$t_b2"
"$bench" chart --kind headline --out docs/charts/headline.svg "${series[@]}" \
	"$i_base" "$i_a" "$i_b1" "$i_b2" "$t_base" "$t_a" "$t_b1" "$t_b2"
