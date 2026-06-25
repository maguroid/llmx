package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Credentials struct {
	Profiles map[string]Profile
}

type Profile struct {
	BaseURL string
	APIKey  string
	Model   string
	lines   map[string]int
}

type LoadOptions struct {
	Insecure  bool
	LookupEnv func(string) (string, bool)
}

type ConfigError struct {
	Message string
}

func (e *ConfigError) Error() string {
	return e.Message
}

func configErrorf(format string, args ...any) error {
	return &ConfigError{Message: fmt.Sprintf(format, args...)}
}

func LoadCredentials(path string, opts LoadOptions) (*Credentials, []string, error) {
	if opts.LookupEnv == nil {
		opts.LookupEnv = os.LookupEnv
	}
	warnings := make([]string, 0)
	if err := checkCredentialsPermissions(path, opts.Insecure, &warnings); err != nil {
		return nil, warnings, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Credentials{Profiles: map[string]Profile{}}, warnings, nil
	}
	if err != nil {
		return nil, warnings, configErrorf("read credentials: %v", err)
	}
	defer f.Close()
	creds, err := ParseCredentials(f, path)
	if err != nil {
		return nil, warnings, err
	}
	return creds, warnings, nil
}

func ParseCredentials(r io.Reader, filename string) (*Credentials, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	creds := &Credentials{Profiles: map[string]Profile{}}
	current := ""
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if lineNo == 1 {
			line = strings.TrimPrefix(line, "\ufeff")
		}
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			if !strings.HasSuffix(trimmed, "]") {
				return nil, parseError(filename, lineNo, "invalid section header")
			}
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]"))
			if name == "" {
				return nil, parseError(filename, lineNo, "empty section name")
			}
			if _, exists := creds.Profiles[name]; exists {
				return nil, parseError(filename, lineNo, "duplicate section %q", name)
			}
			creds.Profiles[name] = Profile{lines: map[string]int{}}
			current = name
			continue
		}
		if current == "" {
			return nil, parseError(filename, lineNo, "key outside section")
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, parseError(filename, lineNo, "invalid key/value syntax")
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key == "" {
			return nil, parseError(filename, lineNo, "empty key")
		}
		profile := creds.Profiles[current]
		if profile.lines == nil {
			profile.lines = map[string]int{}
		}
		if _, exists := profile.lines[key]; exists {
			return nil, parseError(filename, lineNo, "duplicate key %q", key)
		}
		profile.lines[key] = lineNo
		switch key {
		case "base_url":
			profile.BaseURL = value
		case "api_key":
			profile.APIKey = value
		case "model":
			profile.Model = value
		default:
			return nil, parseError(filename, lineNo, "unknown key %q", key)
		}
		creds.Profiles[current] = profile
	}
	if err := scanner.Err(); err != nil {
		return nil, configErrorf("%s: read error: %v", filename, err)
	}
	return creds, nil
}

func parseError(filename string, line int, format string, args ...any) error {
	return configErrorf("%s:%d: %s", filename, line, fmt.Sprintf(format, args...))
}

func checkCredentialsPermissions(path string, insecure bool, warnings *[]string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return configErrorf("stat credentials: %v", err)
	}
	parent := filepath.Dir(path)
	if dirInfo, err := os.Stat(parent); err == nil {
		if dirInfo.Mode().Perm() != 0o700 {
			msg := fmt.Sprintf("%s permissions must be 0700", parent)
			if !insecure {
				return configErrorf("%s (use --insecure to continue)", msg)
			}
			*warnings = append(*warnings, msg+"; continuing because --insecure was set")
		}
	}
	if info.Mode().Perm() != 0o600 {
		msg := fmt.Sprintf("%s permissions must be 0600", path)
		if !insecure {
			return configErrorf("%s (use --insecure to continue)", msg)
		}
		*warnings = append(*warnings, msg+"; continuing because --insecure was set")
	}
	return nil
}

var envRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func ExpandAPIKey(value, filename string, line int, lookup func(string) (string, bool)) (string, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	var invalidAt int
	for i := 0; i < len(value); i++ {
		if value[i] == '$' && i+1 < len(value) && value[i+1] == '{' {
			end := strings.IndexByte(value[i+2:], '}')
			if end < 0 {
				invalidAt = i + 1
				break
			}
			name := value[i+2 : i+2+end]
			if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(name) {
				invalidAt = i + 1
				break
			}
			i = i + 2 + end
		}
	}
	if invalidAt != 0 {
		return "", parseError(filename, line, "invalid api_key environment reference")
	}
	var missing string
	expanded := envRefPattern.ReplaceAllStringFunc(value, func(match string) string {
		if missing != "" {
			return ""
		}
		name := match[2 : len(match)-1]
		envValue, ok := lookup(name)
		if !ok {
			missing = name
			return ""
		}
		return envValue
	})
	if missing != "" {
		return "", parseError(filename, line, "undefined environment variable %s", missing)
	}
	return expanded, nil
}
