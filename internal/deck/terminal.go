//go:build unix

package deck

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/jgsqware/preview/internal/tui"
)

// LiveHooks : touches d / w / r du deck terminal (nil = pas de preview.toml).
type LiveHooks interface {
	Run(scenario string)
	Watch()
	Replay(scenario string)
}

// RunTerminal : deck dans le terminal ; start = index de diapositive (0…). Sans TTY : tout imprimé à plat.
func RunTerminal(d *Deck, start int, printAll bool, live LiveHooks) int {
	if len(d.Slides) == 0 {
		fmt.Println("deck vide")
		return 1
	}
	in, out := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	if printAll || !term.IsTerminal(in) || !term.IsTerminal(out) {
		os.Stdout.WriteString(RenderAll(d, tui.ColorOn(term.IsTerminal(out)), WIDTH))
		return 0
	}
	t := &termDeck{d: d, live: live, p: NewPal(tui.ColorOn(true)), fd: in}
	t.loop(start)
	return 0
}

type termDeck struct {
	d       *Deck
	live    LiveHooks
	p       *Pal
	i, k    int
	scroll  int
	notes   bool
	msg     string
	fd      int
	saved   *term.State
	resized chan os.Signal
}

func write(s string) { os.Stdout.WriteString(s) }

func (t *termDeck) enter() {
	if st, err := term.MakeRaw(t.fd); err == nil {
		t.saved = st
	}
	write("\033[?1049h\033[?25l")
}

func (t *termDeck) leave() {
	if t.saved != nil {
		_ = term.Restore(t.fd, t.saved)
		t.saved = nil
	}
	write("\033[?25h\033[?1049l")
}

func (t *termDeck) liveBlock() *Block {
	for _, b := range t.d.Slides[t.i].Blocks {
		if b.T == "live" {
			return b
		}
	}
	return nil
}

func size() (int, int) {
	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || cols <= 0 || rows <= 0 {
		return 80, 30
	}
	return cols, rows
}

func (t *termDeck) draw() {
	p, d := t.p, t.d
	cols, rows := size()
	width := min(WIDTH, cols)
	padS := strings.Repeat(" ", max(0, (cols-WIDTH)/2))
	s := d.Slides[t.i]
	state := map[string]int{}
	if len(s.Steps) > 0 {
		state = s.Steps[t.k]
	}
	r := &Renderer{p: p, w: width - 3, clipAt: cols - len(padS) - 3}
	var body []string
	for _, ln := range r.slide(s, state, t.notes) {
		if ln != "" {
			ln = "   " + ln
		}
		body = append(body, ln)
	}
	right := fmt.Sprintf("%s / %d", s.Label, d.Main())
	if s.Appendix {
		right = "Annexe " + s.Label
	}
	if len(s.Steps) > 1 {
		right = fmt.Sprintf("étape %d/%d · ", t.k+1, len(s.Steps)) + right
	}
	title := Plain(s.Title)
	room := width - 3 - len([]rune(right))
	if dwidth(title) > room {
		title = clip(title, room)
	}
	head := fmt.Sprintf("%s %s%s%s %s%s%s", padS, p.T, pad(title, room, ""), p.R, p.K, right, p.R)
	avail := max(1, rows-4)
	t.scroll = max(0, min(t.scroll, len(body)-avail))
	end := min(len(body), t.scroll+avail)
	view := body[t.scroll:end]
	more := len(body) - t.scroll - avail
	var dots strings.Builder
	for k, sl := range d.Slides {
		if sl.Appendix && (k == 0 || !d.Slides[k-1].Appendix) {
			dots.WriteString(" ")
		}
		ch := "●"
		if sl.Appendix {
			ch = "◆"
		}
		switch {
		case k == t.i:
			dots.WriteString(p.A + ch + p.R)
		case k < t.i:
			dots.WriteString(p.K + ch + p.R)
		default:
			c := "·"
			if sl.Appendix {
				c = "◇"
			}
			dots.WriteString(p.K + c + p.R)
		}
	}
	var keys string
	switch {
	case t.msg != "":
		keys = t.msg
	case t.liveBlock() != nil:
		keys = "d lancer la démo · w tableau de bord · r rejouer · ←/→ · q"
	default:
		keys = "←/→ naviguer · 1-9 aller à · g/G début/fin"
		if len(s.Notes) > 0 {
			keys += " · n notes"
		}
		keys += " · q quitter"
	}
	if more > 0 || t.scroll > 0 {
		keys = fmt.Sprintf("⇞/⇟ défiler (%d lignes) · ", max(0, more)) + keys
	}
	var b strings.Builder
	b.WriteString("\033[H\033[2J" + head + "\r\n" + fmt.Sprintf("%s %s%s%s", padS, p.K, strings.Repeat("─", width-2), p.R) + "\r\n")
	for _, ln := range view {
		b.WriteString(padS + ln + "\033[K\r\n")
	}
	foot := fmt.Sprintf("%s %s   %s%s%s", padS, dots.String(), p.D, keys, p.R)
	b.WriteString(fmt.Sprintf("\033[%d;1H", rows) + clip(foot, cols))
	write(b.String())
}

func (t *termDeck) goTo(i, k int) {
	n := len(t.d.Slides)
	i = max(0, min(n-1, i))
	if i != t.i {
		t.scroll = 0
	}
	t.i = i
	steps := len(t.d.Slides[i].Steps)
	if k < 0 {
		t.k = steps - 1
	} else {
		t.k = min(k, steps-1)
	}
}

func (t *termDeck) next() {
	if t.k < len(t.d.Slides[t.i].Steps)-1 {
		t.k++
	} else if t.i < len(t.d.Slides)-1 {
		t.goTo(t.i+1, 0)
	}
}

func (t *termDeck) prev() {
	if t.k > 0 {
		t.k--
	} else if t.i > 0 {
		t.goTo(t.i-1, -1)
	}
}

func (t *termDeck) poll(ms int) bool {
	fds := []unix.PollFd{{Fd: int32(t.fd), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, ms)
	return err == nil && n > 0
}

// readKey : une touche (séquence brute) ou "WINCH".
func (t *termDeck) readKey() string {
	buf := make([]byte, 32)
	for {
		select {
		case <-t.resized:
			return "WINCH"
		default:
		}
		if !t.poll(250) {
			continue
		}
		n, err := unix.Read(t.fd, buf)
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		if err != nil || n == 0 {
			return "q"
		}
		data := string(buf[:n])
		if data == "\x1b" && t.poll(50) {
			if m, err := unix.Read(t.fd, buf[:16]); err == nil && m > 0 {
				data += string(buf[:m])
			}
		}
		return strings.ToValidUTF8(data, "�")
	}
}

func (t *termDeck) confirm(text string) bool {
	_, rows := size()
	write(fmt.Sprintf("\033[%d;1H\033[2K %s%s%s o/N%s ", rows, t.p.B, t.p.Y, text, t.p.R))
	switch t.readKey() {
	case "o", "O", "y", "Y":
		return true
	}
	return false
}

// readLine : lit jusqu'à Entrée, octet par octet (rien n'est mis en tampon).
func readLine() {
	b := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(b)
		if err != nil || n == 0 || b[0] == '\n' {
			return
		}
	}
}

func (t *termDeck) runLive(fn func(), pause bool) {
	t.leave()
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, os.Interrupt) // Ctrl-C arrête la démo, pas le deck
	func() {
		defer func() {
			if e := recover(); e != nil {
				fmt.Printf("erreur : %v\n", e)
			}
		}()
		fn()
	}()
	if pause {
		fmt.Printf("\n%s[Entrée] revenir au deck%s", t.p.D, t.p.R)
		readLine()
	}
	signal.Stop(sig)
	t.enter()
}

func (t *termDeck) loop(start int) {
	t.resized = make(chan os.Signal, 1)
	signal.Notify(t.resized, syscall.SIGWINCH)
	defer signal.Stop(t.resized)
	t.goTo(start, 0)
	t.enter()
	defer t.leave()
	for {
		t.draw()
		key := t.readKey()
		t.msg = ""
		switch key {
		case "q", "Q", "\x03":
			return
		case "\x1b[C", "\x1b[B", " ", "l", "j", "\r", "\n", "\x1bOC", "\x1bOB":
			t.next()
		case "\x1b[D", "\x1b[A", "h", "k", "\x7f", "\x08", "\x1bOD", "\x1bOA":
			t.prev()
		case "\x1b[6~":
			t.scroll += 10
		case "\x1b[5~":
			t.scroll = max(0, t.scroll-10)
		case "g", "\x1b[H":
			t.goTo(0, 0)
		case "G", "\x1b[F":
			t.goTo(len(t.d.Slides)-1, 0)
		case "n":
			t.notes = !t.notes
		case "d", "w", "r":
			b := t.liveBlock()
			if b == nil {
				break
			}
			switch {
			case t.live == nil:
				t.msg = "démo live indisponible : pas de preview.toml"
			case key == "w":
				t.runLive(t.live.Watch, false)
			case key == "r":
				t.runLive(func() { t.live.Replay(b.Scenario) }, true)
			default:
				q := "Lancer « preview live " + b.Scenario + " »"
				if b.Title != "" {
					q += " : " + b.Title
				}
				if t.confirm(q + " ?") {
					t.runLive(func() { t.live.Run(b.Scenario) }, true)
				}
			}
		default:
			if len(key) == 1 && key >= "1" && key <= "9" {
				for j, s := range t.d.Slides {
					if !s.Appendix && s.Label == key {
						t.goTo(j, 0)
						break
					}
				}
			}
		}
	}
}
