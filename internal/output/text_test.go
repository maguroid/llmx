package output

import (
	"bytes"
	"testing"
)

func TestTextEnsuresSingleTrailingNewline(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "empty", content: "", want: ""},
		{name: "missing newline", content: "answer", want: "answer\n"},
		{name: "already newline", content: "answer\n", want: "answer\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := Text(&out, tc.content); err != nil {
				t.Fatal(err)
			}
			if out.String() != tc.want {
				t.Fatalf("output = %q, want %q", out.String(), tc.want)
			}
		})
	}
}
