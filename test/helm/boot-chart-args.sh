#!/usr/bin/env bash
# Boots memora-core with the exact args the Helm chart renders, then runs
# the chart's rendered exec probe commands against it. Catches undefined
# flags and broken probes before they ship (Issue #5038).
#
# Usage: test/helm/boot-chart-args.sh   (from repo root; needs helm + go)
set -euo pipefail

CHART=charts/memora-core
PORT=17777
WORK=$(mktemp -d)
SERVER_PID=""
cleanup() {
	[[ -n "$SERVER_PID" ]] && kill "$SERVER_PID" 2>/dev/null || true
	rm -rf "$WORK"
}
trap cleanup EXIT

make build >/dev/null

# Every top-level values key must be consumed by a template; otherwise the
# chart advertises a setting that silently does nothing (#5983).
for key in $(grep -E '^[a-zA-Z][a-zA-Z0-9]*:' "$CHART/values.yaml" | cut -d: -f1); do
	if ! grep -rqE "\.Values\.$key\b" "$CHART/templates"; then
		echo "FAIL: values.yaml key '$key' is not used by any template" >&2
		exit 1
	fi
done
echo "ok: every top-level values key is used by a template"

# Default values must refuse to render: the server would refuse to start.
if helm template t "$CHART" >/dev/null 2>"$WORK/err"; then
	echo "FAIL: default values rendered; expected the no-auth guard to fail" >&2
	exit 1
fi
grep -q "set apiKey, or set allowNoAuth=true" "$WORK/err" ||
	{ echo "FAIL: unexpected render error:" >&2; cat "$WORK/err" >&2; exit 1; }
echo "ok: default values fail with the auth guard message"

# rendered_list <yaml-file> <key>: items of the first YAML list under <key>:,
# one per line, quotes stripped. Handles both block and inline-array forms.
rendered_list() {
	awk -v key="$2:" '
		$1 == key && NF == 1 { inlist = 1; next }
		inlist && $1 == "-" { sub(/^[ \t]*- /, ""); gsub(/"/, ""); print; next }
		inlist { exit }
	' "$1"
}
probe_cmd() { # <yaml-file> <probe>: the exec command array as a line
	awk -v probe="$2:" '
		$1 == probe { found = 1; next }
		found && $1 ~ /^[a-zA-Z]+:$/ && $1 != "exec:" && $1 != "command:" && NF == 1 { exit }
		found && $1 == "command:" { sub(/^[^[]*\[/, ""); sub(/\].*$/, ""); gsub(/[",]/, " "); print; exit }
	' "$1"
}

boot_variant() { # <name> <api-key> <helm --set args...>
	local name=$1 key=$2; shift 2
	local manifest="$WORK/$name.yaml" datadir="$WORK/$name-data"
	helm template t "$CHART" --show-only templates/deployment.yaml "$@" >"$manifest"

	mapfile -t args < <(rendered_list "$manifest" args)
	[[ "${args[0]}" == serve ]] || { echo "FAIL[$name]: first arg ${args[0]:-<none>} != serve" >&2; exit 1; }
	args=("${args[@]/--data-dir=\/data/--data-dir=$datadir}")

	MEMORA_API_KEY=$key MEMORA_ADDR=127.0.0.1:$PORT bin/memora-core "${args[@]}" >"$WORK/$name.log" 2>&1 &
	SERVER_PID=$!

	for probe in livenessProbe readinessProbe; do
		read -r -a cmd <<<"$(probe_cmd "$manifest" "$probe")"
		[[ "${cmd[0]:-}" == /usr/local/bin/memora-cli ]] ||
			{ echo "FAIL[$name]: $probe is not a memora-cli exec probe" >&2; exit 1; }
		cmd[0]=bin/memora-cli
		local ok=""
		for _ in $(seq 1 50); do
			if MEMORA_API_KEY=$key MEMORA_ENDPOINT=http://127.0.0.1:$PORT "${cmd[@]}" >/dev/null 2>&1; then ok=1; break; fi
			kill -0 "$SERVER_PID" 2>/dev/null || break
			sleep 0.2
		done
		[[ -n "$ok" ]] || { echo "FAIL[$name]: $probe (${cmd[*]}) never passed; server log:" >&2; cat "$WORK/$name.log" >&2; exit 1; }
		echo "ok[$name]: $probe passes"
	done

	kill "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null || true; SERVER_PID=""
}

boot_variant with-api-key chart-test-key --set apiKey=chart-test-key
boot_variant allow-no-auth "" --set allowNoAuth=true
