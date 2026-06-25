#!/usr/bin/env bash
#
# Real-API end-to-end smoke test for the llmx CLI.
#
# Builds the binary, runs the unit suite as a free gate, then exercises the
# main user-facing paths against a LIVE OpenAI-compatible endpoint using the
# profile in ~/.llmx/credentials.
#
# Safety / isolation:
#   - Credentials are copied (not read) into a throwaway $HOME so this never
#     prints the api_key and never pollutes the real ~/.llmx/sessions.
#   - Both temp dirs are removed on exit.
#
# Cost: makes roughly 8 real API calls. Keep prompts tiny.
#
# Usage:
#   scripts/e2e.sh [profile]      # default: the [default] profile
#   E2E_SKIP_UNIT=1 scripts/e2e.sh   # skip the go test gate
#
# Exit: 0 if every check passed, 1 if any check failed, 2 on setup error.

set -uo pipefail

PROFILE="${1:-}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT" || { echo "FATAL: cannot cd to repo root" >&2; exit 2; }

CREDS="$HOME/.llmx/credentials"
if [[ ! -f "$CREDS" ]]; then
  echo "FATAL: $CREDS not found. Create it first (see README)." >&2
  exit 2
fi

# --- free gate: unit tests ---------------------------------------------------
if [[ "${E2E_SKIP_UNIT:-0}" != "1" ]]; then
  echo "== unit gate: go vet && go test ./... =="
  if ! go vet ./... || ! go test ./... ; then
    echo "FATAL: unit gate failed; not spending API calls. Fix tests first." >&2
    exit 2
  fi
  echo
fi

# --- build -------------------------------------------------------------------
BIN_DIR="$(mktemp -d)"
BIN="$BIN_DIR/llmx"
echo "== build =="
if ! go build -o "$BIN" . ; then
  echo "FATAL: build failed" >&2
  rm -rf "$BIN_DIR"
  exit 2
fi

# --- isolated HOME (copy creds without reading them) -------------------------
TMPH="$(mktemp -d)"
mkdir -p -m 700 "$TMPH/.llmx"
cp "$CREDS" "$TMPH/.llmx/credentials"
chmod 600 "$TMPH/.llmx/credentials"
trap 'rm -rf "$TMPH" "$BIN_DIR"' EXIT

PROF_ARGS=()
[[ -n "$PROFILE" ]] && PROF_ARGS=(-p "$PROFILE")

run() { HOME="$TMPH" "$BIN" "${PROF_ARGS[@]}" "$@" < /dev/null; }

pass=0; fail=0
ok() { echo "  PASS: $1"; pass=$((pass + 1)); }
no() { echo "  FAIL: $1"; fail=$((fail + 1)); }

echo
echo "== real-API e2e (profile: ${PROFILE:-default}) =="

# [1] non-stream
echo "[1] non-stream"
out="$(run --no-stream 'Reply with exactly the word: pong')"; rc=$?
{ [[ $rc -eq 0 && -n "$out" ]] && ok "non-stream returned output (exit 0)"; } || no "non-stream (exit $rc, out='$out')"

# [2] streaming (forced; stdout is a pipe here)
echo "[2] streaming"
out="$(run --stream 'Count from 1 to 5, space separated.')"; rc=$?
{ [[ $rc -eq 0 && -n "$out" ]] && ok "stream returned output (exit 0)"; } || no "stream (exit $rc, out='$out')"

# [3] --json: single object with content + usage
echo "[3] --json"
out="$(run --json 'Reply with exactly the word: ok')"; rc=$?
if [[ $rc -eq 0 ]] && printf '%s' "$out" | grep -q '"content"' && printf '%s' "$out" | grep -q '"usage"'; then
  ok "--json single object with content+usage"
else
  no "--json (exit $rc): $out"
fi

# [4] -c continuation: establish a codeword, then recall it
echo "[4] -c continuation"
run --new --no-stream 'Remember this codeword: BANANA4242. Acknowledge with OK.' > /dev/null; rc1=$?
out="$(run -c --no-stream 'What was the codeword? Reply with just the codeword.')"; rc2=$?
sess="$(ls -t "$TMPH/.llmx/sessions/"*.json 2> /dev/null | head -1)"
turns=$(grep -o '"role"' "${sess:-/dev/null}" 2> /dev/null | wc -l | tr -d ' ')
{ [[ $rc1 -eq 0 && $rc2 -eq 0 && ${turns:-0} -ge 4 ]] && ok "-c continued conversation (session has ${turns} messages)"; } \
  || no "-c continuation (rc1=$rc1 rc2=$rc2 turns=${turns:-0})"
if printf '%s' "$out" | grep -q 'BANANA4242'; then
  echo "  info: recall OK (model echoed the codeword)"
else
  echo "  info: recall soft-miss (model did not echo codeword; not a hard failure)"
fi

# [5] error handling: unknown model -> exit 1 with a message
echo "[5] error handling"
out="$(HOME="$TMPH" "$BIN" "${PROF_ARGS[@]}" --no-stream -m 'no-such-model-zzz' 'hi' < /dev/null 2>&1)"; rc=$?
{ [[ $rc -eq 1 && -n "$out" ]] && ok "API error -> exit 1 with message"; } || no "error case (exit $rc, out='$out')"

# [6] stdout purity on error: nothing on stdout
echo "[6] stdout purity on error"
so="$(HOME="$TMPH" "$BIN" "${PROF_ARGS[@]}" --json -m 'no-such-model-zzz' 'hi' < /dev/null 2> /dev/null)"
{ [[ -z "$so" ]] && ok "error keeps stdout empty"; } || no "stdout not empty on error: '$so'"

# [7] secret non-leak across stdout+stderr
echo "[7] secret non-leak"
leak="$(HOME="$TMPH" "$BIN" "${PROF_ARGS[@]}" --verbose --no-stream 'Reply with: ok' < /dev/null 2>&1 \
  | grep -ciE 'sk-or-|sk-[a-z0-9]{16}|gsk_|bearer |authorization')"
{ [[ "${leak:-0}" -eq 0 ]] && ok "no api_key/Authorization in output"; } || no "possible secret leak (${leak} hits)"

# [8] --verbose shows the resolved endpoint on stderr
echo "[8] --verbose diagnostics"
verr="$(HOME="$TMPH" "$BIN" "${PROF_ARGS[@]}" --verbose --no-stream 'Reply with: ok' < /dev/null 2>&1 > /dev/null)"
{ printf '%s' "$verr" | grep -q 'endpoint:' && ok "--verbose prints endpoint to stderr"; } || no "--verbose missing endpoint (stderr='$verr')"

# [9] base_url normalization: a base_url that already ends in /chat/completions
#     must be stripped + warned, and still work (no doubled path 404).
echo "[9] base_url normalization"
ep="$(HOME="$TMPH" "$BIN" "${PROF_ARGS[@]}" --verbose --no-stream 'x' < /dev/null 2>&1 > /dev/null | sed -n 's/^endpoint: //p')"
if [[ -n "$ep" ]]; then
  so_file="$TMPH/_norm_out"
  werr="$(HOME="$TMPH" LLMX_BASE_URL="$ep" "$BIN" "${PROF_ARGS[@]}" --no-stream 'Reply with: ok' < /dev/null 2> /dev/null > "$so_file")"
  norm_warn="$(HOME="$TMPH" LLMX_BASE_URL="$ep" "$BIN" "${PROF_ARGS[@]}" --no-stream 'Reply with: ok' < /dev/null 2>&1 > /dev/null)"
  so="$(cat "$so_file")"
  if printf '%s' "$norm_warn" | grep -q 'stripped trailing /chat/completions' && [[ -n "$so" ]]; then
    ok "base_url with /chat/completions auto-stripped + warned + still works"
  else
    no "base_url normalization (warn='$norm_warn' out='$so')"
  fi
else
  no "base_url normalization: could not derive endpoint from --verbose"
fi

echo
echo "== summary: ${pass} passed, ${fail} failed =="
[[ $fail -eq 0 ]]
