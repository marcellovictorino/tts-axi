package cmd

import (
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/marcellovictorino/tts-axi/internal/audio"
	"github.com/marcellovictorino/tts-axi/internal/clones"
	"github.com/marcellovictorino/tts-axi/internal/output"
)

func newVoiceCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "voice",
		Short: "Add a cloned voice",
		Long: `Manage cloned voices. A cloned voice speaks with Qwen3-TTS on this Mac; the
clip and its words never leave it. Built-in voices stay on Phonon.`,
	}
	c.AddCommand(newVoiceAddCmd(a))
	return c
}

func newVoiceAddCmd(a *App) *cobra.Command {
	var clip, text, textFile string
	c := &cobra.Command{
		Use:   "add <name>",
		Short: "Clone a voice from a recording and the words spoken in it",
		Long: `Store a cloned voice. Give a recording of one person speaking clearly and
the exact words said in it. Qwen3-TTS clones the voice from it each time you
use the voice; a clip of 15 to 30 seconds is enough, and a longer one slows
and enlarges every render.

The clip is converted to a 24 kHz mono WAV and kept with its words in
~/.tts-axi/voices/<name>, outside any repository and the library. Double
quote marks are dropped from the words and lines are joined with single
spaces. Use the voice like any other: --voice <name> in say, deck and
render (English).

  tts-axi voice add mf --clip ~/Music/voice-clone/mf-sample.m4a --text-file mf-words.md
  tts-axi say "Good morning." --voice mf`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.load(); err != nil {
				return err
			}
			name := args[0]
			if clip == "" {
				return output.Usage([]string{"tts-axi voice add " + name + " --clip recording.m4a --text \"the words\""}, "--clip is required")
			}
			if (text == "") == (textFile == "") {
				return output.Usage([]string{"give the words with --text or --text-file"}, "give exactly one of --text and --text-file")
			}
			if err := clones.ValidName(name); err != nil {
				return output.Usage(nil, "%v", err)
			}
			if _, ok := a.voices.Find(name); ok {
				return output.Usage([]string{"run `tts-axi voices` to see the names in use"}, "a voice named %q already exists", name)
			}
			if _, err := exec.LookPath("ffmpeg"); err != nil {
				return output.Fail([]string{"brew install ffmpeg"}, "ffmpeg is not installed")
			}
			words := text
			if textFile != "" {
				t, err := readInput(a, textFile)
				if err != nil {
					return err
				}
				words = t
			}
			v, err := clones.Add(cmd.Context(), "ffmpeg", a.paths.VoicesDir(), name, clip, words)
			if err != nil {
				return output.Fail(nil, "%v", err)
			}
			d, err := audio.Duration(v.Ref)
			if err != nil {
				return output.Fail(nil, "%v", err)
			}
			help := []string{`tts-axi say "Hello" --voice ` + v.Name}
			if !a.engine().QwenInstalled() {
				help = append([]string{"run `tts-axi doctor --fix` to download the cloned-voice model (3 GB), or it downloads on first use"}, help...)
			}
			a.printer.Emit(output.Obj{}.Set("voice", v.Name).Set("model", "qwen").Set("clip_seconds", round1(d.Seconds())).
				Set("path", a.paths.VoicesDir()+"/"+v.Name).Set("help", help))
			return nil
		},
	}
	c.Flags().StringVar(&clip, "clip", "", "recording of the voice (any audio ffmpeg reads)")
	c.Flags().StringVar(&text, "text", "", "the exact words spoken in the clip")
	c.Flags().StringVar(&textFile, "text-file", "", "a file holding the exact words spoken in the clip")
	_ = c.MarkFlagFilename("clip")
	_ = c.MarkFlagFilename("text-file")
	return c
}
