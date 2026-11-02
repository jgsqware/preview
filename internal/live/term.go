package live

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/jgsqware/preview/internal/tui"
)

// ReadKeys : touches décodées (up, down, pgup, pgdn, end, ou le caractère) depuis le terminal brut.
func ReadKeys(f *os.File) <-chan string {
	ch := make(chan string, 16)
	go func() {
		buf := make([]byte, 64)
		for {
			n, err := f.Read(buf)
			if err != nil || n == 0 {
				close(ch)
				return
			}
			for _, k := range DecodeKeys(string(buf[:n])) {
				ch <- k
			}
		}
	}()
	return ch
}

var seqs = map[string]string{
	"\x1b[A": "up", "\x1b[B": "down", "\x1b[C": "right", "\x1b[D": "left",
	"\x1bOA": "up", "\x1bOB": "down", "\x1bOC": "right", "\x1bOD": "left",
	"\x1b[5~": "pgup", "\x1b[6~": "pgdn", "\x1b[H": "home", "\x1b[F": "end",
	"\x1bOH": "home", "\x1bOF": "end", "\x1b[1~": "home", "\x1b[4~": "end",
}

// DecodeKeys : découpe un bloc lu en touches.
func DecodeKeys(s string) []string {
	var out []string
	for s != "" {
		if s[0] == 0x1b {
			found := false
			for seq, name := range seqs {
				if strings.HasPrefix(s, seq) {
					out, s, found = append(out, name), s[len(seq):], true
					break
				}
			}
			if found {
				continue
			}
			// séquence inconnue : on l'ignore jusqu'à sa lettre finale
			i := 1
			if len(s) > 1 && (s[1] == '[' || s[1] == 'O') {
				i = 2
				for i < len(s) && !(s[i] >= 0x40 && s[i] <= 0x7e) {
					i++
				}
				i++
			}
			if i == 1 {
				out = append(out, "esc")
			}
			s = s[min(i, len(s)):]
			continue
		}
		r := []rune(s)[0]
		out = append(out, string(r))
		s = s[len(string(r)):]
	}
	return out
}

// RunTUI : boucle plein écran ; rend la main quand l'utilisateur quitte.
func RunTUI(a *App) error {
	if !term.IsTerminal(int(os.Stdout.Fd())) || !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("il faut un terminal (ou --once)")
	}
	if err := a.Start(); err != nil {
		return err
	}
	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	out := os.Stdout
	fmt.Fprint(out, "\x1b[?1049h\x1b[?25l\x1b[?7l")
	defer func() {
		fmt.Fprint(out, "\x1b[0m\x1b[?7h\x1b[?25h\x1b[?1049l")
		_ = term.Restore(int(os.Stdin.Fd()), old)
		a.Kill()
		if a.Mode == "run" {
			a.WaitMain(10 * time.Second) // laisse le run se terminer et fermer l'enregistrement
		}
		a.Stop()
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sig)
	keys := ReadKeys(os.Stdin)
	attrs := tui.Attrs(tui.ColorOn(true))
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	lastW, lastH := 0, 0
	for {
		w, h, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil {
			w, h = 120, 40
		}
		if w != lastW || h != lastH {
			fmt.Fprint(out, "\x1b[2J")
			lastW, lastH = w, h
		}
		fmt.Fprint(out, "\x1b[?2026h"+a.Render(h, w).ANSI(attrs)+"\x1b[?2026l")
		a.frame++
		select {
		case k, ok := <-keys:
			if !ok || !a.Key(k) {
				return nil
			}
		case s := <-sig:
			if s != syscall.SIGWINCH {
				return nil
			}
		case <-tick.C:
		}
	}
}
