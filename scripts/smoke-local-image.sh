#!/usr/bin/env bash
# smoke-local-image.sh IMAGE — start the memora-core local-embedder
# image (#4380), imprint three memories, and check that a vector search
# ranks the two semantically related ones above the unrelated one.
# Needs an x86-64 host with AVX (the bundled llama.cpp build uses it;
# amd64 emulation on Apple silicon does not provide it).
set -euo pipefail

IMAGE="${1:?usage: smoke-local-image.sh IMAGE}"
NAME="memora-local-smoke-$$"
PORT="${SMOKE_PORT:-17820}"
KEY="smoke-$(head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n')"
AGENT="c1a0e5d2-7b3f-4a6e-9d8c-1f2e3a4b5c6d"
U="http://127.0.0.1:${PORT}/v1"

cleanup() { docker logs "$NAME" >"${SMOKE_LOG:-/dev/null}" 2>&1 || true; docker rm -f "$NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker run -d --name "$NAME" --platform linux/amd64 -p "127.0.0.1:${PORT}:7777" -e MEMORA_API_KEY="$KEY" "$IMAGE" >/dev/null
for _ in $(seq 1 60); do
    curl -fsS -H "Authorization: Bearer $KEY" "http://127.0.0.1:${PORT}/readyz" >/dev/null 2>&1 && break
    sleep 1
done
docker logs "$NAME" 2>&1 | grep -q '"model":"local:nomic-embed-text-v1.5"' || { echo "FAIL: the image is not using the local embedder"; docker logs "$NAME"; exit 1; }

api() { curl -fsS --max-time 300 -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -H "Memora-Agent-Id: $AGENT" "$@"; }
WS=$(api -X POST "$U/workspaces" -d '{"name":"smoke"}' | jq -r .id)
imprint() { api -X POST "$U/workspaces/$WS/memories" -d "$(jq -n --arg c "$1" '{content:$c}')" | jq -r .id; }
CAT=$(imprint "The cat sat on the warm windowsill all afternoon.")
KITTEN=$(imprint "Kittens love to nap in sunny spots near windows.")
REVENUE=$(imprint "Quarterly revenue grew twelve percent on strong cloud sales.")

RESULT=$(api -X POST "$U/workspaces/$WS/recall" -d '{"query":"sleepy felines by the window","mode":"vector","k":3}')
echo "$RESULT" | jq -c '[.results[] | {memory_id, score}]'
ORDER=$(echo "$RESULT" | jq -r '[.results[].memory_id] | join(" ")')
LAST=$(echo "$ORDER" | awk '{print $3}')
if [[ "$(echo "$ORDER" | wc -w | tr -d ' ')" != 3 || "$LAST" != "$REVENUE" ]] || ! grep -q "$CAT" <<<"$ORDER" || ! grep -q "$KITTEN" <<<"$ORDER"; then
    echo "FAIL: vector search did not rank the cat memories above the revenue memory: $ORDER"
    exit 1
fi
STALE=$(curl -fsS -H "Authorization: Bearer $KEY" "http://127.0.0.1:${PORT}/readyz" | jq -r '.missing_vectors')
[[ "$STALE" == 0 ]] || { echo "FAIL: missing_vectors=$STALE"; exit 1; }
echo "PASS: local embedder image $IMAGE"
