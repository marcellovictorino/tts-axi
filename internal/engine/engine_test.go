package engine_test

import (
	"bytes"
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcellovictorino/tts-axi/internal/config"
	"github.com/marcellovictorino/tts-axi/internal/engine"
	"github.com/marcellovictorino/tts-axi/internal/guard"
	"github.com/marcellovictorino/tts-axi/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.MaybeRunFake() // acts as the Swift helper or uv when started under those names
	os.Exit(m.Run())
}

func newEngine(t *testing.T, s guard.Sampler) *engine.Engine {
	t.Helper()
	home := t.TempDir()
	testutil.Install(t, home)
	return &engine.Engine{
		Paths:    config.Paths{Home: home, Library: t.TempDir()},
		HTTP:     http.DefaultClient,
		Sampler:  s,
		Limits:   testutil.FastLimits,
		Registry: testutil.Registry(t),
	}
}

func job(t *testing.T, lang string, texts ...string) engine.Job {
	t.Helper()
	dir := t.TempDir()
	j := engine.Job{Lang: lang, Speed: 0.15}
	for i, text := range texts {
		j.Items = append(j.Items, engine.Item{Text: text, Voice: "Hugh", Out: filepath.Join(dir, string(rune('a'+i))+".wav")})
	}
	return j
}

func written(j engine.Job) int {
	n := 0
	for _, it := range j.Items {
		if _, err := os.Stat(it.Out); err == nil {
			n++
		}
	}
	return n
}

// Intent (spec: engine policy): English speaks on the Neural
// Engine by default.
func TestEnglishStartsOnTheNeuralEngine(t *testing.T) {
	jobs := testutil.LogJobs(t)
	e := newEngine(t, &testutil.Sampler{Rest: testutil.Normal})
	j := job(t, "en", "Good morning.", "Three meetings today.")
	out, err := e.Speak(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	if out.Route != engine.ANE || out.Fallback != "" {
		t.Errorf("route %s, fallback %q; want ane with no fallback", out.Route, out.Fallback)
	}
	got := jobs()
	if len(got) != 1 || got[0].Unit != "ane" || len(got[0].Items) != 2 {
		t.Fatalf("want one helper run on ane speaking both lines, got %+v", got)
	}
	// The default speed (+0.15) is its own bundle folder with the shifted conditions.
	if !strings.HasSuffix(got[0].Model, engine.BundleName+"@speed0.15") {
		t.Errorf("model folder %s is not the +0.15 bundle", got[0].Model)
	}
	if written(j) != 2 {
		t.Errorf("wrote %d of 2 files", written(j))
	}
}

// Intent (agreed engine policy): a Neural Engine runtime failure falls back
// to the same bundle on the CPU, and the result says why.
func TestNeuralEngineFailureFallsBackToCPU(t *testing.T) {
	jobs := testutil.LogJobs(t)
	testutil.FakeEnv(t, "ane", "", 0)
	e := newEngine(t, &testutil.Sampler{Rest: testutil.Normal})
	out, err := e.Speak(context.Background(), job(t, "en", "Hello."))
	if err != nil {
		t.Fatal(err)
	}
	if out.Route != engine.CPU || !strings.Contains(out.Fallback, "fake ane failure") {
		t.Errorf("route %s, fallback %q; want cpu naming the Neural Engine error", out.Route, out.Fallback)
	}
	got := jobs()
	if len(got) != 2 || got[0].Unit != "ane" || got[1].Unit != "cpu" || got[0].Model != got[1].Model {
		t.Errorf("want ane then cpu on the same bundle, got %+v", got)
	}
}

// Intent (agreed engine policy): pressure above normal at start means the
// CPU route from the beginning.
func TestWarningPressureAtStartUsesTheCPU(t *testing.T) {
	jobs := testutil.LogJobs(t)
	e := newEngine(t, &testutil.Sampler{Rest: testutil.Warning})
	out, err := e.Speak(context.Background(), job(t, "en", "Hello."))
	if err != nil {
		t.Fatal(err)
	}
	if out.Route != engine.CPU || out.Fallback != "memory pressure was warning at start" {
		t.Errorf("route %s, fallback %q", out.Route, out.Fallback)
	}
	if got := jobs(); len(got) != 1 || got[0].Unit != "cpu" {
		t.Errorf("want a single cpu run, got %+v", got)
	}
}

// Intent (--engine cpu): forcing the CPU skips the Neural Engine and is not
// reported as a fallback.
func TestForcedCPU(t *testing.T) {
	jobs := testutil.LogJobs(t)
	e := newEngine(t, &testutil.Sampler{Rest: testutil.Normal})
	j := job(t, "en", "Hello.")
	j.ForceCPU = true
	out, err := e.Speak(context.Background(), j)
	if err != nil || out.Route != engine.CPU || out.Fallback != "" {
		t.Fatalf("route %s, fallback %q, err %v", out.Route, out.Fallback, err)
	}
	if got := jobs(); len(got) != 1 || got[0].Unit != "cpu" {
		t.Errorf("want a single cpu run, got %+v", got)
	}
}

// Intent (agreed memory stops): a pressure rise while speaking stops and
// unloads the helper, waits for pressure to settle, then retries once on the
// CPU.
func TestPressureRiseStopsThenRetriesOnCPU(t *testing.T) {
	jobs := testutil.LogJobs(t)
	testutil.FakeEnv(t, "", "ane", 5*time.Second) // the ane run is still speaking when pressure rises
	s := &testutil.Sampler{Seq: []guard.Sample{testutil.Normal, testutil.Warning}, Rest: testutil.Normal}
	e := newEngine(t, s)
	start := time.Now()
	out, err := e.Speak(context.Background(), job(t, "en", "Hello."))
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 4*time.Second {
		t.Errorf("the stopped helper was not killed promptly (%s)", time.Since(start))
	}
	if out.Route != engine.CPU || !strings.Contains(out.Fallback, "memory pressure rose from normal to warning") {
		t.Errorf("route %s, fallback %q", out.Route, out.Fallback)
	}
	if len(out.Attempts) != 2 || out.Attempts[0].Route != engine.ANE || !strings.HasPrefix(out.Attempts[0].Result, "stopped") || out.Attempts[1].Route != engine.CPU {
		t.Errorf("want a stopped ane attempt then cpu, got %+v", out.Attempts)
	}
	// The ane helper may be killed before it logs its job; the retry always logs.
	if got := jobs(); len(got) == 0 || got[len(got)-1].Unit != "cpu" {
		t.Errorf("the last helper run should be cpu, got %+v", got)
	}
}

// Intent (agreed memory stops): if the CPU retry is stopped too, the job
// fails and leaves no audio behind.
func TestSecondStopFailsAndWritesNothing(t *testing.T) {
	testutil.FakeEnv(t, "", "any", 5*time.Second)
	s := &testutil.Sampler{
		Seq:  []guard.Sample{testutil.Normal, testutil.Warning, testutil.Normal, testutil.Warning},
		Rest: testutil.Normal,
	}
	e := newEngine(t, s)
	j := job(t, "en", "Hello.")
	_, err := e.Speak(context.Background(), j)
	if err == nil || !strings.Contains(err.Error(), "the CPU retry also failed") {
		t.Fatalf("want a failure after the retry, got %v", err)
	}
	if written(j) != 0 {
		t.Error("a stopped job left audio behind")
	}
}

// Intent (spec: French and Portuguese use phonon-40m-multilingual through
// ptts on the CPU): the job goes to uv with the model, language and speed.
func TestPortugueseGoesToPtts(t *testing.T) {
	jobs := testutil.LogJobs(t)
	e := newEngine(t, &testutil.Sampler{Rest: testutil.Normal})
	j := job(t, "pt", "Bom dia.")
	j.Items[0].Voice = "Davi"
	out, err := e.Speak(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	got := jobs()
	if out.Route != engine.PttsCPU || len(got) != 1 || got[0].Helper != "uv" {
		t.Fatalf("route %s, jobs %+v", out.Route, got)
	}
	if got[0].Model != e.MultilingualDir() || got[0].Lang != "pt" || got[0].Speed == nil || *got[0].Speed != 0.15 || got[0].Items[0].Voice != "Davi" {
		t.Errorf("job %+v", got[0])
	}
}

// Intent (spec: the default speed, duration_delta +0.15, on the Core ML
// route): tts-axi's conditioner arithmetic reproduces the conditions vector
// Gradium's exporter baked into the released bundle at duration_delta 0.
// That vector is num_speakers' "1" row plus duration_delta at 0, both through
// their projections.
func TestConditionerArithmeticMatchesTheReleasedBundle(t *testing.T) {
	w, err := testutil.DurationWeights()
	if err != nil {
		t.Fatal(err)
	}
	embed, _, err := testutil.Tensor(testutil.Conditioners, "flow_lm.condition_provider.conditioners.num_speakers.embed.weight")
	if err != nil {
		t.Fatal(err)
	}
	proj, _, err := testutil.Tensor(testutil.Conditioners, "flow_lm.condition_provider.conditioners.num_speakers.output_proj.weight")
	if err != nil {
		t.Fatal(err)
	}
	bundle, _, err := testutil.Tensor(testutil.HostConditions, "flow_lm.conditions")
	if err != nil {
		t.Fatal(err)
	}
	at0 := engine.DurationCondition(w, 0)
	worst := 0.0
	for o := 0; o < 512; o++ {
		var speakers float64
		for i := 0; i < 16; i++ {
			speakers += float64(proj[o*16+i]) * float64(embed[16+i]) // row 1: num_speakers "1"
		}
		worst = math.Max(worst, math.Abs(speakers+float64(at0[o])-float64(bundle[o])))
	}
	if worst > 1e-5 {
		t.Errorf("rebuilt conditions differ from the bundle by up to %g", worst)
	}
}

// Intent (speed on the Core ML route): a speed folder carries the bundle's
// conditions shifted by duration_delta, links the rest, and the weights are
// read from the checkpoint with range requests, not a full download.
func TestSpeedFolderFromCheckpoint(t *testing.T) {
	var ranges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ranges = append(ranges, r.Header.Get("Range"))
		http.ServeContent(w, r, "model.safetensors", time.Time{}, bytes.NewReader(testutil.Conditioners))
	}))
	defer srv.Close()
	e := newEngine(t, &testutil.Sampler{Rest: testutil.Normal})
	e.CheckpointURL = srv.URL
	base := filepath.Join(e.Paths.ModelsDir(), engine.BundleName)
	if err := os.Remove(base + ".duration_delta.f32"); err != nil { // force a fetch
		t.Fatal(err)
	}

	dir, err := e.SpeedDir(context.Background(), 0.3)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 3 || ranges[0] != "bytes=0-7" {
		t.Errorf("want three range reads (size, header, tensor), got %v", ranges)
	}
	host, err := os.ReadFile(filepath.Join(dir, "host.safetensors"))
	if err != nil {
		t.Fatal(err)
	}
	before, _, _ := testutil.Tensor(testutil.HostConditions, "flow_lm.conditions")
	after, _, _ := testutil.Tensor(host, "flow_lm.conditions")
	w, _ := testutil.DurationWeights()
	want := engine.DurationCondition(w, 0.3)
	zero := engine.DurationCondition(w, 0)
	for i := range after {
		if d := math.Abs(float64(after[i] - (before[i] + want[i] - zero[i]))); d > 1e-5 {
			t.Fatalf("element %d off by %g", i, d)
		}
	}
	if target, err := os.Readlink(filepath.Join(dir, "bundle.json")); err != nil || target != filepath.Join(base, "bundle.json") {
		t.Errorf("bundle.json should link to the released bundle, got %q (%v)", target, err)
	}
	if got, _ := e.SpeedDir(context.Background(), 0); got != base {
		t.Errorf("speed 0 should be the released bundle, got %s", got)
	}
}
