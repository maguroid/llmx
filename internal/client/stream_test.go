package client

import (
	"io"
	"strings"
	"testing"
)

func TestSSEParserMultipleDataAndComments(t *testing.T) {
	parser := NewSSEParser(strings.NewReader(": keepalive\nid: 1\ndata: first\ndata: second\n\n"))
	payload, done, err := parser.Next()
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatal("unexpected done")
	}
	if payload != "first\nsecond" {
		t.Fatalf("payload = %q", payload)
	}
}

func TestSSEParserDoneForms(t *testing.T) {
	for _, input := range []string{"data:[DONE]\n\n", "data: [DONE]\n\n"} {
		parser := NewSSEParser(strings.NewReader(input))
		_, done, err := parser.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !done {
			t.Fatalf("done false for %q", input)
		}
	}
}

func TestSSEParserLongLine(t *testing.T) {
	long := strings.Repeat("x", 80*1024)
	parser := NewSSEParser(strings.NewReader("data: " + long + "\n\n"))
	payload, done, err := parser.Next()
	if err != nil {
		t.Fatal(err)
	}
	if done || payload != long {
		t.Fatalf("done=%v len=%d", done, len(payload))
	}
}

func TestDecodeStreamPayloadRoleOnlyAndNullContent(t *testing.T) {
	chunk, err := decodeStreamPayload(`{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if chunk.Choices[0].Delta.Content != nil {
		t.Fatal("role-only content should be nil")
	}
	chunk, err = decodeStreamPayload(`{"choices":[{"index":0,"delta":{"content":null}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if chunk.Choices[0].Delta.Content != nil {
		t.Fatal("null content should be nil")
	}
	parser := NewSSEParser(strings.NewReader(""))
	if _, _, err := parser.Next(); err != io.EOF {
		t.Fatalf("empty stream err = %v", err)
	}
}
