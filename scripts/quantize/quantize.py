"""Produce the FP8 checkpoint for experiment B2 (docs/02-architecture.md).

Weights: FP8 (E4M3), one static scale per output channel. Activations: FP8 with a
scale computed per token at run time. That is llm-compressor's FP8_DYNAMIC scheme:
it needs no calibration data, because nothing about the activations is fixed ahead
of time. Every Linear layer is quantized except ``lm_head``, which stays in bf16:
it maps to the 152k-token vocabulary, and its errors land directly on the output
distribution.

Runs in the GPU host's distrobox (``requirements.txt`` pins the toolchain):

    python quantize.py --model Qwen/Qwen2.5-7B-Instruct --out <dir>

Writes the compressed checkpoint to ``<dir>`` (llm-compressor adds its
``recipe.yaml``), plus ``provenance.json``: the source snapshot, tool versions,
scheme, ignore list and timing, so the checkpoint can be traced to what made it.
"""

from __future__ import annotations

import argparse
import json
import time
from datetime import UTC, datetime
from importlib.metadata import version
from pathlib import Path

SCHEME = "FP8_DYNAMIC"
IGNORE = ("lm_head",)


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    p.add_argument("--model", required=True, help="Hugging Face model id or path")
    p.add_argument("--out", required=True, type=Path, help="output directory")
    return p.parse_args()


def snapshot_of(model_id: str) -> str:
    """The resolved local snapshot, so provenance names exact weights."""
    from huggingface_hub import snapshot_download

    return snapshot_download(model_id, local_files_only=True)


def main() -> None:
    args = parse_args()
    if args.out.exists() and any(args.out.iterdir()):
        msg = f"{args.out} is not empty; refusing to overwrite a checkpoint"
        raise SystemExit(msg)

    import torch
    from llmcompressor import oneshot
    from llmcompressor.modifiers.quantization import QuantizationModifier
    from transformers import AutoModelForCausalLM, AutoTokenizer

    src = snapshot_of(args.model)
    started = time.monotonic()
    model = AutoModelForCausalLM.from_pretrained(src, torch_dtype="auto")
    tokenizer = AutoTokenizer.from_pretrained(src)

    recipe = QuantizationModifier(targets="Linear", scheme=SCHEME, ignore=list(IGNORE))
    oneshot(model=model, recipe=recipe)

    args.out.mkdir(parents=True, exist_ok=True)
    model.save_pretrained(args.out, save_compressed=True)
    tokenizer.save_pretrained(args.out)

    provenance = {
        "source_model": args.model,
        "source_snapshot": Path(src).name,
        "scheme": SCHEME,
        "targets": "Linear",
        "ignore": list(IGNORE),
        "calibration": "none (dynamic per-token activation scales)",
        "tools": {
            name: version(name)
            for name in ("llmcompressor", "compressed-tensors", "transformers", "torch")
        },
        "cuda_device": torch.cuda.get_device_name(0)
        if torch.cuda.is_available()
        else None,
        "seconds": round(time.monotonic() - started, 1),
        "created_utc": datetime.now(UTC).isoformat(timespec="seconds"),
    }
    (args.out / "provenance.json").write_text(json.dumps(provenance, indent=2) + "\n")
    print(json.dumps(provenance, indent=2))


if __name__ == "__main__":
    main()
