# llmx

Lightweight Go CLI for chat-completion compatible APIs.

```
llmx [flags] [prompt]
```

Flags must appear before the prompt. Go's standard `flag` parser stops at the first positional argument, so `llmx "hello" -p groq` treats `-p groq` as prompt text.

Configuration is read from `~/.llmx/credentials`. The file must be `0600` and `~/.llmx` must be `0700` unless `--insecure` is passed.
