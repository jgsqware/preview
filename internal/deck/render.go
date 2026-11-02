package deck

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jgsqware/preview/internal/tui"
)

// WIDTH : largeur du deck terminal.
const WIDTH = 100

// CalloutFR : libellés des encadrés.
var CalloutFR = map[string]string{"note": "Note", "info": "Info", "tip": "Astuce", "important": "Important",
	"warning": "Attention", "caution": "Prudence", "danger": "Danger"}

// CalloutLabel : libellé d'un encadré (type capitalisé si inconnu).
func CalloutLabel(kind string) string {
	if l, ok := CalloutFR[kind]; ok {
		return l
	}
	return capitalize(kind)
}

func capitalize(s string) string {
	r := []rune(strings.ToLower(s))
	if len(r) == 0 {
		return ""
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func dwidth(s string) int { return tui.DWidth(s) }

func vwidth(s string) int { return tui.DWidth(stripANSI(s)) }

// clip coupe une chaîne (avec ANSI) à width colonnes visibles.
func clip(s string, width int) string {
	if vwidth(s) <= width {
		return s
	}
	var b strings.Builder
	w := 0
	for i := 0; i < len(s); {
		if loc := ansiRe.FindStringIndex(s[i:]); loc != nil && loc[0] == 0 {
			b.WriteString(s[i : i+loc[1]])
			i += loc[1]
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		cw := tui.DCW(r)
		if w+cw > width-1 {
			break
		}
		b.WriteRune(r)
		w += cw
		i += size
	}
	return b.String() + "›\x1b[0m"
}

// Pal : couleurs ANSI (vides sans couleur).
type Pal struct {
	B, D, R, I, U, V, T, A, G, Y, M, K, E, C string
}

func NewPal(on bool) *Pal {
	if !on {
		return &Pal{}
	}
	return &Pal{B: "\033[1m", D: "\033[2m", R: "\033[0m", I: "\033[3m", U: "\033[4m", V: "\033[7m",
		T: "\033[1;38;5;81m", A: "\033[38;5;215m", G: "\033[32m", Y: "\033[33m", M: "\033[38;5;141m",
		K: "\033[38;5;245m", E: "\033[31m", C: "\033[38;5;81m"}
}

func (p *Pal) style(styles []string) string {
	var b strings.Builder
	for _, s := range styles {
		switch s {
		case "b":
			b.WriteString(p.B)
		case "em":
			b.WriteString(p.I + p.A)
		case "code":
			b.WriteString(p.A)
		case "link":
			b.WriteString(p.U + p.C)
		case "dim":
			b.WriteString(p.K)
		}
	}
	return b.String()
}

type tseg struct {
	text   string
	styles []string
}

// termSegs : segments pour le terminal ; l'URL d'un lien suit son texte, en gris.
func termSegs(text string) []tseg {
	segs := Segments(text)
	var out []tseg
	for k, s := range segs {
		out = append(out, tseg{s.Text, s.Styles})
		last := k+1 == len(segs) || segs[k+1].URL != s.URL
		if s.URL != "" && last && s.Has("link") {
			var label strings.Builder
			for _, x := range segs {
				if x.URL == s.URL {
					label.WriteString(x.Text)
				}
			}
			if s.URL != label.String() && !strings.HasPrefix(s.URL, "#") {
				out = append(out, tseg{" (" + s.URL + ")", []string{"dim"}})
			}
		}
	}
	return out
}

// wrap : segments -> lignes ANSI d'au plus width colonnes.
func wrap(segs []tseg, width int, p *Pal) []string {
	width = max(4, width)
	var words [][]tseg
	var cur []tseg
	for _, sg := range segs {
		rs := []rune(sg.text)
		for i := 0; i < len(rs); {
			j := i
			sp := unicode.IsSpace(rs[i])
			for j < len(rs) && unicode.IsSpace(rs[j]) == sp {
				j++
			}
			if sp {
				if len(cur) > 0 {
					words = append(words, cur)
					cur = nil
				}
			} else {
				cur = append(cur, tseg{string(rs[i:j]), sg.styles})
			}
			i = j
		}
	}
	if len(cur) > 0 {
		words = append(words, cur)
	}
	var lines [][]tseg
	var line []tseg
	lw := 0
	for _, word := range words {
		ww := 0
		for _, t := range word {
			ww += dwidth(t.text)
		}
		if len(line) > 0 && lw+1+ww > width {
			lines = append(lines, line)
			line, lw = nil, 0
		}
		if ww > width { // mot trop long : coupé
			for _, t := range word {
				for _, ch := range t.text {
					cw := tui.DCW(ch)
					if lw+cw > width {
						lines = append(lines, line)
						line, lw = nil, 0
					}
					line = append(line, tseg{string(ch), t.styles})
					lw += cw
				}
			}
			continue
		}
		if len(line) > 0 {
			line = append(line, tseg{" ", nil})
			lw++
		}
		line = append(line, word...)
		lw += ww
	}
	if len(line) > 0 {
		lines = append(lines, line)
	}
	var res []string
	for _, ln := range lines {
		var b strings.Builder
		var prev []string
		prevSet := false
		for _, t := range ln {
			if !prevSet || strings.Join(t.styles, ",") != strings.Join(prev, ",") {
				if prevSet && len(prev) > 0 {
					b.WriteString(p.R + p.style(t.styles))
				} else {
					b.WriteString(p.style(t.styles))
				}
				prev, prevSet = t.styles, true
			}
			b.WriteString(t.text)
		}
		if prevSet && len(prev) > 0 {
			b.WriteString(p.R)
		}
		res = append(res, b.String())
	}
	if len(res) == 0 {
		return []string{""}
	}
	return res
}

func pad(s string, w int, align string) string {
	gap := max(0, w-vwidth(s))
	switch align {
	case "right":
		return strings.Repeat(" ", gap) + s
	case "center":
		return strings.Repeat(" ", gap/2) + s + strings.Repeat(" ", gap-gap/2)
	}
	return s + strings.Repeat(" ", gap)
}

// Renderer : blocs -> lignes ANSI pour une largeur donnée.
type Renderer struct {
	p      *Pal
	w      int
	all    bool
	clipAt int // 0 = pas de coupe
}

func (r *Renderer) verbatim(s, color string) string {
	if color != "" && s != "" {
		s = color + s + r.p.R
	}
	if r.clipAt > 0 {
		return clip(s, r.clipAt)
	}
	return s
}

func (r *Renderer) blocks(blocks []*Block, width int, state map[string]int) []string {
	if width == 0 {
		width = r.w
	}
	var out []string
	for _, b := range blocks {
		if b.Only == "html" {
			continue
		}
		lines := r.block(b, width, state)
		if len(lines) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, lines...)
	}
	return out
}

func (r *Renderer) block(b *Block, w int, state map[string]int) []string {
	p := r.p
	switch b.T {
	case "p":
		return wrap(termSegs(b.Text), w, p)
	case "h1", "h2", "h3":
		pre := p.B
		if b.T == "h3" {
			pre = p.B + p.K
		}
		var out []string
		for _, ln := range wrap(termSegs(b.Text), w, p) {
			out = append(out, pre+ln+p.R)
		}
		return out
	case "list":
		return r.list(b, w, 0)
	case "callout":
		col := p.T
		switch b.Kind {
		case "tip":
			col = p.G
		case "warning", "caution":
			col = p.Y
		case "important":
			col = p.M
		case "danger":
			col = p.E
		}
		bar := col + "▌" + p.R + " "
		first := bar + col + p.B + CalloutLabel(b.Kind) + p.R
		if b.Title != "" {
			first += " · " + p.B + Plain(b.Title) + p.R
		}
		out := []string{first}
		for _, ln := range r.blocks(b.Blocks, w-2, state) {
			out = append(out, bar+ln)
		}
		return out
	case "quote":
		var out []string
		for _, ln := range r.blocks(b.Blocks, w-2, state) {
			out = append(out, p.K+"│"+p.R+" "+ln)
		}
		return out
	case "table":
		return r.table(b, w)
	case "text":
		var out []string
		for _, ln := range strings.Split(b.Text, "\n") {
			out = append(out, r.verbatim(ln, ""))
		}
		return out
	case "code":
		var out []string
		if b.Title != "" {
			out = append(out, p.K+"─ "+b.Title+p.R)
		}
		for _, ln := range strings.Split(b.Text, "\n") {
			com := hasAnyPrefix(strings.TrimLeft(ln, " \t"), "#", "//", "--") && b.Lang != "markdown" && b.Lang != "md"
			c := p.A
			if com {
				c = p.K
			}
			out = append(out, r.verbatim(ln, c))
		}
		return out
	case "term":
		title := b.Title
		if title == "" {
			title = "terminal"
		}
		out := []string{p.K + "╭─ " + title + " " + strings.Repeat("─", max(0, min(w, 60)-4-dwidth(title))) + p.R}
		for _, l := range b.Lines {
			k, s := l[0], l[1]
			if k == "$" {
				out = append(out, r.verbatim(p.K+"$ "+p.R+p.A+s+p.R, ""))
				continue
			}
			col := ""
			switch k {
			case "c":
				col = p.K
			case "h":
				col = p.M + p.B
			case "g":
				col = p.G
			case "r":
				col = p.E
			}
			out = append(out, r.verbatim(s, col))
		}
		return out
	case "tabs":
		return r.tabs(b, w, state)
	case "pipeline":
		return r.pipeline(b, w, state)
	case "live":
		title := b.Title
		if title == "" {
			title = "scénario " + b.Scenario
		}
		inner := []string{p.B + "Démo live" + p.R + " · " + title,
			p.K + "scénario " + p.R + p.A + b.Scenario + p.R,
			p.A + "d" + p.R + " lancer   " + p.A + "w" + p.R + " tableau de bord   " +
				p.A + "r" + p.R + " rejouer le dernier enregistrement"}
		mw := 0
		for _, x := range inner {
			mw = max(mw, vwidth(x))
		}
		bw := min(w, mw+4)
		out := []string{p.K + "╭" + strings.Repeat("─", max(0, bw-2)) + "╮" + p.R}
		for _, x := range inner {
			out = append(out, p.K+"│"+p.R+" "+pad(x, bw-4, "")+" "+p.K+"│"+p.R)
		}
		return append(out, p.K+"╰"+strings.Repeat("─", max(0, bw-2))+"╯"+p.R)
	}
	return nil
}

var bullets = []string{"•", "◦", "‣"}

func (r *Renderer) list(b *Block, w, depth int) []string {
	p := r.p
	var out []string
	for k, it := range b.Items {
		mark := bullets[depth%3]
		if b.Ordered {
			mark = fmt.Sprintf("%d.", b.Start+k)
		}
		mw := dwidth(mark) + 1
		lines := wrap(termSegs(it.Text), w-mw, p)
		out = append(out, p.K+mark+p.R+" "+lines[0])
		for _, ln := range lines[1:] {
			out = append(out, strings.Repeat(" ", mw)+ln)
		}
		if it.Sub != nil {
			for _, ln := range r.list(it.Sub, w-2, depth+1) {
				out = append(out, "  "+ln)
			}
		}
	}
	return out
}

func (r *Renderer) table(b *Block, w int) []string {
	p := r.p
	rows := append([][]string{b.Head}, b.Rows...)
	ncol := len(b.Head)
	if ncol == 0 {
		return nil
	}
	widths := make([]int, ncol)
	for c := 0; c < ncol; c++ {
		m := 1
		for _, row := range rows {
			m = max(m, dwidth(Plain(row[c])))
		}
		widths[c] = m
	}
	avail := w - (3*ncol + 1)
	for {
		sum, mx, k := 0, -1, 0
		for i, x := range widths {
			sum += x
			if x > mx {
				mx, k = x, i
			}
		}
		if sum <= avail || mx <= 6 {
			break
		}
		widths[k]--
	}
	rule := func(l, m, rr string) string {
		parts := make([]string, ncol)
		for i, x := range widths {
			parts[i] = strings.Repeat("─", x+2)
		}
		return p.K + l + strings.Join(parts, m) + rr + p.R
	}
	sep := p.K + "│" + p.R
	row := func(cells []string, head bool) []string {
		wrapped := make([][]string, ncol)
		h := 0
		for c := 0; c < ncol; c++ {
			wrapped[c] = wrap(termSegs(cells[c]), widths[c], p)
			h = max(h, len(wrapped[c]))
		}
		var out []string
		for li := 0; li < h; li++ {
			parts := make([]string, ncol)
			for c := 0; c < ncol; c++ {
				s := ""
				if li < len(wrapped[c]) {
					s = wrapped[c][li]
				}
				if head && s != "" {
					s = p.B + s + p.R
				}
				parts[c] = " " + pad(s, widths[c], b.Align[c]) + " "
			}
			out = append(out, sep+strings.Join(parts, sep)+sep)
		}
		return out
	}
	out := []string{rule("┌", "┬", "┐")}
	out = append(out, row(b.Head, true)...)
	out = append(out, rule("├", "┼", "┤"))
	for _, rr := range b.Rows {
		out = append(out, row(rr, false)...)
	}
	return append(out, rule("└", "┴", "┘"))
}

func (r *Renderer) tabs(b *Block, w int, state map[string]int) []string {
	p := r.p
	if r.all {
		var out []string
		for k, tab := range b.Tabs {
			if k > 0 {
				out = append(out, "")
			}
			name := Plain(tab.Name)
			out = append(out, p.A+"── "+p.B+name+p.R+p.K+" "+strings.Repeat("─", max(0, min(w, 60)-4-dwidth(name)))+p.R)
			out = append(out, r.blocks(tab.Blocks, w, state)...)
		}
		return out
	}
	act, ok := state[b.ID]
	if !ok || b.ID == "" {
		act = b.Active
	}
	var bar []string
	for k, tab := range b.Tabs {
		name := Plain(tab.Name)
		if k == act {
			bar = append(bar, p.V+p.A+p.B+" "+name+" "+p.R)
		} else {
			bar = append(bar, p.K+" "+name+" "+p.R)
		}
	}
	line := strings.Join(bar, " ") + p.K + fmt.Sprintf("   onglet %d/%d", act+1, len(b.Tabs)) + p.R
	out := []string{line, p.K + strings.Repeat("─", min(w, max(vwidth(line), 20))) + p.R, ""}
	return append(out, r.blocks(b.Tabs[act].Blocks, w, state)...)
}

func (r *Renderer) pipeline(b *Block, w int, state map[string]int) []string {
	p := r.p
	lit := len(b.Stages)
	if !r.all {
		if v, ok := state[b.ID]; ok && b.ID != "" {
			lit = v
		}
	}
	var out []string
	if b.Cmd != "" {
		out = append(out, p.K+"$ "+p.R+p.A+b.Cmd+p.R, "")
	}
	nw := 0
	for _, s := range b.Stages {
		nw = max(nw, dwidth(Plain(s.Name)))
	}
	for k, s := range b.Stages {
		on := k < lit
		dot, num, nameS := p.K+"○"+p.R, p.K+fmt.Sprintf("%2d", k+1)+p.R, p.K+Plain(s.Name)+p.R
		if on {
			dot, num, nameS = p.T+"●"+p.R, fmt.Sprintf("%2d", k+1)+p.R, p.B+Plain(s.Name)+p.R
		}
		lead := num + " " + dot + " " + pad(nameS, nw, "") + "  "
		desc := []string{""}
		if s.Desc != "" {
			desc = wrap(termSegs(s.Desc), w-vwidth(lead), p)
		}
		if !on {
			for i, d := range desc {
				desc[i] = p.K + stripANSI(d) + p.R
			}
		}
		out = append(out, lead+desc[0])
		for _, d := range desc[1:] {
			out = append(out, strings.Repeat(" ", vwidth(lead))+d)
		}
		if k+1 < len(b.Stages) {
			c := p.K
			if k+1 < lit {
				c = p.T
			}
			out = append(out, "   "+c+"│"+p.R)
		}
	}
	return out
}

func (r *Renderer) slide(s *Slide, state map[string]int, notes bool) []string {
	p := r.p
	var out []string
	if s.Cover {
		title := Plain(s.Title)
		big := strings.ToUpper(title)
		if dwidth(title)*2 <= r.w {
			rs := []rune(big)
			parts := make([]string, len(rs))
			for i, c := range rs {
				parts[i] = string(c)
			}
			big = strings.Join(parts, " ")
		}
		out = append(out, "", "", p.T+big+p.R, p.A+strings.Repeat("━", min(r.w, dwidth(big)))+p.R, "")
	}
	if s.Lead != "" {
		for _, ln := range wrap(termSegs(s.Lead), r.w, p) {
			out = append(out, p.B+ln+p.R)
		}
		out = append(out, "")
	}
	if state == nil {
		state = map[string]int{}
	}
	out = append(out, r.blocks(s.Blocks, r.w, state)...)
	if notes && len(s.Notes) > 0 {
		out = append(out, "", p.K+"── notes "+strings.Repeat("─", max(0, min(r.w, 60)-9))+p.R)
		for _, ln := range r.blocks(s.Notes, r.w, map[string]int{}) {
			out = append(out, p.K+stripANSI(ln)+p.R)
		}
	}
	return out
}

// RenderAll : tout le deck, à plat — onglets dépliés, pipelines allumés, notes incluses.
func RenderAll(d *Deck, color bool, width int) string {
	if width <= 0 {
		width = WIDTH
	}
	p := NewPal(color)
	r := &Renderer{p: p, w: width - 3, all: true}
	var out []string
	for _, s := range d.Slides {
		lab := fmt.Sprintf("%s/%d", s.Label, d.Main())
		if s.Appendix {
			lab = "Annexe " + s.Label
		}
		out = append(out, "", p.T+lab+"  "+Plain(s.Title)+p.R, "")
		for _, ln := range r.slide(s, nil, true) {
			if ln != "" {
				ln = "   " + ln
			}
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n") + "\n"
}
