package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/marcellovictorino/tts-axi/internal/audio"
	"github.com/marcellovictorino/tts-axi/internal/library"
	"github.com/marcellovictorino/tts-axi/internal/output"
	"github.com/marcellovictorino/tts-axi/internal/script"
	"github.com/marcellovictorino/tts-axi/internal/voices"
)

// slideGap is the stillness between one slide's narration and the next
// slide; the player waits the same time.
const slideGap = 600 * time.Millisecond

func newDeckCmd(a *App) *cobra.Command {
	var f speakFlags
	var voice, images string
	var silent time.Duration
	c := &cobra.Command{
		Use:   "deck <slides.md>",
		Short: "Narrate a slide deck from its speaker notes, with an auto-advancing player",
		Long: `Speak each slide's speaker notes into its own clip and build a player page
that shows each slide while its clip plays, then moves on.

Slides are Markdown separated by lines of "---" (Marp and Slidev both work).
A slide's narration is its HTML comments, <!-- like this -->; Marp
directives such as <!-- _class: lead --> are ignored. Slides without notes
stay up for --silent seconds.

The episode folder holds slides/slide-NN.mp3, deck.mp3 (all slides, one
chapter per slide), timings.json and player.html. With --images, the player
shows your exported slide pictures (sorted by name, one per slide) instead
of the simple built-in rendering.

  tts-axi deck talk.md
  tts-axi deck talk.md --images exported-pngs/ --voice Bryony`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.load(); err != nil {
				return err
			}
			src, err := readInput(a, args[0])
			if err != nil {
				return err
			}
			speed, format, err := f.resolve(a)
			if err != nil {
				return err
			}
			if format == "wav" {
				return output.Usage([]string{"use --as mp3 or --as m4a"}, "a deck's clips are mp3 or m4a")
			}
			d, err := script.ParseDeck(src)
			if err != nil {
				return output.Usage(nil, "%v", err)
			}
			var pictures []string
			if images != "" {
				if pictures, err = listImages(images); err != nil {
					return err
				}
				if len(pictures) != len(d.Slides) {
					return output.Usage([]string{"export one picture per slide, named so they sort in slide order"}, "%s has %d pictures for %d slides", images, len(pictures), len(d.Slides))
				}
			}
			lang := firstNonEmpty(f.lang, d.Lang)
			var v voices.Voice
			if voice != "" {
				var ok bool
				if v, ok = voices.Find(voice); !ok {
					return output.Usage([]string{"run `tts-axi voices` to see the voices"}, "unknown voice %q", voice)
				}
				if lang != "" && v.Lang != lang {
					return output.Usage(nil, "%s is a %s voice, not %s", v.Name, v.Lang, lang)
				}
			} else {
				if lang == "" {
					lang = "en"
				}
				if !voices.IsLanguage(lang) {
					return output.Usage([]string{"valid languages: " + strings.Join(voices.Languages, ", ")}, "unknown lang %q", lang)
				}
				v, _ = voices.Find(a.voices.Default(lang))
			}
			var lines []line
			for i, s := range d.Slides {
				for _, seg := range script.NotesScript(s.Notes) {
					seg.Chapter = i
					lines = append(lines, line{Segment: seg, Voice: v})
				}
			}
			title := firstNonEmpty(f.title, d.Title)
			return produceDeck(cmd, a, &f, title, d, lines, pictures, speed, format, silent)
		},
	}
	f.register(c, false)
	c.Flags().StringVar(&f.as, "as", "", "clip format: mp3 or m4a (default mp3)")
	c.Flags().StringVar(&voice, "voice", "", "narrator voice (see `tts-axi voices`)")
	c.Flags().StringVar(&images, "images", "", "folder of exported slide pictures (png, jpg or webp), one per slide")
	c.Flags().DurationVar(&silent, "silent", 4*time.Second, "how long a slide without notes stays up")
	completeSpeakFlags(c)
	_ = c.RegisterFlagCompletionFunc("voice", completeVoices)
	_ = c.MarkFlagDirname("images")
	return c
}

func produceDeck(cmd *cobra.Command, a *App, f *speakFlags, title string, d script.Deck, lines []line, pictures []string, speed float64, format string, silent time.Duration) error {
	ctx := cmd.Context()
	s, err := startSession(ctx, a, f.wait)
	if err != nil {
		return err
	}
	defer s.close()
	sp, err := speakLines(ctx, a, s.stage, lines, speed, f.engine == "cpu")
	if err != nil {
		return err
	}
	staged := filepath.Join(s.stage, "episode")
	if err := mkdir(filepath.Join(staged, "slides")); err != nil {
		return err
	}
	silence := filepath.Join(s.stage, "slide-silent.wav")
	if err := audio.WriteSilence(silence, silent); err != nil {
		return output.Fail(nil, "%v", err)
	}
	gap := filepath.Join(s.stage, "slide-gap.wav")
	if err := audio.WriteSilence(gap, slideGap); err != nil {
		return output.Fail(nil, "%v", err)
	}

	var full []string
	var marks []audio.Chapter
	var timings []library.Timing
	var player []library.PlayerSlide
	var at time.Duration
	for i, slide := range d.Slides {
		// This slide's lines, in order, with their indexes into the spoken set.
		var idx []int
		for j, l := range lines {
			if l.Chapter == i {
				idx = append(idx, j)
			}
		}
		ps := library.PlayerSlide{Title: slide.Title, HTML: library.SlideHTML(slide.Body)}
		if pictures != nil {
			name := fmt.Sprintf("slide-%02d%s", i+1, strings.ToLower(filepath.Ext(pictures[i])))
			if err := copyFile(pictures[i], filepath.Join(staged, "slides", name)); err != nil {
				return output.Fail(nil, "%v", err)
			}
			ps.Image = "slides/" + name
		}
		t := library.Timing{Slide: i + 1, Title: slide.Title, Start: ms(at)}
		var length time.Duration
		if len(idx) == 0 {
			length = silent
			full = append(full, silence)
		} else {
			sub := make([]line, len(idx))
			subSp := spoken{wavs: make([]string, len(idx)), lengths: make([]time.Duration, len(idx))}
			for k, j := range idx {
				sub[k], subSp.wavs[k], subSp.lengths[k] = lines[j], sp.wavs[j], sp.lengths[j]
			}
			clips, _, total, err := timeline(s.stage, sub, subSp)
			if err != nil {
				return output.Fail(nil, "%v", err)
			}
			wav := filepath.Join(s.stage, fmt.Sprintf("slide-%02d.wav", i+1))
			if err := audio.Join(ctx, "ffmpeg", s.stage, clips, nil, audio.Meta{}, wav); err != nil {
				return output.Fail([]string{"nothing was written"}, "%v", err)
			}
			clip := fmt.Sprintf("slides/slide-%02d.%s", i+1, format)
			if err := audio.Encode(ctx, "ffmpeg", wav, filepath.Join(staged, filepath.FromSlash(clip))); err != nil {
				return output.Fail([]string{"nothing was written"}, "%v", err)
			}
			length = total
			full = append(full, wav)
			t.Audio, ps.Audio = clip, clip
		}
		// The slide stays up for its clip plus the gap, as the player does.
		length += slideGap
		full = append(full, gap)
		t.Seconds = ms(length)
		ps.Seconds = ms(length)
		marks = append(marks, audio.Chapter{Title: slide.Title, Start: at, End: at + length})
		at += length
		timings = append(timings, t)
		player = append(player, ps)
	}

	engines, voiceNames, langs, fallback := summary(lines, sp)
	now := a.deps.Now()
	var titles []string
	for _, sl := range d.Slides {
		titles = append(titles, sl.Title)
	}
	ep := library.Episode{
		ID: a.library().NewID(title, now), Title: title, Kind: "deck", Created: now,
		Seconds: at.Seconds(), Audio: "deck." + format, Player: "player.html",
		Engines: engines, Voices: voiceNames, Lang: langs, Chapters: titles, Fallback: fallback,
	}
	meta := audio.Meta{Title: title, Artist: strings.Join(voiceNames, ", "), Album: "tts-axi"}
	if err := audio.Join(ctx, "ffmpeg", s.stage, full, marks, meta, filepath.Join(staged, ep.Audio)); err != nil {
		return output.Fail([]string{"nothing was written"}, "%v", err)
	}
	tj, _ := json.MarshalIndent(timings, "", "  ")
	if err := writeText(filepath.Join(staged, "timings.json"), string(tj)+"\n"); err != nil {
		return err
	}
	if err := writeText(filepath.Join(staged, "player.html"), library.RenderPlayer(title, player)); err != nil {
		return err
	}
	if err := writeText(filepath.Join(staged, "transcript.md"), transcript(title, titles, lines)); err != nil {
		return err
	}
	if err := deliver(a, f, ep, staged, ep.Audio, output.Obj{}.Set("slides", len(d.Slides))); err != nil {
		return err
	}
	return nil
}

// ms rounds a duration to whole milliseconds, in seconds.
func ms(d time.Duration) float64 { return float64(d.Milliseconds()) / 1000 }

// listImages returns the slide pictures in a folder, sorted by name.
func listImages(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, output.Fail(nil, "%v", err)
	}
	var out []string
	for _, e := range entries {
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".png", ".jpg", ".jpeg", ".webp":
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	// Natural order, so slide-2 comes before slide-10.
	sort.Slice(out, func(i, j int) bool { return naturalLess(filepath.Base(out[i]), filepath.Base(out[j])) })
	return out, nil
}

func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		da, db := digits(a), digits(b)
		if da != "" && db != "" {
			if len(strings.TrimLeft(da, "0")) != len(strings.TrimLeft(db, "0")) {
				return len(strings.TrimLeft(da, "0")) < len(strings.TrimLeft(db, "0"))
			}
			if strings.TrimLeft(da, "0") != strings.TrimLeft(db, "0") {
				return strings.TrimLeft(da, "0") < strings.TrimLeft(db, "0")
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func digits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}
