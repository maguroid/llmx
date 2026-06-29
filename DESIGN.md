# llmx 設計メモ

LLM を呼び出すための軽量 CLI クライアント。主に個人利用を想定。

> 改訂履歴: 初版を codex×2 / opus×2 のレビューにかけ、SSE 実装・セッション永続化・機密の出力封じを中心に改訂（rev.2）。

## スコープ / 方針

- 言語: Go
- 対応 API: **chat completion 形式のみ**（OpenAI / Groq / OpenRouter / ローカル LM Studio・vLLM などは `base_url + api_key + model` の差し替えで共通化できるため、プロバイダ抽象化レイヤは持たない）
- 認証情報: `~/.llmx/credentials` から **プロファイル別**に読み込む
- 利用モード: **ワンショット + `--continue` による履歴継続**（フル対話 REPL は持たない）
- 出力: **ストリーミング既定**。整形は既定でプレーンテキスト、`--json` で構造化出力
- CLI: **標準ライブラリ `flag`** を使い、依存を最小化（ゼロ依存で完結させる）
- 対象 OS: **Unix 系（macOS / Linux）**。パーミッションチェック等は Unix 前提とし、Windows は当面非対応と明記

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

# セッション管理
llmx --list-sessions           # 一覧
llmx --rm-session work         # 削除
llmx --clear-sessions          # 全削除

# 出力
llmx --no-stream "..."         # 一括取得
llmx --json "..."              # 構造化出力（単一 JSON オブジェクト、--no-stream を含意）
llmx -v "..."                  # 解決済み profile/model/POST URL を stderr に表示

# その他
llmx --system "あなたは..." "..."
llmx -t 0.7 --max-tokens 1000 --stop "###" "..."
```

### フラグと位置引数の規約（重要）

標準 `flag` は **位置引数より後ろに来たフラグを解析しない**（`llmx "..." -p groq` は `-p` が無視される）。POSIX 風の結合短縮（`-abc`）も不可。よって：

- **フラグは必ずプロンプトより前**に置く規約とし、README/`--help` に明記する
- 将来 permutation（GNU getopt 風の並べ替え）が必要になったら、`flag.Parse` 前に自前で引数を並べ替える薄いラッパで対応（依存追加はしない）
- セッション管理コマンドはサブコマンドではなくフラグ（`--list-sessions` 等）として実装し、単一コマンド構成を保つ

### 短縮形 / 長形 対応表

| 短縮 | 長形 | 意味 |
|---|---|---|
| `-p` | `--profile` | プロファイル選択 |
| `-m` | `--model` | モデル上書き |
| `-c` | `--continue` | 直近セッションを継続 |
| `-t` | `--temperature` | temperature（`--top-p` とは別。`-t` は temperature 固定） |
| `-v` | `--verbose` | 解決済み profile/model/POST URL を stderr に表示 |
| — | `--session NAME` | 名前付きセッション |
| — | `--new` | 新規セッション（既定。`--session` と併用時のみ意味を持つ → 後述） |
| — | `--system` | system プロンプト |
| — | `--max-tokens` / `--stop` / `--top-p` / `--reasoning-effort` | 生成パラメータ |
| — | `--stream` / `--no-stream` / `--json` | 出力モード |

短縮形と長形の両方指定は「同値なら許容、矛盾値ならエラー」とする。

## パッケージ構成

```
llmx/
├── main.go                  # flag 解析と依存注入のみ。実処理は app.Run へ委譲
├── internal/
│   ├── app/
│   │   └── run.go           # 実行フロー（入力収集 → 解決 → 送信 → 出力 → 履歴保存）の orchestration
│   ├── config/
│   │   ├── credentials.go   # ~/.llmx/credentials の INI パース
│   │   └── resolve.go       # CLI > env > profile > 既定 の解決
│   ├── client/
│   │   ├── client.go        # POST /chat/completions（*http.Client を注入）
│   │   └── stream.go        # SSE パーサ
│   ├── chat/
│   │   └── message.go       # Message / Role / Request / Response 等のドメイン型のみ
│   ├── session/
│   │   └── store.go         # ~/.llmx/sessions/ 読み書き（root dir を注入）
│   └── output/
│       ├── text.go          # io.Writer を受ける純粋な renderer
│       └── json.go          # --json レンダラ（安定した構造体で組む）
└── go.mod
```

### 層分離と依存方向（テスタビリティ）

- **依存方向は一方向**: `app` → (`config`/`client`/`session`/`output`)、各下位 → `chat`（ドメイン型）。`chat` は他の internal を import しない（循環依存の防止）
- **orchestration は `app.Run(ctx, opts)` に閉じ込め**、`main.go` は flag 解析と配線のみ
- **注入点を明示**:
  - `client.New(httpClient *http.Client, baseURL string)` — テストは `httptest.Server` / カスタム `RoundTripper`
  - `session.NewStore(root string, now func() time.Time)` — テストは `t.TempDir()` と固定時刻
  - 環境変数は `LookupEnv func(string) (string, bool)` を注入可能に
- `output` は `os.Stdout` に直結せず `io.Writer` を受ける。TTY 判定や中断メッセージ（stderr）は `app` 側の責務

## credentials フォーマット（AWS CLI 風 INI）

`~/.llmx/credentials`

```ini
[default]
base_url = https://api.openai.com/v1
api_key  = sk-xxxx
model    = gpt-4o

[groq]
base_url = https://api.groq.com/openai/v1
api_key  = ${GROQ_API_KEY}     # 環境変数展開（秘密を平文で置かない選択肢）
model    = llama-3.3-70b-versatile

[local]
base_url = http://localhost:1234/v1
# api_key を空にすると Authorization ヘッダを送らない（ローカル互換）
model    = qwen2.5-coder
```

### INI パーサ仕様（自前・狭く固定）

鍵破損を避けるため、対応構文を意図的に狭くする：

- セクション `[name]`、`key = value`、空行、**行頭** `#` / `;` コメントのみ対応
- **インラインコメント非対応**（値中の `#`/`;` を温存。URL や鍵を壊さない）
- **クオート非対応**。値は前後空白のみ trim
- **重複セクション / 重複キーはエラー**（設定ミスの早期検出。credentials は秘密情報のため曖昧な後勝ちにしない）
- パースエラーは **ファイル名 + 行番号**を含める。ただし**値（鍵）はエラーメッセージに出さない**
- 大文字小文字、BOM、CRLF の扱いを実装時に確定（キーは小文字固定を推奨）

### `${VAR}` 環境変数展開仕様（最小固定）

- 展開対象は **`api_key` フィールドのみ**
- **`${VAR}` 形式のみ**対応。`$VAR`、`${VAR:-default}` 等の shell 互換構文は非対応
- 変数名は `[A-Za-z_][A-Za-z0-9_]*` に制限。不正な `${}` はパースエラー
- **未定義変数は設定エラー（終了コード 3）**で `${VAR}` 名を提示（空展開して 401 になる事故を防ぐ）
- リテラルで `${...}` を書きたい場合のエスケープは**非対応**と明記
- 展開後の値はログ・JSON 出力に含めない（後述の Secret 型）

### 設定解決の優先順位（項目ごと）

```
CLI フラグ > 環境変数(LLMX_*) > 選択プロファイル > [default] > 組込み既定
```

- プロファイル選択: `-p` > `LLMX_PROFILE` > `default`
- 環境変数キー一覧:

| 環境変数 | 対応設定 |
|---|---|
| `LLMX_PROFILE` | プロファイル選択 |
| `LLMX_API_KEY` | api_key（**最優先**。profile の `${VAR}` 展開より上） |
| `LLMX_BASE_URL` | base_url |
| `LLMX_MODEL` | model |

`api_key` の解決順は **`LLMX_API_KEY`（最優先）> profile 内 `${VAR}` 展開後の値**と確定する。

`api_key` が未解決かつ `base_url` が組込み既定 `https://api.openai.com/v1` に落ちた場合は、送信前に設定エラー（終了コード 3）で止める。`LLMX_BASE_URL` / profile `base_url` / `[default].base_url` のいずれかで `base_url` が明示されていれば、ローカル互換のため空 `api_key` でも Authorization ヘッダなしで送信してよい。

### credentials の権限ポリシー

- `~/.llmx/` = **0700**、`~/.llmx/credentials` = **0600** を期待
- 権限が緩い場合は **既定で拒否（終了コード 3）**。`--insecure` フラグ指定時のみ警告して続行（ssh 流の厳格運用）
- 権限警告・エラーは必ず **stderr** へ（`--json` 時の stdout 純度を守る）

## 機密情報の取り扱い（出力封じ）

後付けが困難なため設計段階で固定する：

- `api_key` は専用の `Secret` 型で保持し、`String()` / `MarshalJSON()` / `Format()` を `***` でオーバーライド。**いかなる出力経路でも生値を出さない**
- `--verbose` の診断でも `api_key` / `Authorization: Bearer ...` ヘッダは出力しない
- 401 エラーは**どのプロファイルの鍵か**だけを示し、鍵の値は出さない
- `--json` 出力・セッション JSON にリクエスト全体（鍵を含む）を埋め込まない

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
    Content string `json:"content"` // 将来の union 化（vision/tool）に備え内部表現の差し替え余地を残す
}

// client — リクエストは OpenAI chat completions 準拠
type Request struct {
    Model           string         `json:"model"`
    Messages        []Message      `json:"messages"`
    Stream          bool           `json:"stream"`
    Temperature     *float64       `json:"temperature,omitempty"`
    MaxTokens       *int           `json:"max_tokens,omitempty"`
    TopP            *float64       `json:"top_p,omitempty"`
    Stop            []string       `json:"stop,omitempty"`          // 複数 --stop を配列で送る
    ReasoningEffort *string        `json:"reasoning_effort,omitempty"`
    StreamOptions   *StreamOptions `json:"stream_options,omitempty"`
}

type StreamOptions struct {
    IncludeUsage bool `json:"include_usage,omitempty"`
}
```

- ポインタ + `omitempty` で「未指定はサーバ既定に委ねる」を表現
- `reasoning_effort` は CLI のみで指定する provider-dependent な pass-through 文字列。credentials / env では解決しない
- `n`（複数候補）は **対応しない（常に 1）**と明記。出力・履歴保存が複雑化するため
- `max_tokens` は新しめのモデルで `max_completion_tokens` を要求する場合がある。当面は `max_tokens` を採用し、必要に応じプロファイル単位で切替えられる余地を残す（実装時 TODO）
- レスポンスのデコードは **未知フィールドを落とさない**方針（将来の reasoning 系等に備える）

## ストリーミング設計（SSE）

`bufio.Scanner` の 64KB 行上限と単純な行単位デコードは互換プロバイダで破綻するため、**イベント単位**で実装する：

- `bufio.Reader.ReadString('\n')`（または `Scanner` + 十分大きな `Buffer`）で読み、**空行で 1 イベント確定**
- 1 イベント内の**複数 `data:` 行は `\n` で連結**して 1 ペイロードとする
- `:` で始まるコメント行 / keep-alive、`event:` / `id:` / `retry:` フィールド、未知フィールドは**無視**
- 終了判定は trim して `[DONE]`（`data:[DONE]` と `data: [DONE]` の両方を許容）
- `Content-Type` は `text/event-stream`（`; charset=utf-8` 等の付帯も許容）
- チャンクの `choices[0].delta.content` は **`*string`** とし、`nil`（role-only / `content:null` / 空 delta）は出力しない
- `choices` は **`index == 0` のみ対応**と明記（複数 choices は非対応）
- `finish_reason` は最後まで保持し、`--json` とメタ表示に使う。`length` のときは stderr に警告
- `stream_options.include_usage` を有効にした場合、末尾チャンクの `usage` を内部保持できる構造にする
- 読み取りエラーは「ストリーム破損 / 行長超過 / context 中断」を区別して終了コードへ反映
- `resp, err := httpClient.Do(...)` 直後に **`defer resp.Body.Close()`** を必ず置く（中断時の接続/goroutine リーク防止）

`--json` 指定時は `stream=false` で一括取得し、`{content, model, usage, finish_reason}` を**単一 JSON オブジェクト**で出力（`--json` は `--no-stream` を含意）。行単位ストリーム（JSONL）は将来 `--jsonl` として別フラグで検討（未決）。

プレーンテキスト出力はストリーム / 一括のどちらでも、空応答を除き応答末尾に改行を 1 つ保証する。応答が既に `\n` で終わる場合は追加しない。`--json` 出力は対象外。

## 履歴（セッション）設計

```
~/.llmx/sessions/        # 0700
├── last                 # 直近に触れたセッション ID（-c の参照先、0600）
├── 20260625-153012-a1b2.json   # 無名セッション（時刻 + ランダム suffix、0600）
└── work.json            # 名前付き（--session work、0600）
```

セッションファイル（JSON）:

```json
{
  "schema_version": 1,
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

- **`schema_version`** を必須とし、将来のマイグレーション（union content / tool calling 対応）に備える（後から足せないため初版から導入）
- 無名セッション ID は**高分解能時刻 + ランダム suffix**で衝突を回避

### セッション操作の意味論（状態表）

| フラグ組合せ | 動作 |
|---|---|
| （なし）/ `--new` 単独 | 新規無名セッションを作成。`--new` 単独は既定と同じ（no-op 扱い） |
| `-c` | `last` が指すセッションを継続 |
| `--session NAME` | `NAME.json` を使用 / 無ければ作成して継続 |
| `--session NAME --new` | `NAME.json` を**リセット（上書き新規）**。破壊的操作として明示フラグ必須 |
| `-c --session NAME` | `NAME.json` を継続（`-c` は「新規作成しない」意味として作用） |
| `-c`（`last` が dangling） | 参照先が消えている場合は stderr に通知し、新規として開始 |

- `last` は **`--session` 名も含め直近に触れたセッション ID** を指すと定義
- すべての保存は**正常完了時のみ** user + assistant を追記。Ctrl-C / timeout / network / API エラーのいずれでも保存しない（履歴汚染防止）。中断時に画面出力済みでも履歴に残らない旨を stderr に短く表示

### アトミック書き込みと競合

- セッション保存・`last` 更新は **同一ディレクトリに一時ファイル（0600）→ `Sync()` → `Close()` → `os.Rename()`** のアトミック差し替え
- 並行起動時、`last` は**後勝ちを許容**（仕様として明記）。名前付きセッションへの同時追記は read-modify-write 競合で更新が失われ得るため、最小対応として**保存前に読込時の `updated_at` / mtime を確認し、変化していたら競合エラー**にする。ロック（`flock` 等）は当面入れない

## エラー処理の方針

- API エラー body は **サイズ上限付き**で読み、`error.message` → `detail` → `error`(string) → 生 body preview の順で fallback（OpenAI 非互換プロバイダ・HTML・空 body・プロキシ由来エラーに対応）
- HTTP 2xx でも `choices` 欠落・JSON decode 失敗は **protocol error** として扱い、API エラーとは表示上区別する
- 認証エラー(401)は「どのプロファイルの鍵か」を明示（鍵の値は出さない）
- **base_url の結合**: `net/url` で parse し `strings.TrimRight(u.Path, "/") + "/chat/completions"` で組み立てる（`/v1chat/...` や `//` を防ぐ）。`base_url` は API root と定義するが、末尾スラッシュ除去後の path が `/chat/completions` で終わる誤設定はその部分を自動で取り除き、stderr に警告してから `/chat/completions` を付与する。送信前に resolved endpoint を `--verbose` で確認可能に
- **Authorization**: `api_key` が空なら Authorization ヘッダを送らない（ローカル互換）。非空なら `Bearer <key>` を送る
- タイムアウト既定（実装時に確定）: 全体 ~2 分 / 接続 ~10 秒 / ストリーム無通信 ~60 秒
- リトライ: `408` / `429` / `500` / `502`-`504` は `Retry-After`（上限あり）を尊重し小さな指数バックオフ。`501` / `505` 等は retryable にしない。ただし**ストリーム開始後は重複出力を避けるためリトライしない**

### 終了コード

| コード | 意味 |
|---|---|
| 0 | 成功 |
| 1 | API エラー（4xx/5xx）/ プロトコル不整合 |
| 2 | usage エラー（フラグ誤用） |
| 3 | 設定エラー（credentials 権限 / 未定義 `${VAR}` / プロファイル不在 等） |
| 4 | ネットワーク / タイムアウト（API 到達前） |
| 130 | 中断（Ctrl-C） |

usage エラー(2)・設定エラー(3)・ネットワーク(4)を分離し、スクリプトでのリトライ判定を容易にする。

## 入出力の細目

- 最終 user メッセージ = **`引数 + "\n\n" + stdin`**（両方ある場合）。片方のみならその内容
- 引数なし・stdin も TTY（パイプなし）の場合は **usage を出して終了コード 2 で即終了**（REPL は持たないためハングしない）
- `--system` を `-c` 継続時に渡した場合は **既存セッション先頭の system を上書き（置換）**と定義
- ストリーム/一括の既定は **stdout が TTY ならストリーム、非 TTY（パイプ）なら一括**（UNIX 慣習）。`--stream` でストリームを強制し、`--no-stream` で一括を強制する。優先順位は `--json` / `--no-stream` > `--stream` > TTY 自動判定で、`--stream` と `--no-stream` の同時指定は usage エラー（終了コード 2）
- 警告・診断は必ず **stderr**。`--json` 時の stdout は単一 JSON のみを保証
- `--verbose` は送信前に解決済み profile 名・model・POST endpoint URL だけを stderr に出す。`api_key` や Authorization ヘッダ値は出さない

## 未決事項 / 今後の検討

1. **`--json` のストリーミング（JSONL）** — 現状は単一 JSON 一括。スクリプト用途が増えたら `--jsonl`（content delta + final usage）を別フラグで追加
2. **INI vs TOML** — ゼロ依存 INI で開始。ただし profile に headers / timeout / proxy / response_format 等のネスト設定が必要になった時点を移行の分岐点とし、その際は `schema_version` 的なフォーマット判定で後方互換を取る（自前 INI の鍵破損リスクが顕在化するなら `BurntSushi/toml` 採用も再検討）
3. **履歴の retention** — 「サイズ」ではなく**機密の保持期間管理**として扱う。送信時の窓制御（`--max-turns`）と、保存物の削除/期限は別問題として分離。`--list/--rm/--clear-sessions` は MVP に含める
4. **マルチモーダル / tool calling / reasoning 系** — chat completion「のみ」の前提が崩れる最有力ポイント。`Message.Content` の union 化と `schema_version` マイグレーションで対応する余地を初版から確保
