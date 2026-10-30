package guard_test

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcellovictorino/tts-axi/internal/config"
	"github.com/marcellovictorino/tts-axi/internal/engine"
	"github.com/marcellovictorino/tts-axi/internal/guard"
	"github.com/marcellovictorino/tts-axi/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.MaybeRunFake() // acts as the Swift helper when started under its name
	os.Exit(m.Run())
}

// Intent (agreed memory stops): swap growing over 256 MiB within a minute
// stops the run even at steady pressure.
func TestSwapGrowthStops(t *testing.T) {
	w := guard.NewWatcher(guard.DefaultLimits, testutil.Normal, time.Unix(0, 0))
	grown := testutil.Normal
	grown.SwapUsedBytes += 200 << 20
	if r := w.Check(grown, time.Unix(30, 0)); r != "" {
		t.Fatalf("200 MiB stopped the run: %s", r)
	}
	grown.SwapUsedBytes += 100 << 20
	if r := w.Check(grown, time.Unix(50, 0)); !strings.Contains(r, "swap grew by 300 MiB") {
		t.Fatalf("300 MiB in 50 s did not stop the run: %q", r)
	}
	// The same growth spread over more than a minute is not a stop.
	w = guard.NewWatcher(guard.DefaultLimits, testutil.Normal, time.Unix(0, 0))
	slow := testutil.Normal
	for i := 1; i <= 6; i++ {
		slow.SwapUsedBytes += 60 << 20
		if r := w.Check(slow, time.Unix(int64(i*30), 0)); r != "" {
			t.Fatalf("slow growth stopped the run at step %d: %s", i, r)
		}
	}
}

// Intent (agreed memory stops): critical pressure stops at once, and a
// critical start waits for pressure to settle instead of speaking.
func TestCriticalPressure(t *testing.T) {
	w := guard.NewWatcher(guard.DefaultLimits, guard.Sample{Pressure: guard.Warning}, time.Unix(0, 0))
	if r := w.Check(guard.Sample{Pressure: guard.Critical}, time.Unix(1, 0)); r != "memory pressure is critical" {
		t.Errorf("got %q", r)
	}
	jobs := testutil.LogJobs(t)
	home := t.TempDir()
	testutil.Install(t, home)
	e := &engine.Engine{
		Paths:   config.Paths{Home: home},
		HTTP:    http.DefaultClient,
		Sampler: &testutil.Sampler{Rest: guard.Sample{Pressure: guard.Critical}},
		Limits:  testutil.FastLimits,
	}
	j := engine.Job{Lang: "en", Items: []engine.Item{{Text: "Hello.", Voice: "Hugh", Out: t.TempDir() + "/a.wav"}}}
	_, err := e.Speak(context.Background(), j)
	if err == nil || !strings.Contains(err.Error(), "not starting") {
		t.Fatalf("want a refusal to start, got %v", err)
	}
	if len(jobs()) != 0 {
		t.Error("a helper ran at critical pressure")
	}
}

// Intent (agreed memory stops): readings come from sysctl, whose swap line
// looks like this on macOS.
func TestReadsSysctl(t *testing.T) {
	s, err := guard.ParseSysctl("2\ntotal = 11264.00M  used = 9916.25M  free = 1347.75M  (encrypted)\n")
	if err != nil {
		t.Fatal(err)
	}
	if s.Pressure != guard.Warning || s.SwapUsedBytes != int64(9916.25*(1<<20)) {
		t.Errorf("got %+v", s)
	}
	if _, err := guard.ParseSysctl("1\n"); err == nil {
		t.Error("a missing swap line should be an error")
	}
}

// The real sampler works on this Mac (skipped elsewhere).
func TestDarwinSampler(t *testing.T) {
	if _, err := os.Stat("/usr/sbin/sysctl"); err != nil {
		t.Skip("no sysctl")
	}
	s, err := guard.Darwin{}.Sample()
	if err != nil || s.Pressure < guard.Normal {
		t.Fatalf("sample %+v, err %v", s, err)
	}
}

// swapAt is a sampler whose swap reading the test moves while a run goes on.
type swapAt struct {
	swap     atomic.Int64
	pressure atomic.Int64 // 0 reads as normal
}

func (s *swapAt) Sample() (guard.Sample, error) {
	p := int(s.pressure.Load())
	if p == 0 {
		p = guard.Normal
	}
	return guard.Sample{Pressure: p, SwapUsedBytes: s.swap.Load()}, nil
}

// runSwap runs a 600 ms command while the swap reading jumps by jump1 at
// 50 ms (the model loading and first line) and by jump2 at 300 ms (speaking), and reports
// the guard's verdict. loadedAt closes the loaded channel; nil means plain Run.
func runSwap(t *testing.T, jump1, jump2 int64, gated bool) error {
	t.Helper()
	s := &swapAt{}
	s.swap.Store(7 << 30)
	base := guard.Sample{Pressure: guard.Normal, SwapUsedBytes: 7 << 30}
	var loaded chan struct{}
	if gated {
		loaded = make(chan struct{})
		go func() { time.Sleep(200 * time.Millisecond); close(loaded) }() // the first line is spoken
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		s.swap.Add(jump1)
		time.Sleep(250 * time.Millisecond)
		s.swap.Add(jump2)
	}()
	lim := testutil.FastLimits
	if !gated {
		return guard.Run(context.Background(), s, lim, base, exec.Command("sleep", "0.6"))
	}
	return guard.RunAfterWarmup(context.Background(), s, lim, base, exec.Command("sleep", "0.6"), loaded)
}

// Intent (task: a 3 GB model's first load must not trip the swap-growth stop
// when another local model is resident, while growth during speech still
// does): swap growth before the first line is spoken is ignored, growth
// after it is judged from the warmed-up reading.
func TestSwapGrowthIsMeasuredFromAfterTheLoad(t *testing.T) {
	// 2 GiB of paging during the load: plain Run stops, a gated run does not.
	if err := runSwap(t, 2<<30, 0, false); !guard.IsStop(err) {
		t.Fatalf("plain Run should stop on 2 GiB of swap growth, got %v", err)
	}
	if err := runSwap(t, 2<<30, 0, true); err != nil {
		t.Fatalf("growth during the load stopped the run: %v", err)
	}
	// 500 MiB more while speaking is a stop even after a big load.
	if err := runSwap(t, 2<<30, 500<<20, true); !guard.IsStop(err) {
		t.Fatalf("growth after the load should stop the run, got %v", err)
	}
}

// Intent (same): during the warm-up a rise in pressure to warning, or a
// critical flicker, which loading a 3 GB model causes on a busy Mac, is not a
// stop; critical pressure that lasts is, and after the warm-up critical is a
// stop at once.
func TestWarmupToleratesPressureFlickerButNotLastingCritical(t *testing.T) {
	base := guard.Sample{Pressure: guard.Normal, SwapUsedBytes: 7 << 30}
	// warming is a run whose warm-up never ends: levels[i] holds from the
	// i-th 50 ms step on.
	warming := func(levels ...int64) error {
		s := &swapAt{}
		s.swap.Store(7 << 30)
		go func() {
			for _, l := range levels {
				time.Sleep(50 * time.Millisecond)
				s.pressure.Store(l)
			}
		}()
		return guard.RunAfterWarmup(context.Background(), s, testutil.FastLimits, base, exec.Command("sleep", "0.5"), make(chan struct{}))
	}
	if err := warming(guard.Warning); err != nil {
		t.Errorf("a rise to warning during the warm-up stopped the run: %v", err)
	}
	if err := warming(guard.Critical, guard.Warning); err != nil {
		t.Errorf("a critical flicker during the warm-up stopped the run: %v", err)
	}
	if err := warming(guard.Critical); !guard.IsStop(err) {
		t.Errorf("lasting critical pressure during the warm-up should stop the run, got %v", err)
	}
	// After the warm-up (channel already closed) warning is tolerated and
	// critical is a stop.
	run := func(level int64) error {
		s := &swapAt{}
		s.swap.Store(7 << 30)
		done := make(chan struct{})
		close(done)
		go func() { time.Sleep(50 * time.Millisecond); s.pressure.Store(level) }()
		return guard.RunAfterWarmup(context.Background(), s, testutil.FastLimits, base, exec.Command("sleep", "0.3"), done)
	}
	if err := run(guard.Warning); err != nil {
		t.Errorf("warning pressure after the warm-up stopped the run: %v", err)
	}
	if err := run(guard.Critical); !guard.IsStop(err) {
		t.Errorf("critical pressure after the warm-up should stop the run, got %v", err)
	}
}
