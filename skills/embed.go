// Package skills embeds the agent skill that `tts-axi skill install` writes
// into Claude's, Codex's and Pi's skills folders.
//
// //go:embed is a compiler directive: the named file is compiled into the
// binary, so the installed tts-axi needs no files next to it.
package skills

import _ "embed"

// Skill is skills/tts-axi/SKILL.md.
//
//go:embed tts-axi/SKILL.md
var Skill []byte
