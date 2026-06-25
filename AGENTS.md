# AGENTS.md

`llmx` — OpenAI 互換の chat completions を呼ぶ、ゼロ依存の軽量 CLI（Go・個人利用）。

**設計の正典は [DESIGN.md](DESIGN.md)（rev.2）。** 非自明な変更の前に該当節を読むこと。この
ファイルは「破ってはいけない不変条件」と「開発フロー」に絞る（設計詳細は重複させない）。

## 不変条件（レビューで必ず指摘される / 壊しやすい）

- **ゼロ依存**: 標準ライブラリのみ。`go.mod` の `require` は空のまま保つ。外部モジュール追加禁止
  （INI パーサも SSE パーサも自前実装）。
- **機密**: `api_key` は `chat.Secret` 型で保持する。生鍵・`Authorization` 値を stdout / stderr /
  エラーメッセージ / `--json` 出力 / セッション JSON のいずれにも出さない。
- **権限と保存**: `credentials` とセッションファイルは `0600`、`~/.llmx` 等のディレクトリは `0700`。
  セッション保存は temp → `Sync` → `Rename` のアトミック書き込み。正常完了時のみ user+assistant を保存。
- **未設定ガード**: `api_key` 未解決かつ `base_url` が組込み既定のときは、ネットワークに出る前に
  設定エラー（exit 3）。明示 `base_url` + 空 `api_key`（ローカル互換）は従来どおり許可。
- **出力規約**: stdout は応答本文のみ。警告・診断・`--verbose` は必ず stderr。`--json` は単一 JSON
  オブジェクトで stdout を汚さない。
- **終了コード**: 0 成功 / 1 API・プロトコル / 2 usage / 3 設定 / 4 ネットワーク・タイムアウト / 130 中断。

## レイアウトと依存方向

`main.go` + `internal/{app,config,client,chat,session,output}`。依存は `app` → 下位パッケージ →
`chat`（ドメイン型）の一方向。**`chat` は他の `internal` を import しない**。注入点:
`client.New(httpClient, …)` / `session.NewStore(root, now)` / config の `LookupEnv`。詳細は DESIGN.md。

## 開発フロー

- 変更後は必ず通す: `gofmt -l .`（空）/ `go vet ./...` / `go build ./...` / `go test ./...`。
- テストは標準ライブラリで書く（`httptest`、`t.TempDir()`、注入した時刻）。新しい分岐・安全性パスには
  テストを追加する。
- 実 API 動作確認は **e2e スキル**（`scripts/e2e.sh`）で行う。`go test` は無料ゲート、e2e は課金
  （～8 リクエスト）。新機能・修正のあとに回す。
- コミットメッセージ・コード・コメントは英語。会話は日本語。

## 設定の要点（詳細は DESIGN.md）

`~/.llmx/credentials` の INI（行頭 `#`/`;` コメントのみ、インラインコメント非対応、重複キーはエラー）。
`${VAR}` 展開は `api_key` のみ・未定義は exit 3。`base_url` は API ルート（末尾 `/chat/completions` は
自動で剥がして警告）。解決順は CLI > env(`LLMX_*`) > profile > `[default]` > 組込み既定。
