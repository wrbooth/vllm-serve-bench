# /// script
# requires-python = ">=3.12"
# dependencies = ["tokenizers>=0.21"]
# ///
"""Build the candidate word list for internal/prompts/words.txt.

Offline pre-screen only. The authoritative check is `bench prompts verify`
against the running engine, which prunes this list with `--write`.

Inputs (none are committed; see the wiki log entry for where they came from):
  --tokenizer  Qwen2.5 tokenizer.json from the model repo on Hugging Face
  --common     a common-English word list, one word per line, swears removed
  --dict       a spelling dictionary (e.g. /usr/share/dict/words), used to
               drop acronyms and brand names from the common list

A word is kept when it is 3-12 lowercase ASCII letters, is in both lists, is
not in BLOCKED, and " word" encodes to exactly one token.

    uv run scripts/wordlist/candidates.py --tokenizer tokenizer.json \
        --common common.txt --dict /usr/share/dict/words \
        > internal/prompts/words.txt
"""

import argparse
import re
from pathlib import Path

from tokenizers import Tokenizer

# Words that would make random word salad read badly in a public repo or a
# demo. Not a safety filter; the prompts are never shown to anyone.
BLOCKED = frozenset(
    [
        "abortion",
        "abuse",
        "adult",
        "bomb",
        "breast",
        "casino",
        "crime",
        "criminal",
        "dating",
        "dead",
        "death",
        "drug",
        "drugs",
        "fetish",
        "gambling",
        "gay",
        "gun",
        "guns",
        "kill",
        "killed",
        "killing",
        "murder",
        "naked",
        "nude",
        "oral",
        "porn",
        "rape",
        "sex",
        "sexual",
        "sexy",
        "slave",
        "slavery",
        "suicide",
        "terror",
        "terrorism",
        "terrorist",
        "victim",
        "victims",
        "violence",
        "violent",
        "war",
        "weapon",
        "weapons",
    ]
)

WORD = re.compile(r"[a-z]{3,12}")


def read_words(path: Path) -> list[str]:
    """Return the stripped, non-empty lines of path."""
    lines = path.read_text(encoding="utf-8").splitlines()
    return [w.strip() for w in lines if w.strip()]


def main() -> None:
    """Print the header and the sorted candidate words."""
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--tokenizer", type=Path, required=True)
    ap.add_argument("--common", type=Path, required=True)
    ap.add_argument("--dict", type=Path, required=True)
    args = ap.parse_args()

    tok = Tokenizer.from_file(str(args.tokenizer))
    dictionary = {w for w in read_words(args.dict) if w.islower()}
    kept = sorted(
        {
            w
            for w in read_words(args.common)
            if WORD.fullmatch(w)
            and w in dictionary
            and w not in BLOCKED
            and len(tok.encode(" " + w, add_special_tokens=False).ids) == 1
        }
    )
    print("# Candidate list: common English words that are one Qwen2.5 token")
    print("# with a leading space, pre-screened offline by")
    print("# scripts/wordlist/candidates.py. Not yet confirmed against the")
    print("# engine; `bench prompts verify --write` replaces this header.")
    for w in kept:
        print(w)


if __name__ == "__main__":
    main()
