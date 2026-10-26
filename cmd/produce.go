package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/marcellovictorino/tts-axi/internal/audio"
	"github.com/marcellovictorino/tts-axi/internal/config"
	"github.com/marcellovictorino/tts-axi/internal/engine"
	"github.com/marcellovictorino/tts-axi/internal/guard"
	"github.com/marcellovictorino/tts-axi/internal/library"
	"github.com/marcellovictorino/tts-axi/internal/output"
	"github.com/marcellovictorino/tts-axi/internal/script"
	"github.com/marcellovictorino/tts-axi/internal/voices"
)

// Silences inserted between spoken lines when the script asks for none.
// Each clip is first trimmed to clipPad of silence at either end.
const (
	clipPad        = 60 * time.Millisecond
	gapSameSpeaker = 350 * time.Millisecond
	gapNewSpeaker  = 500 * time.Millisecond
	gapNewChapter  = time.Second
)

// speakFlags are the flags say, render and deck share.
type speakFlags struct {
	lang   string
	speed  string // a string so "not given" differs from 0
	engine string
	out    string
	as     string
	title  string
	wait   time.Duration
}

func (f *speakFlags) register(c *cobra.Command, withOut bool) {
	c.Flags().StringVar(&f.lang, "lang", "", "language: en, pt or fr (default: from the voice or the front matter, else en)")
	c.Flags().StringVar(&f.speed, "speed", "", "Phonon duration_delta: 0 is the model's pace, positive is slower (default 0.15)")
	c.Flags().StringVar(&f.engine, "engine", "auto", "auto (Neural Engine, CPU fallback) or cpu")
	c.Flags().StringVar(&f.title, "title", "", "episode title (default: from the text)")
	c.Flags().DurationVar(&f.wait, "wait", 15*time.Minute, "how long to wait for another render to finish")
	if withOut {
		c.Flags().StringVarP(&f.out, "out", "o", "", "write the audio to this file instead of the library (.mp3, .m4a or .wav)")
		c.Flags().StringVar(&f.as, "as", "", "audio format: mp3, m4a or wav (default: from --out, else mp3)")
	}
}

// resolve checks the shared flags and returns the speed and audio format.
func (f *speakFlags) resolve(a *App) (speed float64, format string, err error) {
	if f.lang != "" && !voices.IsLanguage(f.lang) {
		return 0, "", output.Usage([]string{"valid languages: " + strings.Join(voices.Languages, ", ")}, "unknown --lang %q", f.lang)
	}
	if f.engine != "auto" && f.engine != "cpu" {
		return 0, "", output.Usage([]string{"valid engines: auto, cpu"}, "unknown --engine %q", f.engine)
	}
	speed = a.settings.SpeedOrDefault()
	if f.speed != "" {
		if speed, err = strconv.ParseFloat(f.speed, 64); err != nil || speed < -0.5 || speed > 0.5 {
			return 0, "", output.Usage([]string{"use a small number such as 0, 0.15 or -0.1; large values make speech less reliable"}, "--speed must be a number between -0.5 and 0.5")
		}
	}
	format = f.as
	if f.out != "" {
		of := audio.FormatOf(f.out)
		if of == "" {
			return 0, "", output.Usage([]string{"end --out with .mp3, .m4a or .wav"}, "cannot tell the audio format of %q", f.out)
		}
		if format != "" && format != of {
			return 0, "", output.Usage(nil, "--as %s does not match --out %s", format, f.out)
		}
		format = of
	}
	if format == "" {
		format = "mp3"
	}
	if audio.FormatOf("x."+format) == "" {
		return 0, "", output.Usage([]string{"valid formats: " + strings.Join(audio.Formats, ", ")}, "unknown --as %q", format)
	}
	return speed, format, nil
}

// line is one segment with its voice resolved.
type line struct {
	script.Segment
	Voice voices.Voice
}

// spoken is the result of speaking a list of lines.
type spoken struct {
	wavs     []string        // per line; "" for pauses
	lengths  []time.Duration // per line, pauses included
	outcomes map[string]engine.Outcome
}

// speakLines groups the lines by language, speaks each group with one helper
// run, and returns a WAV per line. Pauses become silence files.
func speakLines(ctx context.Context, a *App, stage string, lines []line, speed float64, forceCPU bool) (spoken, error) {
	res := spoken{wavs: make([]string, len(lines)), lengths: make([]time.Duration, len(lines)), outcomes: map[string]engine.Outcome{}}
	jobs := map[string]*engine.Job{}
	var order []string
	for i, l := range lines {
		if l.IsPause() {
			continue
		}
		j, ok := jobs[l.Voice.Lang]
		if !ok {
			j = &engine.Job{Lang: l.Voice.Lang, Speed: speed, ForceCPU: forceCPU}
			jobs[l.Voice.Lang] = j
			order = append(order, l.Voice.Lang)
		}
		res.wavs[i] = filepath.Join(stage, fmt.Sprintf("line-%04d.wav", i+1))
		j.Items = append(j.Items, engine.Item{Text: l.Text, Voice: l.Voice.Name, Out: res.wavs[i]})
	}
	e := a.engine()
	for _, lang := range order {
		out, err := e.Speak(ctx, *jobs[lang])
		if err != nil {
			return res, speakError(err)
		}
		res.outcomes[lang] = out
	}
	for i, l := range lines {
		if l.IsPause() {
			continue
		}
		if err := audio.TrimSilence(res.wavs[i], clipPad); err != nil {
			return res, output.Fail(nil, "%v", err)
		}
		d, err := audio.Duration(res.wavs[i])
		if err != nil {
			return res, output.Fail(nil, "%v", err)
		}
		res.lengths[i] = d
	}
	return res, nil
}

// speakError turns an engine failure into a user-facing error with next steps.
func speakError(err error) error {
	switch {
	case engine.IsMissing(err) && strings.Contains(err.Error(), "French and Portuguese"):
		return output.Fail([]string{
			"run `tts-axi doctor --fix` once: it downloads the French and Portuguese model (122 MB) with no account or token",
			"nothing was written",
		}, "%v", err)
	case engine.IsMissing(err):
		return output.Fail([]string{"run `tts-axi doctor --fix` to install the speech runtime and model"}, "%v", err)
	case guard.IsStop(err):
		return output.Fail([]string{"close memory-hungry apps or unload local models, then try again", "nothing was written"}, "%v", err)
	}
	return output.Fail([]string{"nothing was written", "run `tts-axi doctor` to check the installation"}, "speaking failed: %s", err)
}

// timeline places the lines one after another with the gaps between them,
// writing silence files where needed, and returns the clips to join and each
// line's start time.
func timeline(stage string, lines []line, sp spoken) (clips []string, starts []time.Duration, total time.Duration, err error) {
	starts = make([]time.Duration, len(lines))
	silences := map[time.Duration]string{}
	silence := func(d time.Duration) (string, error) {
		d = d.Round(10 * time.Millisecond)
		if p, ok := silences[d]; ok {
			return p, nil
		}
		p := filepath.Join(stage, fmt.Sprintf("silence-%dms.wav", d.Milliseconds()))
		if err := audio.WriteSilence(p, d); err != nil {
			return "", err
		}
		silences[d] = p
		return p, nil
	}
	prev := -1 // the last speech line
	for i, l := range lines {
		if l.IsPause() {
			p, err := silence(l.Pause)
			if err != nil {
				return nil, nil, 0, err
			}
			starts[i] = total
			clips = append(clips, p)
			total += l.Pause
			prev = -2 // a pause replaces the next gap
			continue
		}
		if prev >= 0 {
			gap := gapSameSpeaker
			switch {
			case lines[prev].Chapter != l.Chapter:
				gap = gapNewChapter
			case lines[prev].Voice.Name != l.Voice.Name:
				gap = gapNewSpeaker
			}
			p, err := silence(gap)
			if err != nil {
				return nil, nil, 0, err
			}
			clips = append(clips, p)
			total += gap
		}
		starts[i] = total
		clips = append(clips, sp.wavs[i])
		total += sp.lengths[i]
		prev = i
	}
	return clips, starts, total, nil
}

// session is one render: the lock, a staging folder, and the cleanup that
// guarantees a failed render leaves nothing behind.
type session struct {
	a      *App
	stage  string
	unlock func()
}

func startSession(ctx context.Context, a *App, wait time.Duration) (*session, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, output.Fail([]string{"brew install ffmpeg"}, "ffmpeg is not installed")
	}
	unlock, err := config.LockRender(ctx, a.paths, wait, func() {
		a.printer.Progress("another render is running; waiting for it to finish")
	})
	if err != nil {
		return nil, output.Fail([]string{"try again later, or pass a longer --wait"}, "%v", err)
	}
	stage := filepath.Join(a.paths.RunDir(), fmt.Sprintf("work-%d-%d", os.Getpid(), time.Now().UnixNano()))
	if err := os.MkdirAll(stage, 0o755); err != nil {
		unlock()
		return nil, output.Fail(nil, "%v", err)
	}
	return &session{a: a, stage: stage, unlock: unlock}, nil
}

// close removes the staging folder (published files were moved out of it)
// and releases the lock.
func (s *session) close() {
	_ = os.RemoveAll(s.stage)
	s.unlock()
}

// summary collects what the result reports about engines and voices.
func summary(lines []line, sp spoken) (engines, voiceNames, langs []string, fallback string) {
	seenV := map[string]bool{}
	for _, l := range lines {
		if !l.IsPause() && !seenV[l.Voice.Name] {
			seenV[l.Voice.Name] = true
			voiceNames = append(voiceNames, l.Voice.Name)
		}
	}
	for lang, o := range sp.outcomes {
		langs = append(langs, lang)
		engines = append(engines, string(o.Route))
		if o.Fallback != "" {
			fallback = o.Fallback
		}
	}
	sort.Strings(langs)
	sort.Strings(engines)
	return engines, voiceNames, langs, fallback
}

// transcript writes the text that was spoken, with speakers and chapters.
func transcript(title string, chapters []string, lines []line) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", title)
	ch := -1
	for _, l := range lines {
		if l.Chapter != ch && l.Chapter >= 0 && l.Chapter < len(chapters) {
			ch = l.Chapter
			fmt.Fprintf(&b, "\n## %s\n", chapters[ch])
		}
		if l.IsPause() {
			fmt.Fprintf(&b, "\n[pause %s]\n", l.Pause)
			continue
		}
		fmt.Fprintf(&b, "\n**%s:** %s\n", l.Voice.Name, l.Text)
	}
	return b.String()
}

// deliver publishes a staged episode folder, or copies its audio to --out,
// and prints the result.
func deliver(a *App, f *speakFlags, ep library.Episode, staged, audioName string, extra output.Obj) error {
	result := output.Obj{}
	var path string
	if f.out != "" {
		dest, err := filepath.Abs(f.out)
		if err != nil {
			return output.Fail(nil, "%v", err)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return output.Fail(nil, "%v", err)
		}
		if err := copyFile(filepath.Join(staged, audioName), dest); err != nil {
			return output.Fail(nil, "%v", err)
		}
		path = dest
		result = result.Set("path", dest)
	} else {
		dir, err := a.library().Publish(staged, ep)
		if err != nil {
			return output.Fail(nil, "publishing to the library: %v", err)
		}
		path = filepath.Join(dir, audioName)
		result = result.Set("episode", ep.ID).Set("path", path)
	}
	result = result.Set("title", ep.Title).
		Set("seconds", round1(ep.Seconds)).
		Set("engine", strings.Join(ep.Engines, ",")).
		Set("voices", ep.Voices)
	if ep.Fallback != "" {
		result = result.Set("fallback", ep.Fallback)
	}
	result = append(result, extra...)
	var help []string
	if f.out == "" {
		help = append(help, "open "+filepath.Join(a.paths.Library, "index.html")+" to play it, or `tts-axi library` to list episodes")
	} else {
		help = append(help, "afplay "+shellQuote(path)+" to listen")
	}
	a.printer.Emit(result.Set("help", help))
	return nil
}

func copyFile(src, dest string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(dest, data, 0o644)
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

func shellQuote(s string) string {
	if !strings.ContainsAny(s, " '\"$`\\") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// readInput returns a file's text, or stdin for "-".
func readInput(a *App, path string) (string, error) {
	if path == "-" {
		if a.deps.In == nil {
			return "", output.Usage(nil, "no standard input to read")
		}
		data, err := io.ReadAll(a.deps.In)
		if err != nil {
			return "", output.Fail(nil, "reading standard input: %v", err)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", output.Fail(nil, "%v", err)
	}
	return string(data), nil
}

// parseVoiceMap reads repeated --voice Speaker=Voice flags.
func parseVoiceMap(pairs []string) (map[string]string, error) {
	m := map[string]string{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			return nil, output.Usage([]string{"for example --voice Host=Hugh --voice Guest=Bryony"}, "--voice takes Speaker=Voice, got %q", p)
		}
		m[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return m, nil
}
