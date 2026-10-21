#!/usr/bin/env bash
# run-cli-tests.sh — Run memora-cli integration tests against a docker-compose setup.
#
# Usage:
#   ./test/fixtures/run-cli-tests.sh dev              # root docker-compose
#   ./test/fixtures/run-cli-tests.sh federation        # 3-node SQLite federation
#   ./test/fixtures/run-cli-tests.sh federation-prod   # 3-node Postgres federation
#
# Prerequisites:
#   - The target docker-compose setup must be running and healthy.
#   - memora-cli must be built (go build ./cmd/memora-cli/ or use the binary in PATH).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CLI="${MEMORA_CLI:-${PROJECT_ROOT}/memora-cli}"

TARGET="${1:-}"
if [[ -z "$TARGET" ]]; then
    echo "Usage: $0 <dev|federation|federation-prod>"
    exit 1
fi

ENV_FILE="$SCRIPT_DIR/env.${TARGET}"
if [[ ! -f "$ENV_FILE" ]]; then
    echo "Error: env file not found: $ENV_FILE"
    exit 1
fi

# shellcheck source=/dev/null
source "$ENV_FILE"

passed=0
failed=0
total=0

run_test() {
    local name="$1"
    shift
    total=$((total + 1))
    echo -n "  [$total] $name ... "
    if output=$("$@" 2>&1); then
        echo "PASS"
        passed=$((passed + 1))
    else
        echo "FAIL"
        echo "    $output" | head -5
        failed=$((failed + 1))
    fi
}

assert_contains() {
    local haystack="$1"
    local needle="$2"
    if echo "$haystack" | grep -q "$needle"; then
        return 0
    fi
    echo "Expected output to contain '$needle', got: $haystack"
    return 1
}

echo "=== memora-cli integration tests: $TARGET ==="
echo "    endpoint: $MEMORA_ENDPOINT"
echo ""

# --- Health ---
echo "[Health]"
run_test "health check" "$CLI" --endpoint "$MEMORA_ENDPOINT" --api-key "$MEMORA_API_KEY" health
run_test "ready check"  "$CLI" --endpoint "$MEMORA_ENDPOINT" --api-key "$MEMORA_API_KEY" ready

# --- Workspace lifecycle ---
echo "[Workspaces]"
WS_NAME="test-ws-$$"
WS_OUTPUT=$("$CLI" --endpoint "$MEMORA_ENDPOINT" --api-key "$MEMORA_API_KEY" -o json workspaces create --name "$WS_NAME" 2>&1) || true
WS_ID=$(echo "$WS_OUTPUT" | grep -o '"workspace_id":"[^"]*"' | head -1 | cut -d'"' -f4)

if [[ -n "$WS_ID" ]]; then
    echo "  Created workspace: $WS_ID"
    export MEMORA_WORKSPACE="$WS_ID"
else
    echo "  Warning: Could not create workspace, trying to list existing ones"
    WS_LIST=$("$CLI" --endpoint "$MEMORA_ENDPOINT" --api-key "$MEMORA_API_KEY" -o json workspaces list 2>&1) || true
    WS_ID=$(echo "$WS_LIST" | grep -o '"workspace_id":"[^"]*"' | head -1 | cut -d'"' -f4)
    if [[ -n "$WS_ID" ]]; then
        export MEMORA_WORKSPACE="$WS_ID"
        echo "  Using existing workspace: $WS_ID"
    else
        echo "  FATAL: No workspace available. Aborting."
        exit 1
    fi
fi

# --- Imprint tests ---
echo "[Imprint]"

run_test "imprint plain text" \
    "$CLI" --endpoint "$MEMORA_ENDPOINT" --api-key "$MEMORA_API_KEY" -w "$MEMORA_WORKSPACE" \
    imprint --from-file "$SCRIPT_DIR/sample.txt"

run_test "imprint markdown" \
    "$CLI" --endpoint "$MEMORA_ENDPOINT" --api-key "$MEMORA_API_KEY" -w "$MEMORA_WORKSPACE" \
    imprint --from-file "$SCRIPT_DIR/sample.md" --chunker markdown

run_test "imprint CSV with config" \
    "$CLI" --endpoint "$MEMORA_ENDPOINT" --api-key "$MEMORA_API_KEY" -w "$MEMORA_WORKSPACE" \
    imprint --from-file "$SCRIPT_DIR/sample.csv" --chunker csv --chunker-opt rows_per_cell=2 --chunker-opt has_header=true

run_test "imprint JSONL with config" \
    "$CLI" --endpoint "$MEMORA_ENDPOINT" --api-key "$MEMORA_API_KEY" -w "$MEMORA_WORKSPACE" \
    imprint --from-file "$SCRIPT_DIR/sample.jsonl" --chunker jsonl --chunker-opt lines_per_cell=2

run_test "imprint with tags" \
    "$CLI" --endpoint "$MEMORA_ENDPOINT" --api-key "$MEMORA_API_KEY" -w "$MEMORA_WORKSPACE" \
    imprint --text "Tagged memory content" --tag source=test --tag env="$TARGET"

# --- List + Recall ---
echo "[List & Recall]"

run_test "list memories" \
    "$CLI" --endpoint "$MEMORA_ENDPOINT" --api-key "$MEMORA_API_KEY" -w "$MEMORA_WORKSPACE" \
    list --limit 10

run_test "recall query" \
    "$CLI" --endpoint "$MEMORA_ENDPOINT" --api-key "$MEMORA_API_KEY" -w "$MEMORA_WORKSPACE" \
    recall "test content" --k 3

# --- Federation-specific tests ---
if [[ "$TARGET" == "federation" || "$TARGET" == "federation-prod" ]]; then
    echo "[Federation]"

    run_test "node1 health" \
        "$CLI" --endpoint "$MEMORA_FED_NODE1_ENDPOINT" --api-key "$MEMORA_FED_NODE1_API_KEY" health

    run_test "node2 health" \
        "$CLI" --endpoint "$MEMORA_FED_NODE2_ENDPOINT" --api-key "$MEMORA_FED_NODE2_API_KEY" health

    run_test "node3 health" \
        "$CLI" --endpoint "$MEMORA_FED_NODE3_ENDPOINT" --api-key "$MEMORA_FED_NODE3_API_KEY" health

    # Federated search, asserted on output (F-10). Peer node2→node1 at
    # runtime with a fresh key: node2 holds the key, node1 its hash. Node
    # IDs and peer URLs come from the target's env file.
    : "${MEMORA_FED_NODE1_ID:?env file must set MEMORA_FED_NODE1_ID}" "${MEMORA_FED_NODE2_ID:?}"
    : "${MEMORA_FED_NODE1_PEER_URL:?}" "${MEMORA_FED_NODE2_PEER_URL:?}"
    command -v jq >/dev/null || { echo "  federation tests need jq"; exit 1; }
    NODE1_ID="$MEMORA_FED_NODE1_ID"
    NODE2_ID="$MEMORA_FED_NODE2_ID"
    new_key() { echo "cli-e2e-$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')"; }
    FED_KEY=$(new_key)
    peer_post() { # endpoint api-key json
        curl -fsS -X POST "$1/v1/federation/peers" -H "Authorization: Bearer $2" \
            -H "Content-Type: application/json" -d "$3" >/dev/null
    }
    register_pair() { # key: node2 calls node1 with it; node1 holds its hash
        peer_post "$MEMORA_FED_NODE2_ENDPOINT" "$MEMORA_FED_NODE2_API_KEY" \
            "{\"node_id\":\"$NODE1_ID\",\"name\":\"node1\",\"url\":\"$MEMORA_FED_NODE1_PEER_URL\",\"api_key\":\"$1\"}" &&
        peer_post "$MEMORA_FED_NODE1_ENDPOINT" "$MEMORA_FED_NODE1_API_KEY" \
            "{\"node_id\":\"$NODE2_ID\",\"name\":\"node2\",\"url\":\"$MEMORA_FED_NODE2_PEER_URL\",\"inbound_key\":\"$1\"}"
    }
    run_test "register node2→node1 with a fresh key" register_pair "$FED_KEY"

    FED_PHRASE="federation probe $$ lives on node1"
    FED_WS=$("$CLI" --endpoint "$MEMORA_FED_NODE1_ENDPOINT" --api-key "$MEMORA_FED_NODE1_API_KEY" -o json \
        workspaces create --name "fed-test-$$" 2>/dev/null | jq -r '.id // empty')
    FED_MEM=$("$CLI" --endpoint "$MEMORA_FED_NODE1_ENDPOINT" --api-key "$MEMORA_FED_NODE1_API_KEY" -w "$FED_WS" -o json \
        imprint --text "$FED_PHRASE" 2>/dev/null | jq -r '.id // empty')
    run_test "seed a memory on node1" test -n "$FED_MEM"

    FED_OUT=$("$CLI" --endpoint "$MEMORA_FED_NODE2_ENDPOINT" --api-key "$MEMORA_FED_NODE2_API_KEY" -o json \
        search "federation probe $$" --federated --peers "$NODE1_ID" --mode keyword 2>&1) || true
    # Assert on the hit itself: a failed peer's entry also names node1.
    run_test "federated search on node2 returns node1's memory, attributed to node1" \
        jq -e --arg m "$FED_MEM" --arg n "$NODE1_ID" \
        'any(.results[]?; .memory_id == $m and .node_id == $n and .via == ("federation:" + $n))' <<<"$FED_OUT"
    run_test "no peer failed" jq -e '(.failed_peers // []) | length == 0' <<<"$FED_OUT"

    FETCH_OUT=$("$CLI" --endpoint "$MEMORA_FED_NODE2_ENDPOINT" --api-key "$MEMORA_FED_NODE2_API_KEY" -o json \
        fetch "$FED_MEM" --node "$NODE1_ID" 2>&1) || true
    run_test "fetch through node2 returns node1's full memory" \
        jq -e --arg m "$FED_MEM" --arg n "$NODE1_ID" --arg c "$FED_PHRASE" \
        '.id == $m and .node_id == $n and .content == $c' <<<"$FETCH_OUT"

    # Retire the test key: rotate both sides to a key nobody holds. The
    # peers stay (they are this stack's configured peers).
    run_test "rotate the test key out" register_pair "$(new_key)"
fi

# --- Cleanup ---
echo "[Cleanup]"
if [[ "$WS_NAME" == "test-ws-$$" && -n "$WS_ID" ]]; then
    run_test "delete test workspace" \
        "$CLI" --endpoint "$MEMORA_ENDPOINT" --api-key "$MEMORA_API_KEY" workspaces delete "$WS_ID"
fi

# --- Summary ---
echo ""
echo "=== Results: $passed/$total passed, $failed failed ==="
if [[ $failed -gt 0 ]]; then
    exit 1
fi
