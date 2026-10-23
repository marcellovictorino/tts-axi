// Package output renders tts-axi's results for two audiences.
//
// A person at a terminal gets aligned tables and colour. Anything else (a
// pipe, a file, an AI agent) gets TOON on stdout with minimal fields, a
// "help[n]:" line per suggested next step, and no colour. The
// audience is decided once, from whether stdout is a terminal, and can be
// overridden with --format.
package output

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"
)

// Exit codes: 0 success, 1 runtime failure, 2 usage error (bad flags or
// arguments). Agents branch on these without parsing text.
const (
	ExitOK      = 0
	ExitFailure = 1
	ExitUsage   = 2
)

// Error is a user-facing failure: a message, optional next steps, and the
// process exit code.
type Error struct {
	Msg  string
	Help []string
	Code int
}

func (e *Error) Error() string { return e.Msg }

// ExitWith returns an error that only sets the exit code, for commands that
// have already printed their result (a failed benchmark, for example).
func ExitWith(code int) *Error { return &Error{Code: code} }

// Fail builds a runtime error (exit 1).
func Fail(help []string, format string, a ...any) *Error {
	return &Error{Msg: fmt.Sprintf(format, a...), Help: help, Code: ExitFailure}
}

// Usage builds a usage error (exit 2).
func Usage(help []string, format string, a ...any) *Error {
	return &Error{Msg: fmt.Sprintf(format, a...), Help: help, Code: ExitUsage}
}

// Printer writes documents in the chosen format.
type Printer struct {
	Out   io.Writer
	Err   io.Writer
	Human bool
	Color bool
	TTY   bool // stdout is a terminal (governs progress output)
}

// NewPrinter decides the output format. format is "", "auto", "toon" or "human".
func NewPrinter(out, errOut io.Writer, format string, stdoutIsTTY bool) (*Printer, error) {
	p := &Printer{Out: out, Err: errOut, TTY: stdoutIsTTY}
	switch format {
	case "", "auto":
		p.Human = stdoutIsTTY
	case "human":
		p.Human = true
	case "toon":
	default:
		return nil, fmt.Errorf("unknown --format %q (valid: toon, human)", format)
	}
	// NO_COLOR is the community convention: any non-empty value disables colour.
	p.Color = p.Human && stdoutIsTTY && os.Getenv("NO_COLOR") == ""
	return p, nil
}

// IsTerminal reports whether f is attached to a terminal.
func IsTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

// Emit prints a document. A top-level "help" entry ([]string) is treated as
// next-step suggestions rather than data.
func (p *Printer) Emit(doc Obj) {
	data, help := splitHelp(doc)
	if p.Human {
		p.humanObj(data, 0)
		for _, h := range help {
			fmt.Fprintln(p.Out, p.dim("hint: "+h))
		}
		return
	}
	fmt.Fprint(p.Out, EncodeTOON(data))
	writeHelp(p.Out, help)
}

// Fail prints an error and its next steps. In TOON mode it goes to stdout
// (agents read stdout; stderr is often discarded); for people it goes to stderr.
func (p *Printer) Fail(e *Error) {
	if e.Msg == "" {
		return // exit code only: the command already printed its own result
	}
	if p.Human {
		fmt.Fprintf(p.Err, "%s %s\n", p.paint("31;1", "error:"), e.Msg)
		for _, h := range e.Help {
			fmt.Fprintf(p.Err, "%s %s\n", p.dim("hint:"), h)
		}
		return
	}
	fmt.Fprintf(p.Out, "error: %s\n", oneLine(e.Msg))
	writeHelp(p.Out, e.Help)
}

// Progress writes a transient status line to stderr, only for a person at a
// terminal. Agents and pipes never see it.
func (p *Printer) Progress(format string, a ...any) {
	if p.TTY && p.Human {
		fmt.Fprintf(p.Err, format+"\n", a...)
	}
}

func splitHelp(doc Obj) (Obj, []string) {
	var data Obj
	var help []string
	for _, kv := range doc {
		if kv.Key == "help" {
			if hs, ok := kv.Val.([]string); ok {
				help = append(help, hs...)
				continue
			}
		}
		data = append(data, kv)
	}
	return data, help
}

func writeHelp(w io.Writer, help []string) {
	for i, h := range help {
		fmt.Fprintf(w, "help[%d]: %s\n", i+1, oneLine(h))
	}
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// --- human rendering ---

func (p *Printer) paint(code, s string) string {
	if !p.Color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p *Printer) dim(s string) string { return p.paint("2", s) }

// status words get a colour so a glance at a table says what matters.
func (p *Printer) status(s string) string {
	switch s {
	case "ok", "yes", "installed", "already installed", "ane", "default":
		return p.paint("32", s)
	case "fail", "failed", "missing", "no", "stopped":
		return p.paint("31", s)
	case "warn", "cpu", "fallback", "updated":
		return p.paint("33", s)
	}
	return s
}

func (p *Printer) humanObj(o Obj, depth int) {
	pad := strings.Repeat("  ", depth)
	width := 0
	for _, kv := range o {
		if isScalar(kv.Val) || isStrings(kv.Val) {
			width = max(width, len(kv.Key))
		}
	}
	for _, kv := range o {
		switch v := kv.Val.(type) {
		case Obj:
			fmt.Fprintf(p.Out, "%s%s\n", pad, p.paint("1", kv.Key+":"))
			p.humanObj(v, depth+1)
		case []Obj:
			fmt.Fprintf(p.Out, "%s%s\n", pad, p.paint("1", kv.Key+":"))
			p.humanTable(v, pad+"  ")
		case []string:
			fmt.Fprintf(p.Out, "%s%-*s  %s\n", pad, width+1, p.paint("1", kv.Key+":"), strings.Join(v, ", "))
		default:
			fmt.Fprintf(p.Out, "%s%s%s  %s\n", pad, p.paint("1", kv.Key+":"), strings.Repeat(" ", width-len(kv.Key)), p.status(fmt.Sprint(scalarText(v))))
		}
	}
}

func (p *Printer) humanTable(rows []Obj, pad string) {
	if len(rows) == 0 {
		fmt.Fprintf(p.Out, "%s%s\n", pad, p.dim("(none)"))
		return
	}
	tw := tabwriter.NewWriter(p.Out, 0, 0, 2, ' ', 0)
	var head []string
	for _, kv := range rows[0] {
		head = append(head, strings.ToUpper(kv.Key))
	}
	fmt.Fprintf(tw, "%s%s\n", pad, strings.Join(head, "\t"))
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, kv := range row {
			cells[i] = scalarText(kv.Val)
		}
		fmt.Fprintf(tw, "%s%s\n", pad, strings.Join(cells, "\t"))
	}
	_ = tw.Flush()
	// Colour is applied after alignment would be ideal, but ANSI codes break
	// tabwriter widths; tables stay plain and status words are coloured in
	// key/value blocks only.
}

func scalarText(v any) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprint(v)
}

func isScalar(v any) bool {
	switch v.(type) {
	case Obj, []Obj, []string:
		return false
	}
	return true
}

func isStrings(v any) bool { _, ok := v.([]string); return ok }
