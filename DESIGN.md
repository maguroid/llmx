# llmx 設計メモ

LLM を呼び出すための軽量 CLI クライアント。主に個人利用を想定。

## スコープ / 方針

- 言語: Go
- 対応 API: **chat completion 形式のみ**（OpenAI / Groq / OpenRouter / ローカル LM Studio・vLLM などは `base_url + api_key + model` の差し替えで共通化できるため、プロバイダ抽象化レイヤは持たない）
- 認証情報: `~/.llmx/credentials` から **プロファイル別**に読み込む
- 利用モード: **ワンショット + `--continue` による履歴継続**（フル対話 REPL は持たない）
- 出力: **ストリーミング既定**。整形は既定でプレーンテキスト、`--json` で構造化出力
- CLI: **標準ライブラリ `flag`** を使い、依存を最小化（ゼロ依存で完結させる）

## コマンド体系

```
llmx [flags] [プロンプト]

# 入力ソース
llmx "質問"                    # 引数
echo "..." | llmx              # stdin（引数なし時）
llmx "要約して:" < file.txt     # 引数 + stdin を結合

# プロファイル / モデル
llmx -p groq "..."             # プロファイル切替
llmx -m gpt-4o-mini "..."      # モデル上書き

# 履歴
llmx -c "続けて"               # 直近セッションを継続
llmx --session work "..."      # 名前付きセッション
llmx --new "..."               # 明示的に新規（既定挙動）

# 出力
llmx --no-stream "..."         # 一括取得
llmx --json "..."              # 構造化出力（usage 等メタ込み、--no-stream を含意）

# その他
llmx --system "あなたは..." "..."
llmx -t 0.7 --max-tokens 1000 "..."
```

> `flag` は `-profile` と `--profile` を等価に扱うが、短縮形 `-p` と `-profile` は別フラグとして登録し同じ変数へ束ねる（短縮形が欲しいフラグのみ）。

## パッケージ構成

```
llmx/
├── main.go                  # フラグ解析・配線のみ
├── internal/
│   ├── config/
│   │   ├── credentials.go   # ~/.llmx/credentials の INI パース
│   │   └── resolve.go       # CLI > env > profile > 既定 の解決
│   ├── client/
│   │   ├── client.go        # POST /chat/completions
│   │   └── stream.go        # SSE パーサ
│   ├── chat/
│   │   ├── message.go       # Message / Role 型
│   │   └── run.go           # 入力収集 → 送信 → 出力 → 履歴保存
│   ├── session/
│   │   └── store.go         # ~/.llmx/sessions/ 読み書き
│   └── output/
│       ├── text.go          # ストリーム / 一括のプレーン出力
│       └── json.go          # --json レンダラ
└── go.mod
```

依存は標準ライブラリのみで完結（INI パーサは自前で約 40 行、SSE は `bufio.Scanner` で実装）。

## credentials フォーマット（AWS CLI 風 INI）

`~/.llmx/credentials`（パーミッション `0600` 推奨。緩い場合は ssh 同様に警告を出す）

```ini
[default]
base_url = https://api.openai.com/v1
api_key  = sk-xxxx
model    = gpt-4o

[groq]
base_url = https://api.groq.com/openai/v1
api_key  = ${GROQ_API_KEY}     # 環境変数展開を許可（秘密を平文で置かない選択肢）
model    = llama-3.3-70b-versatile

[local]
base_url = http://localhost:1234/v1
api_key  = dummy               # ローカルは不要だが API 互換のため
model    = qwen2.5-coder
```

### 設定解決の優先順位（項目ごと）

```
CLI フラグ > 環境変数(LLMX_*) > 選択プロファイル > [default] > 組込み既定
```

- プロファイル選択: `-p` > `LLMX_PROFILE` > `default`
- `api_key` は `${VAR}` 展開で環境変数から注入可能にし、dotfiles 管理と両立させる

## 主要な型

```go
// chat/message.go
type Role string
const (
    RoleSystem    Role = "system"
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
)

type Message struct {
    Role    Role   `json:"role"`
    Content string `json:"content"`
}

// client — リクエスト/レスポンスは OpenAI chat completions 準拠
type Request struct {
    Model       string    `json:"model"`
    Messages    []Message `json:"messages"`
    Stream      bool      `json:"stream"`
    Temperature *float64  `json:"temperature,omitempty"`
    MaxTokens   *int      `json:"max_tokens,omitempty"`
}
```

ポインタ + `omitempty` で「未指定はサーバ既定に委ねる」を表現し、プロバイダ間の既定値差異を尊重する。

## ストリーミング設計

- `text/event-stream` を `bufio.Scanner` で行読みし、`data: ` プレフィックスを剥がして各チャンクを JSON デコード
- `data: [DONE]` で終了、`choices[].delta.content` を逐次 `os.Stdout` へ
- `context.Context` で Ctrl-C / タイムアウトをハンドル（`signal.NotifyContext`）
- `--json` 指定時は `stream=false` で一括取得し、`{content, model, usage, finish_reason}` を整形出力（`--json` は `--no-stream` を含意）

## 履歴（セッション）設計

```
~/.llmx/sessions/
├── last                     # 直近セッション ID を指す（-c の参照先）
├── 20260625-153012.json     # 無名セッション（新規ごとに作成）
└── work.json                # 名前付き（--session work）
```

セッションファイル（JSON）:

```json
{
  "profile": "groq",
  "model": "llama-3.3-70b-versatile",
  "created_at": "...",
  "updated_at": "...",
  "messages": [
    {"role": "system", "content": "..."},
    {"role": "user", "content": "..."},
    {"role": "assistant", "content": "..."}
  ]
}
```

- 既定（`--new` 含む）: 新規ファイル作成 → 完了後に user/assistant を追記 → `last` 更新
- `-c`: `last` のセッションを読み、messages に追記して送信
- `--session NAME`: `NAME.json` を使用 / 作成
- assistant メッセージはストリーム完了後の全文を保存。途中失敗時は user 追記もロールバックして履歴汚染を防ぐ

## エラー処理の方針

- HTTP 4xx/5xx は OpenAI のエラー JSON（`{"error":{"message":...}}`）をパースして人間可読に整形
- 終了コード: 成功 = 0 / API エラー = 1 / 設定エラー = 2 / 中断 = 130
- 認証エラー (401) は「どのプロファイルの鍵か」を明示してデバッグしやすくする

## 未決事項 / 今後の検討

1. `--json` 時のストリーミング — 現状は一括取得。スクリプト用途次第で JSONL チャンク出力も検討
2. INI 自前実装 vs TOML — ゼロ依存優先で自前 INI。ネスト設定が増えるなら TOML(`BurntSushi/toml`) に倒す
3. セッションの肥大化 — `--continue` 多用で履歴が伸びるため、最大ターン数 / トークン上限での切り詰めを検討
