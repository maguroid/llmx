package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maguroid/llmx/internal/chat"
)

func TestEndpointJoin(t *testing.T) {
	tests := map[string]string{
		"https://api.test":     "https://api.test/chat/completions",
		"https://api.test/":    "https://api.test/chat/completions",
		"https://api.test/v1":  "https://api.test/v1/chat/completions",
		"https://api.test/v1/": "https://api.test/v1/chat/completions",
	}
	for input, want := range tests {
		got, err := Endpoint(input)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("Endpoint(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCompleteWithHTTPTestServer(t *testing.T) {
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
			t.Fatal("stream should be false")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"total_tokens":3}}`))
	}))
	defer server.Close()
	c, err := New(server.Client(), server.URL+"/v1/")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Complete(context.Background(), chat.NewSecret("sk-test"), chat.Request{Model: "m", Messages: []chat.Message{{Role: chat.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Choices[0].Message.Content != "ok" || resp.Usage.TotalTokens != 3 {
		t.Fatalf("bad response: %+v", resp)
	}
}

func TestStreamWithHTTPTestServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("authorization should be omitted, got %q", got)
		}
		var req chat.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if !req.Stream || req.StreamOptions == nil || !req.StreamOptions.IncludeUsage {
			t.Fatalf("bad stream request: %+v", req)
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = w.Write([]byte("data: {\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"he\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":1,\"delta\":{\"content\":\"skip\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"llo\"},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":5}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	c, err := New(server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	result, err := c.Stream(context.Background(), chat.NewSecret(""), chat.Request{
		Model:         "m",
		Messages:      []chat.Message{{Role: chat.RoleUser, Content: "hi"}},
		Stream:        true,
		StreamOptions: &chat.StreamOptions{IncludeUsage: true},
	}, func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "hello" || result.FinishReason != "stop" || result.Usage.TotalTokens != 5 {
		t.Fatalf("bad result: %+v", result)
	}
	if strings.Join(deltas, "") != "hello" {
		t.Fatalf("deltas = %q", deltas)
	}
}

func TestAPIErrorMessageFallbacks(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "error message",
			body: `{"error":{"message":"nested"}}`,
			want: "nested",
		},
		{
			name: "detail",
			body: `{"detail":"detail text"}`,
			want: "detail text",
		},
		{
			name: "error string",
			body: `{"error":"top error"}`,
			want: "top error",
		},
		{
			name: "raw body",
			body: `<html>bad gateway</html>`,
			want: "<html>bad gateway</html>",
		},
		{
			name: "empty body",
			body: `   `,
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := apiErrorMessage([]byte(tc.body)); got != tc.want {
				t.Fatalf("apiErrorMessage() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadAPIErrorEmptyBodyUsesStatusText(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusBadGateway,
		Body:       io.NopCloser(strings.NewReader("")),
	}
	err := readAPIError(resp)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("err type = %T", err)
	}
	if apiErr.Message != http.StatusText(http.StatusBadGateway) {
		t.Fatalf("message = %q", apiErr.Message)
	}
}

func TestRetryableStatus(t *testing.T) {
	for _, code := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, 500, 502, 503, 504} {
		if !retryableStatus(code) {
			t.Fatalf("%d should be retryable", code)
		}
	}
	for _, code := range []int{501, 505} {
		if retryableStatus(code) {
			t.Fatalf("%d should not be retryable", code)
		}
	}
}

func TestRetryAfterIsCapped(t *testing.T) {
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("Retry-After", "120")
	if got := retryDelay(resp, 0); got != 30*time.Second {
		t.Fatalf("retryDelay = %v", got)
	}
}
