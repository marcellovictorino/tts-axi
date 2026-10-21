package cmd

import (
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/marcellovictorino/tts-axi/internal/output"
	"github.com/marcellovictorino/tts-axi/internal/script"
	"github.com/marcellovictorino/tts-axi/internal/voices"
)

func newRenderCmd(a *App) *cobra.Command {
	var f speakFlags
	var mapping []string
	c := &cobra.Command{
		Use:   "render <script.md>",
		Short: "Speak a multi-voice script with chapters and pauses into one episode",
		Long: `Speak a script with several voices into one mp3 or m4a with chapter marks.

  ---
  title: Your day ahead
  lang: en
  voices: {Host: Hugh, Guest: Bryony}
  ---
  ## Calendar
  Host: Morning. Three things today.
  [pause 1s]
  Guest: First, the ten o'clock review.

"## Heading" starts a chapter. "Name: words" starts a speaker's turn; the next
lines continue it until a blank line. A speaker named after a voice (Hugh:,
Davi:) uses it, mapped speakers use their voice, and the others take the
language's default voices in order. [pause 2s] inserts real silence. Voices
of different languages can share a script.`,
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
			flagMap, err := parseVoiceMap(mapping)
			if err != nil {
				return err
			}
			sc, err := script.ParseScript(src)
			if err != nil {
				return output.Usage(nil, "%v", err)
			}
			lang := firstNonEmpty(f.lang, sc.Lang, "en")
			if !voices.IsLanguage(lang) {
				return output.Usage([]string{"valid languages: " + strings.Join(voices.Languages, ", ")}, "unknown lang %q in the front matter", lang)
			}
			for k, v := range flagMap { // flags win over the front matter
				sc.Voices[k] = v
			}
			lines, err := cast(a, lang, sc.Voices, sc.Segments)
			if err != nil {
				return err
			}
			title := firstNonEmpty(f.title, sc.Title, strings.TrimSuffix(filepath.Base(args[0]), filepath.Ext(args[0])))
			return produce(cmd, a, &f, "render", title, sc.Chapters, lines, speed, format)
		},
	}
	f.register(c, true)
	c.Flags().StringArrayVar(&mapping, "voice", nil, "give a speaker a voice, Speaker=Voice (repeatable)")
	completeSpeakFlags(c)
	return c
}

// cast gives every segment its voice.
func cast(a *App, lang string, mapping map[string]string, segs []script.Segment) ([]line, error) {
	caster, err := voices.NewCaster(a.voices, lang, mapping)
	if err != nil {
		return nil, output.Usage([]string{"run `tts-axi voices` to see the voices"}, "%v", err)
	}
	lines := make([]line, len(segs))
	for i, seg := range segs {
		lines[i] = line{Segment: seg}
		if seg.IsPause() {
			continue
		}
		v, err := caster.Voice(seg.Speaker)
		if err != nil {
			return nil, output.Usage(nil, "%v", err)
		}
		lines[i].Voice = v
	}
	return lines, nil
}
