"""Publish a checkpoint made by quantize.py to the Hugging Face Hub.

    python publish.py --dir <checkpoint> --repo <user>/<name> --token-file <path>

Copies MODEL_CARD.md in as the repo's README.md, then uploads the whole
directory, including recipe.yaml and provenance.json. The token is read from a
file, so it never appears in argv, the shell history or the environment of
other processes. The repo is created public if it does not exist.
"""

from __future__ import annotations

import argparse
import shutil
from pathlib import Path

CARD = Path(__file__).with_name("MODEL_CARD.md")


def main() -> None:
    p = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    p.add_argument("--dir", required=True, type=Path)
    p.add_argument("--repo", required=True)
    p.add_argument("--token-file", required=True, type=Path)
    args = p.parse_args()

    for required in ("config.json", "recipe.yaml", "provenance.json"):
        if not (args.dir / required).is_file():
            msg = f"{args.dir} has no {required}; not a quantize.py checkpoint"
            raise SystemExit(msg)

    from huggingface_hub import HfApi

    token = args.token_file.read_text().strip()
    api = HfApi(token=token)
    shutil.copyfile(CARD, args.dir / "README.md")
    api.create_repo(args.repo, repo_type="model", exist_ok=True)
    info = api.upload_folder(
        repo_id=args.repo,
        folder_path=args.dir,
        commit_message="FP8_DYNAMIC checkpoint from vllm-serve-bench scripts/quantize",
    )
    print(info)


if __name__ == "__main__":
    main()
