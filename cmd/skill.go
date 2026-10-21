package cmd

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/marcellovictorino/tts-axi/internal/config"
	"github.com/marcellovictorino/tts-axi/internal/output"
	"github.com/marcellovictorino/tts-axi/skills"
)

// skillDirs are the agents' skills folders: Claude Code, Codex (and other
// agents reading the shared ~/.agents layout) and Pi.
type skillTarget struct{ agent, dir string }

func skillDirs() []skillTarget {
	home, _ := os.UserHomeDir()
	pi := filepath.Join(home, ".pi", "agent")
	if v := os.Getenv("PI_CODING_AGENT_DIR"); v != "" {
		pi = v
	}
	return []skillTarget{
		{"claude", filepath.Join(home, ".claude", "skills")},
		{"codex", filepath.Join(home, ".agents", "skills")},
		{"pi", filepath.Join(pi, "skills")},
	}
}

// missingSkillDirs lists the agents whose copy of the skill is absent or
// out of date.
func missingSkillDirs() []string {
	var out []string
	for _, d := range skillDirs() {
		dest := filepath.Join(d.dir, "tts-axi", "SKILL.md")
		if old, err := os.ReadFile(dest); err != nil || string(old) != string(skills.Skill) {
			out = append(out, d.agent)
		}
	}
	return out
}

func newSkillCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "skill",
		Short: "Manage the tts-axi skill for coding agents",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	var dir string
	install := &cobra.Command{
		Use:   "install",
		Short: "Install the tts-axi skill for Claude, Codex and Pi",
		Long: `Write the embedded SKILL.md to <skills dir>/tts-axi/SKILL.md for each agent:
Claude Code (~/.claude/skills), Codex and other agents reading ~/.agents/skills,
and Pi (~/.pi/agent/skills; PI_CODING_AGENT_DIR is honoured). --dir installs
into one folder instead. Running it again only rewrites a changed skill.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			targets := skillDirs()
			if dir != "" {
				targets = []skillTarget{{"custom", dir}}
			}
			var rows []output.Obj
			for _, t := range targets {
				dest := filepath.Join(t.dir, "tts-axi", "SKILL.md")
				status := "installed"
				if old, err := os.ReadFile(dest); err == nil {
					if string(old) == string(skills.Skill) {
						status = "already installed"
					} else {
						status = "updated"
					}
				}
				if status != "already installed" {
					if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
						return output.Fail(nil, "%v", err)
					}
					if err := config.WriteFileAtomic(dest, skills.Skill, 0o644); err != nil {
						return output.Fail(nil, "%v", err)
					}
				}
				rows = append(rows, output.Obj{}.Set("agent", t.agent).Set("status", status).Set("path", dest))
			}
			a.printer.Emit(output.Obj{}.Set("skill", rows).
				Set("help", []string{"agents load skills when they start; restart open sessions to pick it up"}))
			return nil
		},
	}
	install.Flags().StringVar(&dir, "dir", "", "install into this skills folder only")
	c.AddCommand(install)
	return c
}
