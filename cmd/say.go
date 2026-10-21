package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/marcellovictorino/tts-axi/internal/audio"
	"github.com/marcellovictorino/tts-axi/internal/library"
	"github.com/marcellovictorino/tts-axi/internal/output"
	"github.com/marcellovictorino/tts-axi/internal/script"
	"github.com/marcellovictorino/tts-axi/internal/voices"
)

func newSayCmd(a *App) *cobra.Command {
	var f speakFlags
	var file, voice string
	c := &cobra.Command{
		Use:   "say [text]",
		Short: "Speak text in one voice into one audio file",
		Long: `Speak text, a file or standard input in one voice.

The text is split into paragraphs; Markdown marks are dropped. The result goes
into the library as an episode, or to --out. The default voice is the first
default of the language (Hugh in English, Davi in Portuguese, Augustin in
French).

  tts-axi say "Good morning. Here is your briefing."
  tts-axi say --file notes.md --title "Release notes"
  echo "Bom dia" | tts-axi say - --lang pt
  tts-axi say "Hello" --voice Bryony --out hello.m4a`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.load(); err != nil {
				return err
			}
			var text string
			switch {
			case len(args) == 1 && file != "":
				return output.Usage([]string{"pass the text or --file, not both"}, "both text and --file given")
			case file != "":
				t, err := readInput(a, file)
				if err != nil {
					return err
				}
				text = t
			case len(args) == 1 && args[0] == "-":
				t, err := readInput(a, "-")
				if err != nil {
					return err
				}
				text = t
			case len(args) == 1:
				text = args[0]
			default:
				return output.Usage([]string{`tts-axi say "Hello"`, "tts-axi say --file notes.md", "echo Hello | tts-axi say -"}, "nothing to say: give text, --file or -")
			}
			speed, format, err := f.resolve(a)
			if err != nil {
				return err
			}
			sc, err := script.ParseText(text)
			if err != nil {
				return output.Usage(nil, "%v", err)
			}
			lang := firstNonEmpty(f.lang, sc.Lang)
			var v voices.Voice
			if voice != "" {
				var ok bool
				if v, ok = voices.Find(voice); !ok {
					return output.Usage([]string{"run `tts-axi voices` to see the voices"}, "unknown voice %q", voice)
				}
				if lang != "" && v.Lang != lang {
					return output.Usage([]string{fmt.Sprintf("%s voices: %s", lang, strings.Join(voices.Names(lang), ", "))}, "%s is a %s voice, not %s", v.Name, v.Lang, lang)
				}
			} else {
				if lang == "" {
					lang = "en"
				}
				v, _ = voices.Find(a.voices.Default(lang))
			}
			lines := make([]line, len(sc.Segments))
			for i, seg := range sc.Segments {
				lines[i] = line{Segment: seg, Voice: v}
			}
			title := firstNonEmpty(f.title, sc.Title, titleFrom(lines))
			return produce(cmd, a, &f, "say", title, nil, lines, speed, format)
		},
	}
	f.register(c, true)
	c.Flags().StringVarP(&file, "file", "f", "", "read the text from a file (- for standard input)")
	c.Flags().StringVar(&voice, "voice", "", "voice name (see `tts-axi voices`)")
	return c
}

// produce speaks lines, joins them with chapters and delivers the episode;
// say and render share it.
func produce(cmd *cobra.Command, a *App, f *speakFlags, kind, title string, chapters []string, lines []line, speed float64, format string) error {
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
	clips, starts, total, err := timeline(s.stage, lines, sp)
	if err != nil {
		return output.Fail(nil, "%v", err)
	}
	// One chapter mark per heading that has lines under it; the first mark
	// starts at zero so any introduction belongs to it.
	var marks []audio.Chapter
	var markTitles []string
	last := -1
	for i, l := range lines {
		if l.Chapter < 0 || l.Chapter == last {
			continue
		}
		last = l.Chapter
		if len(marks) > 0 {
			marks[len(marks)-1].End = starts[i]
		}
		marks = append(marks, audio.Chapter{Title: chapters[l.Chapter], Start: starts[i]})
		markTitles = append(markTitles, chapters[l.Chapter])
	}
	if len(marks) > 0 {
		marks[0].Start = 0
		marks[len(marks)-1].End = total
	}
	engines, voiceNames, langs, fallback := summary(lines, sp)
	now := a.deps.Now()
	ep := library.Episode{
		ID: a.library().NewID(title, now), Title: title, Kind: kind, Created: now,
		Seconds: total.Seconds(), Audio: "episode." + format,
		Engines: engines, Voices: voiceNames, Lang: langs, Chapters: markTitles, Fallback: fallback,
	}
	staged := filepath.Join(s.stage, "episode")
	if err := mkdir(staged); err != nil {
		return err
	}
	meta := audio.Meta{Title: title, Artist: strings.Join(voiceNames, ", "), Album: "tts-axi"}
	if err := audio.Join(ctx, "ffmpeg", s.stage, clips, marks, meta, filepath.Join(staged, ep.Audio)); err != nil {
		return output.Fail([]string{"nothing was written", "run `tts-axi doctor` to check ffmpeg"}, "%v", err)
	}
	if err := writeText(filepath.Join(staged, "transcript.md"), transcript(title, chapters, lines)); err != nil {
		return err
	}
	var extra output.Obj
	if len(markTitles) > 0 {
		extra = extra.Set("chapters", markTitles)
	}
	return deliver(a, f, ep, staged, ep.Audio, extra)
}

// titleFrom takes the first sentence of the first line, up to eight words.
func titleFrom(lines []line) string {
	for _, l := range lines {
		if l.IsPause() {
			continue
		}
		words := strings.Fields(l.Text)
		for i, w := range words {
			if i >= 2 && strings.ContainsAny(w[len(w)-1:], ".!?") {
				words = words[:i+1]
				break
			}
		}
		if len(words) > 8 {
			words = words[:8]
		}
		return strings.TrimRight(strings.Join(words, " "), ".,;:!?")
	}
	return "Untitled"
}

func mkdir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return output.Fail(nil, "%v", err)
	}
	return nil
}

func writeText(path, text string) error {
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return output.Fail(nil, "%v", err)
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
