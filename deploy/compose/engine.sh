#!/usr/bin/env bash
# Run the engine for one experiment config, on the GPU host.
#
#   ./engine.sh up <config> <model>   start engine/<config>.env with <model>, wait until ready
#   ./engine.sh argv                  the running engine's actual command line
#   ./engine.sh down                  stop and remove the engine
#
# Env files are layered in this order, later wins: .env (committed defaults),
# engine/<config>.env (the experiment's flags), .env.local (this machine).
set -euo pipefail
cd "$(dirname "$0")"

compose() {
	local cfg=${CONFIG:-baseline}
	docker compose --env-file .env --env-file "engine/${cfg}.env" --env-file .env.local "$@"
}

# PID 1's argv is what vLLM parsed. The compose file and env files are what we
# asked for; after a wrapper once appended a default that overrode a tuned
# value, only this counts (AGENTS.md, "Record the engine's actual argv").
argv() {
	docker exec vsb-vllm cat /proc/1/cmdline | tr '\0' ' ' | sed 's/ $//'
	echo
}

case "${1:-}" in
up)
	[[ $# -eq 3 ]] || { echo "usage: $0 up <config> <model>" >&2; exit 2; }
	[[ -f "engine/$2.env" ]] || { echo "no engine/$2.env" >&2; exit 2; }
	[[ -f .env.local ]] || { echo "missing .env.local (HF_CACHE=..., MODELS_DIR=...)" >&2; exit 2; }
	export CONFIG=$2 MODEL=$3
	start=$(date +%s)
	compose up -d --force-recreate vllm
	until curl -fsS "http://127.0.0.1:${VLLM_PORT:-8000}/v1/models" >/dev/null 2>&1; do
		if [[ "$(docker inspect -f '{{.State.Running}}' vsb-vllm)" != true ]]; then
			docker logs --tail 50 vsb-vllm >&2
			echo "engine exited during startup" >&2
			exit 1
		fi
		sleep 2
	done
	echo "ready after $(($(date +%s) - start)) s"
	argv
	;;
argv) argv ;;
down) compose down ;;
*)
	sed -n '2,9p' "$0" >&2
	exit 2
	;;
esac
