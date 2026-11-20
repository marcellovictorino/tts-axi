// Package skills embeds the agent skill that `tts-axi skill install` writes
// into Claude's, Codex's and Pi's skills folders.
//
// //go:embed is a compiler directive: the named files are compiled into the
// binary, so the installed tts-axi needs no files next to it.
package skills

import (
	"embed"
	"io/fs"
	"sort"
)

//go:embed tts-axi
var files embed.FS

// File is one file of the skill, by its path inside the skill folder.
type File struct {
	Path string // SKILL.md, references/slidev.md
	Data []byte
}

// Files lists the skill's files: SKILL.md and its references.
func Files() []File {
	var out []File
	_ = fs.WalkDir(files, "tts-axi", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, File{Path: p[len("tts-axi/"):], Data: data})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
