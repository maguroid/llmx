package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maguroid/llmx/internal/chat"
	"github.com/maguroid/llmx/internal/client"
	"github.com/maguroid/llmx/internal/config"
	"github.com/maguroid/llmx/internal/output"
	"github.com/maguroid/llmx/internal/session"
)

const (
	ExitOK        = 0
	ExitAPI       = 1
	ExitUsage     = 2
	ExitConfig    = 3
	ExitNetwork   = 4
	ExitInterrupt = 130
)

type Options struct {
	Args        []string
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	StdinIsTTY  bool
	StdoutIsTTY bool
	HomeDir     string
	LookupEnv   func(string) (string, bool)
	HTTPClient  *http.Client
	Usage       func()

	Profile     string
	Model       string
	Insecure    bool
	Continue    bool
	SessionName string
	New         bool
	System      string
	SystemSet   bool

	Temperature *float64
	MaxTokens   *int
	TopP        *float64
	Stops       []string

	NoStream      bool
	Stream        bool
	JSON          bool
	Verbose       bool
	ListSessions  bool
	RemoveSession string
	ClearSessions bool
}

func Run(ctx context.Context, opts Options) int {
	fillDefaults(&opts)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	root := filepath.Join(opts.HomeDir, ".llmx")
	store := session.NewStore(root, time.Now)
	if code := handleSessionManagement(store, opts); code >= 0 {
		return code
	}
	if code := validateOutputMode(opts); code != ExitOK {
		return code
	}
	prompt, code := collectPrompt(opts)
	if code != ExitOK {
		return code
	}
	creds, warnings, err := config.LoadCredentials(config.CredentialsPath(opts.HomeDir), config.LoadOptions{
		Insecure:  opts.Insecure,
		LookupEnv: opts.LookupEnv,
	})
	for _, warning := range warnings {
		fmt.Fprintf(opts.Stderr, "warning: %s\n", warning)
	}
	if err != nil {
		fmt.Fprintf(opts.Stderr, "configuration error: %v\n", err)
		return ExitConfig
	}
	resolved, err := config.Resolve(creds, config.CLIValues{Profile: opts.Profile, Model: opts.Model}, opts.LookupEnv, config.CredentialsPath(opts.HomeDir))
	if err != nil {
		fmt.Fprintf(opts.Stderr, "configuration error: %v\n", err)
		return ExitConfig
	}
	if resolved.APIKey.Reveal() == "" && resolved.BaseURLFromDefault {
		fmt.Fprintln(opts.Stderr, "configuration error: no API key configured; create ~/.llmx/credentials or set LLMX_API_KEY")
		return ExitConfig
	}
	loaded, dangling, err := openSession(store, opts, resolved)
	if dangling {
		fmt.Fprintln(opts.Stderr, "warning: last session was missing; starting a new session")
	}
	if err != nil {
		fmt.Fprintf(opts.Stderr, "session error: %v\n", err)
		return ExitAPI
	}
	messages := applySystem(append([]chat.Message(nil), loaded.Session.Messages...), opts)
	messages = append(messages, chat.Message{Role: chat.RoleUser, Content: prompt})
	req := chat.Request{
		Model:       resolved.Model,
		Messages:    messages,
		Stream:      shouldStream(opts),
		Temperature: opts.Temperature,
		MaxTokens:   opts.MaxTokens,
		TopP:        opts.TopP,
		Stop:        opts.Stops,
	}
	if req.Stream {
		req.StreamOptions = &chat.StreamOptions{IncludeUsage: true}
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = defaultHTTPClient()
	}
	endpoint, err := client.ResolveEndpoint(resolved.BaseURL)
	if err != nil {
		fmt.Fprintf(opts.Stderr, "configuration error: %v\n", err)
		return ExitConfig
	}
	if endpoint.StrippedChatCompletions {
		fmt.Fprintf(opts.Stderr, "warning: %s\n", client.StrippedChatCompletionsWarning)
	}
	if opts.Verbose {
		fmt.Fprintf(opts.Stderr, "profile: %s\n", resolved.Profile)
		fmt.Fprintf(opts.Stderr, "model: %s\n", resolved.Model)
		fmt.Fprintf(opts.Stderr, "endpoint: %s\n", endpoint.URL)
	}
	apiClient := client.NewWithEndpoint(httpClient, endpoint.URL)
	var assistant string
	var model string
	var usage *chat.Usage
	var finishReason string
	if req.Stream {
		result, err := apiClient.Stream(ctx, resolved.APIKey, req, func(delta string) error {
			_, err := io.WriteString(opts.Stdout, delta)
			if err != nil {
				return outputWriteError{err: err}
			}
			return nil
		})
		if err != nil {
			return handleRunError(ctx, opts, err)
		}
		assistant = result.Content
		model = first(result.Model, resolved.Model)
		usage = result.Usage
		finishReason = result.FinishReason
		if err := writeStreamTrailingNewline(opts.Stdout, assistant); err != nil {
			fmt.Fprintf(opts.Stderr, "output error: %v\n", err)
			return ExitAPI
		}
	} else {
		resp, err := apiClient.Complete(ctx, resolved.APIKey, req)
		if err != nil {
			return handleRunError(ctx, opts, err)
		}
		choice := firstChoice(resp.Choices)
		assistant = choice.Message.Content
		model = first(resp.Model, resolved.Model)
		usage = resp.Usage
		if choice.FinishReason != nil {
			finishReason = *choice.FinishReason
		}
		if opts.JSON {
			if err := output.JSON(opts.Stdout, output.JSONResponse{Content: assistant, Model: model, Usage: usage, FinishReason: finishReason}); err != nil {
				fmt.Fprintf(opts.Stderr, "output error: %v\n", err)
				return ExitAPI
			}
		} else if err := output.Text(opts.Stdout, assistant); err != nil {
			fmt.Fprintf(opts.Stderr, "output error: %v\n", err)
			return ExitAPI
		}
	}
	if finishReason == "length" {
		fmt.Fprintln(opts.Stderr, "warning: response stopped because max token length was reached")
	}
	saved := append(messages, chat.Message{Role: chat.RoleAssistant, Content: assistant})
	loaded.Session.Profile = resolved.Profile
	loaded.Session.Model = resolved.Model
	saveResult, err := store.SaveDetailed(loaded, saved)
	if err != nil {
		fmt.Fprintf(opts.Stderr, "session error: %v\n", err)
		return ExitAPI
	}
	if saveResult.LastErr != nil {
		fmt.Fprintf(opts.Stderr, "warning: saved session but failed to update last: %v\n", saveResult.LastErr)
	}
	return ExitOK
}

func fillDefaults(opts *Options) {
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.LookupEnv == nil {
		opts.LookupEnv = os.LookupEnv
	}
	if opts.HomeDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			opts.HomeDir = home
		}
	}
}

func defaultHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 2 * time.Minute,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ResponseHeaderTimeout: 60 * time.Second,
		},
	}
}

func handleSessionManagement(store *session.Store, opts Options) int {
	count := 0
	if opts.ListSessions {
		count++
	}
	if opts.RemoveSession != "" {
		count++
	}
	if opts.ClearSessions {
		count++
	}
	if count == 0 {
		return -1
	}
	if count > 1 {
		fmt.Fprintln(opts.Stderr, "usage error: session management flags are mutually exclusive")
		if opts.Usage != nil {
			opts.Usage()
		}
		return ExitUsage
	}
	if opts.ListSessions {
		infos, err := store.List()
		if err != nil {
			fmt.Fprintf(opts.Stderr, "session error: %v\n", err)
			return ExitAPI
		}
		for _, info := range infos {
			fmt.Fprintf(opts.Stdout, "%s\t%s\t%s\t%d\n", info.ID, info.UpdatedAt.Format(time.RFC3339), info.Model, info.Messages)
		}
		return ExitOK
	}
	if opts.RemoveSession != "" {
		if err := store.Remove(opts.RemoveSession); err != nil {
			fmt.Fprintf(opts.Stderr, "session error: %v\n", err)
			return ExitAPI
		}
		return ExitOK
	}
	if err := store.Clear(); err != nil {
		fmt.Fprintf(opts.Stderr, "session error: %v\n", err)
		return ExitAPI
	}
	return ExitOK
}

func collectPrompt(opts Options) (string, int) {
	argText := stringsJoin(opts.Args, " ")
	stdinText := ""
	if !opts.StdinIsTTY {
		data, err := io.ReadAll(opts.Stdin)
		if err != nil {
			fmt.Fprintf(opts.Stderr, "input error: %v\n", err)
			return "", ExitUsage
		}
		stdinText = string(data)
	}
	if argText == "" && stdinText == "" && opts.StdinIsTTY {
		if opts.Usage != nil {
			opts.Usage()
		}
		return "", ExitUsage
	}
	if argText != "" && stdinText != "" {
		return argText + "\n\n" + stdinText, ExitOK
	}
	return argText + stdinText, ExitOK
}

func openSession(store *session.Store, opts Options, resolved config.Resolved) (session.Loaded, bool, error) {
	var systemPtr *string
	if opts.SystemSet {
		systemPtr = &opts.System
	}
	if opts.SessionName != "" {
		if opts.Continue {
			loaded, err := store.ContinueNamed(opts.SessionName)
			return loaded, false, err
		}
		loaded, err := store.OpenNamed(opts.SessionName, resolved.Profile, resolved.Model, opts.New, systemPtr)
		return loaded, false, err
	}
	if opts.Continue {
		return store.ContinueLast(resolved.Profile, resolved.Model, systemPtr)
	}
	loaded, err := store.Start(resolved.Profile, resolved.Model, systemPtr)
	return loaded, false, err
}

func applySystem(messages []chat.Message, opts Options) []chat.Message {
	if !opts.SystemSet {
		return messages
	}
	for i := range messages {
		if messages[i].Role == chat.RoleSystem {
			messages[i].Content = opts.System
			return messages
		}
	}
	return append([]chat.Message{{Role: chat.RoleSystem, Content: opts.System}}, messages...)
}

func shouldStream(opts Options) bool {
	if opts.JSON || opts.NoStream {
		return false
	}
	if opts.Stream {
		return true
	}
	return opts.StdoutIsTTY
}

func validateOutputMode(opts Options) int {
	if opts.Stream && opts.NoStream {
		fmt.Fprintln(opts.Stderr, "usage error: --stream and --no-stream are mutually exclusive")
		if opts.Usage != nil {
			opts.Usage()
		}
		return ExitUsage
	}
	return ExitOK
}

func firstChoice(choices []chat.Choice) chat.Choice {
	for _, choice := range choices {
		if choice.Index == 0 {
			return choice
		}
	}
	return choices[0]
}

func handleRunError(ctx context.Context, opts Options, err error) int {
	var outErr outputWriteError
	if errors.As(err, &outErr) {
		fmt.Fprintf(opts.Stderr, "output error: %v\n", outErr.err)
		return ExitAPI
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		fmt.Fprintln(opts.Stderr, "interrupted; session was not saved")
		return ExitInterrupt
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		fmt.Fprintf(opts.Stderr, "network error: %v\n", err)
		return ExitNetwork
	}
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		fmt.Fprintf(opts.Stderr, "%v\n", apiErr)
		return ExitAPI
	}
	var protoErr *client.ProtocolError
	if errors.As(err, &protoErr) {
		fmt.Fprintf(opts.Stderr, "protocol error: %v\n", protoErr)
		return ExitAPI
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		fmt.Fprintf(opts.Stderr, "network error: %v\n", err)
		return ExitNetwork
	}
	fmt.Fprintf(opts.Stderr, "network error: %v\n", err)
	return ExitNetwork
}

type outputWriteError struct {
	err error
}

func (e outputWriteError) Error() string {
	return e.err.Error()
}

func (e outputWriteError) Unwrap() error {
	return e.err
}

func writeStreamTrailingNewline(w io.Writer, content string) error {
	if content == "" || strings.HasSuffix(content, "\n") {
		return nil
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func stringsJoin(values []string, sep string) string {
	if len(values) == 0 {
		return ""
	}
	out := values[0]
	for _, value := range values[1:] {
		out += sep + value
	}
	return out
}
