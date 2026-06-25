---
name: llmx
description: Use when calling an LLM from the shell through the `llmx` CLI — one-shot prompts, scripted/non-interactive model calls, piping text into a model, structured `--json` output, or multi-turn sessions against any OpenAI-compatible chat-completions endpoint (OpenAI, Groq, OpenRouter, local LM Studio/vLLM). Covers first-time setup (install + `~/.llmx/credentials`) and the non-obvious invocation rules that make `llmx` work correctly from an agent.
---

# Using the llmx CLI

`llmx` is a zero-dependency CLI that sends a prompt to an OpenAI-compatible
`chat/completions` endpoint and prints the reply. Use it to call a model from a
shell command or script. This skill covers setup and the rules that make
non-interactive (agent/script) invocation reliable.

## Invocation rules (read first — these prevent the common failures)

1. **Always redirect stdin from `/dev/null` for non-interactive calls:**
   `llmx "prompt" < /dev/null`. With a prompt argument, `llmx` still reads stdin
   when stdin is a non-TTY (which it is under most agents/CI), and an open,
   data-less pipe makes it **hang forever**. Redirecting `< /dev/null` gives an
   immediate EOF. Only omit it when you are *deliberately* piping input
   (`some-cmd | llmx "instruction:"`).
2. **Flags go before the prompt:** `llmx -p groq --json "hello" < /dev/null`.
   The standard `flag` parser stops at the first positional argument, so flags
   placed after the prompt are silently treated as prompt text.
3. **For machine-readable output use `--json`:** it prints a single object
   `{"content": "...", "model": "...", "usage": {...}, "finish_reason": "..."}`
   on stdout. Extract the reply from `.content`. Diagnostics never touch stdout.
4. **Branch on the exit code**, not on output text:
   `0` ok · `1` API/protocol error · `2` usage error · `3` config error ·
   `4` network/timeout · `130` interrupted.

## Setup (first run)

Skip if `~/.llmx/credentials` already exists.

Install (needs Go 1.26+):

```sh
go install github.com/maguroid/llmx@latest   # installs `llmx` into $(go env GOPATH)/bin
```

Create credentials (one INI section per profile; dir must be `0700`, file `0600`):

```sh
mkdir -p -m 700 ~/.llmx
cat > ~/.llmx/credentials <<'EOF'
[default]
base_url = https://api.openai.com/v1
api_key  = sk-xxxxxxxx
model    = gpt-4o-mini
EOF
chmod 600 ~/.llmx/credentials
```

- `base_url` is the API **root** (e.g. `.../v1`), not the full endpoint.
- Add more sections (`[groq]`, `[openrouter]`, `[local]`) and select with `-p`.
- `api_key` may be `${ENV_VAR}` to avoid storing the secret in the file.
- Never read, print, or echo the credentials file or the API key. Use
  `--verbose` (writes profile/model/endpoint to stderr, key masked) to debug
  configuration.

## Recipes

Capture a reply as plain text (piped output is non-streaming, so this is safe):

```sh
reply="$(llmx "Summarize in one sentence: $text" < /dev/null)" || { echo "llmx failed ($?)"; exit 1; }
```

Robust capture with metadata (requires `jq`):

```sh
out="$(llmx --json "Classify sentiment as positive/negative: $text" < /dev/null)"
content="$(printf '%s' "$out" | jq -r '.content')"
```

Pipe a file or command output as the input:

```sh
git diff | llmx "Write a conventional-commit message for this diff:"
```

Pick a provider/model, set parameters, or add a system prompt:

```sh
llmx -p groq -m llama-3.3-70b-versatile --system "You are terse." -t 0.2 "..." < /dev/null
```

Multi-turn / stateful work (sessions persist under `~/.llmx/sessions/`):

```sh
llmx --session task "Step 1: outline the plan." < /dev/null
llmx --session task "Step 2: expand step one." < /dev/null   # same name continues it
llmx --session task --new "Start over." < /dev/null          # --new resets the named session
# manage: --list-sessions | --rm-session <name> | --clear-sessions
```

`--session <name>` continues that session by default; `-c` (without
`--session`) continues the most recent anonymous session instead.

## Reference

Common flags: `-p/--profile`, `-m/--model`, `--system`, `-t/--temperature`,
`--top-p`, `--max-tokens`, `--stop` (repeatable), `--stream`/`--no-stream`,
`--json`, `-c/--continue`, `--session`, `--new`, `-v/--verbose`. Run
`llmx -h` or see the project README for the full list and details.
