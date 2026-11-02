// preview : présenter un projet — deck terminal + HTML, démo live, enregistrements rejouables.
package main

import (
	"os"

	"github.com/jgsqware/preview/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
