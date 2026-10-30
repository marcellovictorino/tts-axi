package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcellovictorino/tts-axi/internal/engine"
	"github.com/marcellovictorino/tts-axi/internal/guard"
	"github.com/marcellovictorino/tts-axi/internal/library"
	"github.com/marcellovictorino/tts-axi/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.MaybeRunFake() // acts as the Swift helper or uv when started under those names
	os.Exit(m.Run())
}

// env is an isolated tts-axi installation: every path tts-axi uses lives
// under one temp dir, so tests never touch the real ~/.tts-axi, ~/Music or
// the agents' skill folders.
type env struct {
	t        *testing.T
	dir      string
	sampler  *testutil.Sampler
	now      time.Time
	registry engine.Registry
	qwen     engine.QwenSource
}

func newEnv(t *testing.T) *env {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed (brew install ffmpeg); CI installs it")
	}
	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r // macOS temp dirs sit behind a /var -> /private/var symlink
	}
	e := &env{t: t, dir: dir, sampler: &testutil.Sampler{Rest: testutil.Normal},
		now: time.Date(2026, 10, 22, 7, 0, 0, 0, time.UTC)}
	t.Setenv("HOME", filepath.Join(dir, "home"))
	t.Setenv("TTS_AXI_HOME", filepath.Join(dir, "tts"))
	t.Setenv("TTS_AXI_LIBRARY", filepath.Join(dir, "lib"))
	t.Setenv("TTS_AXI_BASE_URL", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("NO_COLOR", "1")
	testutil.Install(t, filepath.Join(dir, "tts"))
	e.registry = testutil.Registry(t)
	e.qwen = testutil.QwenSource(t)
	return e
}

func (e *env) lib() string { return filepath.Join(e.dir, "lib") }

type result struct {
	out, err string
	code     int
}

func (e *env) run(args ...string) result {
	e.t.Helper()
	var out, errOut bytes.Buffer
	deps := Deps{Out: &out, Err: &errOut, In: strings.NewReader("Bom dia a todos."), Sampler: e.sampler,
		Limits: testutil.FastLimits, HTTP: http.DefaultClient, Now: func() time.Time { return e.now }, Registry: e.registry, Qwen: e.qwen}
	code := Run(context.Background(), deps, args)
	norm := func(s string) string { return strings.ReplaceAll(s, e.dir, "$TMP") }
	return result{out: norm(out.String()), err: norm(errOut.String()), code: code}
}

func (r result) ok(t *testing.T) result {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", r.code, r.out, r.err)
	}
	return r
}

func (r result) fails(t *testing.T, code int, contains string) result {
	t.Helper()
	if r.code != code || !strings.Contains(r.out+r.err, contains) {
		t.Fatalf("want exit %d containing %q, got exit %d\nstdout:\n%s\nstderr:\n%s", code, contains, r.code, r.out, r.err)
	}
	return r
}

func (e *env) episodes() []library.Episode {
	e.t.Helper()
	eps, err := library.Library{Dir: e.lib()}.List()
	if err != nil {
		e.t.Fatal(err)
	}
	return eps
}

func (e *env) write(name, text string) string {
	e.t.Helper()
	p := filepath.Join(e.dir, name)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

// probe reads a media file's duration and chapters with ffprobe.
func probe(t *testing.T, path string) (seconds float64, chapters []string) {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-show_chapters", "-show_format", "-of", "json", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	var p struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Chapters []struct {
			Tags struct {
				Title string `json:"title"`
			} `json:"tags"`
		} `json:"chapters"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatal(err)
	}
	var s float64
	_, _ = fmt.Sscan(p.Format.Duration, &s)
	for _, c := range p.Chapters {
		chapters = append(chapters, c.Tags.Title)
	}
	return s, chapters
}

// Intent (spec: say turns text into one audio file in the library, and every
// result names its engine): an agent's say lands as an episode with its
// transcript, listed on the index page and in the feed.
func TestSayPublishesAnEpisode(t *testing.T) {
	e := newEnv(t)
	r := e.run("say", "Good morning. Here is your briefing for today.").ok(t)
	want := `episode: 2026-10-22-good-morning-here-is-your-briefing-for-today
path: $TMP/lib/2026-10-22-good-morning-here-is-your-briefing-for-today/episode.mp3
title: Good morning. Here is your briefing for today
`
	if !strings.HasPrefix(r.out, want) || !strings.Contains(r.out, "\nengine: ane\nvoices[1]: Hugh\nhelp[1]: open $TMP/lib/index.html") {
		t.Errorf("unexpected output:\n%s", r.out)
	}
	eps := e.episodes()
	if len(eps) != 1 || eps[0].Kind != "say" || eps[0].Engines[0] != "ane" {
		t.Fatalf("episodes %+v", eps)
	}
	dir := filepath.Join(e.lib(), eps[0].ID)
	secs, _ := probe(t, filepath.Join(dir, "episode.mp3"))
	if secs < 1.5 || secs > 3.5 {
		t.Errorf("episode is %.2fs; the fake speaks about 1.9s for this text", secs)
	}
	transcript, _ := os.ReadFile(filepath.Join(dir, "transcript.md"))
	if !strings.Contains(string(transcript), "**Hugh:** Good morning. Here is your briefing for today.") {
		t.Errorf("transcript:\n%s", transcript)
	}
	index, _ := os.ReadFile(filepath.Join(e.lib(), "index.html"))
	if !strings.Contains(string(index), eps[0].ID+"/episode.mp3") {
		t.Error("index.html does not link the episode")
	}
	var feed struct {
		Items []struct {
			Enclosure struct {
				URL    string `xml:"url,attr"`
				Length int64  `xml:"length,attr"`
			} `xml:"enclosure"`
		} `xml:"channel>item"`
	}
	data, _ := os.ReadFile(filepath.Join(e.lib(), "feed.xml"))
	if err := xml.Unmarshal(data, &feed); err != nil {
		t.Fatal(err)
	}
	if len(feed.Items) != 1 || feed.Items[0].Enclosure.Length == 0 || !strings.HasSuffix(feed.Items[0].Enclosure.URL, "/episode.mp3") {
		t.Errorf("feed items %+v", feed.Items)
	}
}

// Intent (spec: French and Portuguese speak through the multilingual model,
// with Davi as the Portuguese default voice): text from stdin with --lang pt.
func TestSayPortugueseFromStdin(t *testing.T) {
	e := newEnv(t)
	jobs := testutil.LogJobs(t)
	r := e.run("say", "-", "--lang", "pt", "--out", filepath.Join(e.dir, "bom-dia.m4a")).ok(t)
	if !strings.Contains(r.out, "path: $TMP/bom-dia.m4a") || !strings.Contains(r.out, "engine: ptts-cpu") || !strings.Contains(r.out, "voices[1]: Davi") {
		t.Errorf("output:\n%s", r.out)
	}
	if j := jobs(); len(j) != 1 || j[0].Lang != "pt" || j[0].Items[0].Voice != "Davi" || j[0].Items[0].Text != "Bom dia a todos." {
		t.Errorf("jobs %+v", j)
	}
	if len(e.episodes()) != 0 {
		t.Error("--out should not add a library episode")
	}
	if _, err := os.Stat(filepath.Join(e.dir, "bom-dia.m4a")); err != nil {
		t.Error(err)
	}
}

// Intent (spec: render makes one file with chapter marks and real-silence
// pauses from a multi-voice script).
func TestRenderChaptersVoicesAndPauses(t *testing.T) {
	e := newEnv(t)
	jobs := testutil.LogJobs(t)
	script := e.write("day.md", `---
title: Your day ahead
voices: {Guest: Bryony}
---
## Calendar
Host: Morning.
[pause 2s]
Guest: First, the review.

## Weather
Host: Rain later.
Davi: Bom dia.
`)
	r := e.run("render", script).ok(t)
	if !strings.Contains(r.out, "chapters[2]: Calendar,Weather") || !strings.Contains(r.out, "voices[3]: Hugh,Bryony,Davi") || !strings.Contains(r.out, "engine: \"ane,ptts-cpu\"") {
		t.Errorf("output:\n%s", r.out)
	}
	j := jobs()
	if len(j) != 2 || j[0].Unit != "ane" || len(j[0].Items) != 3 || j[1].Helper != "uv" || j[1].Lang != "pt" {
		t.Errorf("want one English and one Portuguese helper run, got %+v", j)
	}
	eps := e.episodes()
	secs, chapters := probe(t, filepath.Join(e.lib(), eps[0].ID, "episode.mp3"))
	if len(chapters) != 2 || chapters[0] != "Calendar" || chapters[1] != "Weather" {
		t.Errorf("chapters in the file: %v", chapters)
	}
	// Speech (trimmed to 60 ms of silence each side) plus the gaps and the 2 s pause.
	speech := testutil.SpeechLength("Morning.") + testutil.SpeechLength("First, the review.") +
		testutil.SpeechLength("Rain later.") + testutil.SpeechLength("Bom dia.") + 8*clipPad
	want := speech + 2*time.Second + gapNewChapter + gapNewSpeaker
	if d := secs - want.Seconds(); d < -0.15 || d > 0.15 {
		t.Errorf("episode is %.2fs, want about %.2fs", secs, want.Seconds())
	}
}

// Intent (spec: deck makes one clip per slide from speaker notes, plus
// timings.json and an auto-advancing player.html).
func TestDeckMakesClipsTimingsAndPlayer(t *testing.T) {
	e := newEnv(t)
	deck := e.write("talk.md", `# Hello

<!-- Welcome to the talk. -->

---

## No notes here

---

## Thanks

<!-- Thank you for listening. -->
`)
	r := e.run("deck", deck, "--silent", "2s").ok(t)
	if !strings.Contains(r.out, "slides: 3") {
		t.Errorf("output:\n%s", r.out)
	}
	dir := filepath.Join(e.lib(), e.episodes()[0].ID)
	for _, f := range []string{"deck.mp3", "slides/slide-01.mp3", "slides/slide-03.mp3", "player.html", "timings.json", "transcript.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "slides/slide-02.mp3")); err == nil {
		t.Error("a slide without notes got a clip")
	}
	var timings []library.Timing
	data, _ := os.ReadFile(filepath.Join(dir, "timings.json"))
	if err := json.Unmarshal(data, &timings); err != nil {
		t.Fatal(err)
	}
	if len(timings) != 3 || timings[1].Audio != "" || timings[1].Seconds != 2.6 || timings[2].Start != timings[1].Start+timings[1].Seconds {
		t.Errorf("timings %+v", timings)
	}
	_, chapters := probe(t, filepath.Join(dir, "deck.mp3"))
	if len(chapters) != 3 || chapters[2] != "Thanks" {
		t.Errorf("deck chapters %v", chapters)
	}
	player, _ := os.ReadFile(filepath.Join(dir, "player.html"))
	if !strings.Contains(string(player), `"audio":"slides/slide-01.mp3"`) {
		t.Error("player.html does not carry the slide clips")
	}
}

// Intent (agreed memory stops): when both attempts are stopped, the render
// fails, says why, and nothing reaches the library.
func TestStoppedRenderWritesNothing(t *testing.T) {
	e := newEnv(t)
	testutil.FakeEnv(t, "", "any", 5*time.Second)
	e.sampler.Seq = []guard.Sample{testutil.Normal, testutil.Warning, testutil.Normal, testutil.Warning}
	r := e.run("say", "Hello there.").fails(t, 1, "stopped to protect memory")
	if !strings.Contains(r.out, "help[2]: nothing was written") {
		t.Errorf("output:\n%s", r.out)
	}
	if len(e.episodes()) != 0 {
		t.Error("a stopped render reached the library")
	}
	work, _ := filepath.Glob(filepath.Join(e.dir, "tts", "run", "work-*"))
	if len(work) != 0 {
		t.Errorf("staging folders left behind: %v", work)
	}
}

// Intent (axi: exit code 2 for usage errors, with the valid choices listed).
func TestUsageErrors(t *testing.T) {
	e := newEnv(t)
	e.run("say", "Hi", "--lang", "de").fails(t, 2, "valid languages: en, pt, fr")
	e.run("say", "Hi", "--voice", "Nobody").fails(t, 2, "unknown voice")
	e.run("say", "Hi", "--voice", "Davi", "--lang", "en").fails(t, 2, "Davi is a pt voice, not en")
	e.run("say", "Hi", "--out", "x.ogg").fails(t, 2, "end --out with .mp3, .m4a or .wav")
	e.run("say", "Hi", "--bogus").fails(t, 2, "valid flags:")
	e.run("speak").fails(t, 2, "valid commands:")
	script := e.write("three.md", "A: one.\nB: two.\nC: three.\n")
	e.run("render", script).fails(t, 2, `speaker "C" has no voice`)
}

// Intent (spec: one render at a time by file lock): a second render waits,
// and gives up with a clear error after --wait.
func TestOneRenderAtATime(t *testing.T) {
	e := newEnv(t)
	testutil.FakeEnv(t, "", "any", 3*time.Second)
	done := make(chan result)
	go func() { done <- e.run("say", "First.") }()
	time.Sleep(500 * time.Millisecond)
	e.run("say", "Second.", "--wait", "200ms").fails(t, 1, "another render is still running")
	(<-done).ok(t)
}

// Intent (spec: the library lists episodes and rm deletes one and refreshes
// the page).
func TestLibraryListAndRemove(t *testing.T) {
	e := newEnv(t)
	e.run("library").ok(t)
	e.run("say", "One.").ok(t)
	e.now = e.now.Add(time.Hour)
	e.run("say", "Two.").ok(t)
	r := e.run("library").ok(t)
	if !strings.Contains(r.out, "episodes[2]{episode,title,kind,length,model,engine}:\n  2026-10-22-two,Two,say,") {
		t.Errorf("newest first:\n%s", r.out)
	}
	e.run("library", "rm", "2026-10-22-one").ok(t)
	if eps := e.episodes(); len(eps) != 1 || eps[0].ID != "2026-10-22-two" {
		t.Errorf("after rm: %+v", eps)
	}
	index, _ := os.ReadFile(filepath.Join(e.lib(), "index.html"))
	if strings.Contains(string(index), "2026-10-22-one") {
		t.Error("index.html still lists the removed episode")
	}
	e.run("library", "rm", "../etc").fails(t, 1, "not an episode id")
}

// Intent (spec: the skill is installed for Claude, Codex and Pi).
func TestSkillInstall(t *testing.T) {
	e := newEnv(t)
	r := e.run("skill", "install").ok(t)
	for _, p := range []string{"home/.claude/skills/tts-axi/SKILL.md", "home/.agents/skills/tts-axi/SKILL.md", "home/.pi/agent/skills/tts-axi/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(e.dir, p)); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	if !strings.Contains(r.out, "claude,installed") {
		t.Errorf("output:\n%s", r.out)
	}
	if r := e.run("skill", "install").ok(t); strings.Count(r.out, "already installed") != 3 {
		t.Errorf("second install should be a no-op:\n%s", r.out)
	}
}

// Intent (spec: doctor checks the installation; exit 1 when something fails).
func TestDoctor(t *testing.T) {
	e := newEnv(t)
	r := e.run("doctor").ok(t)
	for _, want := range []string{"helper,ok", "english model,ok", "speed,warn,duration_delta 0.15 is prepared on first use", `memory,ok,"pressure normal`} {
		if !strings.Contains(r.out, want) {
			t.Errorf("missing %q in:\n%s", want, r.out)
		}
	}
	e.run("say", "Hello.").ok(t)
	if r := e.run("doctor").ok(t); !strings.Contains(r.out, "speed,ok,duration_delta 0.15") {
		t.Errorf("the first render should prepare the speed:\n%s", r.out)
	}
	if err := os.RemoveAll(filepath.Join(e.dir, "tts", "models")); err != nil {
		t.Fatal(err)
	}
	e.run("doctor").fails(t, 1, "english model,fail")
}

// Intent (axi: TOON when piped, tables for a person).
func TestVoicesListsDefaultsFirst(t *testing.T) {
	e := newEnv(t)
	r := e.run("voices").ok(t)
	if !strings.HasPrefix(r.out, "voices[64]{name,lang,default,model,engine,description}:\n  Hugh,en,default,phonon,ane,") {
		t.Errorf("output starts:\n%.200s", r.out)
	}
	lines := strings.Split(r.out, "\n")
	firstEight := strings.Join(lines[1:9], "\n")
	for _, n := range []string{"Bryony,en,speaker 2", "Davi,pt,default", "Débora,pt,speaker 4", "Augustin,fr,default", "Coralie,fr,speaker 2"} {
		if !strings.Contains(firstEight, n) {
			t.Errorf("%q not among the first eight rows:\n%s", n, firstEight)
		}
	}
}

// Intent (owner request: download the Portuguese and French weights by the
// Ollama pull route, without the Ollama engine, so users need no Hugging Face
// account or key): without the model, Portuguese fails with the one command
// that fixes it; doctor --fix downloads it with no token in the environment;
// then Portuguese speaks from the downloaded folder.
func TestPortugueseNeedsNoAccount(t *testing.T) {
	e := newEnv(t)
	t.Setenv("HF_TOKEN", "")
	t.Setenv("HF_HOME", t.TempDir()) // no Hugging Face sign-in either
	folder := filepath.Join(e.dir, "tts", "models", engine.MultilingualName)
	if err := os.RemoveAll(folder); err != nil {
		t.Fatal(err)
	}
	r := e.run("say", "Bom dia.", "--lang", "pt").fails(t, 1, "French and Portuguese model is not downloaded")
	if !strings.Contains(r.out, "tts-axi doctor --fix") || strings.Contains(r.out, "Hugging Face") || len(e.episodes()) != 0 {
		t.Errorf("output:\n%s", r.out)
	}
	if r := e.run("doctor"); !strings.Contains(r.out, "multilingual model,warn,") {
		t.Errorf("doctor:\n%s", r.out)
	}

	e.run("doctor", "--fix").ok(t)

	if r := e.run("doctor"); !strings.Contains(r.out, "multilingual model,ok,") {
		t.Errorf("doctor after the download:\n%s", r.out)
	}
	jobs := testutil.LogJobs(t)
	e.run("say", "Bom dia.", "--lang", "pt").ok(t)
	if j := jobs(); len(j) != 1 || j[0].Model != folder {
		t.Errorf("want the downloaded folder, got %+v", j)
	}
}

// Intent (owner request: shell completion): voices, languages and
// episode ids complete.
func TestCompletion(t *testing.T) {
	e := newEnv(t)
	if r := e.run("__complete", "say", "--voice", ""); !strings.Contains(r.out, "Débora\tpt\n") || !strings.Contains(r.out, "Hugh\ten\n") {
		t.Errorf("voices:\n%s", r.out)
	}
	if r := e.run("__complete", "deck", "x.md", "--lang", ""); !strings.HasPrefix(r.out, "en\npt\nfr\n") {
		t.Errorf("languages:\n%s", r.out)
	}
	e.run("say", "One.").ok(t)
	if r := e.run("__complete", "library", "rm", ""); !strings.Contains(r.out, "2026-10-22-one\tOne\n") {
		t.Errorf("episodes:\n%s", r.out)
	}
	if r := e.run("completion", "zsh").ok(t); !strings.HasPrefix(r.out, "#compdef tts-axi") {
		t.Errorf("zsh script starts:\n%.100s", r.out)
	}
}
