// Package engine runs the speech helpers and decides which one speaks.
//
// English goes to a small Swift helper that runs Phonon 31M through Gradium's
// Core ML runtime, on the Neural Engine by default. French and Portuguese go
// to ptts (Gradium's Python runtime) with the 40M multilingual model on the
// CPU, run by uv. Either way a helper is a separate process that loads the
// model, speaks a batch of lines into WAV files, and exits, so no memory is
// held between renders.
//
// The policy, agreed for a Mac that is often short of memory:
//
//  1. Note the memory baseline. At critical pressure, wait for it to settle,
//     or give up.
//  2. English starts on the Neural Engine, unless --engine cpu forces the CPU
//     or pressure is already above normal, which starts it on the CPU.
//  3. While a helper speaks, package guard stops it on critical pressure, any
//     pressure rise, or swap growth over 256 MiB in a minute.
//  4. A Neural Engine runtime failure falls back to the same bundle on the
//     CPU. A guard stop waits for pressure to settle, then retries once on the
//     CPU.
//  5. If the retry also fails, the render fails and nothing is written.
package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/marcellovictorino/tts-axi/internal/config"
	"github.com/marcellovictorino/tts-axi/internal/guard"
)

// Route names the engine that spoke. Every result reports it.
type Route string

const (
	ANE     Route = "ane"      // Core ML on the Neural Engine (English)
	CPU     Route = "cpu"      // the same Core ML bundle on the CPU (English)
	PttsCPU Route = "ptts-cpu" // ptts with the multilingual model on the CPU
)

// Engine holds what the helpers need. Zero values of the optional fields
// mean the real thing.
type Engine struct {
	Paths   config.Paths
	HTTP    *http.Client
	Sampler guard.Sampler
	Limits  guard.Limits

	Swiftc        string // swiftc, to build the helper
	Uv            string // uv, to run the multilingual helper
	ReleaseBase   string // tests: where release assets are served from
	CheckpointURL string // tests: where the checkpoint is served from
	// MultilingualModel overrides the French and Portuguese model, for
	// example to try the route with the ungated English checkpoint.
	MultilingualModel string

	Progress func(format string, a ...any) // optional status lines for a person
}

func (e *Engine) swiftc() string { return orDefault(e.Swiftc, "swiftc") }
func (e *Engine) uv() string     { return orDefault(e.Uv, "uv") }

func (e *Engine) multilingualModel() string {
	return orDefault(e.MultilingualModel, MultilingualModel)
}

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// Item is one line to speak into one WAV file.
type Item struct {
	Text  string `json:"text"`
	Voice string `json:"voice"`
	Out   string `json:"out"`
}

// Job is a batch of lines in one language, spoken by one helper process.
type Job struct {
	Lang  string
	Speed float64
	Items []Item
	// ForceCPU skips the Neural Engine (--engine cpu).
	ForceCPU bool
}

// Attempt records one helper run.
type Attempt struct {
	Route  Route
	Result string // "ok", "failed: ...", "stopped: ..."
}

// Outcome is how a job was spoken.
type Outcome struct {
	Route Route
	// Fallback says why the job did not run on its first-choice engine; ""
	// when it did.
	Fallback string
	Attempts []Attempt
	Load     time.Duration // the successful helper's model load time
}

// Speak runs a job under the policy in the package comment.
func (e *Engine) Speak(ctx context.Context, job Job) (Outcome, error) {
	var out Outcome
	base, err := e.Sampler.Sample()
	if err != nil {
		return out, fmt.Errorf("reading memory pressure: %w", err)
	}
	if base.Pressure >= guard.Critical {
		e.progress("memory pressure is critical; waiting for it to settle")
		if base, err = guard.Settle(ctx, e.Sampler, e.Limits, guard.Warning); err != nil {
			return out, fmt.Errorf("not starting: %w", err)
		}
	}

	first := PttsCPU
	if job.Lang == "en" {
		first = ANE
		switch {
		case job.ForceCPU:
			first = CPU
		case base.Pressure > guard.Normal:
			first = CPU
			out.Fallback = "memory pressure was " + guard.PressureName(base.Pressure) + " at start"
		}
	}

	load, err := e.attempt(ctx, job, first, base)
	out.Attempts = append(out.Attempts, Attempt{first, describe(err)})
	if err == nil {
		out.Route, out.Load = first, load
		return out, nil
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}

	retry := first
	if first == ANE {
		retry = CPU
	}
	switch {
	case guard.IsStop(err):
		out.Fallback = err.Error()
		e.progress("%s; waiting for memory to settle before one retry on the CPU", err)
		if base, err = guard.Settle(ctx, e.Sampler, e.Limits, base.Pressure); err != nil {
			return out, fmt.Errorf("%s, and %w", out.Fallback, err)
		}
	case first == ANE:
		out.Fallback = "the Neural Engine failed: " + err.Error()
		e.progress("%s; retrying on the CPU", out.Fallback)
	default:
		// A runtime failure on the CPU would fail the same way again.
		return out, err
	}

	load, err = e.attempt(ctx, job, retry, base)
	out.Attempts = append(out.Attempts, Attempt{retry, describe(err)})
	if err != nil {
		return out, fmt.Errorf("%s; the CPU retry also failed: %w", out.Fallback, err)
	}
	out.Route, out.Load = retry, load
	return out, nil
}

func describe(err error) string {
	switch {
	case err == nil:
		return "ok"
	case guard.IsStop(err):
		return err.Error()
	}
	return "failed: " + err.Error()
}

func (e *Engine) progress(format string, a ...any) {
	if e.Progress != nil {
		e.Progress(format, a...)
	}
}

// attempt runs one helper process for the whole job under the guard.
func (e *Engine) attempt(ctx context.Context, job Job, route Route, base guard.Sample) (time.Duration, error) {
	for _, it := range job.Items {
		_ = os.Remove(it.Out) // a previous attempt's partial output
	}
	var name string
	var args []string
	var input any
	switch route {
	case ANE, CPU:
		if !e.HelperCurrent() {
			return 0, errMissing("the Swift helper is not built")
		}
		dir, err := e.SpeedDir(ctx, job.Speed)
		if err != nil {
			return 0, err
		}
		unit := "ane"
		if route == CPU {
			unit = "cpu"
		}
		name = e.Paths.HelperBin()
		input = map[string]any{"model": dir, "unit": unit, "lang": job.Lang, "items": job.Items}
	case PttsCPU:
		script, err := e.WritePttsHelper()
		if err != nil {
			return 0, err
		}
		name, args = e.uv(), e.PttsArgs(script)
		input = map[string]any{"model": e.multilingualModel(), "lang": job.Lang, "speed": job.Speed, "items": job.Items}
	}
	stdin, err := json.Marshal(input)
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	events := make(chan helperResult, 1)
	go func() { events <- readEvents(pr, len(job.Items), e.progress) }()

	runErr := guard.Run(ctx, e.Sampler, e.Limits, base, cmd)
	_ = pw.Close()
	res := <-events
	if runErr != nil {
		if guard.IsStop(runErr) || ctx.Err() != nil {
			return 0, runErr
		}
		msg := res.errMsg
		if msg == "" {
			msg = firstLines(stderr.String(), 2)
		}
		if msg == "" {
			msg = runErr.Error()
		}
		return 0, fmt.Errorf("%s", msg)
	}
	if res.items != len(job.Items) {
		return 0, fmt.Errorf("the helper spoke %d of %d lines", res.items, len(job.Items))
	}
	for _, it := range job.Items {
		if _, err := os.Stat(it.Out); err != nil {
			return 0, fmt.Errorf("the helper did not write %s", it.Out)
		}
	}
	return res.load, nil
}

type helperResult struct {
	items  int
	load   time.Duration
	errMsg string
}

// readEvents follows the helper's JSON lines.
func readEvents(r io.Reader, total int, progress func(string, ...any)) helperResult {
	var res helperResult
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var ev struct {
			Event   string  `json:"event"`
			LoadS   float64 `json:"load_s"`
			Message string  `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue // uv or Python may print other lines
		}
		switch ev.Event {
		case "loaded":
			res.load = time.Duration(ev.LoadS * float64(time.Second))
		case "item":
			res.items++
			if progress != nil && total > 1 {
				progress("spoke %d of %d", res.items, total)
			}
		case "error":
			res.errMsg = strings.TrimSpace(ev.Message)
		}
	}
	_, _ = io.Copy(io.Discard, r)
	return res
}
