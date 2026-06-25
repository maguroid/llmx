package config

import (
	"os"
	"path/filepath"

	"github.com/maguroid/llmx/internal/chat"
)

const (
	DefaultBaseURL = "https://api.openai.com/v1"
	DefaultModel   = "gpt-4o-mini"
)

type CLIValues struct {
	Profile string
	Model   string
}

type Resolved struct {
	Profile            string
	BaseURL            string
	BaseURLFromDefault bool
	APIKey             chat.Secret
	Model              string
}

func CredentialsPath(home string) string {
	return filepath.Join(home, ".llmx", "credentials")
}

func Resolve(creds *Credentials, cli CLIValues, lookup func(string) (string, bool), filename string) (Resolved, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	profileName := cli.Profile
	if profileName == "" {
		if value, ok := lookup("LLMX_PROFILE"); ok && value != "" {
			profileName = value
		}
	}
	if profileName == "" {
		profileName = "default"
	}
	var selected Profile
	if creds != nil {
		var ok bool
		selected, ok = creds.Profiles[profileName]
		if !ok && profileName != "default" {
			return Resolved{}, configErrorf("profile %q not found", profileName)
		}
	}
	var def Profile
	if creds != nil {
		def = creds.Profiles["default"]
	}
	baseURL, baseURLFromDefault := resolveBaseURL(lookup, selected, def)
	model := firstNonEmpty(cli.Model, envValue(lookup, "LLMX_MODEL"), selected.Model, def.Model, DefaultModel)
	apiKey, apiKeySet := lookup("LLMX_API_KEY")
	if !apiKeySet {
		raw := firstNonEmpty(selected.APIKey, def.APIKey)
		if raw != "" {
			line := selected.lines["api_key"]
			if selected.APIKey == "" {
				line = def.lines["api_key"]
			}
			var err error
			apiKey, err = ExpandAPIKey(raw, filename, line, lookup)
			if err != nil {
				return Resolved{}, err
			}
		}
	}
	return Resolved{
		Profile:            profileName,
		BaseURL:            baseURL,
		BaseURLFromDefault: baseURLFromDefault,
		APIKey:             chat.NewSecret(apiKey),
		Model:              model,
	}, nil
}

func resolveBaseURL(lookup func(string) (string, bool), selected, def Profile) (string, bool) {
	baseURL := firstNonEmpty(envValue(lookup, "LLMX_BASE_URL"), selected.BaseURL, def.BaseURL)
	if baseURL != "" {
		return baseURL, false
	}
	return DefaultBaseURL, true
}

func envValue(lookup func(string) (string, bool), key string) string {
	if value, ok := lookup(key); ok {
		return value
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
