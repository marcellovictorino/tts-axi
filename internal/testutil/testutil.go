// Package testutil provides fake speech helpers and an isolated installation
// for tests, so no test needs a real model, the Neural Engine or a network.
//
// The fakes use test-binary re-exec: the test binary is linked into a temp
// folder under the helper's name (tts-axi-helper, uv). When the code under
// test runs that name, TestMain calls MaybeRunFake, which notices the name and
// behaves like the helper instead of running the tests.
package testutil

import (
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcellovictorino/tts-axi/internal/engine"
	"github.com/marcellovictorino/tts-axi/internal/guard"
)

// Conditioners and HostConditions are real tensors; see testdata/README.md.
var (
	//go:embed testdata/conditioners.safetensors
	Conditioners []byte
	//go:embed testdata/host-conditions.safetensors
	HostConditions []byte
)

// Environment variables that steer the fakes.
const (
	envLog       = "TTS_AXI_FAKE_LOG"        // append each job as a JSON line here
	envFailUnit  = "TTS_AXI_FAKE_FAIL_UNIT"  // ane, cpu or any: fail with a runtime error
	envSleepUnit = "TTS_AXI_FAKE_SLEEP_UNIT" // ane, cpu or any: sleep before speaking
	envSleep     = "TTS_AXI_FAKE_SLEEP"      // how long, as a Go duration
)

// MaybeRunFake turns the process into a fake helper when it was started
// under a helper's name. Call it first in TestMain.
func MaybeRunFake() {
	switch filepath.Base(os.Args[0]) {
	case "uv":
		if slices.Contains(os.Args, "download") {
			os.Exit(fakeHFDownload())
		}
		if slices.Contains(os.Args, "-c") {
			os.Exit(0) // `uv run ... python -c "import ptts"`: ptts is "installed"
		}
		os.Exit(fakeHelper())
	case "tts-axi-helper":
		os.Exit(fakeHelper())
	}
}

// Job is what a helper received, as the fakes log it.
type Job struct {
	Helper string        `json:"helper"`
	Model  string        `json:"model"`
	Unit   string        `json:"unit"`
	Lang   string        `json:"lang"`
	Speed  *float64      `json:"speed"`
	Items  []engine.Item `json:"items"`
}

func fakeHelper() int {
	var job Job
	if err := json.NewDecoder(os.Stdin).Decode(&job); err != nil {
		emit(map[string]any{"event": "error", "message": "bad job: " + err.Error()})
		return 1
	}
	job.Helper = filepath.Base(os.Args[0])
	if job.Unit == "" {
		job.Unit = "cpu" // the multilingual helper runs on the CPU
	}
	if path := os.Getenv(envLog); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			_ = json.NewEncoder(f).Encode(job)
			_ = f.Close()
		}
	}
	matches := func(env string) bool { v := os.Getenv(env); return v == "any" || v == job.Unit }
	emit(map[string]any{"event": "loaded", "load_s": 0.01, "unit": job.Unit})
	if matches(envSleepUnit) {
		d, _ := time.ParseDuration(os.Getenv(envSleep))
		time.Sleep(d)
	}
	if matches(envFailUnit) {
		emit(map[string]any{"event": "error", "message": "fake " + job.Unit + " failure"})
		return 1
	}
	for i, it := range job.Items {
		if err := os.WriteFile(it.Out, SpeechWAV(it.Text), 0o644); err != nil {
			emit(map[string]any{"event": "error", "message": err.Error()})
			return 1
		}
		emit(map[string]any{"event": "item", "index": i})
	}
	emit(map[string]any{"event": "done"})
	return 0
}

// fakeHFDownload stands in for `uv tool run --from huggingface_hub hf
// download <repo> <files...> --revision <rev>`: with a token it writes the
// files into the cache layout huggingface_hub uses; without one it fails as
// the Hub does for a gated model.
func fakeHFDownload() int {
	if os.Getenv("HF_TOKEN") == "" {
		fmt.Fprintln(os.Stderr, "401 Client Error: gated repo, access restricted")
		return 1
	}
	args := os.Args[slices.Index(os.Args, "download")+1:]
	repo, rev := args[0], ""
	var files []string
	for i := 1; i < len(args); i++ {
		if args[i] == "--revision" {
			rev = args[i+1]
			i++
			continue
		}
		files = append(files, args[i])
	}
	snap := filepath.Join(os.Getenv("HF_HOME"), "hub", "models--"+strings.ReplaceAll(repo, "/", "--"), "snapshots", rev)
	if err := os.MkdirAll(snap, 0o755); err != nil {
		return 1
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(snap, f), []byte("fake "+f), 0o644); err != nil {
			return 1
		}
	}
	return 0
}

// CacheMultilingual puts the pinned multilingual model's files in the
// Hugging Face cache under HF_HOME, as a finished download leaves them.
func CacheMultilingual(t *testing.T) {
	t.Helper()
	t.Setenv("HF_TOKEN", "test-token")
	cmd := exec.Command(filepath.Join(os.Getenv("TTS_AXI_FAKE_BIN"), "uv"), "tool", "run", "--from", "huggingface_hub", "hf", "download",
		engine.MultilingualRepo, "config.json", "tokenizer.json", "model.q8.gguf", "default-voice.safetensors", "--revision", engine.MultilingualRevision)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("caching the model: %v %s", err, out)
	}
	t.Setenv("HF_TOKEN", "")
}

func emit(v map[string]any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}

// SpeechLength is how long the fake speaks a text: 40 ms per character, at
// least 300 ms, without the silence around it.
func SpeechLength(text string) time.Duration {
	return max(300*time.Millisecond, time.Duration(len([]rune(text)))*40*time.Millisecond)
}

// SpeechWAV is a 24 kHz mono 16-bit tone of SpeechLength(text), with 100 ms
// of silence before and 400 ms after, as Phonon leaves around an utterance.
func SpeechWAV(text string) []byte {
	const rate = 24000
	lead, tail := rate/10, rate*4/10
	tone := int(SpeechLength(text).Seconds() * rate)
	pcm := make([]byte, 2*(lead+tone+tail))
	for i := 0; i < tone; i++ {
		v := int16(8000 * math.Sin(2*math.Pi*220*float64(i)/rate))
		binary.LittleEndian.PutUint16(pcm[2*(lead+i):], uint16(v))
	}
	hdr := make([]byte, 44)
	copy(hdr, "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:], uint32(36+len(pcm)))
	copy(hdr[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(hdr[16:], 16)
	binary.LittleEndian.PutUint16(hdr[20:], 1)
	binary.LittleEndian.PutUint16(hdr[22:], 1)
	binary.LittleEndian.PutUint32(hdr[24:], rate)
	binary.LittleEndian.PutUint32(hdr[28:], rate*2)
	binary.LittleEndian.PutUint16(hdr[32:], 2)
	binary.LittleEndian.PutUint16(hdr[34:], 16)
	copy(hdr[36:], "data")
	binary.LittleEndian.PutUint32(hdr[40:], uint32(len(pcm)))
	return append(hdr, pcm...)
}

// Install makes a complete fake installation under home: the helper (the
// test binary under the helper's name, marked as built from the current
// source), an English bundle carrying the real conditions vector, and the
// cached real duration_delta weights, so every speed works offline. It also
// puts a fake uv first on PATH.
func Install(t *testing.T, home string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helperDir := filepath.Join(home, "helper")
	must(t, os.MkdirAll(helperDir, 0o755))
	must(t, os.Symlink(exe, filepath.Join(helperDir, "tts-axi-helper")))
	must(t, os.WriteFile(filepath.Join(helperDir, "main.swift"), engine.SwiftHelper, 0o644))

	bundle := filepath.Join(home, "models", engine.BundleName)
	must(t, os.MkdirAll(filepath.Join(bundle, "flow-c414-t1.mlpackage"), 0o755))
	must(t, os.WriteFile(filepath.Join(bundle, "bundle.json"), []byte(`{"files":[]}`), 0o644))
	must(t, os.WriteFile(filepath.Join(bundle, "host.safetensors"), HostConditions, 0o644))
	w, err := DurationWeights()
	if err != nil {
		t.Fatal(err)
	}
	cache := make([]byte, 4*len(w))
	for i, v := range w {
		binary.LittleEndian.PutUint32(cache[4*i:], math.Float32bits(v))
	}
	must(t, os.WriteFile(filepath.Join(home, "models", engine.BundleName+".duration_delta.f32"), cache, 0o644))

	bin := t.TempDir()
	must(t, os.Symlink(exe, filepath.Join(bin, "uv")))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TTS_AXI_FAKE_BIN", bin)

	// A Hugging Face cache of its own, with the multilingual model in it, and
	// no token: the state after a one-time download.
	t.Setenv("HF_HOME", t.TempDir())
	t.Setenv("HF_HUB_CACHE", "")
	CacheMultilingual(t)
}

// FakeEnv steers the fakes for one test.
func FakeEnv(t *testing.T, failUnit, sleepUnit string, sleep time.Duration) {
	t.Helper()
	t.Setenv(envFailUnit, failUnit)
	t.Setenv(envSleepUnit, sleepUnit)
	t.Setenv(envSleep, sleep.String())
}

// LogJobs makes the fakes record every job they receive, and returns a
// function that reads them back.
func LogJobs(t *testing.T) func() []Job {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jobs.jsonl")
	t.Setenv(envLog, path)
	return func() []Job {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var jobs []Job
		dec := json.NewDecoder(strings.NewReader(string(data)))
		for {
			var j Job
			if err := dec.Decode(&j); err == io.EOF {
				break
			} else if err != nil {
				t.Fatal(err)
			}
			jobs = append(jobs, j)
		}
		return jobs
	}
}

// Tensor reads one tensor of a safetensors file as float32.
func Tensor(file []byte, name string) ([]float32, []int, error) {
	n := binary.LittleEndian.Uint64(file)
	var meta map[string]struct {
		Dtype   string `json:"dtype"`
		Shape   []int  `json:"shape"`
		Offsets []int  `json:"data_offsets"`
	}
	if err := json.Unmarshal(file[8:8+n], &meta); err != nil {
		return nil, nil, err
	}
	m, ok := meta[name]
	if !ok {
		return nil, nil, fmt.Errorf("no tensor %s", name)
	}
	raw := file[8+int(n)+m.Offsets[0] : 8+int(n)+m.Offsets[1]]
	var out []float32
	switch m.Dtype {
	case "BF16":
		for i := 0; i < len(raw); i += 2 {
			out = append(out, math.Float32frombits(uint32(binary.LittleEndian.Uint16(raw[i:]))<<16))
		}
	case "F32":
		for i := 0; i < len(raw); i += 4 {
			out = append(out, math.Float32frombits(binary.LittleEndian.Uint32(raw[i:])))
		}
	default:
		return nil, nil, fmt.Errorf("dtype %s", m.Dtype)
	}
	return out, m.Shape, nil
}

// DurationWeights is the real duration_delta projection, [512 × 128].
func DurationWeights() ([]float32, error) {
	w, _, err := Tensor(Conditioners, "flow_lm.condition_provider.conditioners.duration_delta.output_proj.weight")
	return w, err
}

// Sampler replays memory readings: one per call from Seq, then Rest forever.
type Sampler struct {
	mu    sync.Mutex
	Seq   []guard.Sample
	Rest  guard.Sample
	Calls int
}

func (s *Sampler) Sample() (guard.Sample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls++
	if len(s.Seq) > 0 {
		v := s.Seq[0]
		s.Seq = s.Seq[1:]
		return v, nil
	}
	return s.Rest, nil
}

// Normal is a calm reading: normal pressure, 7 GiB of swap in use.
var Normal = guard.Sample{Pressure: guard.Normal, SwapUsedBytes: 7 << 30}

// Warning is the same machine at warning pressure.
var Warning = guard.Sample{Pressure: guard.Warning, SwapUsedBytes: 7 << 30}

// FastLimits are the real thresholds with a fast sampling interval and a
// short settle time, so stop tests take milliseconds.
var FastLimits = guard.Limits{SwapGrowth: time.Minute, SwapBytes: 256 << 20, Interval: 10 * time.Millisecond, Settle: 2 * time.Second}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
