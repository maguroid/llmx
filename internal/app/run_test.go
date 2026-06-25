package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maguroid/llmx/internal/chat"
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
	if stdout.String() != "answer" {
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
	if stdout.String() != "local ok" {
		t.Fatalf("stdout = %q", stdout.String())
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
