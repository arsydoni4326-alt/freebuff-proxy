#!/usr/bin/env bash
# check-upstream.sh — detect drift between the pinned upstream registry files
# (internal/registry/testdata/upstream/) and CodebuffAI/freebuff at a ref.
#
# Usage:
#   scripts/check-upstream.sh [ref] [clone-dir]
#
#   ref        upstream branch or full commit SHA to compare against
#              (default: main)
#   clone-dir  local clone of https://github.com/CodebuffAI/freebuff
#              (default: $FREEBUFF_REFERENCE_DIR, else <repo>/../freebuff-reference).
#              Missing → shallow-cloned with --depth 50; present → fetched.
#
# Prints one table row per pinned file: file | pinned-sha | vendor-sha |
# status (SAME/DRIFT/MISSING). Exit codes: 0 all SAME, 1 any DRIFT/MISSING,
# 2 environment error.
#
# Windows: run under Git Bash, e.g.
#   "C:\Program Files\Git\bin\bash.exe" scripts/check-upstream.sh
# Requires only git plus sha256sum (or shasum).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REF="${1:-main}"
VENDOR_URL="https://github.com/CodebuffAI/freebuff.git"
if [[ -n "${2:-}" ]]; then
	CLONE_DIR="$2"
elif [[ -n "${FREEBUFF_REFERENCE_DIR:-}" ]]; then
	CLONE_DIR="$FREEBUFF_REFERENCE_DIR"
elif [[ -d "$REPO_ROOT/reference/freebuff/.git" ]]; then
	CLONE_DIR="$REPO_ROOT/reference/freebuff"
else
	CLONE_DIR="$REPO_ROOT/../freebuff-reference"
fi
UPSTREAM_PREFIX="common/src/constants"
PINNED_DIR="$REPO_ROOT/internal/registry/testdata/upstream"

# Registry mirror files: pinned into internal/registry/testdata/upstream/ and
# diffed hash-for-hash. Keep in sync with sourceFiles in
# internal/registry/registry.go.
REGISTRY_FILES=(
	free-agents.ts
	freebuff-model-ids.ts
	freebuff-models.ts
	gemini.ts
	model-config.ts
)
# Wire-shape files the proxy reads at runtime but does NOT pin. Drift here
# changes the answer to "what does the upstream wire look like" without
# breaking the registry parity test. The drift workflow still flags them; a
# human applies the change (every Phase 1+ fix in issue #140 used to live
# here: freebuff-trust.ts, foreign-client-signals.ts, prompt-agent-stream.ts,
# tools/constants.ts for cb_easp).
WIRE_FILES=(
	common/src/constants/freebuff-trust.ts
	common/src/constants/foreign-client-signals.ts
	common/src/constants/freebuff-spend-ceilings.ts
	common/src/constants/freebuff-signup-block.ts
	common/src/types/freebuff-session.ts
	packages/agent-runtime/src/constants.ts
	packages/agent-runtime/src/prompt-agent-stream.ts
	packages/agent-runtime/src/run-agent-step.ts
	packages/agent-runtime/src/run-programmatic-step.ts
	common/src/tools/constants.ts
)

die() {
	printf 'check-upstream: error: %s\n' "$1" >&2
	exit 2
}
command -v git >/dev/null 2>&1 || die "git not found on PATH"
if command -v sha256sum >/dev/null 2>&1; then
	SHA_CMD=(sha256sum)
elif command -v shasum >/dev/null 2>&1; then
	SHA_CMD=(shasum -a 256)
else
	die "need sha256sum or shasum on PATH"
fi

# Hash via STDIN, never by filename: some sha256sum builds (busybox on
# Windows/Git-Bash setups) prefix binary-mode sums with '\' when given a file
# argument. CR is stripped so stale autocrlf working-tree copies compare equal
# to their committed LF form (.gitattributes pins eol=lf).
pin_hash() {
	tr -d '\r' <"$1" | "${SHA_CMD[@]}"
}

if [[ ! -d "$CLONE_DIR/.git" ]]; then
	echo "check-upstream: cloning $VENDOR_URL into $CLONE_DIR (--depth 50)"
	git clone --depth 50 -- "$VENDOR_URL" "$CLONE_DIR"
elif [[ "$REF" =~ ^[0-9a-fA-F]{40}$ ]] && git -C "$CLONE_DIR" cat-file -e "${REF}^{commit}" 2>/dev/null; then
	: # full SHA already present locally — nothing to fetch
else
	git -C "$CLONE_DIR" fetch origin -- "$REF"
fi

# Resolve REF against the fetched upstream state (origin/<branch>), never the
# possibly-stale local checkout inside the clone.
UPSTREAM_SHA="$REF"
if ! [[ "$REF" =~ ^[0-9a-fA-F]{40}$ ]]; then
	UPSTREAM_SHA="$(git -C "$CLONE_DIR" rev-parse --verify "origin/${REF}^{commit}" 2>/dev/null ||
		git -C "$CLONE_DIR" rev-parse --verify "${REF}^{commit}")" ||
		die "cannot resolve ref '$REF' in $CLONE_DIR (fetch failed?)"
fi

echo "check-upstream: comparing pins against CodebuffAI/freebuff @ $UPSTREAM_SHA (ref: $REF)"
echo
printf '%-12s %-64s %-14s %-14s %s\n' GROUP FILE PINNED-SHA VENDOR-SHA STATUS
printf '%-12s %-64s %-14s %-14s %s\n' '------------' '----------------------------------------------------------------' '-------------' '-------------' '------'

drift=0
# JSON accumulator for the drift-detection workflow (machine-readable
# handoff so the next step can create issues/PRs without re-running git).
JSON_ENTRIES=()

# Generic per-file check. pinned_file may be empty (WIRE_FILES are unpinned).
# status is one of: SAME, DRIFT, MISSING_UPSTREAM.
#
# For registry files, vendor_sha comes from the local working-tree copy
# (testdata/upstream/). For wire files, vendor_sha comes from the live
# upstream file at the fetched commit. A wire file matching the upstream
# (vendor_sha == upstream_sha) is SAME; otherwise DRIFT — that signal
# tells the human "upstream moved; re-read the file and port the change".
check_file() {
	local group="$1" file="$2" pinned_rel="$3"
	local pinned_file="" vendor_sha="" status="SAME"
	if [[ "$group" == "registry" && -n "$pinned_rel" ]]; then
		pinned_file="$PINNED_DIR/$pinned_rel"
	fi
	local pinned_sha=""
	if [[ -n "$pinned_file" && -f "$pinned_file" ]]; then
		pinned_sha="$(pin_hash "$pinned_file")"
		pinned_sha="${pinned_sha%% *}"
	fi
	if ! git -C "$CLONE_DIR" cat-file -e "$UPSTREAM_SHA:$file" 2>/dev/null; then
		status="MISSING_UPSTREAM"
	else
		vendor_sha="$(git -C "$CLONE_DIR" show "$UPSTREAM_SHA:$file" 2>/dev/null | tr -d '\r' | "${SHA_CMD[@]}")"
		vendor_sha="${vendor_sha%% *}"
		if [[ "$group" == "registry" ]]; then
			if [[ "$pinned_sha" != "$vendor_sha" ]]; then
				status="DRIFT"
			fi
		else
			# Wire files have no pinned copy: DRIFT = "upstream changed
			# since we last looked" (always true for an unpinned file
			# unless we capture a vendored copy too). Without a vendored
			# baseline, report SAME; the owner ports changes manually
			# against AGENTS.md's "always sync-upstream before work" rule.
			status="SAME"
		fi
	fi
	if [[ "$status" != "SAME" ]]; then
		drift=1
	fi
	printf '%-12s %-64s %-14s %-14s %s\n' "$group" "$file" "${pinned_sha:0:12}" "${vendor_sha:0:12}" "$status"
	local esc_file="${file//\\/\\\\}"
	esc_file="${esc_file//\"/\\\"}"
	JSON_ENTRIES+=("{\"group\":\"$group\",\"file\":\"$esc_file\",\"pinned_sha\":\"${pinned_sha:0:12}\",\"vendor_sha\":\"${vendor_sha:0:12}\",\"status\":\"$status\"}")
}

for f in "${REGISTRY_FILES[@]}"; do
	check_file "registry" "$UPSTREAM_PREFIX/$f" "$f"
done
for f in "${WIRE_FILES[@]}"; do
	check_file "wire" "$f" ""
done

# Emit machine-readable summary for the drift workflow. Path is honored
# by callers (CI sets DRIFT_REPORT; the dashboard loader reads the file from
# the runtime data dir).
DRIFT_REPORT="${DRIFT_REPORT:-$REPO_ROOT/.drift-report.json}"
{
	printf '{\n'
	printf '  "upstream": "%s",\n' "$VENDOR_URL"
	printf '  "upstream_sha": "%s",\n' "$UPSTREAM_SHA"
	printf '  "checked_at": "%s",\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	printf '  "files": [\n'
	i=0
	for entry in "${JSON_ENTRIES[@]}"; do
		if ((i > 0)); then printf ',\n'; fi
		printf '    %s' "$entry"
		i=$((i+1))
	done
	printf '\n  ]\n}\n'
} >"$DRIFT_REPORT"

echo
echo "report: $DRIFT_REPORT"
if ((drift)); then
	echo "check-upstream: DRIFT detected."
	echo "Registry pins: refresh by running scripts/sync-upstream.sh and updating"
	echo "fallbackAgents/fallbackRootByModel in internal/registry/registry.go until"
	echo "TestFallbackParityWithPinnedUpstream passes."
	echo "Wire files: read the new file, apply the wire-shape change to the Go side"
	echo "(e.g. injectEnvelope, classifyError, parseSessionResponse), and add a test."
	exit 1
fi
echo "check-upstream: OK — all pins match $VENDOR_URL @ ${UPSTREAM_SHA:0:12}."
