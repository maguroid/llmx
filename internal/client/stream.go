package client

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/maguroid/llmx/internal/chat"
)

type SSEParser struct {
	reader *bufio.Reader
	data   []string
}

func NewSSEParser(r io.Reader) *SSEParser {
	return &SSEParser{reader: bufio.NewReader(r)}
}

func (p *SSEParser) Next() (payload string, done bool, err error) {
	for {
		line, readErr := p.reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return "", false, readErr
		}
		if line != "" {
			p.consumeLine(strings.TrimRight(line, "\r\n"))
		}
		if line == "\n" || line == "\r\n" || (errors.Is(readErr, io.EOF) && len(p.data) > 0) {
			joined := strings.Join(p.data, "\n")
			p.data = nil
			if strings.TrimSpace(joined) == "[DONE]" {
				return "", true, nil
			}
			if joined == "" {
				if errors.Is(readErr, io.EOF) {
					return "", false, io.EOF
				}
				continue
			}
			return joined, false, nil
		}
		if errors.Is(readErr, io.EOF) {
			return "", false, io.EOF
		}
	}
}

func (p *SSEParser) consumeLine(line string) {
	if line == "" || strings.HasPrefix(line, ":") {
		return
	}
	field, value, ok := strings.Cut(line, ":")
	if !ok {
		return
	}
	if strings.HasPrefix(value, " ") {
		value = value[1:]
	}
	if field == "data" {
		p.data = append(p.data, value)
	}
}

type StreamResult struct {
	Content      string
	Model        string
	Usage        *chat.Usage
	FinishReason string
}

type streamChunk struct {
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
	Usage   *chat.Usage   `json:"usage"`
}

type chunkChoice struct {
	Index        int        `json:"index"`
	Delta        chunkDelta `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

type chunkDelta struct {
	Content *string `json:"content"`
}

func decodeStreamPayload(payload string) (streamChunk, error) {
	var chunk streamChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return streamChunk{}, fmt.Errorf("decode stream chunk: %w", err)
	}
	return chunk, nil
}
