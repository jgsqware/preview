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

// Choice : une entrée du sélecteur.
type Choice struct{ Name, Detail string }

// Choose : liste numérotée, lecture d'un numéro sur l'entrée standard ; -1 si rien de valide.
func Choose(title string, items []Choice, cur int) int {
	fmt.Println(title)
	for i, it := range items {
		fmt.Printf("  %d. %s  %s\n", i+1, it.Name, it.Detail)
	}
	fmt.Print("numéro (vide = quitter) : ")
	var n int
	if _, err := fmt.Scanln(&n); err != nil || n < 1 || n > len(items) {
		return -1
	}
	return n - 1
}
