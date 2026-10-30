package cmd

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcellovictorino/tts-axi/internal/config"
	"github.com/marcellovictorino/tts-axi/internal/engine"
	"github.com/marcellovictorino/tts-axi/internal/testutil"
)

// words is a clip's text as a person would save it: wrapped lines, double
// quote marks, an apostrophe that belongs to a word.
const words = "It is not Shakespeare.\nWhen to say \"yes\", when to say \"no\".\nDon't forget."

// clip makes a 48 kHz stereo recording, so adding it proves the conversion
// to the 24 kHz mono reference. It is a synthetic tone: no test ships anyone's
// real voice.
func (e *env) clip() string {
	e.t.Helper()
	p := filepath.Join(e.dir, "clip.wav")
	out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=220:duration=2:sample_rate=48000", "-ac", "2", p).CombinedOutput()
	if err != nil {
		e.t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	return p
}

// addVoice stores the voice "mf" from the synthetic clip.
func (e *env) addVoice() {
	e.t.Helper()
	e.run("voice", "add", "mf", "--clip", e.clip(), "--text", words).ok(e.t)
}

// installQwen downloads the stand-in cloned-voice model, as doctor --fix does.
func (e *env) installQwen() {
	e.t.Helper()
	eng := engine.Engine{Paths: config.Paths{Home: filepath.Join(e.dir, "tts")}, HTTP: http.DefaultClient, Qwen: e.qwen}
	if err := eng.InstallQwen(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

// Intent (accepted spec: `voice add <Name> --clip --text|--text-file` converts
// the clip to 24 kHz mono WAV and stores it with its words in
// ~/.tts-axi/voices/<Name>, outside the repository and the library; the quote
// marks go and lines join with single spaces).
func TestVoiceAddStoresAConvertedClipOutsideTheLibrary(t *testing.T) {
	e := newEnv(t)
	e.addVoice()
	dir := filepath.Join(e.dir, "tts", "voices", "mf")
	text, err := os.ReadFile(filepath.Join(dir, "text.txt"))
	if err != nil || string(text) != "It is not Shakespeare. When to say yes, when to say no. Don't forget.\n" {
		t.Errorf("stored words %q, err %v", text, err)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=sample_rate,channels,codec_name", "-of", "csv=p=0", filepath.Join(dir, "reference.wav")).Output()
	if err != nil || strings.TrimSpace(string(out)) != "pcm_s16le,24000,1" {
		t.Errorf("reference is %q, err %v", out, err)
	}
	if len(e.episodes()) != 0 {
		t.Error("adding a voice put something in the library")
	}
	words2 := e.write("words.md", "one\ntwo\n")
	e.run("voice", "add", "ana", "--clip", e.clip(), "--text-file", words2).ok(t)
	if b, _ := os.ReadFile(filepath.Join(e.dir, "tts", "voices", "ana", "text.txt")); string(b) != "one two\n" {
		t.Errorf("--text-file stored %q", b)
	}
}

// Intent (usage mistakes are exit 2 with the reason): a clone cannot take a
// built-in voice's name, repeat a name, or go without its words.
func TestVoiceAddRefusesMistakes(t *testing.T) {
	e := newEnv(t)
	clip := e.clip()
	e.run("voice", "add", "Hugh", "--clip", clip, "--text", "x").fails(t, 2, `a voice named "Hugh" already exists`)
	e.run("voice", "add", "my voice", "--clip", clip, "--text", "x").fails(t, 2, "not a valid voice name")
	e.run("voice", "add", "mf", "--clip", clip).fails(t, 2, "exactly one of --text and --text-file")
	e.run("voice", "add", "mf", "--text", "x").fails(t, 2, "--clip is required")
	e.addVoice()
	e.run("voice", "add", "mf", "--clip", clip, "--text", "x").fails(t, 2, `a voice named "mf" already exists`)
}

// Intent (owner: the cloned voice is just another option for the CLI, with the
// model easy to tell apart): voices lists it with model qwen beside Phonon's
// built-ins, and it completes like any voice.
func TestClonedVoiceIsListedAndCompleted(t *testing.T) {
	e := newEnv(t)
	e.addVoice()
	r := e.run("voices").ok(t)
	if !strings.Contains(r.out, "voices[65]{name,lang,default,model,engine,description}:") ||
		!strings.Contains(r.out, "\n  mf,en,\"\",qwen,qwen-mlx,") && !strings.Contains(r.out, "\n  mf,en,,qwen,qwen-mlx,") {
		t.Errorf("voices:\n%s", r.out)
	}
	if !strings.Contains(r.out, "Hugh,en,default,phonon,ane,") {
		t.Errorf("built-in voices must stay on phonon:\n%s", r.out)
	}
	if c := e.run("__complete", "say", "--voice", ""); !strings.Contains(c.out, "mf\tcloned\n") {
		t.Errorf("completion:\n%s", c.out)
	}
}

// Intent (accepted spec: --voice mf routes to the Qwen helper with its clip
// and words, offline, and says which model spoke; built-in voices stay on
// Phonon and never reach it).
func TestSayRoutesClonedAndBuiltInVoicesToTheirModels(t *testing.T) {
	e := newEnv(t)
	e.addVoice()
	e.installQwen()
	jobs := testutil.LogJobs(t)

	r := e.run("say", "Good morning.", "--voice", "mf").ok(t)
	for _, want := range []string{"model: qwen", "engine: qwen-mlx", "voices[1]: mf"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("missing %q in:\n%s", want, r.out)
		}
	}
	j := jobs()
	ref := filepath.Join(e.dir, "tts", "voices", "mf", "reference.wav")
	if len(j) != 1 || filepath.Base(j[0].Model) != engine.QwenName || !j[0].Offline ||
		j[0].Items[0].RefAudio != ref || j[0].Items[0].RefText != "It is not Shakespeare. When to say yes, when to say no. Don't forget." {
		t.Fatalf("cloned job: %+v", j)
	}
	if eps := e.episodes(); len(eps) != 1 || eps[0].Models[0] != "qwen" || eps[0].Engines[0] != "qwen-mlx" {
		t.Errorf("episode: %+v", eps)
	}

	r = e.run("say", "Good morning.").ok(t)
	if !strings.Contains(r.out, "model: phonon") || !strings.Contains(r.out, "engine: ane") {
		t.Errorf("a built-in voice must stay on phonon:\n%s", r.out)
	}
	if j := jobs(); len(j) != 2 || filepath.Base(j[1].Model) == engine.QwenName || j[1].Offline || j[1].Items[0].RefAudio != "" {
		t.Errorf("the built-in voice reached the Qwen helper: %+v", j)
	}
}

// Intent (accepted spec: render and deck take --voice mf too): a script
// can mix a built-in speaker and the clone, which run as two helper jobs, and
// a deck narrated by the clone speaks every slide with Qwen.
func TestRenderAndDeckUseTheClone(t *testing.T) {
	e := newEnv(t)
	e.addVoice()
	e.installQwen()
	jobs := testutil.LogJobs(t)

	script := e.write("duo.md", "---\nvoices: {Host: mf}\n---\nHugh: Morning.\n\nHost: Hello there.\n")
	r := e.run("render", script).ok(t)
	if !strings.Contains(r.out, `model: "phonon,qwen"`) {
		t.Errorf("render:\n%s", r.out)
	}
	j := jobs()
	if len(j) != 2 || filepath.Base(j[0].Model) == engine.QwenName || filepath.Base(j[1].Model) != engine.QwenName ||
		j[0].Items[0].Voice != "Hugh" || j[1].Items[0].Voice != "mf" {
		t.Fatalf("render jobs: %+v", j)
	}

	// A bare --voice is the narrator's voice.
	solo := e.write("solo.md", "No speaker here.\n")
	e.run("render", solo, "--voice", "mf").ok(t)
	if j := jobs(); len(j) != 3 || j[2].Items[0].Voice != "mf" {
		t.Fatalf("narrator job: %+v", j)
	}

	deck := e.write("talk.md", "# One\n<!-- First slide. -->\n\n---\n\n# Two\n<!-- Second slide. -->\n")
	r = e.run("deck", deck, "--voice", "mf").ok(t)
	if !strings.Contains(r.out, "model: qwen") {
		t.Errorf("deck:\n%s", r.out)
	}
	if j := jobs(); len(j) != 4 || len(j[3].Items) != 2 || j[3].Items[1].RefAudio == "" {
		t.Errorf("deck job: %+v", j[3:])
	}
}

// Intent (accepted spec: the model comes from doctor --fix or the first use,
// pinned by size and SHA-256; owner: it is available independent of any
// clone): doctor reports it missing and --fix downloads it with no voice
// added; without it, a cloned render downloads it first.
func TestQwenModelComesFromDoctorOrFirstUse(t *testing.T) {
	e := newEnv(t)
	if r := e.run("doctor").ok(t); !strings.Contains(r.out, "cloned-voice model,warn,") {
		t.Errorf("doctor:\n%s", r.out)
	}
	e.run("doctor", "--fix").ok(t)
	if r := e.run("doctor").ok(t); !strings.Contains(r.out, "cloned-voice model,ok,") {
		t.Errorf("doctor after --fix:\n%s", r.out)
	}

	e2 := newEnv(t)
	e2.addVoice()
	e2.run("say", "Hello.", "--voice", "mf").ok(t)
	if r := e2.run("doctor").ok(t); !strings.Contains(r.out, "cloned voices: 1") {
		t.Errorf("first use should have downloaded the model:\n%s", r.out)
	}
}

// Intent (AGENTS.md: downloads are pinned by size and SHA-256): a file that
// does not match its pin is refused and the model is not marked complete.
func TestQwenDownloadMustMatchItsPin(t *testing.T) {
	e := newEnv(t)
	e.qwen.Files[0].SHA256 = strings.Repeat("0", 64)
	e.run("doctor", "--fix").fails(t, 1, "checksum mismatch")
	if r := e.run("doctor"); !strings.Contains(r.out, "cloned-voice model,warn,") {
		t.Errorf("a refused download counted as installed:\n%s", r.out)
	}
}
