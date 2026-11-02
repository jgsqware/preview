// Package deck : deck Markdown (SPEC.md §5) — analyse, modèle d'étapes, rendu terminal.
package deck

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
	"github.com/dlclark/regexp2"

	"github.com/jgsqware/preview/internal/events"
	"github.com/jgsqware/preview/internal/rx"
)

// Error : erreur d'analyse du deck.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func errf(f string, a ...any) error { return &Error{fmt.Sprintf(f, a...)} }

// -- modèle -------------------------------------------------------------------------------------

// Item : élément de liste.
type Item struct {
	Text string
	Sub  *Block
}

// Tab : onglet d'un bloc tabs.
type Tab struct {
	Name   string
	Blocks []*Block
}

// Stage : étape d'un bloc pipeline.
type Stage struct {
	Name, Desc string
}

// Block : un bloc. T : p, h1, h2, h3, list, callout, quote, table, text, code, term, tabs, pipeline, live.
type Block struct {
	T        string
	Text     string      // p, h*, text, code
	Kind     string      // callout
	Title    string      // callout, term, code, live
	Blocks   []*Block    // callout, quote
	Head     []string    // table
	Align    []string    // table
	Rows     [][]string  // table
	Ordered  bool        // list
	Start    int         // list
	Items    []*Item     // list
	Lang     string      // code
	Lines    [][2]string // term : (type, texte)
	Tabs     []*Tab      // tabs
	Active   int         // tabs
	Cmd      string      // pipeline
	Stages   []Stage     // pipeline
	Scenario string      // live
	ID       string      // widgets à étapes (premier niveau)
	Only     string      // "terminal" | "html" | ""
}

// Slide : une diapositive.
type Slide struct {
	Index    int
	Title    string
	Blocks   []*Block
	Line     int
	Cover    bool
	Appendix bool
	Lead     string
	Notes    []*Block
	Label    string
	Steps    []map[string]int
}

// Deck : le deck analysé.
type Deck struct {
	Path   string
	Meta   map[string]any
	Title  string
	Slides []*Slide
}

// Main : nombre de diapositives hors annexes.
func (d *Deck) Main() int {
	n := 0
	for _, s := range d.Slides {
		if !s.Appendix {
			n++
		}
	}
	return n
}

// StepsJSON : modèle d'étapes commun aux deux rendus (déterministe).
func (d *Deck) StepsJSON() map[string]any {
	slides := make([]any, len(d.Slides))
	for i, s := range d.Slides {
		blocks := make([]any, len(s.Blocks))
		for k, b := range s.Blocks {
			blocks[k] = b.T
		}
		steps := make([]any, len(s.Steps))
		for k, st := range s.Steps {
			m := map[string]any{}
			for w, v := range st {
				m[w] = v
			}
			steps[k] = m
		}
		slides[i] = map[string]any{
			"label": s.Label, "title": s.Title, "cover": s.Cover, "appendix": s.Appendix,
			"notes": len(s.Notes) > 0, "blocks": blocks, "steps": steps,
		}
	}
	return map[string]any{"title": d.Title, "main": d.Main(), "slides": slides}
}

// LiveScenarios : scénarios cités par les blocs ```live, dans l'ordre, sans doublon.
func LiveScenarios(d *Deck) []string {
	var out []string
	for _, s := range d.Slides {
		Walk(s.Blocks, func(b *Block) {
			if b.T == "live" {
				for _, x := range out {
					if x == b.Scenario {
						return
					}
				}
				out = append(out, b.Scenario)
			}
		})
	}
	return out
}

// Walk : parcourt les blocs et leurs enfants (callout, quote, tabs).
func Walk(blocks []*Block, fn func(*Block)) {
	for _, b := range blocks {
		fn(b)
		switch b.T {
		case "callout", "quote":
			Walk(b.Blocks, fn)
		case "tabs":
			for _, t := range b.Tabs {
				Walk(t.Blocks, fn)
			}
		}
	}
}

// -- inline -------------------------------------------------------------------------------------

var (
	escIn  = []string{`\\`, "", `\*`, "", `\_`, "", "\\`", "", `\[`, "", `\]`, ""}
	escOut = strings.NewReplacer("", `\`, "", "*", "", "_", "", "`", "", "[",
		"", "]")
	inlineRe = rx.MustCompile("`([^`]+)`" +
		`|\[([^\]]+)\]\(([^)\s]+)\)` +
		`|\*\*(?=\S)(.+?)(?<=\S)\*\*(?!\*)` +
		`|__(.+?)__` +
		`|(?<![\w*])\*(?=\S)(.+?)(?<=\S)\*(?![\w*])` +
		`|(?<![\w_])_(?=\S)(.+?)(?<=\S)_(?![\w_])` +
		`|<(https?://[^\s>]+)>`)
)

// Seg : morceau de texte inline ; Styles ⊂ {b, em, code, link}.
type Seg struct {
	Text   string
	Styles []string
	URL    string
}

// Has : vrai si le segment porte ce style.
func (s Seg) Has(style string) bool {
	for _, x := range s.Styles {
		if x == style {
			return true
		}
	}
	return false
}

func with(styles []string, s string) []string {
	out := make([]string, len(styles), len(styles)+1)
	copy(out, styles)
	return append(out, s)
}

// Segments : texte Markdown inline -> segments.
func Segments(text string) []Seg {
	for i := 0; i < len(escIn); i += 2 {
		text = strings.ReplaceAll(text, escIn[i], escIn[i+1])
	}
	var rec func(t string, styles []string, url string) []Seg
	rec = func(t string, styles []string, url string) []Seg {
		rs := []rune(t)
		var out []Seg
		pos := 0
		for _, m := range inlineRe.FindAll(t) {
			if m.Start > pos {
				out = append(out, Seg{string(rs[pos:m.Start]), styles, url})
			}
			g, set := m.Groups, m.Set
			switch {
			case set[1]:
				out = append(out, Seg{g[1], with(styles, "code"), url})
			case set[2]:
				out = append(out, rec(g[2], with(styles, "link"), g[3])...)
			case set[4] || set[5]:
				x := g[4]
				if !set[4] {
					x = g[5]
				}
				out = append(out, rec(x, with(styles, "b"), url)...)
			case set[6] || set[7]:
				x := g[6]
				if !set[6] {
					x = g[7]
				}
				out = append(out, rec(x, with(styles, "em"), url)...)
			default:
				out = append(out, Seg{g[8], with(styles, "link"), g[8]})
			}
			pos = m.End
		}
		if pos < len(rs) {
			out = append(out, Seg{string(rs[pos:]), styles, url})
		}
		return out
	}
	var res []Seg
	for _, s := range rec(text, nil, "") {
		if s.Text == "" {
			continue
		}
		s.Text = escOut.Replace(s.Text)
		if s.URL != "" {
			s.URL = escOut.Replace(s.URL)
		}
		res = append(res, s)
	}
	return res
}

// Plain : texte sans mise en forme.
func Plain(text string) string {
	var b strings.Builder
	for _, s := range Segments(text) {
		b.WriteString(s.Text)
	}
	return b.String()
}

// -- analyse ------------------------------------------------------------------------------------

var (
	fenceRe   = rx.MustCompile("^\\s{0,3}(`{3,}|~{3,})\\s*([^`\\s]*)\\s*(.*?)\\s*$")
	headRe    = rx.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)
	listRe    = rx.MustCompile(`^(\s*)([-*+]|\d{1,3}[.)])\s+(.*)$`)
	tsepRe    = rx.MustCompile(`^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$`)
	calloutRe = rx.MustCompile(`^\[!(\w+)\]\s*(.*)$`)
	tabRe     = rx.MustCompile(`^##\s+(.*?)(\s+\*)?\s*$`)
	flagRe    = rx.MustCompile(`^<!--\s*(cover|appendix|couverture|annexe)\s*-->$`, regexp2.IgnoreCase)
	quoteRe   = rx.MustCompile(`^\s*> ?`)
)

func isClose(line, fence string) bool {
	s := strings.TrimSpace(line)
	if len([]rune(s)) < len([]rune(fence)) || s == "" {
		return false
	}
	c := []rune(fence)[0]
	for _, r := range s {
		if r != c {
			return false
		}
	}
	return true
}

func indent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func isDigit(s string) bool {
	r := []rune(s)
	return len(r) > 0 && unicode.IsDigit(r[0])
}

// expandTabs : comme str.expandtabs(4).
func expandTabs(s string, size int) string {
	var b strings.Builder
	col := 0
	for _, r := range s {
		switch r {
		case '\t':
			n := size - col%size
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case '\n', '\r':
			b.WriteRune(r)
			col = 0
		default:
			b.WriteRune(r)
			col++
		}
	}
	return b.String()
}

// Parse lit et analyse un fichier deck.
func Parse(path string) (*Deck, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		msg := err.Error()
		var pe *fs.PathError
		if errors.As(err, &pe) {
			msg = pe.Err.Error()
			if msg != "" {
				msg = strings.ToUpper(msg[:1]) + msg[1:]
			}
		}
		return nil, errf("%s : %s", path, msg)
	}
	return ParseText(string(data), path)
}

func pyStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	case int64:
		return x != 0
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// ParseText analyse le texte d'un deck ; path sert aux messages (peut être vide).
func ParseText(text, path string) (*Deck, error) {
	name := "deck"
	if path != "" {
		name = filepath.Base(path)
	}
	lines := events.SplitLines(expandTabs(text, 4))
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	meta := map[string]any{}
	if i < len(lines) && strings.TrimSpace(lines[i]) == "+++" {
		j := -1
		for k := i + 1; k < len(lines); k++ {
			if strings.TrimSpace(lines[k]) == "+++" {
				j = k
				break
			}
		}
		if j < 0 {
			return nil, errf("%s : en-tête +++ non refermé (ligne %d)", name, i+1)
		}
		if _, err := toml.Decode(strings.Join(lines[i+1:j], "\n"), &meta); err != nil {
			return nil, errf("%s : en-tête TOML : %v", name, err)
		}
		i = j + 1
	}

	// découpe sur `---` hors blocs clôturés
	type chunk struct {
		start int
		lines []string
	}
	var chunks []chunk
	var cur []string
	start, fence := i, ""
	for n := i; n < len(lines); n++ {
		ln := lines[n]
		if fence != "" {
			if isClose(ln, fence) {
				fence = ""
			}
		} else if m := fenceRe.Match(ln); m != nil {
			fence = m[1]
		} else if strings.TrimRight(ln, " \t\r\n\v\f") == "---" {
			chunks = append(chunks, chunk{start, cur})
			cur, start = nil, n+1
			continue
		}
		cur = append(cur, ln)
	}
	chunks = append(chunks, chunk{start, cur})

	var slides []*Slide
	for _, c := range chunks {
		s, err := parseSlide(c.lines, c.start+1, len(slides), name)
		if err != nil {
			return nil, err
		}
		if s != nil {
			slides = append(slides, s)
		}
	}
	nMain, nApp := 0, 0
	for _, s := range slides {
		if s.Appendix {
			if nApp < 26 {
				s.Label = string(rune('A' + nApp))
			} else {
				s.Label = fmt.Sprintf("A%d", nApp+1)
			}
			nApp++
		} else {
			nMain++
			s.Label = strconv.Itoa(nMain)
		}
		if s.Title == "" {
			s.Title = s.Lead
			if s.Title == "" {
				s.Title = "Diapositive " + s.Label
			}
		}
	}
	title := ""
	if truthy(meta["title"]) {
		title = pyStr(meta["title"])
	} else if len(slides) > 0 {
		title = slides[0].Title
	} else if path != "" {
		b := filepath.Base(path)
		title = strings.TrimSuffix(b, filepath.Ext(b))
	} else {
		title = "Deck"
	}
	return &Deck{Path: path, Meta: meta, Title: title, Slides: slides}, nil
}

func parseSlide(chunk []string, lineNo, index int, name string) (*Slide, error) {
	var body, notes []string
	hasNotes := false
	fence, comment := "", false
	cover, appendix := false, false
	for _, ln := range chunk {
		target := &body
		if hasNotes {
			target = &notes
		}
		if fence != "" {
			*target = append(*target, ln)
			if isClose(ln, fence) {
				fence = ""
			}
			continue
		}
		if m := fenceRe.Match(ln); m != nil {
			fence = m[1]
			*target = append(*target, ln)
			continue
		}
		s := strings.TrimSpace(ln)
		if comment {
			comment = !strings.Contains(s, "-->")
			continue
		}
		if strings.HasPrefix(s, "<!--") {
			if f := flagRe.Match(s); f != nil {
				kind := strings.ToLower(f[1])
				cover = cover || kind == "cover" || kind == "couverture"
				appendix = appendix || kind == "appendix" || kind == "annexe"
			}
			comment = !strings.Contains(s, "-->")
			continue
		}
		if s == "???" && !hasNotes {
			hasNotes = true
			continue
		}
		*target = append(*target, ln)
	}
	nonblank := false
	for _, ln := range body {
		if strings.TrimSpace(ln) != "" {
			nonblank = true
			break
		}
	}
	if !nonblank && len(notes) == 0 && !cover && !appendix {
		return nil, nil
	}
	where := fmt.Sprintf("%s, diapositive %d (ligne %d)", name, index+1, lineNo)
	blocks, err := parseBlocks(body, where)
	if err != nil {
		return nil, err
	}
	title, lead := "", ""
	for k, b := range blocks {
		if b.T == "h1" {
			title = b.Text
			blocks = append(blocks[:k], blocks[k+1:]...)
			if k < len(blocks) && blocks[k].T == "h2" {
				lead = blocks[k].Text
				blocks = append(blocks[:k], blocks[k+1:]...)
			}
			break
		}
	}
	nb, err := parseBlocks(notes, where)
	if err != nil {
		return nil, err
	}
	s := &Slide{Index: index, Title: title, Blocks: blocks, Line: lineNo, Cover: cover, Appendix: appendix,
		Lead: lead, Notes: nb}
	s.Steps = slideSteps(s)
	return s, nil
}

func startsBlock(ln string) bool {
	s := strings.TrimLeft(ln, " \t\n\r\v\f")
	return fenceRe.Match(ln) != nil || headRe.Match(s) != nil || strings.HasPrefix(s, ">") ||
		listRe.Match(ln) != nil || (strings.HasPrefix(s, "|") && strings.Count(s, "|") >= 2)
}

func parseBlocks(lines []string, where string) ([]*Block, error) {
	var out []*Block
	i, n := 0, len(lines)
	for i < n {
		ln := lines[i]
		s := strings.TrimSpace(ln)
		if s == "" {
			i++
			continue
		}
		if m := fenceRe.Match(ln); m != nil {
			fence, lang, info := m[1], strings.ToLower(m[2]), m[3]
			j := i + 1
			for j < n && !isClose(lines[j], fence) {
				j++
			}
			words := strings.Fields(info)
			only := ""
			for _, w := range words {
				if w == "terminal" || w == "html" {
					only = w
					break
				}
			}
			if only != "" { // ```text terminal : bloc affiché seulement dans cette cible
				var keep []string
				for _, w := range words {
					if w != only {
						keep = append(keep, w)
					}
				}
				info = strings.Join(keep, " ")
			}
			end := min(j, n)
			blk, err := fenced(lang, info, lines[i+1:end], fmt.Sprintf("%s, ligne +%d", where, i+1))
			if err != nil {
				return nil, err
			}
			blk.Only = only
			out = append(out, blk)
			i = j + 1
			continue
		}
		if m := headRe.Match(s); m != nil {
			t := "h3"
			switch len(m[1]) {
			case 1:
				t = "h1"
			case 2:
				t = "h2"
			}
			out = append(out, &Block{T: t, Text: m[2]})
			i++
			continue
		}
		if strings.HasPrefix(s, ">") {
			var q []string
			for i < n && strings.HasPrefix(strings.TrimSpace(lines[i]), ">") {
				q = append(q, quoteRe.Sub(lines[i], ""))
				i++
			}
			var c []string
			if len(q) > 0 {
				c = calloutRe.Match(strings.TrimSpace(q[0]))
			}
			if c != nil {
				inner, err := parseBlocks(q[1:], where)
				if err != nil {
					return nil, err
				}
				out = append(out, &Block{T: "callout", Kind: strings.ToLower(c[1]), Title: c[2], Blocks: inner})
			} else {
				inner, err := parseBlocks(q, where)
				if err != nil {
					return nil, err
				}
				out = append(out, &Block{T: "quote", Blocks: inner})
			}
			continue
		}
		if strings.Contains(s, "|") && i+1 < n && strings.Contains(lines[i+1], "|") &&
			strings.Contains(lines[i+1], "-") && tsepRe.Match(lines[i+1]) != nil {
			head := cells(lines[i])
			var align []string
			for _, c := range cells(lines[i+1]) {
				a := ""
				switch {
				case strings.HasPrefix(c, ":") && strings.HasSuffix(c, ":"):
					a = "center"
				case strings.HasSuffix(c, ":"):
					a = "right"
				case strings.HasPrefix(c, ":"):
					a = "left"
				}
				align = append(align, a)
			}
			align = fit(align, len(head))
			var rows [][]string
			i += 2
			for i < n && strings.Contains(lines[i], "|") && strings.TrimSpace(lines[i]) != "" {
				rows = append(rows, fit(cells(lines[i]), len(head)))
				i++
			}
			out = append(out, &Block{T: "table", Head: head, Align: align, Rows: rows})
			continue
		}
		if listRe.Match(ln) != nil {
			var b *Block
			b, i = parseList(lines, i)
			out = append(out, b)
			continue
		}
		var para []string
		for i < n && strings.TrimSpace(lines[i]) != "" && !(len(para) > 0 && startsBlock(lines[i])) {
			para = append(para, strings.TrimSpace(lines[i]))
			i++
		}
		out = append(out, &Block{T: "p", Text: strings.Join(para, " ")})
	}
	return out, nil
}

func fit(xs []string, n int) []string {
	out := make([]string, n)
	copy(out, xs)
	return out
}

func cells(row string) []string {
	s := strings.TrimSpace(row)
	s = strings.TrimPrefix(s, "|")
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, `\|`) {
		s = s[:len(s)-1]
	}
	rs := []rune(s)
	var out []string
	var cur strings.Builder
	code := false
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if c == '\\' && i+1 < len(rs) && rs[i+1] == '|' {
			cur.WriteRune('|')
			i++
			continue
		}
		if c == '`' {
			code = !code
		}
		if c == '|' && !code {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		} else {
			cur.WriteRune(c)
		}
	}
	return append(out, strings.TrimSpace(cur.String()))
}

func parseList(lines []string, i int) (*Block, int) {
	n, base := len(lines), indent(lines[i])
	var got []string
	kind := isDigit(listRe.Match(lines[i])[2])
	otherKind := func(ln string) bool { // puce <-> numéro au même niveau : nouvelle liste
		m := listRe.Match(ln)
		return m != nil && indent(ln) <= base+1 && isDigit(m[2]) != kind
	}
	for i < n {
		ln := lines[i]
		if len(got) > 0 && otherKind(ln) {
			break
		}
		if strings.TrimSpace(ln) == "" {
			k := i + 1
			for k < n && strings.TrimSpace(lines[k]) == "" {
				k++
			}
			if k < n && !otherKind(lines[k]) && (indent(lines[k]) > base ||
				(listRe.Match(lines[k]) != nil && indent(lines[k]) >= base)) {
				i = k
				continue
			}
			break
		}
		if len(got) > 0 && indent(ln) <= base && listRe.Match(ln) == nil && startsBlock(ln) {
			break
		}
		if len(got) > 0 && indent(ln) < base {
			break
		}
		got = append(got, ln)
		i++
	}
	return buildList(got), i
}

func buildList(lines []string) *Block {
	base := indent(lines[0])
	first := listRe.Match(lines[0])
	ordered := isDigit(first[2])
	b := &Block{T: "list", Ordered: ordered, Start: 1}
	if ordered {
		b.Start, _ = strconv.Atoi(first[2][:len(first[2])-1])
	}
	var cur *Item
	var child []string
	flush := func() {
		if cur != nil && len(child) > 0 {
			cur.Sub = buildList(child)
		}
		child = nil
	}
	for _, ln := range lines {
		m := listRe.Match(ln)
		ind := indent(ln)
		switch {
		case m != nil && ind <= base+1:
			flush()
			cur = &Item{Text: strings.TrimSpace(m[3])}
			b.Items = append(b.Items, cur)
		case m != nil || (len(child) > 0 && ind > base+1):
			child = append(child, ln)
		case cur != nil:
			cur.Text += " " + strings.TrimSpace(ln)
		}
	}
	flush()
	return b
}

func hasAnyPrefix(s string, ps ...string) bool {
	for _, p := range ps {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func fenced(lang, info string, body []string, where string) (*Block, error) {
	text := strings.Join(body, "\n")
	switch lang {
	case "", "text", "txt", "plain":
		return &Block{T: "text", Text: text}, nil
	case "term":
		var out [][2]string
		for _, ln := range body {
			s := strings.TrimRight(ln, " \t\r\n\v\f")
			ls := strings.TrimLeft(s, " \t")
			switch {
			case strings.HasPrefix(s, "$"):
				out = append(out, [2]string{"$", strings.TrimLeft(s[1:], " \t")})
			case strings.HasPrefix(s, "#"):
				out = append(out, [2]string{"c", s})
			case strings.HasPrefix(s, "="):
				out = append(out, [2]string{"h", strings.TrimSpace(s[1:])})
			case hasAnyPrefix(ls, "✔", "✓"):
				out = append(out, [2]string{"g", s})
			case hasAnyPrefix(ls, "✘", "✗"):
				out = append(out, [2]string{"r", s})
			default:
				out = append(out, [2]string{"o", s})
			}
		}
		for len(out) > 0 && out[len(out)-1] == [2]string{"o", ""} {
			out = out[:len(out)-1]
		}
		return &Block{T: "term", Title: info, Lines: out}, nil
	case "tabs":
		type rawTab struct {
			name  string
			lines []string
		}
		var tabs []*rawTab
		var cur *rawTab
		fence, active := "", 0
		for _, ln := range body {
			if fence == "" {
				var m []string
				if fenceRe.Match(ln) == nil {
					m = tabRe.Match(strings.TrimSpace(ln))
				}
				if m != nil {
					cur = &rawTab{name: strings.TrimSpace(m[1])}
					if m[2] != "" {
						active = len(tabs)
					}
					tabs = append(tabs, cur)
					continue
				}
				if f := fenceRe.Match(ln); f != nil {
					fence = f[1]
				}
			} else if isClose(ln, fence) {
				fence = ""
			}
			if cur != nil {
				cur.lines = append(cur.lines, ln)
			}
		}
		if len(tabs) == 0 {
			return nil, errf("%s : bloc tabs sans section « ## Nom »", where)
		}
		b := &Block{T: "tabs", Active: active}
		for _, t := range tabs {
			bl, err := parseBlocks(t.lines, where)
			if err != nil {
				return nil, err
			}
			b.Tabs = append(b.Tabs, &Tab{Name: t.name, Blocks: bl})
		}
		return b, nil
	case "pipeline":
		b := &Block{T: "pipeline"}
		for _, ln := range body {
			s := strings.TrimSpace(ln)
			if s == "" || strings.HasPrefix(s, "#") {
				continue
			}
			if strings.HasPrefix(s, "$ ") {
				b.Cmd = strings.TrimSpace(s[2:])
				continue
			}
			name, desc, _ := strings.Cut(s, "|")
			b.Stages = append(b.Stages, Stage{strings.TrimSpace(name), strings.TrimSpace(desc)})
		}
		return b, nil
	case "live":
		conf := map[string]any{}
		if _, err := toml.Decode(text, &conf); err != nil {
			return nil, errf("%s : bloc live : %v", where, err)
		}
		sc, ok := conf["scenario"].(string)
		if !ok || sc == "" {
			return nil, errf(`%s : bloc live sans scenario = "…"`, where)
		}
		title := ""
		if t, ok := conf["title"]; ok {
			title = pyStr(t)
		}
		return &Block{T: "live", Scenario: sc, Title: title}, nil
	}
	return &Block{T: "code", Lang: lang, Title: info, Text: text}, nil
}

// slideSteps : chaque widget à étapes (tabs, pipeline) au premier niveau se déroule l'un après l'autre.
func slideSteps(s *Slide) []map[string]int {
	type seq struct {
		id   string
		vals []int
	}
	var seqs []seq
	for k, b := range s.Blocks {
		switch b.T {
		case "tabs", "pipeline", "term", "live":
			b.ID = fmt.Sprintf("s%dw%d", s.Index, k)
		}
		switch b.T {
		case "tabs":
			n, a := len(b.Tabs), b.Active
			var v []int
			for j := 0; j < n; j++ {
				v = append(v, (a+j)%n)
			}
			seqs = append(seqs, seq{b.ID, v})
		case "pipeline":
			var v []int
			for j := 1; j <= len(b.Stages); j++ {
				v = append(v, j)
			}
			if len(v) == 0 {
				v = []int{0}
			}
			seqs = append(seqs, seq{b.ID, v})
		}
	}
	state := map[string]int{}
	for _, q := range seqs {
		state[q.id] = q.vals[0]
	}
	cp := func() map[string]int {
		m := make(map[string]int, len(state))
		for k, v := range state {
			m[k] = v
		}
		return m
	}
	steps := []map[string]int{cp()}
	for _, q := range seqs {
		for _, v := range q.vals[1:] {
			state[q.id] = v
			steps = append(steps, cp())
		}
	}
	return steps
}
