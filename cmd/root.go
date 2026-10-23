package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/marcellovictorino/tts-axi/internal/output"
)

func newRoot(deps Deps) (*cobra.Command, *App) {
	app := &App{deps: deps}
	var showVersion bool

	root := &cobra.Command{
		Use:   "tts-axi",
		Short: "Turn text, scripts and slide notes into audio, locally",
		Long: `tts-axi turns text, multi-voice scripts and slide speaker notes into audio
with Phonon, on this Mac's Neural Engine, and keeps the results in a library
folder (~/Music/tts-axi) with a web page and a podcast feed.

Pipe any command (or pass --format toon) to get compact, agent-friendly TOON
output instead of tables. First run: tts-axi doctor --fix.`,
		SilenceUsage:  true, // we print our own errors
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				return printVersion(app)
			}
			return cmd.Help()
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			p, err := output.NewPrinter(deps.Out, deps.Err, app.format, deps.StdoutTTY)
			if err != nil {
				return output.Usage([]string{"valid formats: toon, human"}, "%v", err)
			}
			app.printer = p
			return nil
		},
	}
	root.SetOut(deps.Out)
	root.SetErr(deps.Err)
	root.PersistentFlags().StringVar(&app.format, "format", "", "output format: toon or human (default: human on a terminal, toon otherwise)")

	// Flag and argument mistakes become exit-code-2 usage errors that list
	// what would have been valid.
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return output.Usage(validFlagsHelp(c), "%v", err)
	})
	root.SetHelpFunc(func(c *cobra.Command, _ []string) {
		if c.Long != "" {
			fmt.Fprintln(deps.Out, strings.TrimSpace(c.Long))
		} else {
			fmt.Fprintln(deps.Out, c.Short)
		}
		fmt.Fprint(deps.Out, "\n", c.UsageString())
	})
	root.Flags().BoolVar(&showVersion, "version", false, "print the tts-axi version")

	root.AddCommand(
		newSayCmd(app),
		newRenderCmd(app),
		newDeckCmd(app),
		newVoicesCmd(app),
		newLibraryCmd(app),
		newDoctorCmd(app),
		newSkillCmd(app),
		newVersionCmd(app),
	)
	return root, app
}

// validFlagsHelp lists the flags a command accepts, for usage errors.
func validFlagsHelp(c *cobra.Command) []string {
	var names []string
	add := func(f *pflag.Flag) { names = append(names, "--"+f.Name) }
	c.LocalFlags().VisitAll(add)
	c.InheritedFlags().VisitAll(add)
	sort.Strings(names)
	help := []string{"valid flags: " + strings.Join(names, ", ")}
	if len(c.Commands()) > 0 {
		var subs []string
		for _, s := range c.Commands() {
			if !s.Hidden && s.Name() != "help" && s.Name() != "completion" {
				subs = append(subs, s.Name())
			}
		}
		help = append(help, "valid commands: "+strings.Join(subs, ", "))
	}
	return help
}
