# llmx

A lightweight, zero-dependency Go CLI for OpenAI-compatible chat completion APIs.

`llmx` talks to any endpoint that speaks the OpenAI `chat/completions` format —
OpenAI, Groq, OpenRouter, or a local server such as LM Studio or vLLM — selected
by profile from `~/.llmx/credentials`.

- **Zero dependencies** — pure Go standard library, single static binary.
- **Profile-based** — switch providers/models with `-p`.
- **Streaming** by default on a TTY, batch when piped.
- **Scriptable** — `--json` for a single structured object, clean exit codes.
- **Conversations** — continue the last chat with `-c`, or use named sessions.
- **Secret-safe** — your API key is never printed, logged, or saved.

## Installation

With a Go toolchain (Go 1.26+):

```sh
go install github.com/maguroid/llmx@latest
```

This installs the `llmx` binary into `$(go env GOPATH)/bin` (make sure it is on
your `PATH`).

Or build from source:

```sh
git clone https://github.com/maguroid/llmx
cd llmx
go build -o llmx .
# then move ./llmx somewhere on your PATH
```

## Configuration

Credentials live in `~/.llmx/credentials`, an INI file with one section per
profile. The directory must be `0700` and the file `0600` (override with
`--insecure`).

```sh
mkdir -p -m 700 ~/.llmx
$EDITOR ~/.llmx/credentials
chmod 600 ~/.llmx/credentials
```

```ini
[default]
base_url = https://api.openai.com/v1
api_key  = sk-xxxxxxxx
model    = gpt-4o-mini

[groq]
base_url = https://api.groq.com/openai/v1
api_key  = ${GROQ_API_KEY}     ; api_key may reference an environment variable
model    = llama-3.3-70b-versatile

[local]
base_url = http://localhost:1234/v1
; api_key omitted: no Authorization header is sent (local-compatible servers)
model    = qwen2.5-coder
```

Notes:

- `base_url` is the **API root** (e.g. `.../v1`), not the full endpoint. A
  trailing `/chat/completions` is stripped automatically (with a warning).
- Only `api_key` supports `${VAR}` expansion. An undefined variable is a
  configuration error.
- Comments must be on their own line (`#` or `;`); inline comments are not
  supported, so a `#`/`;` inside a value (URL, key) is preserved.

### Environment variables

These override the selected profile per field:

| Variable        | Overrides             |
| --------------- | --------------------- |
| `LLMX_PROFILE`  | which profile is used |
| `LLMX_API_KEY`  | `api_key`             |
| `LLMX_BASE_URL` | `base_url`            |
| `LLMX_MODEL`    | `model`               |

Resolution order per field: CLI flag > `LLMX_*` > selected profile > `[default]`
> built-in default.

## Usage

```
llmx [flags] [prompt]
```

> Flags must appear **before** the prompt. Go's standard `flag` parser stops at
> the first positional argument, so `llmx "hello" -p groq` would treat `-p groq`
> as part of the prompt.

```sh
# Basic
llmx "Explain goroutines in one sentence."

# From stdin (no prompt argument)
echo "Summarize this:" | llmx
git diff | llmx "Write a commit message for this diff:"

# Pick a provider/model
llmx -p groq "Translate to French: good morning"
llmx -m gpt-4o "..."

# System prompt and generation parameters
llmx --system "You are a terse assistant." -t 0.2 --max-tokens 200 "..."
llmx --stop "###" "..."

# Force streaming even when piping, or disable it
llmx --stream "Tell me a short story." | tee story.txt
llmx --no-stream "..."

# Structured output for scripts (single JSON object with usage, etc.)
llmx --json "Reply with: ok"

# See the resolved profile, model, and endpoint (on stderr; key is never shown)
llmx --verbose "..."
```

When both a prompt argument and stdin are given, they are joined (`argument` +
blank line + `stdin`).

### Conversations

By default each run is a fresh, anonymous session. Continue or name them:

```sh
llmx "Remember the number 42."
llmx -c "What number did I mention?"     # continue the last session

llmx --session work "Let's plan the sprint."
llmx --session work -c "Add a testing task."   # continue a named session
llmx --session work --new "Start over."        # reset a named session

llmx --list-sessions
llmx --rm-session work
llmx --clear-sessions
```

Sessions are stored as JSON under `~/.llmx/sessions/` (`0600`).

## Flags

| Flag                        | Description                                          |
| --------------------------- | ---------------------------------------------------- |
| `-p`, `--profile <name>`    | Profile to use (default `default`)                   |
| `-m`, `--model <name>`      | Override the model                                   |
| `--system <text>`           | System prompt                                        |
| `-t`, `--temperature <n>`   | Sampling temperature                                 |
| `--top-p <n>`               | Nucleus sampling (`top_p`)                           |
| `--max-tokens <n>`          | Maximum tokens to generate                           |
| `--stop <seq>`              | Stop sequence (repeatable)                           |
| `--stream`                  | Force streaming output                               |
| `--no-stream`               | Disable streaming                                    |
| `--json`                    | Emit a single JSON object (implies `--no-stream`)    |
| `-c`, `--continue`          | Continue the last session                            |
| `--session <name>`          | Use a named session                                  |
| `--new`                     | New session; with `--session`, reset it              |
| `--list-sessions`           | List sessions                                        |
| `--rm-session <name>`       | Remove a named session                               |
| `--clear-sessions`          | Remove all sessions                                  |
| `-v`, `--verbose`           | Print resolved request diagnostics to stderr         |
| `--insecure`                | Allow loose permissions on `~/.llmx`                 |

Streaming defaults to on when stdout is a TTY and off when piped; `--stream` /
`--no-stream` override this. Diagnostics and warnings go to stderr, so stdout
(the response, or the `--json` object) stays clean for piping.

## Exit codes

| Code | Meaning                                   |
| ---- | ----------------------------------------- |
| 0    | Success                                   |
| 1    | API or protocol error                     |
| 2    | Usage error (bad flags)                   |
| 3    | Configuration error (credentials, etc.)   |
| 4    | Network / timeout (before reaching the API) |
| 130  | Interrupted (Ctrl-C)                       |

## License

[MIT](LICENSE) © maguroid
