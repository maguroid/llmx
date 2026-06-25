package chat

import (
	"encoding/json"
	"fmt"
)

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

type Secret string

func NewSecret(value string) Secret {
	return Secret(value)
}

func (s Secret) Reveal() string {
	return string(s)
}

func (s Secret) String() string {
	return "***"
}

func (s Secret) Format(state fmt.State, verb rune) {
	_, _ = state.Write([]byte("***"))
}

func (s Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal("***")
}

type Request struct {
	Model         string         `json:"model"`
	Messages      []Message      `json:"messages"`
	Stream        bool           `json:"stream"`
	Temperature   *float64       `json:"temperature,omitempty"`
	MaxTokens     *int           `json:"max_tokens,omitempty"`
	TopP          *float64       `json:"top_p,omitempty"`
	Stop          []string       `json:"stop,omitempty"`
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

type Response struct {
	ID      string   `json:"id,omitempty"`
	Object  string   `json:"object,omitempty"`
	Created int64    `json:"created,omitempty"`
	Model   string   `json:"model,omitempty"`
	Choices []Choice `json:"choices,omitempty"`
	Usage   *Usage   `json:"usage,omitempty"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason *string `json:"finish_reason"`
}
