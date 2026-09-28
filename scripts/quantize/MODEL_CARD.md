---
license: apache-2.0
base_model: Qwen/Qwen2.5-7B-Instruct
base_model_relation: quantized
pipeline_tag: text-generation
library_name: transformers
tags:
  - fp8
  - llm-compressor
  - compressed-tensors
  - vllm
---

# Qwen2.5-7B-Instruct-FP8-Dynamic

An FP8 quantization of [Qwen/Qwen2.5-7B-Instruct](https://huggingface.co/Qwen/Qwen2.5-7B-Instruct),
produced with [llm-compressor](https://github.com/vllm-project/llm-compressor) for the
benchmark project [vllm-serve-bench](https://github.com/wrbooth/vllm-serve-bench). Not an official
Qwen release.

## Scheme

- **Weights:** every `Linear` layer is FP8 (E4M3) with one static scale per output channel.
- **Activations:** FP8, with a scale computed per token at run time (dynamic), so no calibration
  data was used.
- **Kept in bf16:** `lm_head`. It maps to the full vocabulary, and its errors land directly on the
  output distribution.
- **KV cache:** not quantized.
- **Format:** `compressed-tensors`. vLLM reads the scheme from `config.json`, so no
  `--quantization` flag is needed.

`recipe.yaml` is the exact llm-compressor recipe. `provenance.json` records the source snapshot,
tool versions, device and time taken.

## Reproduce

```sh
pip install -r scripts/quantize/requirements.txt   # pinned toolchain
python scripts/quantize/quantize.py --model Qwen/Qwen2.5-7B-Instruct --out <dir>
```

Both files are in the [repository](https://github.com/wrbooth/vllm-serve-bench/tree/main/scripts/quantize).

## Serve

```sh
vllm serve wrbooth/Qwen2.5-7B-Instruct-FP8-Dynamic
```

Tested with vLLM 0.29.0 on an RTX 5090 (Blackwell, sm_120), where vLLM selects a CUTLASS FP8
kernel.

## Measured

Serving performance against the bf16 original, with the same engine flags, sweep and SLOs, is in
[docs/03-results.md](https://github.com/wrbooth/vllm-serve-bench/blob/main/docs/03-results.md)
(Experiment B2). Every figure there is generated from committed raw results.

A quality smoke check is in
[results/quality/report.md](https://github.com/wrbooth/vllm-serve-bench/blob/main/results/quality/report.md):
greedy decoding on a small fixed set of exact-answer and open prompts, compared with bf16.

## Limitations

- The quality check is a smoke test on a small hand-written prompt set, scored by containment. It
  is not an evaluation. Use a proper eval suite before relying on this checkpoint.
- FP8 kernels need hardware support (Ada, Hopper, Blackwell). Elsewhere vLLM may fall back or
  refuse.
- Performance results come from one consumer GPU. They do not transfer to other hardware.

## License

Apache 2.0, as for the base model.
