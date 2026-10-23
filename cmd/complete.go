package cmd

import (
	"github.com/spf13/cobra"

	"github.com/marcellovictorino/tts-axi/internal/audio"
	"github.com/marcellovictorino/tts-axi/internal/voices"
)

// Shell completion. cobra generates the scripts (`tts-axi completion zsh`);
// these functions add the values it cannot know: voice names, languages,
// engines, formats and episode ids. Each returns the candidates and a
// directive telling the shell not to fall back to file names.

const noFiles = cobra.ShellCompDirectiveNoFileComp

func fixed(vals ...string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) { return vals, noFiles }
}

func completeVoices(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	var names []string
	for _, v := range voices.Catalog() {
		names = append(names, v.Name+"\t"+v.Lang)
	}
	return names, noFiles
}

// completeSpeakFlags registers completion for the flags say, render and deck share.
func completeSpeakFlags(c *cobra.Command) {
	_ = c.RegisterFlagCompletionFunc("lang", fixed(voices.Languages...))
	_ = c.RegisterFlagCompletionFunc("engine", fixed("auto", "cpu"))
	_ = c.RegisterFlagCompletionFunc("as", fixed(audio.Formats...))
	_ = c.RegisterFlagCompletionFunc("speed", fixed("0", "0.15", "0.3", "-0.1"))
}

// completeEpisodes offers the library's episode ids.
func completeEpisodes(a *App) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		if a.load() != nil {
			return nil, noFiles
		}
		eps, _ := a.library().List()
		var ids []string
		for _, ep := range eps {
			ids = append(ids, ep.ID+"\t"+ep.Title)
		}
		return ids, noFiles
	}
}
