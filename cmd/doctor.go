package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/marcellovictorino/tts-axi/internal/engine"
	"github.com/marcellovictorino/tts-axi/internal/guard"
	"github.com/marcellovictorino/tts-axi/internal/output"
)

func newDoctorCmd(a *App) *cobra.Command {
	var fix bool
	c := &cobra.Command{
		Use:   "doctor",
		Short: "Check what tts-axi needs; --fix installs the runtime and model",
		Long: `Check ffmpeg, the Phonon runtime, the Swift helper, the English model, the
French and Portuguese route, memory, the library folder and the agent skill.

--fix downloads Gradium's Phonon runtime (39 MB) and the English Core ML model
(137 MB) into ~/.tts-axi, builds the Swift helper with swiftc, prepares the
default speed, installs ptts for French and Portuguese with uv, and downloads
the French and Portuguese model (122 MB) from Ollama's registry, with no
account or token; Ollama itself is not needed. It also installs mlx-audio
and downloads the cloned-voice model (Qwen3-TTS 1.7B Base, 3 GB, from Hugging
Face, no account needed), which cloned voices (tts-axi voice add) run on. Every download is checked
against a pinned checksum. It loads no model. Running it again is a no-op.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.load(); err != nil {
				return err
			}
			ctx := cmd.Context()
			e := a.engine()
			var fixErr error
			if fix {
				step := func(what string, fn func() error) {
					if fixErr != nil {
						return
					}
					a.printer.Progress("%s", what)
					if err := fn(); err != nil {
						fixErr = fmt.Errorf("%s: %w", what, err)
					}
				}
				step("installing the Phonon runtime", func() error { return e.InstallRuntime(ctx) })
				step("building the Swift helper", func() error { return e.BuildHelper(ctx) })
				step("installing the English model", func() error { return e.InstallBundle(ctx) })
				step("preparing the default speed", func() error {
					_, err := e.SpeedDir(ctx, a.settings.SpeedOrDefault())
					return err
				})
				step("installing ptts for French and Portuguese", func() error {
					if _, err := e.WritePttsHelper(); err != nil {
						return err
					}
					if _, err := exec.LookPath("uv"); err != nil {
						return nil // reported below as a warning
					}
					return e.WarmPtts(ctx)
				})
				step("downloading the French and Portuguese model", func() error { return e.InstallMultilingual(ctx) })
				step("installing mlx-audio for cloned voices", func() error {
					if _, err := e.WriteQwenHelper(); err != nil {
						return err
					}
					if _, err := exec.LookPath("uv"); err != nil {
						return nil // reported below as a warning
					}
					return e.WarmQwen(ctx)
				})
				step("downloading the cloned-voice model", func() error { return e.InstallQwen(ctx) })
				step("creating the library", func() error { return a.library().Rebuild() })
			}

			var rows []output.Obj
			failed := false
			add := func(check, status, detail string) {
				if status == "fail" {
					failed = true
				}
				rows = append(rows, output.Obj{}.Set("check", check).Set("status", status).Set("detail", detail))
			}
			if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
				add("mac", "ok", "Apple silicon")
			} else {
				add("mac", "fail", "needs an Apple silicon Mac; this is "+runtime.GOOS+"/"+runtime.GOARCH)
			}
			if p, err := exec.LookPath("ffmpeg"); err == nil {
				add("ffmpeg", "ok", p)
			} else {
				add("ffmpeg", "fail", "missing: brew install ffmpeg")
			}
			switch {
			case e.HelperCurrent():
				add("helper", "ok", a.paths.HelperBin())
			case !e.RuntimeInstalled():
				add("helper", "fail", "the Phonon runtime is not installed: tts-axi doctor --fix")
			default:
				if _, err := exec.LookPath("swiftc"); err != nil {
					add("helper", "fail", "not built, and swiftc is missing: xcode-select --install")
				} else {
					add("helper", "fail", "not built: tts-axi doctor --fix")
				}
			}
			if e.BundleInstalled() {
				add("english model", "ok", "Phonon 31M, Core ML, Neural Engine with CPU fallback")
			} else {
				add("english model", "fail", "not installed (137 MB): tts-axi doctor --fix")
			}
			speed := a.settings.SpeedOrDefault()
			if dir, ok := speedReady(e, speed); ok {
				add("speed", "ok", fmt.Sprintf("duration_delta %g (%s)", speed, filepath.Base(dir)))
			} else if e.BundleInstalled() {
				add("speed", "warn", fmt.Sprintf("duration_delta %g is prepared on first use (reads 128 KiB from Hugging Face)", speed))
			}
			if _, err := exec.LookPath("uv"); err == nil {
				add("french, portuguese", "ok", "ptts "+engine.PttsVersion+" on the CPU, run by uv")
			} else {
				add("french, portuguese", "warn", "uv is missing (brew install uv); English still works")
			}
			add(multilingualStatus(e))
			add(qwenStatus(a, e))
			add(memoryStatus(a))
			add(libraryStatus(a))
			missing := missingSkillDirs()
			if len(missing) == 0 {
				add("skill", "ok", "installed for Claude, Codex and Pi")
			} else {
				add("skill", "warn", "not installed in "+strings.Join(missing, ", ")+": tts-axi skill install")
			}

			doc := output.Obj{}.Set("checks", rows)
			var help []string
			if fixErr != nil {
				help = append(help, "the fix stopped at a step; the error follows")
			}
			if failed && !fix {
				help = append(help, "tts-axi doctor --fix installs what is missing")
			}
			if !failed {
				help = append(help, `tts-axi say "Hello" to try it`)
			}
			if fixErr != nil {
				a.printer.Emit(doc.Set("help", help))
				return output.Fail([]string{"run `tts-axi doctor --fix` again; finished steps are kept"}, "%v", fixErr)
			}
			a.printer.Emit(doc.Set("help", help))
			if failed {
				return output.ExitWith(output.ExitFailure)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&fix, "fix", false, "install the runtime, helper and model")
	return c
}

// speedReady reports whether the bundle for a speed exists without making it.
func speedReady(e *engine.Engine, speed float64) (string, bool) {
	dir := e.SpeedPath(speed)
	_, err := os.Stat(filepath.Join(dir, "host.safetensors"))
	return dir, err == nil
}

// multilingualStatus says whether the French and Portuguese model folder is
// in place.
func multilingualStatus(e *engine.Engine) (string, string, string) {
	const name = "multilingual model"
	if e.MultilingualInstalled() {
		return name, "ok", "phonon-40m-multilingual, downloaded from Ollama's registry"
	}
	return name, "warn", "not downloaded (122 MB, no account needed): tts-axi doctor --fix; English works without it"
}

// qwenStatus says whether the cloned-voice model is in place and how many
// voices use it.
func qwenStatus(a *App, e *engine.Engine) (string, string, string) {
	const name = "cloned-voice model"
	n := len(a.voices.Clones())
	if e.QwenInstalled() {
		return name, "ok", fmt.Sprintf("Qwen3-TTS 1.7B Base 8-bit through mlx-audio %s; cloned voices: %d", engine.MlxAudioVersion, n)
	}
	return name, "warn", "not downloaded (3 GB, no account needed): tts-axi doctor --fix; built-in voices work without it"
}

func memoryStatus(a *App) (string, string, string) {
	s, err := a.deps.Sampler.Sample()
	if err != nil {
		return "memory", "warn", "cannot read memory pressure: " + err.Error()
	}
	detail := fmt.Sprintf("pressure %s, swap used %d MiB", guard.PressureName(s.Pressure), s.SwapUsedBytes>>20)
	switch {
	case s.Pressure >= guard.Critical:
		return "memory", "fail", detail + "; renders wait for it to settle"
	case s.Pressure > guard.Normal:
		return "memory", "warn", detail + "; English starts on the CPU until pressure is normal"
	}
	return "memory", "ok", detail
}

func libraryStatus(a *App) (string, string, string) {
	dir := a.paths.Library
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		probe, err := os.CreateTemp(dir, ".write-test-*")
		if err != nil {
			return "library", "fail", dir + " is not writable"
		}
		_ = probe.Close()
		_ = os.Remove(probe.Name())
		return "library", "ok", dir
	}
	return "library", "ok", dir + " (created on the first render)"
}
