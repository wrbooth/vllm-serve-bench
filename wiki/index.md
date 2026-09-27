# Index

Catalog of the wiki, one line per page. Update on every ingest (schema:
[AGENTS.md](../AGENTS.md), "The wiki"). Read this first when answering
questions, then drill into the pages.

## Overview

- [project.md](project.md) — goal, deadlines, status of each deliverable, where the design lives.

## Systems

- [gpu-host.md](gpu-host.md) — the RTX 5090 lab host: what the card is and is not, Docker CDI GPU access, the pinned vLLM image, cached models, sharing the card.

## Concepts

- [prior-art-5090.md](prior-art-5090.md) — what earlier vLLM tuning on this same card already measured, and which lessons carry into this build.
- [walkthrough-topics.md](walkthrough-topics.md) — the topics the walkthrough must cover and the vocabulary to be ready for, each mapped to where this repo answers it.

## Ingested sources

- [2026-09-12-llm-wiki-pattern.md](../raw/2026-09-12-llm-wiki-pattern.md) — the charter: Karpathy's LLM-wiki pattern (raw sources / wiki / schema; ingest, query, lint; index + log).
