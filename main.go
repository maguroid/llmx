package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"

	"github.com/maguroid/llmx/internal/app"
)

type stringValue struct {
	value string
	set   bool
}

func (v *stringValue) Set(s string) error {
	v.value = s
	v.set = true
	return nil
}

func (v *stringValue) String() string { return v.value }

type floatValue struct {
	value float64
	set   bool
}

func (v *floatValue) Set(s string) error {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	v.value = f
	v.set = true
	return nil
}

func (v *floatValue) String() string {
	if !v.set {
		return ""
	}
	return strconv.FormatFloat(v.value, 'g', -1, 64)
}

type intValue struct {
	value int
	set   bool
}

func (v *intValue) Set(s string) error {
	i, err := strconv.Atoi(s)
	if err != nil {
		return err
	}
	v.value = i
	v.set = true
	return nil
}

func (v *intValue) String() string {
	if !v.set {
		return ""
	}
	return strconv.Itoa(v.value)
}

type stopValues []string

func (v *stopValues) Set(s string) error {
	*v = append(*v, s)
	return nil
}

func (v *stopValues) String() string {
	return fmt.Sprint([]string(*v))
}

func main() {
	code := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	os.Exit(code)
}

func run(args []string, stdin *os.File, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("llmx", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var pShort, pLong, mShort, mLong stringValue
	var tShort, tLong, topP floatValue
	var maxTokens intValue
	var stops stopValues
	var cShort, cLong bool
	var sessionName, system stringValue
	var newSession, insecure, noStream, jsonOut, listSessions, clearSessions bool
	var removeSession stringValue
	fs.Var(&pShort, "p", "profile name (flags must appear before the prompt)")
	fs.Var(&pLong, "profile", "profile name (flags must appear before the prompt)")
	fs.Var(&mShort, "m", "model override")
	fs.Var(&mLong, "model", "model override")
	fs.BoolVar(&cShort, "c", false, "continue the last session")
	fs.BoolVar(&cLong, "continue", false, "continue the last session")
	fs.Var(&tShort, "t", "temperature")
	fs.Var(&tLong, "temperature", "temperature")
	fs.Var(&sessionName, "session", "named session")
	fs.BoolVar(&newSession, "new", false, "start a new session; with --session, reset it")
	fs.Var(&system, "system", "system prompt")
	fs.Var(&maxTokens, "max-tokens", "maximum tokens")
	fs.Var(&stops, "stop", "stop sequence; may be repeated")
	fs.Var(&topP, "top-p", "top_p")
	fs.BoolVar(&noStream, "no-stream", false, "disable streaming")
	fs.BoolVar(&jsonOut, "json", false, "write a single JSON object; implies --no-stream")
	fs.BoolVar(&listSessions, "list-sessions", false, "list sessions")
	fs.Var(&removeSession, "rm-session", "remove a named session")
	fs.BoolVar(&clearSessions, "clear-sessions", false, "remove all sessions")
	fs.BoolVar(&insecure, "insecure", false, "allow loose ~/.llmx permissions")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: llmx [flags] [prompt]")
		fmt.Fprintln(stderr, "flags must be placed before the prompt; standard flag parsing does not read flags after positional arguments.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return app.ExitUsage
	}
	profile, code := mergeString(stderr, "profile", pShort, pLong)
	if code != app.ExitOK {
		fs.Usage()
		return code
	}
	model, code := mergeString(stderr, "model", mShort, mLong)
	if code != app.ExitOK {
		fs.Usage()
		return code
	}
	temperature, code := mergeFloat(stderr, "temperature", tShort, tLong)
	if code != app.ExitOK {
		fs.Usage()
		return code
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	home, _ := os.UserHomeDir()
	opts := app.Options{
		Args:          fs.Args(),
		Stdin:         stdin,
		Stdout:        stdout,
		Stderr:        stderr,
		StdinIsTTY:    isTTY(stdin),
		StdoutIsTTY:   fileWriterIsTTY(stdout),
		HomeDir:       home,
		Usage:         fs.Usage,
		Profile:       profile,
		Model:         model,
		Insecure:      insecure,
		Continue:      cShort || cLong,
		SessionName:   sessionName.value,
		New:           newSession,
		System:        system.value,
		SystemSet:     system.set,
		Temperature:   temperature,
		Stops:         stops,
		NoStream:      noStream,
		JSON:          jsonOut,
		ListSessions:  listSessions,
		RemoveSession: removeSession.value,
		ClearSessions: clearSessions,
	}
	if maxTokens.set {
		opts.MaxTokens = &maxTokens.value
	}
	if topP.set {
		opts.TopP = &topP.value
	}
	return app.Run(ctx, opts)
}

func mergeString(stderr io.Writer, name string, short, long stringValue) (string, int) {
	if short.set && long.set && short.value != long.value {
		fmt.Fprintf(stderr, "usage error: -%s and --%s conflict\n", string(name[0]), name)
		return "", app.ExitUsage
	}
	if long.set {
		return long.value, app.ExitOK
	}
	return short.value, app.ExitOK
}

func mergeFloat(stderr io.Writer, name string, short, long floatValue) (*float64, int) {
	if short.set && long.set && short.value != long.value {
		fmt.Fprintf(stderr, "usage error: -%s and --%s conflict\n", string(name[0]), name)
		return nil, app.ExitUsage
	}
	if long.set {
		return &long.value, app.ExitOK
	}
	if short.set {
		return &short.value, app.ExitOK
	}
	return nil, app.ExitOK
}

func isTTY(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func fileWriterIsTTY(w io.Writer) bool {
	file, ok := w.(*os.File)
	return ok && isTTY(file)
}
