// Package config resolves where tts-axi keeps its files and reads the optional
// settings file.
//
// Three places matter:
//
//   - Home (~/.tts-axi, or TTS_AXI_HOME): the Phonon runtime, the built
//     helper, the model bundles and the render lock. Safe to delete; `tts-axi
//     doctor --fix` rebuilds it.
//   - Library (~/Music/tts-axi, or TTS_AXI_LIBRARY): one folder per episode,
//     plus index.html and feed.xml. This is what a person plays and deletes.
//   - Settings (~/.config/tts-axi/config.toml): optional overrides.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Paths are the resolved locations.
type Paths struct {
	Home    string
	Library string
	Config  string // the settings file, which may not exist
}

// Resolve reads the environment overrides and falls back to the defaults.
func Resolve() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("cannot find the home directory: %w", err)
	}
	p := Paths{
		Home:    filepath.Join(home, ".tts-axi"),
		Library: filepath.Join(home, "Music", "tts-axi"),
	}
	cfgDir := os.Getenv("XDG_CONFIG_HOME")
	if cfgDir == "" {
		cfgDir = filepath.Join(home, ".config")
	}
	p.Config = filepath.Join(cfgDir, "tts-axi", "config.toml")
	if v := os.Getenv("TTS_AXI_HOME"); v != "" {
		p.Home = v
	}
	if v := os.Getenv("TTS_AXI_LIBRARY"); v != "" {
		p.Library = v
	}
	return p, nil
}

// RunDir holds locks and the staging folders of renders in progress.
func (p Paths) RunDir() string { return filepath.Join(p.Home, "run") }

// RuntimeDir holds Gradium's released Swift runtime.
func (p Paths) RuntimeDir() string { return filepath.Join(p.Home, "runtime") }

// HelperBin is the built Swift helper.
func (p Paths) HelperBin() string { return filepath.Join(p.Home, "helper", "tts-axi-helper") }

// ModelsDir holds the Core ML bundles.
func (p Paths) ModelsDir() string { return filepath.Join(p.Home, "models") }

// PttsDir holds the Python helper script for the multilingual route.
func (p Paths) PttsDir() string { return filepath.Join(p.Home, "ptts") }

// QwenHelperDir holds the Python helper script for cloned voices.
func (p Paths) QwenHelperDir() string { return filepath.Join(p.Home, "qwen") }

// VoicesDir holds the cloned voices: a clip and its words per voice. It sits
// beside the models, outside the library and any repository.
func (p Paths) VoicesDir() string { return filepath.Join(p.Home, "voices") }

// Settings are the values a person may change in config.toml. Every field is
// optional; zero values mean "use the default".
type Settings struct {
	// Speed is Phonon's duration_delta: 0 is the model's pace, positive is
	// slower. nil means the default, 0.15.
	Speed *float64 `toml:"speed"`
	// Voices overrides the default voice order per language, for example
	// voices.en = ["Hugh", "Bryony"].
	Voices map[string][]string `toml:"voices"`
	// Library moves the library folder (TTS_AXI_LIBRARY still wins).
	Library string `toml:"library"`
	// BaseURL is where the library is served, for example a Tailscale address.
	// feed.xml needs it for absolute links; without it links are relative.
	BaseURL string `toml:"base_url"`
}

// DefaultSpeed is the duration_delta used when none is configured: a little
// slower than the model's own pace.
const DefaultSpeed = 0.15

// Load reads the settings file if it exists and applies its library override.
func Load(p *Paths) (Settings, error) {
	var s Settings
	data, err := os.ReadFile(p.Config)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	md, err := toml.Decode(string(data), &s)
	if err != nil {
		return s, fmt.Errorf("%s: %w", p.Config, err)
	}
	if extra := md.Undecoded(); len(extra) > 0 {
		keys := make([]string, len(extra))
		for i, k := range extra {
			keys[i] = k.String()
		}
		return s, fmt.Errorf("%s: unknown setting %s (valid: speed, voices, library, base_url)", p.Config, strings.Join(keys, ", "))
	}
	if s.Library != "" && os.Getenv("TTS_AXI_LIBRARY") == "" {
		p.Library = expandHome(s.Library)
	}
	return s, nil
}

// SpeedOrDefault returns the configured speed or DefaultSpeed.
func (s Settings) SpeedOrDefault() float64 {
	if s.Speed == nil {
		return DefaultSpeed
	}
	return *s.Speed
}

func expandHome(path string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return path
}
