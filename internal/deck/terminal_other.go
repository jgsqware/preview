//go:build !unix

package deck

import (
	"fmt"
	"os"
)

// LiveHooks : touches d / w / r du deck terminal (nil = pas de preview.toml).
type LiveHooks interface {
	Run(scenario string)
	Watch()
	Replay(scenario string)
}

// RunTerminal : sans terminal Unix, le deck est imprimé à plat.
func RunTerminal(d *Deck, start int, printAll bool, live LiveHooks) int {
	if len(d.Slides) == 0 {
		fmt.Println("deck vide")
		return 1
	}
	os.Stdout.WriteString(RenderAll(d, false, WIDTH))
	return 0
}
