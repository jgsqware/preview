//go:build unix

package deck

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/jgsqware/preview/internal/tui"
)

// Choice : une entrée du sélecteur.
type Choice struct{ Name, Detail string }

// Choose : sélecteur plein écran (↑ ↓ / 1-9 / Entrée, q pour quitter) ; renvoie l'index choisi ou -1.
// cur = entrée sélectionnée au départ. Sans TTY : -1.
func Choose(title string, items []Choice, cur int) int {
	in, out := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	if len(items) == 0 || !term.IsTerminal(in) || !term.IsTerminal(out) {
		return -1
	}
	t := &termDeck{p: NewPal(tui.ColorOn(true)), fd: in, resized: make(chan os.Signal, 1)}
	signal.Notify(t.resized, syscall.SIGWINCH)
	defer signal.Stop(t.resized)
	t.enter()
	defer t.leave()
	cur = max(0, min(cur, len(items)-1))
	p := t.p
	for {
		cols, rows := size()
		width := min(WIDTH, cols)
		padS := strings.Repeat(" ", max(0, (cols-width)/2)+3)
		var b strings.Builder
		b.WriteString("\033[H\033[2J")
		top := max(1, (rows-len(items)*2-6)/2)
		b.WriteString(strings.Repeat("\r\n", top))
		fmt.Fprintf(&b, "%s%s%s%s\r\n\r\n", padS, p.T, title, p.R)
		for i, it := range items {
			mark, name := "  ", it.Name
			if i == cur {
				mark, name = p.A+"▶ "+p.R, p.B+p.A+it.Name+p.R
			}
			num := ""
			if i < 9 {
				num = fmt.Sprintf("%s%d%s ", p.K, i+1, p.R)
			}
			fmt.Fprintf(&b, "%s%s%s%s\r\n", padS, mark, num, name)
			if it.Detail != "" {
				fmt.Fprintf(&b, "%s      %s%s%s\r\n", padS, p.K, clip(it.Detail, max(10, width-9)), p.R)
			}
		}
		fmt.Fprintf(&b, "\r\n%s%s↑ ↓ choisir · Entrée ouvrir · q quitter%s", padS, p.D, p.R)
		write(b.String())
		switch key := t.readKey(); key {
		case "q", "Q", "\x03", "\x1b":
			return -1
		case "\x1b[A", "\x1bOA", "k", "h", "\x1b[D", "\x1bOD":
			cur = (cur + len(items) - 1) % len(items)
		case "\x1b[B", "\x1bOB", "j", "l", "\x1b[C", "\x1bOC", "\t":
			cur = (cur + 1) % len(items)
		case "\r", "\n", " ":
			return cur
		default:
			if len(key) == 1 && key >= "1" && key <= "9" && int(key[0]-'1') < len(items) {
				return int(key[0] - '1')
			}
		}
	}
}
