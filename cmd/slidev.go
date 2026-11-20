package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcellovictorino/tts-axi/internal/engine"
	"github.com/marcellovictorino/tts-axi/internal/guard"
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
