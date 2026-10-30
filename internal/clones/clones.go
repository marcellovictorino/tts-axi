// Package clones stores cloned voices: for each, a 24 kHz mono WAV of the
// reference clip and the words spoken in it, in a folder per voice under
// ~/.tts-axi/voices. The folder is outside every repository and the library.
package clones

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/marcellovictorino/tts-axi/internal/audio"
	"github.com/marcellovictorino/tts-axi/internal/config"
)

// Files inside a voice's folder.
const (
	RefFile  = "reference.wav"
	TextFile = "text.txt"
)

// Voice is one stored clone.
type Voice struct {
	Name string
	Ref  string // path of the reference WAV
	Text string // the words spoken in it
}

var nameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)

// ValidName checks a voice name: a letter, then letters, digits, - or _, so it
// is safe as a folder name and as a script speaker.
func ValidName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("%q is not a valid voice name: start with a letter, then letters, digits, - or _ (up to 32)", name)
	}
	return nil
}

// CleanText prepares the words of a clip: double quote marks are dropped and
// lines are joined with single spaces. Apostrophes stay, since they are part
// of words.
func CleanText(s string) string {
	s = strings.NewReplacer(`"`, "", "“", "", "”", "").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// Add converts clip to the reference WAV and stores it with its words as
// root/<name>. It refuses to replace an existing voice.
func Add(ctx context.Context, ffmpeg, root, name, clip, words string) (Voice, error) {
	if err := ValidName(name); err != nil {
		return Voice{}, err
	}
	words = CleanText(words)
	if words == "" {
		return Voice{}, errors.New("the words of the clip are empty")
	}
	dir := filepath.Join(root, name)
	if _, err := os.Stat(dir); err == nil {
		return Voice{}, fmt.Errorf("voice %s already exists; remove the folder %s to replace it", name, dir)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Voice{}, err
	}
	tmp, err := os.MkdirTemp(root, ".add-")
	if err != nil {
		return Voice{}, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := audio.ToReference(ctx, ffmpeg, clip, filepath.Join(tmp, RefFile)); err != nil {
		return Voice{}, fmt.Errorf("converting %s: %w", clip, err)
	}
	if err := config.WriteFileAtomic(filepath.Join(tmp, TextFile), []byte(words+"\n"), 0o600); err != nil {
		return Voice{}, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return Voice{}, err
	}
	return Voice{Name: name, Ref: filepath.Join(dir, RefFile), Text: words}, nil
}

// List reads every stored voice, sorted by name. A folder without both files
// is skipped.
func List(root string) ([]Voice, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Voice
	for _, e := range entries {
		if !e.IsDir() || ValidName(e.Name()) != nil {
			continue
		}
		dir := filepath.Join(root, e.Name())
		text, err := os.ReadFile(filepath.Join(dir, TextFile))
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, RefFile)); err != nil {
			continue
		}
		out = append(out, Voice{Name: e.Name(), Ref: filepath.Join(dir, RefFile), Text: strings.TrimSpace(string(text))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
