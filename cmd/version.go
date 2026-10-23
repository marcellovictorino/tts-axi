package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/marcellovictorino/tts-axi/internal/output"
	"github.com/marcellovictorino/tts-axi/internal/version"
)

func newVersionCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the tts-axi version",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return printVersion(a) },
	}
}

// printVersion shows the version: one line for a person, TOON for an agent.
func printVersion(a *App) error {
	if a.printer.Human {
		fmt.Fprintln(a.deps.Out, version.String())
		return nil
	}
	a.printer.Emit(output.Obj{}.Set("version", version.Version).Set("commit", version.Commit()))
	return nil
}
