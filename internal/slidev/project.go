package slidev

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// layer is the narration layer written into every narrated deck: the player
// (global-top.vue, which also holds the marks, spotlight and bar styles), the
// laser (slide-top.vue), and the CountUp and PiTerminal components. Slidev
// picks these files up by name. //go:embed compiles the folder into the
// binary.
//
//go:embed layer
var layer embed.FS

// Recorder is the Node script that records a deck; see record.mjs.
//
//go:embed record.mjs
var Recorder []byte

// projectEntries are the files and folders of a Slidev project that a deck
// can rely on; they are copied beside the narrated slides.md, with anything
// the slides name by a ./ path (a local theme, ./assets/diagram.png).
// Anything else next to the deck (node_modules, dist, other files) is left
// behind.
var projectEntries = []string{
	"components", "layouts", "public", "styles", "setup", "snippets", "pages",
	"style.css", "global-bottom.vue", "custom-nav-controls.vue",
	"vite.config.ts", "vite.config.js", "uno.config.ts", "unocss.config.ts",
}

// localRefRE finds ./path references in slides, bare or in a Markdown
// <./path> destination.
var localRefRE = regexp.MustCompile(`(?:^|[\s(\[{"'=:,<])\./([A-Za-z0-9_][^\s)\]}"'<>]*)`)

// inlineCodeRE is a `code span`; with code blocks and comments (notes) it
// is text Slidev shows or ignores, never a file it loads.
var inlineCodeRE = regexp.MustCompile("`[^`\n]+`")

// entries is projectEntries plus the paths the slides refer to: only the
// file or folder named, never its neighbours, and never outside the deck.
func (p Project) entries() []string {
	names := append([]string(nil), projectEntries...)
	// A path in a code example or the notes is not loaded, so it is not copied.
	loaded := inlineCodeRE.ReplaceAllString(commentRE.ReplaceAllString(codeRE.ReplaceAllString(p.Slides, ""), ""), "")
	for _, m := range localRefRE.FindAllStringSubmatch(loaded, -1) {
		ref := m[1]
		if i := strings.IndexAny(ref, "?#"); i >= 0 {
			ref = ref[:i] // ./diagram.svg#detail names diagram.svg
		}
		name := filepath.Clean(filepath.FromSlash(ref))
		parts := strings.Split(name, string(filepath.Separator))
		if name == "." || contains(parts, "..") || contains(parts, "node_modules") || contains(parts, ".git") ||
			contains(names, name) || contains(projectEntries, parts[0]) {
			continue
		}
		names = append(names, name)
	}
	return names
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Project is what a narrated deck folder needs.
type Project struct {
	SourceDir string  // the folder holding the original slides.md
	Slides    string  // the narrated slides.md
	Audio     string  // the deck's audio file, copied into public/
	Total     float64 // seconds of narration
	Package   []byte  // package.json pinning Slidev
	Lock      []byte  // its lockfile
}

// narrationNames are the files tts-axi writes the narration to.
var narrationNames = []string{"public/tts-axi-narration.mp3", "public/tts-axi-narration.m4a"}

// LayerConflicts lists the layer files the source deck already has; tts-axi
// will not overwrite a deck's own player, laser, components or audio.
func LayerConflicts(sourceDir string) []string {
	var out []string
	for _, n := range narrationNames {
		if _, err := os.Stat(filepath.Join(sourceDir, filepath.FromSlash(n))); err == nil {
			out = append(out, n)
		}
	}
	_ = fs.WalkDir(layer, "layer", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, "layer/")
		if _, err := os.Stat(filepath.Join(sourceDir, filepath.FromSlash(rel))); err == nil {
			out = append(out, rel)
		}
		return nil
	})
	return out
}

// Write builds the narrated deck folder at dir.
func (p Project) Write(dir string) error {
	if c := LayerConflicts(p.SourceDir); len(c) > 0 {
		return fmt.Errorf("the deck already has %s, which tts-axi's narration layer provides", strings.Join(c, ", "))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	abs, err := filepath.Abs(p.SourceDir)
	if err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return err
	}
	for _, name := range p.entries() {
		src := filepath.Join(root, name)
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		// A folder on the way could itself be a link out of the deck.
		if real, err := filepath.EvalSymlinks(filepath.Dir(src)); err != nil || !inside(root, real) {
			return fmt.Errorf("%s links outside the deck folder; copy the file into the deck instead", name)
		}
		dest := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := copyTree(root, src, dest); err != nil {
			return err
		}
	}
	// A name of tts-axi's own, so a deck's public/deck.mp3 keeps its URL.
	audioName := "tts-axi-narration" + filepath.Ext(p.Audio)
	err = fs.WalkDir(layer, "layer", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := layer.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, "global-top.vue") {
			s := strings.ReplaceAll(string(data), "__TTS_AXI_TOTAL__", strconv.FormatFloat(p.Total, 'f', 3, 64))
			data = []byte(strings.ReplaceAll(s, "__TTS_AXI_AUDIO__", audioName))
		}
		dest := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(path, "layer/")))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "public"), 0o755); err != nil {
		return err
	}
	files := map[string][]byte{"slides.md": []byte(p.Slides), "package.json": p.Package, "package-lock.json": p.Lock}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}
	return copyFile(p.Audio, filepath.Join(dir, "public", audioName))
}

// copyTree copies a file or folder from inside root. A link is copied as
// what it points to, but only when that is inside root too: a deck must not
// carry a private file from elsewhere on the Mac into a published episode.
func copyTree(root, src, dest string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dest, rel)
		if d.Type()&fs.ModeSymlink != 0 {
			real, err := filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			if !inside(root, real) {
				inDeck, _ := filepath.Rel(root, path)
				return fmt.Errorf("%s links outside the deck folder; copy the file into the deck instead", inDeck)
			}
			if strings.HasPrefix(path, real+string(filepath.Separator)) {
				return nil // a link to a folder holding it would copy forever
			}
			return copyTree(root, real, target)
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil // sockets, devices and pipes are not deck files
		}
		return copyFile(path, target)
	})
}

// copyFile streams src to dest, so a large video in public/ never sits in
// memory whole.
func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// inside reports whether path is root or below it.
func inside(root, path string) bool {
	r, err := filepath.Rel(root, path)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}
