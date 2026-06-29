package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maguroid/llmx/internal/chat"
	"github.com/maguroid/llmx/internal/client"
	"github.com/maguroid/llmx/internal/session"
)

func TestRunCompleteSavesSession(t *testing.T) {
	home := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Fatalf("authorization = %q", got)
		}
		var req chat.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Stream {
			t.Fatalf("stream = true")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"server-model","choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"total_tokens":7}}`))
	}))
	defer server.Close()
	writeCredentials(t, home, "[default]\nbase_url="+server.URL+"/v1\napi_key=sk-test\nmodel=test-model\n")

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Args:        []string{"hello"},
		Stdin:       strings.NewReader(""),
		Stdout:      &stdout,
		Stderr:      &stderr,
		StdinIsTTY:  true,
		StdoutIsTTY: false,
		HomeDir:     home,
		HTTPClient:  server.Client(),
		SessionName: "it",
		LookupEnv:   emptyEnv,
		Usage:       func() {},
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != "answer\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	sess := readSession(t, home, "it")
	if sess.Profile != "default" || sess.Model != "test-model" {
		t.Fatalf("session profile/model = %q/%q", sess.Profile, sess.Model)
	}
	if got := len(sess.Messages); got != 2 {
		t.Fatalf("session messages = %d", got)
	}
	if sess.Messages[0].Role != chat.RoleUser || sess.Messages[0].Content != "hello" {
		t.Fatalf("bad user message: %+v", sess.Messages[0])
	}
	if sess.Messages[1].Role != chat.RoleAssistant || sess.Messages[1].Content != "answer" {
		t.Fatalf("bad assistant message: %+v", sess.Messages[1])
	}
}

func TestRunNoCredentialsDefaultBaseURLFailsBeforeNetwork(t *testing.T) {
	home := t.TempDir()
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"unexpected"}}`)),
			Request:    r,
		}, nil
	})}

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Args:        []string{"hello"},
		Stdin:       strings.NewReader(""),
		Stdout:      &stdout,
		Stderr:      &stderr,
		StdinIsTTY:  true,
		StdoutIsTTY: false,
		HomeDir:     home,
		HTTPClient:  httpClient,
		LookupEnv:   emptyEnv,
		Usage:       func() {},
	})
	if code != ExitConfig {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "no API key configured") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if requests != 0 {
		t.Fatalf("requests = %d", requests)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunExplicitBaseURLOmitsAuthorizationWhenAPIKeyEmpty(t *testing.T) {
	home := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("authorization = %q", got)
		}
		var req chat.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Stream {
			t.Fatalf("stream = true")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"local-model","choices":[{"index":0,"message":{"role":"assistant","content":"local ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	writeCredentials(t, home, "[default]\nbase_url="+server.URL+"/v1\nmodel=local-model\n")

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Args:        []string{"hello"},
		Stdin:       strings.NewReader(""),
		Stdout:      &stdout,
		Stderr:      &stderr,
		StdinIsTTY:  true,
		StdoutIsTTY: false,
		HomeDir:     home,
		HTTPClient:  server.Client(),
		LookupEnv:   emptyEnv,
		Usage:       func() {},
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if requests != 1 {
		t.Fatalf("requests = %d", requests)
	}
	if stdout.String() != "local ok\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunSendsReasoningEffort(t *testing.T) {
	home := t.TempDir()
	effort := "high"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chat.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.ReasoningEffort == nil || *req.ReasoningEffort != effort {
			t.Fatalf("reasoning_effort = %v", req.ReasoningEffort)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	writeCredentials(t, home, "[default]\nbase_url="+server.URL+"/v1\napi_key=sk-test\nmodel=m\n")

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Args:            []string{"hello"},
		Stdin:           strings.NewReader(""),
		Stdout:          &stdout,
		Stderr:          &stderr,
		StdinIsTTY:      true,
		StdoutIsTTY:     false,
		HomeDir:         home,
		HTTPClient:      server.Client(),
		LookupEnv:       emptyEnv,
		Usage:           func() {},
		ReasoningEffort: &effort,
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != "ok\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunStripsTrailingChatCompletionsFromBaseURL(t *testing.T) {
	tests := []struct {
		name        string
		baseSuffix  string
		wantWarning bool
	}{
		{name: "endpoint included", baseSuffix: "/v1/chat/completions", wantWarning: true},
		{name: "endpoint included with slash", baseSuffix: "/v1/chat/completions/", wantWarning: true},
		{name: "api root", baseSuffix: "/v1", wantWarning: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/v1/chat/completions" {
					t.Fatalf("path = %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
			}))
			defer server.Close()
			writeCredentials(t, home, "[default]\nbase_url="+server.URL+tc.baseSuffix+"\napi_key=sk-test\nmodel=m\n")

			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), Options{
				Args:        []string{"hello"},
				Stdin:       strings.NewReader(""),
				Stdout:      &stdout,
				Stderr:      &stderr,
				StdinIsTTY:  true,
				StdoutIsTTY: false,
				HomeDir:     home,
				HTTPClient:  server.Client(),
				LookupEnv:   emptyEnv,
				Usage:       func() {},
			})
			if code != ExitOK {
				t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
			}
			if requests != 1 {
				t.Fatalf("requests = %d", requests)
			}
			hasWarning := strings.Contains(stderr.String(), "warning: "+client.StrippedChatCompletionsWarning)
			if hasWarning != tc.wantWarning {
				t.Fatalf("warning = %v, want %v; stderr = %q", hasWarning, tc.wantWarning, stderr.String())
			}
			if stdout.String() != "ok\n" {
				t.Fatalf("stdout = %q", stdout.String())
			}
		})
	}
}

func TestRunVerboseWritesDiagnosticsToStderrOnly(t *testing.T) {
	home := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"server-model","choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	writeCredentials(t, home, "[default]\nbase_url="+server.URL+"/v1\napi_key=sk-secret\nmodel=m\n")

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Args:        []string{"hello"},
		Stdin:       strings.NewReader(""),
		Stdout:      &stdout,
		Stderr:      &stderr,
		StdinIsTTY:  true,
		StdoutIsTTY: false,
		HomeDir:     home,
		HTTPClient:  server.Client(),
		LookupEnv:   emptyEnv,
		Usage:       func() {},
		JSON:        true,
		Verbose:     true,
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not JSON response: %q", stdout.String())
	}
	if out["content"] != "answer" {
		t.Fatalf("stdout content = %v", out["content"])
	}
	if strings.Contains(stdout.String(), "endpoint:") || strings.Contains(stdout.String(), "sk-secret") {
		t.Fatalf("stdout polluted or leaked secret: %q", stdout.String())
	}
	stderrText := stderr.String()
	for _, want := range []string{
		"profile: default",
		"model: m",
		"endpoint: " + server.URL + "/v1/chat/completions",
	} {
		if !strings.Contains(stderrText, want) {
			t.Fatalf("stderr missing %q: %q", want, stderrText)
		}
	}
	if strings.Contains(stderrText, "sk-secret") {
		t.Fatalf("secret leaked in stderr: %q", stderrText)
	}
}

func TestRunStreamAndNoStreamModes(t *testing.T) {
	tests := []struct {
		name       string
		opts       Options
		wantStream bool
		wantCode   int
	}{
		{
			name:       "stream forces streaming when stdout is not tty",
			opts:       Options{Stream: true, StdoutIsTTY: false},
			wantStream: true,
			wantCode:   ExitOK,
		},
		{
			name:       "no stream wins over tty",
			opts:       Options{NoStream: true, StdoutIsTTY: true},
			wantStream: false,
			wantCode:   ExitOK,
		},
		{
			name:       "json wins over stream",
			opts:       Options{JSON: true, Stream: true, StdoutIsTTY: true},
			wantStream: false,
			wantCode:   ExitOK,
		},
		{
			name:     "stream conflicts with no stream",
			opts:     Options{Stream: true, NoStream: true, StdoutIsTTY: true},
			wantCode: ExitUsage,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			var gotStream bool
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var req chat.Request
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatal(err)
				}
				gotStream = req.Stream
				if req.Stream {
					w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
					_, _ = w.Write([]byte("data: {\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"s\"},\"finish_reason\":\"stop\"}]}\n\n"))
					_, _ = w.Write([]byte("data: [DONE]\n\n"))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"c"},"finish_reason":"stop"}]}`))
			}))
			defer server.Close()
			writeCredentials(t, home, "[default]\nbase_url="+server.URL+"/v1\napi_key=sk-test\nmodel=m\n")

			var stdout, stderr bytes.Buffer
			opts := tc.opts
			opts.Args = []string{"hello"}
			opts.Stdin = strings.NewReader("")
			opts.Stdout = &stdout
			opts.Stderr = &stderr
			opts.StdinIsTTY = true
			opts.HomeDir = home
			opts.HTTPClient = server.Client()
			opts.LookupEnv = emptyEnv
			opts.Usage = func() {}
			code := Run(context.Background(), opts)
			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d, stderr = %s", code, tc.wantCode, stderr.String())
			}
			if tc.wantCode != ExitOK {
				if requests != 0 {
					t.Fatalf("requests = %d", requests)
				}
				return
			}
			if requests != 1 {
				t.Fatalf("requests = %d", requests)
			}
			if gotStream != tc.wantStream {
				t.Fatalf("stream = %v, want %v", gotStream, tc.wantStream)
			}
		})
	}
}

func TestRunStreamAddsTrailingNewline(t *testing.T) {
	home := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = w.Write([]byte("data: {\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	writeCredentials(t, home, "[default]\nbase_url="+server.URL+"/v1\napi_key=sk-test\nmodel=m\n")

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Args:        []string{"hello"},
		Stdin:       strings.NewReader(""),
		Stdout:      &stdout,
		Stderr:      &stderr,
		StdinIsTTY:  true,
		StdoutIsTTY: false,
		HomeDir:     home,
		HTTPClient:  server.Client(),
		SessionName: "stream-newline",
		Stream:      true,
		LookupEnv:   emptyEnv,
		Usage:       func() {},
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != "answer\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunStreamOutputErrorDoesNotSaveSession(t *testing.T) {
	home := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	writeCredentials(t, home, "[default]\nbase_url="+server.URL+"/v1\napi_key=sk-test\nmodel=m\n")

	var stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Args:        []string{"hello"},
		Stdin:       strings.NewReader(""),
		Stdout:      failingWriter{err: errors.New("broken pipe")},
		Stderr:      &stderr,
		StdinIsTTY:  true,
		StdoutIsTTY: false,
		HomeDir:     home,
		HTTPClient:  server.Client(),
		SessionName: "pipe",
		Stream:      true,
		LookupEnv:   emptyEnv,
		Usage:       func() {},
	})
	if code != ExitAPI {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "output error: broken pipe") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".llmx", "sessions", "pipe.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session file err = %v, want not exist", err)
	}
}

func TestRunRedactsAPIKeyFromAPIError(t *testing.T) {
	home := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key sk-secret"}}`))
	}))
	defer server.Close()
	writeCredentials(t, home, "[default]\nbase_url="+server.URL+"/v1\napi_key=sk-secret\nmodel=m\n")

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Args:        []string{"hello"},
		Stdin:       strings.NewReader(""),
		Stdout:      &stdout,
		Stderr:      &stderr,
		StdinIsTTY:  true,
		StdoutIsTTY: false,
		HomeDir:     home,
		HTTPClient:  server.Client(),
		LookupEnv:   emptyEnv,
		Usage:       func() {},
	})
	if code != ExitAPI {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "sk-secret") || strings.Contains(stdout.String(), "sk-secret") {
		t.Fatalf("secret leaked; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunRedactsAPIKeyFromRequestFailure(t *testing.T) {
	home := t.TempDir()
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("request failed with sk-secret")
	})}
	writeCredentials(t, home, "[default]\nbase_url=https://example.test/v1\napi_key=sk-secret\nmodel=m\n")

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Args:        []string{"hello"},
		Stdin:       strings.NewReader(""),
		Stdout:      &stdout,
		Stderr:      &stderr,
		StdinIsTTY:  true,
		StdoutIsTTY: false,
		HomeDir:     home,
		HTTPClient:  httpClient,
		LookupEnv:   emptyEnv,
		Usage:       func() {},
	})
	if code != ExitNetwork {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "sk-secret") || strings.Contains(stdout.String(), "sk-secret") {
		t.Fatalf("secret leaked; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func writeCredentials(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".llmx")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readSession(t *testing.T, home, name string) session.Session {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".llmx", "sessions", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var sess session.Session
	if err := json.Unmarshal(data, &sess); err != nil {
		t.Fatal(err)
	}
	return sess
}

func emptyEnv(string) (string, bool) {
	return "", false
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}
