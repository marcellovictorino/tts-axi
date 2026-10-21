// Package guard keeps speech synthesis from hurting a Mac that is short of
// memory.
//
// It reads macOS memory pressure and swap use, and while a helper process
// speaks it stops that process (and everything it started) at once on:
//
//   - critical memory pressure;
//   - any rise in pressure above the lowest level seen since the run began;
//   - swap use growing by more than 256 MiB within a minute.
//
// The policy of what happens next (wait, retry on the CPU, give up) lives in
// package engine; this package only measures and stops.
package guard

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Pressure levels, as kern.memorystatus_vm_pressure_level reports them.
const (
	Normal   = 1
	Warning  = 2
	Critical = 4
)

// PressureName turns a level into a word.
func PressureName(level int) string {
	switch level {
	case Normal:
		return "normal"
	case Warning:
		return "warning"
	case Critical:
		return "critical"
	}
	return "level " + strconv.Itoa(level)
}

// Sample is one reading.
type Sample struct {
	Pressure      int
	SwapUsedBytes int64
}

// Sampler reads the machine. The real one shells out to sysctl; tests script
// their own.
type Sampler interface {
	Sample() (Sample, error)
}

// Limits are the stop thresholds and timings.
type Limits struct {
	SwapGrowth time.Duration // window for swap growth
	SwapBytes  int64         // growth within the window that stops a run
	Interval   time.Duration // how often to sample while a helper runs
	// Settle is how long to wait for pressure to come back down before a
	// retry, or before starting at all when pressure is critical.
	Settle time.Duration
}

// DefaultLimits are the agreed stops: swap growth over 256 MiB in a minute,
// sampled four times a second.
var DefaultLimits = Limits{
	SwapGrowth: time.Minute,
	SwapBytes:  256 << 20,
	Interval:   250 * time.Millisecond,
	Settle:     2 * time.Minute,
}

// StopError reports that the guard stopped a run.
type StopError struct {
	Reason string
}

func (e *StopError) Error() string { return "stopped to protect memory: " + e.Reason }

// IsStop reports whether err is a guard stop.
func IsStop(err error) bool {
	var s *StopError
	return errors.As(err, &s)
}

// Watcher tracks one run's readings against its baseline.
type Watcher struct {
	lim     Limits
	lowest  int
	history []timed
}

type timed struct {
	at   time.Time
	swap int64
}

// NewWatcher starts from a baseline reading.
func NewWatcher(lim Limits, base Sample, at time.Time) *Watcher {
	return &Watcher{lim: lim, lowest: base.Pressure, history: []timed{{at, base.SwapUsedBytes}}}
}

// Check records a reading and returns a stop reason, or "" to carry on.
func (w *Watcher) Check(s Sample, at time.Time) string {
	w.history = append(w.history, timed{at, s.SwapUsedBytes})
	for len(w.history) > 1 && at.Sub(w.history[0].at) > w.lim.SwapGrowth {
		w.history = w.history[1:]
	}
	minSwap := w.history[0].swap
	for _, h := range w.history {
		minSwap = min(minSwap, h.swap)
	}
	var reason string
	switch {
	case s.Pressure >= Critical:
		reason = "memory pressure is critical"
	case s.Pressure > w.lowest:
		reason = fmt.Sprintf("memory pressure rose from %s to %s", PressureName(w.lowest), PressureName(s.Pressure))
	case s.SwapUsedBytes-minSwap > w.lim.SwapBytes:
		reason = fmt.Sprintf("swap grew by %d MiB within %s", (s.SwapUsedBytes-minSwap)>>20, w.lim.SwapGrowth)
	}
	w.lowest = min(w.lowest, s.Pressure)
	return reason
}

// Run starts cmd in its own process group and waits for it, sampling memory
// every Interval. If a stop condition fires, the whole group is killed (which
// unloads the model with it) and a *StopError is returned.
func Run(ctx context.Context, s Sampler, lim Limits, base Sample, cmd *exec.Cmd) error {
	// Setpgid puts the helper and anything it starts (uv starts Python) in a
	// new process group, so one kill reaches all of them and nothing else.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	var waitErr error
	done := make(chan struct{})
	go func() { waitErr = cmd.Wait(); close(done) }()
	kill := func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done // reap it; a killed helper's exit status says nothing new
	}
	w := NewWatcher(lim, base, time.Now())
	tick := time.NewTicker(lim.Interval)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return waitErr
		case <-ctx.Done():
			kill()
			return ctx.Err()
		case <-tick.C:
			smp, err := s.Sample()
			if err != nil {
				continue // a failed reading is not a reason to stop
			}
			if reason := w.Check(smp, time.Now()); reason != "" {
				kill()
				return &StopError{Reason: reason}
			}
		}
	}
}

// Settle waits until pressure is no higher than ceiling and not critical,
// sampling once a second, for at most lim.Settle. It returns the last reading.
func Settle(ctx context.Context, s Sampler, lim Limits, ceiling int) (Sample, error) {
	deadline := time.Now().Add(lim.Settle)
	step := min(time.Second, max(lim.Settle/10, time.Millisecond))
	for {
		smp, err := s.Sample()
		if err != nil {
			return smp, err
		}
		if smp.Pressure < Critical && smp.Pressure <= max(ceiling, Normal) {
			return smp, nil
		}
		if time.Now().After(deadline) {
			return smp, fmt.Errorf("memory pressure stayed %s for %s", PressureName(smp.Pressure), lim.Settle)
		}
		select {
		case <-ctx.Done():
			return smp, ctx.Err()
		case <-time.After(step):
		}
	}
}

// Darwin is the real sampler: sysctl, which needs no privileges.
type Darwin struct{}

func (Darwin) Sample() (Sample, error) {
	var s Sample
	out, err := exec.Command("sysctl", "-n", "kern.memorystatus_vm_pressure_level", "vm.swapusage").Output()
	if err != nil {
		return s, fmt.Errorf("sysctl: %w", err)
	}
	return ParseSysctl(string(out))
}

// ParseSysctl reads the two lines `sysctl -n kern.memorystatus_vm_pressure_level
// vm.swapusage` prints, for example:
//
//	1
//	total = 11264.00M  used = 9916.25M  free = 1347.75M  (encrypted)
func ParseSysctl(out string) (Sample, error) {
	var s Sample
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return s, fmt.Errorf("unexpected sysctl output %q", out)
	}
	p, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return s, fmt.Errorf("unexpected pressure level %q", lines[0])
	}
	s.Pressure = p
	_, used, ok := strings.Cut(lines[1], "used = ")
	if !ok {
		return s, fmt.Errorf("unexpected swap usage %q", lines[1])
	}
	used, _, _ = strings.Cut(used, " ")
	if used == "" {
		return s, fmt.Errorf("unexpected swap usage %q", lines[1])
	}
	mult := int64(1)
	switch used[len(used)-1] {
	case 'K':
		mult = 1 << 10
	case 'M':
		mult = 1 << 20
	case 'G':
		mult = 1 << 30
	}
	v, err := strconv.ParseFloat(strings.TrimRight(used, "KMG"), 64)
	if err != nil {
		return s, fmt.Errorf("unexpected swap usage %q", lines[1])
	}
	s.SwapUsedBytes = int64(v * float64(mult))
	return s, nil
}
