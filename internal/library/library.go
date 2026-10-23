// Package library keeps the audio tts-axi makes in one folder a person can
// browse, play and clean up: one sub-folder per episode, each with its audio,
// transcript and an episode.json, plus an index.html page and a feed.xml
// podcast feed at the top that are rewritten after every change.
package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/marcellovictorino/tts-axi/internal/config"
)

// Episode is one entry, stored as episode.json in its folder.
type Episode struct {
	ID       string    `json:"id"` // the folder name
	Title    string    `json:"title"`
	Kind     string    `json:"kind"` // say, render or deck
	Created  time.Time `json:"created"`
	Seconds  float64   `json:"seconds"`
	Audio    string    `json:"audio"`              // file name inside the folder
	Player   string    `json:"player,omitempty"`   // deck player page
	Engines  []string  `json:"engines"`            // routes that spoke it
	Voices   []string  `json:"voices"`             // voices heard
	Lang     []string  `json:"lang"`               // languages
	Chapters []string  `json:"chapters,omitempty"` // chapter titles
	Fallback string    `json:"fallback,omitempty"` // why the first-choice engine was not used

	Bytes int64 `json:"-"` // the audio file's size, read when listing
}

// Library is the folder.
type Library struct {
	Dir     string
	BaseURL string // where the folder is served; "" for relative links
}

const metaFile = "episode.json"

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

// Slug turns a title into a folder-friendly name.
func Slug(title string) string {
	s := strings.Trim(slugRE.ReplaceAllString(strings.ToLower(asciiFold(title)), "-"), "-")
	if len(s) > 48 {
		s = strings.TrimRight(s[:48], "-")
	}
	if s == "" {
		s = "episode"
	}
	return s
}

// asciiFold maps the accented letters common in French and Portuguese to
// plain ones, so "Olá, manhã" becomes "ola-manha" rather than "ol-manh".
func asciiFold(s string) string {
	r := strings.NewReplacer(
		"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
		"é", "e", "è", "e", "ê", "e", "ë", "e",
		"í", "i", "î", "i", "ï", "i",
		"ó", "o", "ô", "o", "õ", "o", "ö", "o",
		"ú", "u", "ù", "u", "û", "u", "ü", "u",
		"ç", "c", "ñ", "n", "œ", "oe", "æ", "ae")
	return r.Replace(strings.ToLower(s))
}

// NewID returns an unused folder name: date, then the slug, with a number
// added when the same title was used the same day.
func (l Library) NewID(title string, now time.Time) string {
	base := now.Format("2006-01-02") + "-" + Slug(title)
	id := base
	for n := 2; ; n++ {
		if _, err := os.Stat(filepath.Join(l.Dir, id)); errors.Is(err, os.ErrNotExist) {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

// Publish moves a finished episode folder into the library under ep.ID,
// writes its episode.json and rewrites the index and feed. The move is a
// rename, so a reader never sees a half-copied episode.
func (l Library) Publish(staged string, ep Episode) (string, error) {
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(ep, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(staged, metaFile), append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	dest := filepath.Join(l.Dir, ep.ID)
	if err := moveDir(staged, dest); err != nil {
		return "", err
	}
	return dest, l.Rebuild()
}

// moveDir renames, or copies then removes when the staging folder is on
// another volume.
func moveDir(src, dest string) error {
	if err := os.Rename(src, dest); err == nil {
		return nil
	}
	if err := os.CopyFS(dest, os.DirFS(src)); err != nil {
		_ = os.RemoveAll(dest)
		return err
	}
	return os.RemoveAll(src)
}

// List returns the episodes, newest first. Folders without a readable
// episode.json are skipped.
func (l Library) List() ([]Episode, error) {
	entries, err := os.ReadDir(l.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var eps []Episode
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(l.Dir, ent.Name(), metaFile))
		if err != nil {
			continue
		}
		var ep Episode
		if json.Unmarshal(data, &ep) != nil {
			continue
		}
		ep.ID = ent.Name()
		if fi, err := os.Stat(filepath.Join(l.Dir, ep.ID, ep.Audio)); err == nil {
			ep.Bytes = fi.Size()
		}
		eps = append(eps, ep)
	}
	sort.Slice(eps, func(i, j int) bool {
		if !eps[i].Created.Equal(eps[j].Created) {
			return eps[i].Created.After(eps[j].Created)
		}
		return eps[i].ID > eps[j].ID
	})
	return eps, nil
}

// Remove deletes one episode folder and rewrites the index and feed.
func (l Library) Remove(id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return fmt.Errorf("%q is not an episode id", id)
	}
	dir := filepath.Join(l.Dir, id)
	if _, err := os.Stat(filepath.Join(dir, metaFile)); err != nil {
		return fmt.Errorf("no episode %q in %s", id, l.Dir)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return l.Rebuild()
}

// Rebuild rewrites index.html and feed.xml from the episode folders.
func (l Library) Rebuild() error {
	eps, err := l.List()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return err
	}
	if err := config.WriteFileAtomic(filepath.Join(l.Dir, "index.html"), []byte(renderIndex(eps)), 0o644); err != nil {
		return err
	}
	return config.WriteFileAtomic(filepath.Join(l.Dir, "feed.xml"), []byte(renderFeed(eps, l.BaseURL)), 0o644)
}
