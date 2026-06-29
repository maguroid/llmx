package chat

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestSecretMasksOutput(t *testing.T) {
	secret := NewSecret("sk-real")
	if secret.String() != "***" {
		t.Fatalf("String() = %q", secret.String())
	}
	if fmt.Sprintf("%s", secret) != "***" {
		t.Fatalf("Format = %q", fmt.Sprintf("%s", secret))
	}
	got, err := json.Marshal(secret)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `"***"` {
		t.Fatalf("MarshalJSON = %s", got)
	}
	if secret.Reveal() != "sk-real" {
		t.Fatalf("Reveal() = %q", secret.Reveal())
	}
}

func TestRequestReasoningEffortJSON(t *testing.T) {
	got, err := json.Marshal(Request{})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == "" {
		t.Fatal("empty JSON")
	}
	var without map[string]json.RawMessage
	if err := json.Unmarshal(got, &without); err != nil {
		t.Fatal(err)
	}
	if _, ok := without["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort present in %s", got)
	}

	effort := "high"
	got, err = json.Marshal(Request{ReasoningEffort: &effort})
	if err != nil {
		t.Fatal(err)
	}
	var with struct {
		ReasoningEffort string `json:"reasoning_effort"`
	}
	if err := json.Unmarshal(got, &with); err != nil {
		t.Fatal(err)
	}
	if with.ReasoningEffort != effort {
		t.Fatalf("reasoning_effort = %q", with.ReasoningEffort)
	}
}
