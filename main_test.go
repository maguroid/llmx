package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunVerboseShortAndLongFlagsAreAliases(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	writeMainTestCredentials(t, home, "[default]\nbase_url="+server.URL+"/v1\napi_key=sk-secret\nmodel=m\n")
	stdinPath := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(stdinPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, err := os.Open(stdinPath)
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"-v", "--verbose", "--json", "hello"}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "endpoint: "+server.URL+"/v1/chat/completions") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "sk-secret") || strings.Contains(stdout.String(), "sk-secret") {
		t.Fatalf("secret leaked; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "endpoint:") {
		t.Fatalf("stdout polluted: %q", stdout.String())
	}
}

func writeMainTestCredentials(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".llmx")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
