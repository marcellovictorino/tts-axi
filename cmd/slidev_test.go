package cmd

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcellovictorino/tts-axi/internal/engine"
	"github.com/marcellovictorino/tts-axi/internal/library"
	"github.com/marcellovictorino/tts-axi/internal/slidev"
	"github.com/marcellovictorino/tts-axi/internal/testutil"
)

// sampleDeck copies the public sample deck into the test folder.
func (e *env) sampleDeck() string {
	e.t.Helper()
	dir := filepath.Join(e.dir, "sample")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	for _, f := range []string{"slides.md", "style.css"} {
		data, err := os.ReadFile(filepath.Join("..", "internal", "slidev", "testdata", "sample", f))
		if err != nil {
			e.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	return filepath.Join(dir, "slides.md")
}

// loudness returns the RMS level of audio between from and to seconds.
func loudness(t *testing.T, pcm []int16, from, to float64) float64 {
	t.Helper()
	const rate = 8000
	a, b := max(0, int(from*rate)), min(len(pcm), int(to*rate))
	if b <= a {
		t.Fatalf("window %.2f-%.2f s is outside the audio", from, to)
	}
	var sum float64
	for _, v := range pcm[a:b] {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(b-a))
}

func decode(t *testing.T, path string) []int16 {
	t.Helper()
	raw, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-f", "s16le", "-ac", "1", "-ar", "8000", "-").Output()
	if err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	pcm := make([]int16, len(raw)/2)
	_ = binary.Read(bytes.NewReader(raw), binary.LittleEndian, pcm)
	return pcm
}

// Intent (spec items 2 to 4, and the captain's test ask: a sample deck
// renders to MP4 with cues): deck --video speaks one clip per step, writes
// each slide's start and click cues into a copy of the deck with the
// narration layer, and joins the recording with the narration into an H.264
// MP4 with YouTube chapters and captions. Each cue lands in the pause before
// its click's words.
func TestSlidevDeckRendersToVideoWithCues(t *testing.T) {
	e := newEnv(t)
	testutil.InstallSlidev(t, filepath.Join(e.dir, "tts"))
	deck := e.sampleDeck()
	before, _ := os.ReadFile(deck)
	r := e.run("deck", deck, "--video").ok(t)
	if !strings.Contains(r.out, "clicks: 19") || !strings.Contains(r.out, "video: ") {
		t.Errorf("output:\n%s", r.out)
	}
	after, _ := os.ReadFile(deck)
	if !bytes.Equal(before, after) {
		t.Error("the source deck was changed")
	}
	dir := filepath.Join(e.lib(), e.episodes()[0].ID)
	if ep := e.episodes()[0]; ep.Player != "deck.mp4" {
		t.Errorf("the library links the episode to %q, not the video", ep.Player)
	}
	for _, f := range []string{"slidev/slides.md", "slidev/style.css",
		"slidev/global-top.vue", "slidev/slide-top.vue", "slidev/components/CountUp.vue", "slidev/components/PiTerminal.vue",
		"slidev/public/deck.mp3", "slidev/package.json", "slidev/package-lock.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	work, err := os.ReadDir(filepath.Join(e.dir, "tts", "slidev", engine.SlidevVersion, "work"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(work) != 0 {
		t.Errorf("the recording's work folder was left behind: %v", work)
	}

	// Cues: one per [click], and each fires in the silence before its words.
	src, _ := os.ReadFile(filepath.Join(dir, "slidev", "slides.md"))
	narrated, err := slidev.Parse(string(src))
	if err != nil {
		t.Fatal(err)
	}
	var timings []library.Timing
	data, _ := os.ReadFile(filepath.Join(dir, "timings.json"))
	if err := json.Unmarshal(data, &timings); err != nil {
		t.Fatal(err)
	}
	pcm := decode(t, filepath.Join(dir, "deck.mp3"))
	speech := loudness(t, pcm, timings[0].Start+0.1, timings[0].Start+0.3)
	for i, s := range narrated.Slides {
		start, cues := readCues(t, string(src), i)
		if math.Abs(start-timings[i].Start) > 0.002 { // both rounded to the millisecond
			t.Errorf("slide %d start %.3f, timings say %.3f", i+1, start, timings[i].Start)
		}
		if len(cues) != len(s.Steps)-1 {
			t.Fatalf("slide %d has %d cues for %d steps", i+1, len(cues), len(s.Steps))
		}
		for k, c := range cues {
			at := start + c
			if v := loudness(t, pcm, at-0.1, at+0.5); v > speech/10 {
				t.Errorf("slide %d click %d at %.2f s is not in a pause (level %.0f, speech %.0f)", i+1, k+1, at, v, speech)
			}
			if v := loudness(t, pcm, at+0.75, at+0.95); v < speech/2 {
				t.Errorf("slide %d click %d at %.2f s is not followed by its words (level %.0f)", i+1, k+1, at, v)
			}
			if v := loudness(t, pcm, at-0.4, at-0.2); v < speech/2 {
				t.Errorf("slide %d click %d at %.2f s does not follow the step before it (level %.0f)", i+1, k+1, at, v)
			}
		}
	}

	// The video: H.264 picture, AAC sound, as long as the narration, with
	// the deck's chapters.
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name,pix_fmt", "-of", "csv=p=0", filepath.Join(dir, "deck.mp4")).Output()
	if err != nil {
		t.Fatal(err)
	}
	if s := string(out); !strings.Contains(s, "h264,yuv420p") || !strings.Contains(s, "aac") {
		t.Errorf("deck.mp4 streams:\n%s", s)
	}
	audioLen, _ := probe(t, filepath.Join(dir, "deck.mp3"))
	videoLen, chapters := probe(t, filepath.Join(dir, "deck.mp4"))
	if math.Abs(videoLen-audioLen) > 0.3 {
		t.Errorf("deck.mp4 is %.2f s, the narration %.2f s", videoLen, audioLen)
	}
	if len(chapters) != 6 || chapters[1] != "How it works" {
		t.Errorf("deck.mp4 chapters %v", chapters)
	}
	ch, _ := os.ReadFile(filepath.Join(dir, "chapters.txt"))
	if lines := strings.Split(strings.TrimSpace(string(ch)), "\n"); len(lines) != 6 || !strings.HasPrefix(lines[0], "0:00 ") || !strings.HasSuffix(lines[1], " How it works") {
		t.Errorf("chapters.txt:\n%s", ch)
	}
	srt, _ := os.ReadFile(filepath.Join(dir, "captions.srt"))
	if !strings.Contains(string(srt), "Here is how it works.") || !strings.HasPrefix(string(srt), "1\n00:00:00,") {
		t.Errorf("captions.srt:\n%.300s", srt)
	}
}

// readCues reads slide i's start and cues from the narrated deck's
// frontmatter, the way the narration player does.
func readCues(t *testing.T, src string, i int) (float64, []float64) {
	t.Helper()
	var start float64
	var cues []float64
	n := -1
	for _, l := range strings.Split(src, "\n") {
		if strings.HasPrefix(l, "start: ") {
			n++
			if n == i {
				_ = json.Unmarshal([]byte(strings.TrimPrefix(l, "start: ")), &start)
			}
		}
		if n == i && strings.HasPrefix(l, "cues: ") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(l, "cues: ")), &cues); err != nil {
				t.Fatal(err)
			}
			return start, cues
		}
	}
	t.Fatalf("slide %d has no cues", i+1)
	return 0, nil
}

// Intent (spec items 5 and 6): --slidev refuses what it cannot narrate or
// record before speaking a word: a deck without notes, a theme Slidev was
// not installed with, files that would hide tts-axi's layer, a view
// transition (it freezes the recording) and a missing Slidev install.
func TestSlidevDeckRefusals(t *testing.T) {
	e := newEnv(t)
	plain := e.write("plain.md", "# Hello\n\nNo notes.\n")
	e.run("deck", plain, "--slidev").fails(t, 2, "no presenter notes found")
	theme := e.write("theme.md", "---\ntheme: apple-basic\n---\n\n# Hi\n\n<!-- Hello. -->\n")
	e.run("deck", theme, "--slidev").fails(t, 2, `theme "apple-basic" is not installed`)
	vt := e.write("vt.md", "---\ntransition: view-transition\n---\n\n# Hi\n\n<!-- Hello. -->\n")
	e.run("deck", vt, "--video").fails(t, 2, "view-transition freezes the recording")
	ok := e.write("ok.md", "# Hi\n\n<!-- Hello. -->\n")
	e.run("deck", ok, "--video").fails(t, 1, "doctor --fix")
	e.write("global-top.vue", "<template />\n")
	e.run("deck", ok, "--slidev").fails(t, 2, "global-top.vue")
	if len(e.episodes()) != 0 {
		t.Error("a refused deck reached the library")
	}
}

// Intent (spec item 5: doctor --fix installs Slidev and Chrome for Testing
// into tts-axi's own folder, from pinned downloads): before the fix doctor
// warns and --video refuses; after it doctor reports both and the Chrome
// archive is unpacked where the recorder looks for it.
func TestDoctorFixInstallsSlidevAndChrome(t *testing.T) {
	e := newEnv(t)
	if r := e.run("doctor"); !strings.Contains(r.out, `slidev video,warn,"Slidev and Chrome for Testing are not installed`) {
		t.Errorf("doctor:\n%s", r.out)
	}
	e.run("doctor", "--fix").ok(t)
	if r := e.run("doctor"); !strings.Contains(r.out, "slidev video,ok,Slidev 53.1.0 and Chrome for Testing") {
		t.Errorf("doctor after the fix:\n%s", r.out)
	}
}

// Intent (branch review): Slidev needs a recent Node.js, so --video refuses
// an older one with what to do, before speaking a word.
func TestSlidevVideoRefusesOldNode(t *testing.T) {
	e := newEnv(t)
	testutil.InstallSlidev(t, filepath.Join(e.dir, "tts"))
	testutil.SetNodeVersion(t, "20.11.0")
	e.run("deck", e.sampleDeck(), "--video").fails(t, 1, "brew upgrade node")
	if len(e.episodes()) != 0 {
		t.Error("a refused deck reached the library")
	}
}
