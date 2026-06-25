package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/maguroid/llmx/internal/chat"
)

const maxErrorBody = 64 * 1024

type Client struct {
	httpClient *http.Client
	endpoint   string
}

type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api error %d: %s", e.StatusCode, e.Message)
}

type ProtocolError struct {
	Message string
}

func (e *ProtocolError) Error() string {
	return e.Message
}

func New(httpClient *http.Client, baseURL string) (*Client, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	endpoint, err := Endpoint(baseURL)
	if err != nil {
		return nil, err
	}
	return &Client{httpClient: httpClient, endpoint: endpoint}, nil
}

func Endpoint(baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid base_url: missing scheme or host")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/chat/completions"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func (c *Client) Complete(ctx context.Context, apiKey chat.Secret, req chat.Request) (chat.Response, error) {
	body, err := c.do(ctx, apiKey, req)
	if err != nil {
		return chat.Response{}, err
	}
	defer body.Close()
	var response chat.Response
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		return chat.Response{}, &ProtocolError{Message: fmt.Sprintf("decode response: %v", err)}
	}
	if len(response.Choices) == 0 {
		return chat.Response{}, &ProtocolError{Message: "response missing choices"}
	}
	return response, nil
}

func (c *Client) Stream(ctx context.Context, apiKey chat.Secret, req chat.Request, onDelta func(string)) (StreamResult, error) {
	body, err := c.doStream(ctx, apiKey, req)
	if err != nil {
		return StreamResult{}, err
	}
	defer body.Close()
	parser := NewSSEParser(body)
	var result StreamResult
	var builder strings.Builder
	for {
		payload, done, err := parser.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return StreamResult{}, &ProtocolError{Message: "stream ended before [DONE]"}
			}
			return StreamResult{}, err
		}
		if done {
			result.Content = builder.String()
			return result, nil
		}
		chunk, err := decodeStreamPayload(payload)
		if err != nil {
			return StreamResult{}, &ProtocolError{Message: err.Error()}
		}
		if chunk.Model != "" {
			result.Model = chunk.Model
		}
		if chunk.Usage != nil {
			result.Usage = chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			if choice.FinishReason != nil {
				result.FinishReason = *choice.FinishReason
			}
			if choice.Delta.Content != nil {
				builder.WriteString(*choice.Delta.Content)
				if onDelta != nil {
					onDelta(*choice.Delta.Content)
				}
			}
		}
	}
}

func (c *Client) do(ctx context.Context, apiKey chat.Secret, req chat.Request) (io.ReadCloser, error) {
	resp, err := c.sendWithRetry(ctx, apiKey, req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, readAPIError(resp)
	}
	return resp.Body, nil
}

func (c *Client) doStream(ctx context.Context, apiKey chat.Secret, req chat.Request) (io.ReadCloser, error) {
	resp, err := c.sendWithRetry(ctx, apiKey, req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, readAPIError(resp)
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		defer resp.Body.Close()
		return nil, &ProtocolError{Message: "stream response content-type is not text/event-stream"}
	}
	return newIdleTimeoutBody(resp.Body, 60*time.Second), nil
}

func (c *Client) send(ctx context.Context, apiKey chat.Secret, req chat.Request) (*http.Response, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(req); err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, &buf)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey.Reveal() != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey.Reveal())
	}
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) sendWithRetry(ctx context.Context, apiKey chat.Secret, req chat.Request) (*http.Response, error) {
	const attempts = 3
	for attempt := 0; attempt < attempts; attempt++ {
		resp, err := c.send(ctx, apiKey, req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if attempt < attempts-1 && retryableStatus(resp.StatusCode) {
				delay := retryDelay(resp, attempt)
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
				_ = resp.Body.Close()
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				case <-timer.C:
					continue
				}
			}
		}
		return resp, nil
	}
	return nil, &ProtocolError{Message: "retry attempts exhausted"}
}

func retryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusRequestTimeout, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func retryDelay(resp *http.Response, attempt int) time.Duration {
	if value := resp.Header.Get("Retry-After"); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second
		}
		if when, err := http.ParseTime(value); err == nil {
			if delay := time.Until(when); delay > 0 {
				return delay
			}
		}
	}
	return time.Duration(100*(1<<attempt)) * time.Millisecond
}

func readAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	message := apiErrorMessage(body)
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return &APIError{StatusCode: resp.StatusCode, Message: message}
}

func apiErrorMessage(body []byte) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err == nil {
		if message := nestedString(obj, "error", "message"); message != "" {
			return message
		}
		if message := topString(obj, "detail"); message != "" {
			return message
		}
		if message := topString(obj, "error"); message != "" {
			return message
		}
	}
	return strings.TrimSpace(string(body))
}

func nestedString(obj map[string]any, outer, inner string) string {
	nested, ok := obj[outer].(map[string]any)
	if !ok {
		return ""
	}
	return topString(nested, inner)
}

func topString(obj map[string]any, key string) string {
	value, ok := obj[key].(string)
	if !ok {
		return ""
	}
	return value
}
