package cmd

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcellovictorino/tts-axi/internal/engine"
	"github.com/marcellovictorino/tts-axi/internal/guard"
	"github.com/marcellovictorino/tts-axi/internal/library"
	"github.com/marcellovictorino/tts-axi/internal/output"
	"github.com/marcellovictorino/tts-axi/internal/script"
	"github.com/marcellovictorino/tts-axi/internal/slidev"
	"github.com/marcellovictorino/tts-axi/internal/voices"
)

// Click timing. Before each click's words tts-axi inserts clickPause of
// silence; the click fires clickLead into it, so the animation settles while
// the voice is quiet and the words follow it, unhurried.
const (
	clickPause = 700 * time.Millisecond
	clickLead  = 100 * time.Millisecond
)

// slidevThemes are the themes installed with Slidev; a deck may also name a
// theme folder by path.
var slidevThemes = []string{"default", "seriph", "none"}

// parseSlidev reads a Slidev deck and checks it can be narrated (and, with
// --video, recorded) before anything is spoken. It returns the deck and the
// narrated slides as a script.Deck for the shared deck pipeline.
func parseSlidev(a *App, src, path string, video bool) (slidev.Deck, script.Deck, error) {
	sd, err := slidev.Parse(src)
	if err != nil {
		return sd, script.Deck{}, output.Usage([]string{"see the Slidev deck section of `tts-axi skill install`'s reference, references/slidev.md"}, "%v", err)
	}
	if c := slidev.LayerConflicts(filepath.Dir(path)); len(c) > 0 {
		return sd, script.Deck{}, output.Usage([]string{"rename or remove them: tts-axi writes its own narration player, laser and components"},
			"the deck already has %s", strings.Join(c, ", "))
	}
	theme := sd.Theme
	if t := filepath.ToSlash(filepath.Clean(theme)); strings.HasPrefix(theme, "/") || t == ".." || strings.HasPrefix(t, "../") {
		return sd, script.Deck{}, output.Usage([]string{"move the theme folder into the deck's folder and name it ./" + filepath.Base(theme)},
			"theme %q is outside the deck's folder, so the narrated copy cannot carry it", theme)
	}
	if strings.HasPrefix(theme, "./") {
		// A local theme that links elsewhere could not be copied either.
		root, err1 := filepath.EvalSymlinks(filepath.Dir(path))
		real, err2 := filepath.EvalSymlinks(filepath.Join(filepath.Dir(path), theme))
		if err1 != nil || err2 != nil {
			return sd, script.Deck{}, output.Usage([]string{"put the theme folder beside slides.md, or use an installed theme"}, "theme folder %s is not in the deck's folder", theme)
		}
		if r, err := filepath.Rel(root, real); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return sd, script.Deck{}, output.Usage([]string{"copy the theme folder into the deck's folder instead of linking it"}, "theme %s links outside the deck's folder", theme)
		}
	}
	if theme != "" && !strings.HasPrefix(theme, ".") && !strings.HasPrefix(theme, "/") && !contains(slidevThemes, theme) {
		return sd, script.Deck{}, output.Usage([]string{"installed themes: " + strings.Join(slidevThemes, ", ") + ", or a theme folder by path (./my-theme)"},
			"theme %q is not installed with tts-axi's Slidev", theme)
	}
	if video {
		if sd.Transition == "view-transition" {
			return sd, script.Deck{}, output.Usage([]string{"use slide-left, slide-up or fade for a recorded deck"}, "transition: view-transition freezes the recording")
		}
		e := a.engine()
		if err := e.CheckNode(context.Background()); err != nil {
			return sd, script.Deck{}, output.Fail([]string{"then run tts-axi doctor --fix", "nothing was spoken"}, "--video needs Node.js %s or later: %v", engine.SlidevNode, err)
		}
		if !e.SlidevInstalled() || !e.ChromeInstalled() {
			return sd, script.Deck{}, output.Fail([]string{"run `tts-axi doctor --fix` once: it installs Slidev " + engine.SlidevVersion + " and Chrome for Testing into ~/.tts-axi", "nothing was spoken"},
				"--video needs Slidev and Chrome for Testing, which are not installed")
		}
	}
	d := script.Deck{Title: sd.Title, Lang: sd.Lang}
	for _, s := range sd.Slides {
		if s.Hidden {
			continue
		}
		// The plain player (player.html) shows each slide's title; the
		// narrated Slidev copy and the video show the slides themselves.
		d.Slides = append(d.Slides, script.Slide{Number: len(d.Slides) + 1, Title: s.Title, Body: "# " + s.Title, Notes: strings.Join(s.Steps, " ")})
	}
	return sd, d, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// slidevLines turns the narrated slides' steps into lines: each click starts
// with a cue, a clickPause of silence marked as the click.
func slidevLines(sd slidev.Deck, v voices.Voice) []line {
	var lines []line
	chapter := 0
	for _, s := range sd.Slides {
		if s.Hidden {
			continue
		}
		for k, step := range s.Steps {
			if k > 0 {
				lines = append(lines, line{Segment: script.Segment{Pause: clickPause, Chapter: chapter}, cue: true})
			}
			for _, seg := range script.NotesScript(step) {
				seg.Chapter = chapter
				lines = append(lines, line{Segment: seg, Voice: v})
			}
		}
		chapter++
	}
	return lines
}

// finishSlidev writes the narrated deck folder into the staged episode and,
// with --video, records it.
func finishSlidev(ctx context.Context, a *App, s *session, staged, sourceDir string, sd slidev.Deck, lines []line, l deckLayout, video bool) (output.Obj, error) {
	cues := map[int]slidev.Cues{}
	var chapters []slidev.Chapter
	chapter := 0
	for i, sl := range sd.Slides {
		if sl.Hidden {
			continue
		}
		start := l.slideAt[chapter]
		c := slidev.Cues{Start: start.Seconds(), Clicks: []float64{}}
		for j, ln := range lines {
			if ln.cue && ln.Chapter == chapter {
				c.Clicks = append(c.Clicks, (l.lineAt[j] - start + clickLead).Seconds())
			}
		}
		cues[i] = c
		chapters = append(chapters, slidev.Chapter{Title: sl.Title, Start: start})
		chapter++
	}
	project := slidev.Project{
		SourceDir: sourceDir, Slides: sd.Render(cues), Audio: l.audio, Total: l.total.Seconds(),
		Package: engine.SlidevPackage, Lock: engine.SlidevLock,
	}
	if err := project.Write(filepath.Join(staged, "slidev")); err != nil {
		return nil, output.Fail([]string{"nothing was written"}, "writing the narrated deck: %v", err)
	}
	clicks := 0
	for _, c := range cues {
		clicks += len(c.Clicks)
	}
	extra := output.Obj{}.Set("clicks", clicks).Set("slidev", filepath.Join(l.dir, "slidev"))
	if !video {
		return extra, nil
	}

	// The deck is recorded from a folder inside Slidev's install folder, so
	// Node finds Slidev's packages by walking up from it.
	e := a.engine()
	work := filepath.Join(e.SlidevDir(), "work", filepath.Base(s.stage))
	defer func() { _ = os.RemoveAll(work) }()
	if err := project.Write(work); err != nil {
		return nil, output.Fail([]string{"nothing was written"}, "preparing the recording: %v", err)
	}
	a.printer.Progress("recording %d slides in a hidden Chrome (%.0f s, in real time)", len(cues), l.total.Seconds())
	frames := filepath.Join(s.stage, "frames")
	fr, err := slidev.Record(ctx, slidev.Recording{
		Node: e.NodeBin(), Slidev: e.SlidevBin(), Chrome: e.ChromeBin(), Deck: work, Frames: frames,
		Seconds: l.total.Seconds(), Sampler: a.deps.Sampler, Limits: a.deps.Limits,
	})
	if err != nil {
		if guard.IsStop(err) {
			return nil, output.Fail([]string{"close memory-hungry apps or unload local models, then try again", "nothing was written"}, "%v", err)
		}
		return nil, output.Fail([]string{"nothing was written", "tts-axi deck --slidev (without --video) still writes the narrated deck"}, "%v", err)
	}
	if err := slidev.Mux(ctx, "ffmpeg", frames, fr, l.total.Seconds(), l.audio, filepath.Join(staged, "deck.mp4")); err != nil {
		return nil, output.Fail([]string{"nothing was written"}, "joining the video: %v", err)
	}
	var captions []slidev.Caption
	for j, ln := range lines {
		if !ln.IsPause() {
			captions = append(captions, slidev.Caption{Text: ln.Text, Start: l.lineAt[j], End: l.lineAt[j] + l.lengths[j]})
		}
	}
	if err := writeText(filepath.Join(staged, "chapters.txt"), slidev.YouTubeChapters(chapters)); err != nil {
		return nil, err
	}
	if err := writeText(filepath.Join(staged, "captions.srt"), slidev.SRT(captions)); err != nil {
		return nil, err
	}
	return extra.Set("video", filepath.Join(l.dir, "deck.mp4")).
		Set("frames", len(fr.Frames)), nil
}

// narrationDir is where a Slidev deck's narration is written: out, or
// <name>-narration beside the deck. It refuses a folder tts-axi did not
// make, and one holding the deck, since writing replaces the folder.
func narrationDir(deck, out string) (string, error) {
	deckDir, err := filepath.Abs(filepath.Dir(deck))
	if err != nil {
		return "", output.Fail(nil, "%v", err)
	}
	if out == "" {
		out = filepath.Join(deckDir, strings.TrimSuffix(filepath.Base(deck), filepath.Ext(deck))+"-narration")
	}
	dest, err := filepath.Abs(out)
	if err != nil {
		return "", output.Fail(nil, "%v", err)
	}
	if r, err := filepath.Rel(dest, deckDir); err == nil && (r == "." || !strings.HasPrefix(r, "..")) {
		return "", output.Usage([]string{"choose an --out folder that does not hold the deck"}, "--out %s holds the deck, and writing the narration replaces it", out)
	}
	entries, err := os.ReadDir(dest)
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return "", output.Usage([]string{"choose another --out folder"}, "%s is not a folder tts-axi can write: %v", out, err)
	case len(entries) > 0 && !isNarrationDir(dest):
		return "", output.Usage([]string{"choose another --out folder, or empty this one"}, "%s already holds files tts-axi did not write", out)
	}
	return dest, nil
}

// isNarrationDir reports whether dir is a narration folder tts-axi wrote.
func isNarrationDir(dir string) bool {
	for _, f := range []string{"timings.json", "slidev/slides.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			return false
		}
	}
	return true
}

// deliverFolder moves a staged deck episode to dest, replacing an earlier
// narration there, and prints the result.
func deliverFolder(a *App, ep library.Episode, staged, dest string, extra output.Obj) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return output.Fail(nil, "%v", err)
	}
	// Build the new folder beside dest first, so a failure leaves the old one.
	next := dest + ".tts-axi-new"
	_ = os.RemoveAll(next)
	if err := os.Rename(staged, next); err != nil {
		// The stage can be on another volume than the deck.
		if err := copyDir(staged, next); err != nil {
			_ = os.RemoveAll(next)
			return output.Fail([]string{"nothing was written"}, "writing %s: %v", dest, err)
		}
	}
	if err := os.RemoveAll(dest); err != nil {
		return output.Fail(nil, "replacing %s: %v", dest, err)
	}
	if err := os.Rename(next, dest); err != nil {
		return output.Fail(nil, "%v", err)
	}
	path := filepath.Join(dest, ep.Audio)
	result := output.Obj{}.Set("folder", dest).Set("path", path).
		Set("title", ep.Title).
		Set("seconds", round1(ep.Seconds)).
		Set("model", strings.Join(ep.Models, ",")).
		Set("engine", strings.Join(ep.Engines, ",")).
		Set("voices", ep.Voices)
	if ep.Fallback != "" {
		result = result.Set("fallback", ep.Fallback)
	}
	result = append(result, extra...)
	help := []string{"cd " + shellQuote(filepath.Join(dest, "slidev")) + " && npm ci && npm run dev, then press p to present it narrated"}
	if _, err := os.Stat(filepath.Join(dest, "deck.mp4")); err == nil {
		help = append([]string{"open " + shellQuote(filepath.Join(dest, "deck.mp4")) + " to watch it"}, help...)
	} else {
		help = append([]string{"afplay " + shellQuote(path) + " to listen"}, help...)
	}
	a.printer.Emit(result.Set("help", help))
	return nil
}

// copyDir copies a folder of regular files and folders.
func copyDir(src, dest string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(p, target)
	})
}
