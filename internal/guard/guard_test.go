package guard_test

import (
	"context"
	"net/http"
	"os"
	"strings"
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
