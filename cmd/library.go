package cmd

import (
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/marcellovictorino/tts-axi/internal/library"
	"github.com/marcellovictorino/tts-axi/internal/output"
)

func newLibraryCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "library",
		Short: "List the episodes in the library folder",
		Long: `List the episodes in the library (~/Music/tts-axi, or TTS_AXI_LIBRARY), newest
first. Each episode is a folder you can delete in Finder; run
"tts-axi library rebuild" afterwards to refresh index.html and feed.xml, or
use "tts-axi library rm <episode>", which does both.

To play the library on your phone over Tailscale (private to your tailnet):

  tailscale serve --bg ~/Music/tts-axi

then open the address it prints on the phone. Set base_url in
~/.config/tts-axi/config.toml to that address so feed.xml has full links.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.load(); err != nil {
				return err
			}
			lib := a.library()
			eps, err := lib.List()
			if err != nil {
				return output.Fail(nil, "%v", err)
			}
			rows := make([]output.Obj, 0, len(eps))
			for _, ep := range eps {
				rows = append(rows, output.Obj{}.Set("episode", ep.ID).Set("title", ep.Title).Set("kind", ep.Kind).
					Set("length", library.FormatSeconds(ep.Seconds)).Set("model", strings.Join(ep.Models, ",")).Set("engine", strings.Join(ep.Engines, ",")))
			}
			doc := output.Obj{}.Set("library", lib.Dir).Set("episodes", rows)
			help := []string{"open " + filepath.Join(lib.Dir, "index.html") + " to play them"}
			if len(eps) > 0 {
				help = append(help, "tts-axi library rm <episode> to delete one")
			} else {
				help = append(help, `tts-axi say "Hello" to make the first one`)
			}
			if lib.BaseURL == "" {
				help = append(help, "phone: `tailscale serve --bg "+shellQuote(lib.Dir)+"`, then set base_url in "+a.paths.Config)
			}
			a.printer.Emit(doc.Set("help", help))
			return nil
		},
	}
	rm := &cobra.Command{
		Use:               "rm <episode>...",
		ValidArgsFunction: completeEpisodes(a),
		Short:             "Delete episodes and refresh the index and feed",
		Args:              cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.load(); err != nil {
				return err
			}
			lib := a.library()
			for _, id := range args {
				if err := lib.Remove(id); err != nil {
					return output.Fail([]string{"`tts-axi library` lists the episodes"}, "%v", err)
				}
			}
			a.printer.Emit(output.Obj{}.Set("removed", args))
			return nil
		},
	}
	rebuild := &cobra.Command{
		Use:   "rebuild",
		Short: "Rewrite index.html and feed.xml from the episode folders",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.load(); err != nil {
				return err
			}
			lib := a.library()
			if err := lib.Rebuild(); err != nil {
				return output.Fail(nil, "%v", err)
			}
			eps, _ := lib.List()
			a.printer.Emit(output.Obj{}.Set("index", filepath.Join(lib.Dir, "index.html")).
				Set("feed", filepath.Join(lib.Dir, "feed.xml")).Set("episodes", len(eps)))
			return nil
		},
	}
	c.AddCommand(rm, rebuild)
	return c
}
