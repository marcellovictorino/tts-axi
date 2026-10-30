// Package cmd holds tts-axi's cobra commands, one file per command.
//
// cobra (github.com/spf13/cobra) is the command-line framework behind
// kubectl, gh and Hugo: each command is a *cobra.Command value with a Use
// line, flags, and a RunE function. Commands are built by constructor
// functions (newSayCmd, ...) that receive the shared *App, which keeps state
// out of package globals and makes the commands easy to test.
package cmd

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/marcellovictorino/tts-axi/internal/clones"
	"github.com/marcellovictorino/tts-axi/internal/config"
	"github.com/marcellovictorino/tts-axi/internal/engine"
	"github.com/marcellovictorino/tts-axi/internal/guard"
	"github.com/marcellovictorino/tts-axi/internal/library"
	"github.com/marcellovictorino/tts-axi/internal/output"
	"github.com/marcellovictorino/tts-axi/internal/voices"
)

// Deps are the things tests replace: streams, the terminal check, the memory
// sampler and its limits, the HTTP client, the clock, and the registry the
// multilingual model comes from.
type Deps struct {
	Out, Err  io.Writer
	In        io.Reader // stdin, read when the text argument is "-"
	StdoutTTY bool
	Sampler   guard.Sampler
	Limits    guard.Limits
	HTTP      *http.Client
	Now       func() time.Time
	Registry  engine.Registry   // zero: Ollama's registry and the pinned manifest
	Qwen      engine.QwenSource // zero: Hugging Face and the pinned files
}

// App is the state shared by all commands. Paths and settings load lazily so
// that `tts-axi --help` and `tts-axi version` work even when the settings
// file is broken.
type App struct {
	deps    Deps
	format  string
	printer *output.Printer

	paths    config.Paths
	settings config.Settings
	voices   voices.Set
	loaded   bool
}

// P is the printer.
func (a *App) P() *output.Printer { return a.printer }

// load resolves paths and settings on first use.
func (a *App) load() error {
	if a.loaded {
		return nil
	}
	p, err := config.Resolve()
	if err != nil {
		return output.Fail(nil, "%v", err)
	}
	s, err := config.Load(&p)
	if err != nil {
		return output.Fail([]string{"fix or remove the settings file"}, "%v", err)
	}
	vs, err := voices.NewSet(s.Voices)
	if err != nil {
		return output.Fail([]string{"fix the voices table in " + p.Config}, "%v", err)
	}
	stored, err := clones.List(p.VoicesDir())
	if err != nil {
		return output.Fail(nil, "reading the cloned voices in %s: %v", p.VoicesDir(), err)
	}
	cloned := make([]voices.Voice, len(stored))
	for i, c := range stored {
		cloned[i] = voices.Voice{Name: c.Name, Lang: "en", Description: "cloned voice (Qwen3-TTS)", Ref: c.Ref, RefText: c.Text}
	}
	a.paths, a.settings, a.voices = p, s, vs.WithClones(cloned)
	a.loaded = true
	return nil
}

// engine returns the speech engine for this invocation.
func (a *App) engine() *engine.Engine {
	return &engine.Engine{
		Paths:             a.paths,
		HTTP:              a.deps.HTTP,
		Sampler:           a.deps.Sampler,
		Limits:            a.deps.Limits,
		Registry:          a.deps.Registry,
		Qwen:              a.deps.Qwen,
		MultilingualModel: os.Getenv("TTS_AXI_MULTILINGUAL_MODEL"),
		Progress:          a.printer.Progress,
	}
}

// library returns the library folder.
func (a *App) library() library.Library {
	base := a.settings.BaseURL
	if v := os.Getenv("TTS_AXI_BASE_URL"); v != "" {
		base = v
	}
	return library.Library{Dir: a.paths.Library, BaseURL: base}
}

// Execute runs tts-axi and returns the process exit code.
func Execute() int {
	deps := Deps{
		Out: os.Stdout, Err: os.Stderr, In: os.Stdin,
		StdoutTTY: output.IsTerminal(os.Stdout),
		Sampler:   guard.Darwin{},
		Limits:    guard.DefaultLimits,
		HTTP:      &http.Client{Timeout: 10 * time.Minute},
		Now:       time.Now,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return Run(ctx, deps, os.Args[1:])
}

// Run executes one tts-axi invocation with explicit dependencies; tests call
// it directly.
func Run(ctx context.Context, deps Deps, args []string) int {
	root, app := newRoot(deps)
	if args == nil {
		args = []string{} // SetArgs(nil) would make cobra read os.Args instead
	}
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return output.ExitOK
	}
	if app.printer == nil {
		// Failure before flags were parsed (bad --format, unknown flag on root).
		p, _ := output.NewPrinter(deps.Out, deps.Err, "", deps.StdoutTTY)
		app.printer = p
	}
	e, ok := err.(*output.Error)
	if !ok {
		e = classify(root, err)
	}
	app.printer.Fail(e)
	return e.Code
}

// classify turns cobra's plain errors into tts-axi errors: argument and
// command mistakes are usage errors (exit 2) with the valid choices listed.
func classify(root *cobra.Command, err error) *output.Error {
	msg := err.Error()
	for _, prefix := range []string{"unknown command", "accepts ", "requires ", "invalid argument", "required flag"} {
		if strings.HasPrefix(msg, prefix) {
			help := []string{"run `tts-axi --help` for usage"}
			if strings.HasPrefix(msg, "unknown command") {
				help = validFlagsHelp(root)[1:]
			}
			return output.Usage(help, "%s", msg)
		}
	}
	return output.Fail(nil, "%s", msg)
}
