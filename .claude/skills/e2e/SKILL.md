---
name: e2e
description: Run the real-API end-to-end smoke test for this llmx CLI project. Use after implementing a feature or fix (client, session, output, config, or CLI changes), before committing or releasing, or whenever the user asks to "verify against the real API", "run e2e", "実APIで動作確認", "e2e を回す". Builds the binary, runs the unit suite as a free gate, then exercises non-stream, streaming, --json, -c continuation, error handling, --verbose, and base_url normalization against a live OpenAI-compatible endpoint using ~/.llmx/credentials.
---

# llmx real-API e2e

Runs `scripts/e2e.sh`, which validates the main user-facing paths of the llmx
CLI against a **live** OpenAI-compatible endpoint.

## When to use

- After implementing a new feature or a fix that touches request building,
  streaming, session persistence, output rendering, config resolution, or the
  CLI surface.
- Before committing or cutting a release, as the final behavioral gate beyond
  `go test`.
- When the user explicitly asks to run e2e / verify against the real API.

The free `go test ./...` suite is the first-line check and runs on every change.
This skill is the **paid** second line: it confirms the binary actually talks to
a real provider correctly. Run it deliberately, not on every tiny edit.

## Prerequisites

- `~/.llmx/credentials` with a working profile (`base_url`, `api_key`, `model`).
- A reachable, funded OpenAI-compatible endpoint for that profile.

If credentials are missing, the script exits 2 and tells the user to create them
(see README). Do not try to read or print the credentials file.

## How to run

```bash
bash scripts/e2e.sh            # uses the [default] profile
bash scripts/e2e.sh groq       # uses a named profile
E2E_SKIP_UNIT=1 bash scripts/e2e.sh   # skip the go test gate (rarely needed)
```

Before running, briefly tell the user it will make **~8 real API calls** (cost
applies). If they've already asked for e2e, just run it.

## What it checks

1. non-stream — returns output, exit 0
2. streaming (`--stream`) — returns output, exit 0
3. `--json` — single object containing `content` + `usage`
4. `-c` continuation — multi-turn history accumulates (hard) and the model
   recalls a codeword (soft/info only)
5. error handling — unknown model → exit 1 with a message
6. stdout purity — error path keeps stdout empty
7. secret non-leak — no api_key / `Authorization` in any output
8. `--verbose` — resolved endpoint printed to stderr
9. base_url normalization — a `base_url` ending in `/chat/completions` is
   stripped + warned and still works (the doubled-path 404 we hit in testing)

## Safety / isolation (already handled by the script)

- Credentials are `cp`'d into a throwaway `$HOME`, so the api_key is never read
  or printed, and the real `~/.llmx/sessions` is never polluted.
- Both temp dirs are removed on exit.

## Interpreting results

- The script prints `PASS:` / `FAIL:` per check and a final
  `summary: N passed, M failed`. Exit 0 means all passed; exit 1 means at least
  one check failed; exit 2 means a setup/gate failure (creds missing, unit tests
  or build failed) — in that case no API calls were made.
- Report the summary to the user. On any `FAIL`, quote the failing check's
  detail line and investigate the relevant package; do not just rerun.
- Check 4's "recall soft-miss" line is informational (model wording varies) and
  is **not** a failure on its own — the hard part is the session growing to ≥4
  messages.
- A check-9 or endpoint failure usually points at the profile's `base_url`, not
  the code.
