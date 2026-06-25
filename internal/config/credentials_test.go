package config

import (
	"strings"
	"testing"
)

func TestParseCredentialsNormalCommentsAndTrim(t *testing.T) {
	input := "\ufeff# comment\n; also comment\n[default]\nbase_url = https://example.test/v1\napi_key = sk-value # not comment\nmodel = test-model\n\n[local]\napi_key = \nmodel = local\n"
	creds, err := ParseCredentials(strings.NewReader(input), "credentials")
	if err != nil {
		t.Fatal(err)
	}
	def := creds.Profiles["default"]
	if def.BaseURL != "https://example.test/v1" {
		t.Fatalf("base_url = %q", def.BaseURL)
	}
	if def.APIKey != "sk-value # not comment" {
		t.Fatalf("api_key inline comment was stripped: %q", def.APIKey)
	}
	if creds.Profiles["local"].APIKey != "" {
		t.Fatalf("empty api_key = %q", creds.Profiles["local"].APIKey)
	}
}

func TestParseCredentialsDuplicateKeyErrorHasLineAndNoValue(t *testing.T) {
	input := "[default]\napi_key = secret-one\napi_key = secret-two\n"
	_, err := ParseCredentials(strings.NewReader(input), "/tmp/credentials")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "/tmp/credentials:3") {
		t.Fatalf("missing line number: %s", msg)
	}
	if strings.Contains(msg, "secret-one") || strings.Contains(msg, "secret-two") {
		t.Fatalf("error leaked key value: %s", msg)
	}
}

func TestParseCredentialsDuplicateSection(t *testing.T) {
	input := "[default]\nmodel = a\n[default]\nmodel = b\n"
	_, err := ParseCredentials(strings.NewReader(input), "credentials")
	if err == nil || !strings.Contains(err.Error(), "duplicate section") {
		t.Fatalf("expected duplicate section error, got %v", err)
	}
}

func TestExpandAPIKey(t *testing.T) {
	got, err := ExpandAPIKey("prefix-${TOKEN}", "credentials", 12, func(key string) (string, bool) {
		if key == "TOKEN" {
			return "secret", true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "prefix-secret" {
		t.Fatalf("expanded = %q", got)
	}
	_, err = ExpandAPIKey("${MISSING}", "credentials", 9, func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("expected missing env error")
	}
	if !strings.Contains(err.Error(), "credentials:9") || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("bad missing env error: %v", err)
	}
}

func TestResolvePriority(t *testing.T) {
	creds, err := ParseCredentials(strings.NewReader("[default]\nbase_url=https://default\napi_key=${KEY}\nmodel=default\n[p]\nmodel=profile\n"), "credentials")
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"KEY": "from-env-ref", "LLMX_MODEL": "from-llmx-env"}
	resolved, err := Resolve(creds, CLIValues{Profile: "p", Model: "from-cli"}, func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	}, "credentials")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Model != "from-cli" {
		t.Fatalf("model = %q", resolved.Model)
	}
	if resolved.APIKey.Reveal() != "from-env-ref" {
		t.Fatalf("api_key = %q", resolved.APIKey.Reveal())
	}
	if resolved.BaseURLFromDefault {
		t.Fatal("base_url should be explicit")
	}
}

func TestResolveEmptyLLMXAPIKeyOverridesProfile(t *testing.T) {
	creds, err := ParseCredentials(strings.NewReader("[default]\napi_key=profile-key\n"), "credentials")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := Resolve(creds, CLIValues{}, func(key string) (string, bool) {
		if key == "LLMX_API_KEY" {
			return "", true
		}
		return "", false
	}, "credentials")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.APIKey.Reveal() != "" {
		t.Fatalf("api_key = %q", resolved.APIKey.Reveal())
	}
}

func TestResolveBaseURLFromDefault(t *testing.T) {
	resolved, err := Resolve(nil, CLIValues{}, func(string) (string, bool) {
		return "", false
	}, "credentials")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.BaseURL != DefaultBaseURL {
		t.Fatalf("base_url = %q", resolved.BaseURL)
	}
	if !resolved.BaseURLFromDefault {
		t.Fatal("base_url should be marked as default")
	}
}

func TestResolveBaseURLExplicitFromEnv(t *testing.T) {
	resolved, err := Resolve(nil, CLIValues{}, func(key string) (string, bool) {
		if key == "LLMX_BASE_URL" {
			return "http://localhost:1234/v1", true
		}
		return "", false
	}, "credentials")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.BaseURL != "http://localhost:1234/v1" {
		t.Fatalf("base_url = %q", resolved.BaseURL)
	}
	if resolved.BaseURLFromDefault {
		t.Fatal("base_url should be marked as explicit")
	}
}
