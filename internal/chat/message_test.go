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
