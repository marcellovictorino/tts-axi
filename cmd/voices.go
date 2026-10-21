package cmd

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/marcellovictorino/tts-axi/internal/engine"
	"github.com/marcellovictorino/tts-axi/internal/output"
	"github.com/marcellovictorino/tts-axi/internal/voices"
)

func newVoicesCmd(a *App) *cobra.Command {
	var lang string
	var full bool
	c := &cobra.Command{
		Use:   "voices",
		Short: "List the voices: your defaults first, then the catalog",
		Long: `List Phonon's catalog voices for English, Portuguese and French.

Default voices come first, in order: the first speaks when no voice is given,
the next ones go to a script's second, third... speaker. Change the order in
~/.config/tts-axi/config.toml, for example voices.en = ["Bryony", "Hugh"].`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.load(); err != nil {
				return err
			}
			if lang != "" && !voices.IsLanguage(lang) {
				return output.Usage([]string{"valid languages: " + strings.Join(voices.Languages, ", ")}, "unknown --lang %q", lang)
			}
			all := voices.Catalog()
			langRank := map[string]int{}
			for i, l := range voices.Languages {
				langRank[l] = i
			}
			sort.SliceStable(all, func(i, j int) bool {
				ri, rj := a.voices.Rank(all[i]), a.voices.Rank(all[j])
				if (ri > 0) != (rj > 0) {
					return ri > 0
				}
				if all[i].Lang != all[j].Lang {
					return langRank[all[i].Lang] < langRank[all[j].Lang]
				}
				if ri != rj {
					return ri < rj
				}
				return all[i].Name < all[j].Name
			})
			var rows []output.Obj
			for _, v := range all {
				if lang != "" && v.Lang != lang {
					continue
				}
				def := ""
				if r := a.voices.Rank(v); r > 0 {
					def = "default"
					if r > 1 {
						def = "speaker " + string(rune('0'+r))
					}
				}
				desc := v.Description
				if !full {
					desc = shorten(desc, 60)
				}
				engineName := string(engine.ANE)
				if v.Lang != "en" {
					engineName = string(engine.PttsCPU)
				}
				rows = append(rows, output.Obj{}.Set("name", v.Name).Set("lang", v.Lang).Set("default", def).Set("engine", engineName).Set("description", desc))
			}
			a.printer.Emit(output.Obj{}.Set("voices", rows).Set("help", []string{
				`tts-axi say "Hello" --voice <name>`,
				"in a script, start a line with the voice's name: Bryony: Hello",
			}))
			return nil
		},
	}
	c.Flags().StringVar(&lang, "lang", "", "only this language: en, pt or fr")
	c.Flags().BoolVar(&full, "full", false, "show whole descriptions")
	return c
}

// shorten cuts s at a word boundary.
func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndex(cut, " "); i > len(cut)/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;") + "…"
}
