// Command tts-axi turns text, scripts and slide notes into audio with Phonon.
package main

import (
	"os"

	"github.com/marcellovictorino/tts-axi/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
