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
CLONE_DIR="${2:-${FREEBUFF_REFERENCE_DIR:-$REPO_ROOT/../freebuff-reference}}"
UPSTREAM_PREFIX="common/src/constants"
PINNED_DIR="$REPO_ROOT/internal/registry/testdata/upstream"

# Keep in sync with sourceFiles in internal/registry/registry.go.
FILES=(
	free-agents.ts
	freebuff-model-ids.ts
	freebuff-models.ts
	gemini.ts
	model-config.ts
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
printf '%-26s %-14s %-14s %s\n' FILE PINNED-SHA VENDOR-SHA STATUS
printf '%-26s %-14s %-14s %s\n' '-------------------------' '-------------' '-------------' '------'

drift=0
for f in "${FILES[@]}"; do
	pinned_file="$PINNED_DIR/$f"
	if [[ ! -f "$pinned_file" ]]; then
		printf '%-26s %-14s %-14s %s\n' "$f" '-' '-' 'MISSING'
		drift=1
		continue
	fi
	pinned_sha="$(pin_hash "$pinned_file")"
	pinned_sha="${pinned_sha%% *}"
	if ! git -C "$CLONE_DIR" cat-file -e "$UPSTREAM_SHA:$UPSTREAM_PREFIX/$f" 2>/dev/null; then
		printf '%-26s %-14s %-14s %s\n' "$f" "${pinned_sha:0:12}" '-' 'MISSING'
		drift=1
		continue
	fi
	vendor_sha="$(git -C "$CLONE_DIR" show "$UPSTREAM_SHA:$UPSTREAM_PREFIX/$f" | tr -d '\r' | "${SHA_CMD[@]}")"
	vendor_sha="${vendor_sha%% *}"
	status=SAME
	if [[ "$pinned_sha" != "$vendor_sha" ]]; then
		status=DRIFT
		drift=1
	fi
	printf '%-26s %-14s %-14s %s\n' "$f" "${pinned_sha:0:12}" "${vendor_sha:0:12}" "$status"
done

echo
if ((drift)); then
	echo "check-upstream: DRIFT detected."
	echo "Refresh the pins: copy the changed files into internal/registry/testdata/upstream/"
	echo "and update fallbackAgents/fallbackRootByModel in internal/registry/registry.go until"
	echo "TestFallbackParityWithPinnedUpstream passes."
	exit 1
fi
echo "check-upstream: OK — all pinned files match $VENDOR_URL @ ${UPSTREAM_SHA:0:12}."
