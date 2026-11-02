// Package tui : rendu terminal commun — canevas, cadres, couleurs, panneaux de sondes génériques.
package tui

import (
	"fmt"
	"math"
	"os"
	"strings"
	"unicode"

	"golang.org/x/text/width"

	"github.com/jgsqware/preview/internal/probes"
)

const (
	Spark = "▁▂▃▄▅▆▇█"
	Spin  = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
)

var StatusStyle = map[string]string{"ok": "ok", "warn": "chg", "fail": "fail", "unknown": "dim"}

// StatusStyleOf : style d'un statut (dim si inconnu).
func StatusStyleOf(st string) string {
	if s, ok := StatusStyle[st]; ok {
		return s
	}
	return "dim"
}

// SpinFrame : image n du spinner.
func SpinFrame(n int) string {
	r := []rune(Spin)
	return string(r[n%len(r)])
}

func wide(r rune) bool {
	k := width.LookupRune(r).Kind()
	return k == width.EastAsianWide || k == width.EastAsianFullwidth
}

func combining(r rune) bool {
	return unicode.In(r, unicode.Mn, unicode.Me)
}

// CW : largeur d'un caractère sur le canevas.
func CW(r rune) int {
	if combining(r) {
		return 0
	}
	if wide(r) {
		return 2
	}
	return 1
}

// Width : largeur d'une chaîne sur le canevas.
func Width(s string) int {
	n := 0
	for _, r := range s {
		n += CW(r)
	}
	return n
}

var zero = map[rune]bool{0x200b: true, 0x200c: true, 0x200d: true, 0x200e: true, 0x200f: true, 0xfe0e: true,
	0xfe0f: true, 0x2060: true}

// DCW : largeur à l'écran d'un caractère (deck) : emoji et CJK doubles, combinants et contrôles zéro.
func DCW(r rune) int {
	if r < 32 || zero[r] || combining(r) {
		return 0
	}
	if wide(r) {
		return 2
	}
	return 1
}

// DWidth : largeur à l'écran d'une chaîne (deck).
func DWidth(s string) int {
	n := 0
	for _, r := range s {
		n += DCW(r)
	}
	return n
}

// Clip : coupe à w colonnes avec « … ».
func Clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if Width(s) <= w {
		return s
	}
	var b strings.Builder
	n := 0
	for _, c := range s {
		if n+CW(c) > w-1 {
			break
		}
		b.WriteRune(c)
		n += CW(c)
	}
	return b.String() + "…"
}

// SparkLine : les w dernières valeurs en barres ▁…█.
func SparkLine(values []float64, w int) string {
	if len(values) > w {
		values = values[len(values)-w:]
	}
	if len(values) == 0 {
		return ""
	}
	lo, hi := values[0], values[0]
	for _, v := range values {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	sp := []rune(Spark)
	if hi == lo {
		k := 0
		if hi != 0 {
			k = 4
		}
		return strings.Repeat(string(sp[k]), len(values))
	}
	var b strings.Builder
	for _, v := range values {
		b.WriteRune(sp[min(7, int((v-lo)/(hi-lo)*7.999))])
	}
	return b.String()
}

// Bar : barre ████░░ de w cellules.
func Bar(frac float64, w int) string {
	frac = math.Max(0, math.Min(1, frac))
	full := int(math.RoundToEven(frac * float64(w)))
	return strings.Repeat("█", full) + strings.Repeat("░", w-full)
}

// MMSS : « 01:05 ».
func MMSS(seconds float64) string {
	s := int(math.Max(0, seconds))
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// FmtNum : nombre lisible ; nil -> « — ».
func FmtNum(v any) string {
	f, ok := v.(float64)
	if !ok {
		if v == nil {
			return "—"
		}
		if i, isInt := v.(int); isInt {
			f = float64(i)
		} else {
			return fmt.Sprint(v)
		}
	}
	if f != math.Trunc(f) {
		return fmt.Sprintf("%.1f", f)
	}
	n := int64(f)
	if n >= 10000 || n <= -10000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

// PyNum : str() Python d'un nombre JSON (entier sans « .0 »).
func PyNum(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e16 {
			return fmt.Sprint(int64(x))
		}
		return fmt.Sprint(x)
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	}
	return fmt.Sprint(v)
}

// -- canevas -----------------------------------------------------------------------------------

// Canvas : grille de caractères stylés ; "" = suite d'un caractère large.
type Canvas struct {
	H, W int
	ch   [][]string
	st   [][]string
}

func NewCanvas(h, w int) *Canvas {
	c := &Canvas{H: h, W: w, ch: make([][]string, h), st: make([][]string, h)}
	for y := range c.ch {
		c.ch[y] = make([]string, w)
		c.st[y] = make([]string, w)
		for x := range c.ch[y] {
			c.ch[y][x] = " "
		}
	}
	return c
}

// Put écrit text à (y, x) ; renvoie la largeur écrite.
func (c *Canvas) Put(y, x int, text, style string) int {
	if y < 0 || y >= c.H {
		return 0
	}
	xx := x
	for _, r := range text {
		n := CW(r)
		if n == 0 {
			continue
		}
		if xx+n > c.W {
			break
		}
		if xx >= 0 {
			c.ch[y][xx], c.st[y][xx] = string(r), style
			if n == 2 {
				c.ch[y][xx+1], c.st[y][xx+1] = "", style
			}
		}
		xx += n
	}
	return xx - x
}

// Fill : ligne entière en espaces stylés.
func (c *Canvas) Fill(y int, style string) {
	for x := 0; x < c.W; x++ {
		c.ch[y][x], c.st[y][x] = " ", style
	}
}

// Text : rendu brut, sans styles.
func (c *Canvas) Text() string {
	rows := make([]string, c.H)
	for y := range c.ch {
		rows[y] = strings.TrimRight(strings.Join(c.ch[y], ""), " \t")
	}
	return strings.Join(rows, "\n")
}

// ANSI : image complète pour un terminal (curseur en haut à gauche, retour à la ligne désactivé).
func (c *Canvas) ANSI(attrs map[string]string) string {
	var b strings.Builder
	b.WriteString("\x1b[H")
	for y := 0; y < c.H; y++ {
		if y > 0 {
			b.WriteString("\r\n")
		}
		cur := "\x00"
		for x := 0; x < c.W; x++ {
			if y == c.H-1 && x == c.W-1 {
				break
			}
			if c.st[y][x] != cur {
				cur = c.st[y][x]
				b.WriteString("\x1b[0m" + attrs[cur])
			}
			b.WriteString(c.ch[y][x])
		}
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// Box : cadre arrondi avec titre et note.
func Box(c *Canvas, y, x, h, w int, title, note string) {
	if h < 2 || w < 4 {
		return
	}
	const style = "frame"
	c.Put(y, x, "╭"+strings.Repeat("─", w-2)+"╮", style)
	for r := 1; r < h-1; r++ {
		c.Put(y+r, x, "│", style)
		c.Put(y+r, x+w-1, "│", style)
	}
	c.Put(y+h-1, x, "╰"+strings.Repeat("─", w-2)+"╯", style)
	n := c.Put(y, x+2, " "+Clip(title, w-6)+" ", "title")
	if note != "" {
		c.Put(y, x+2+n, Clip(note+" ", w-5-n), "dim")
	}
}

// Attrs : styles -> séquences SGR ; color = false pour NO_COLOR.
func Attrs(color bool) map[string]string {
	const (
		bold, dim, rev = "\x1b[1m", "\x1b[2m", "\x1b[7m"
	)
	if !color {
		return map[string]string{
			"frame": dim, "title": bold, "dim": dim, "label": bold, "bar": rev, "bar_run": rev + bold,
			"bar_ok": rev + bold, "bar_ko": rev + bold, "bar_watch": rev, "bar_replay": rev, "fail": bold,
			"step": bold, "task": bold,
		}
	}
	fg := func(n int) string { return fmt.Sprintf("\x1b[38;5;%dm", n) }
	on := func(fgc string, bg int) string { return fgc + fmt.Sprintf("\x1b[48;5;%dm", bg) + bold }
	const teal, amber, purple, grey, green, red = 81, 215, 141, 245, 114, 203
	black, white := "\x1b[30m", "\x1b[37m"
	return map[string]string{
		"teal": fg(teal), "acc": fg(amber), "chg": fg(amber), "ok": fg(green), "fail": fg(red) + bold,
		"dim": fg(grey), "frame": fg(grey), "title": fg(teal) + bold, "step": fg(purple) + bold,
		"task": fg(teal) + bold, "label": bold,
		"bar": on(black, teal), "bar_run": on(black, amber), "bar_ok": on(black, green),
		"bar_ko": on(white, red), "bar_watch": on(black, purple), "bar_replay": on(black, grey),
	}
}

// ColorOn : couleurs si la sortie est un terminal et NO_COLOR absent.
func ColorOn(isTTY bool) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" {
		return true
	}
	return isTTY
}

// -- panneau de sonde générique (SPEC.md §4) ----------------------------------------------------

type cellExtra struct {
	kind  string // "" | bar | ratio
	frac  float64
	style string
	n, m  int
}

func num(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

func cellText(col probes.Obj, v any) (string, string, cellExtra) {
	t := probes.Str(col, "type", "text")
	if v == nil {
		return "—", "dim", cellExtra{}
	}
	vm, isMap := v.(map[string]any)
	switch t {
	case "text":
		return PyNum(v), probes.Str(col, "style", "text"), cellExtra{}
	case "badge":
		st, txt := PyNum(v), PyNum(v)
		if isMap {
			st = probes.Str(vm, "status", "unknown")
			txt = st
			if x, ok := vm["text"]; ok {
				txt = PyNum(x)
			}
		}
		return "● " + txt, StatusStyleOf(st), cellExtra{}
	case "num":
		n, unit := v, ""
		if isMap {
			n = vm["v"]
			unit, _ = vm["unit"].(string)
		}
		if unit == "" {
			unit, _ = col["unit"].(string)
		}
		sty := "text"
		if f, ok := num(n); ok {
			if c, ok := num(col["crit"]); ok && f >= c {
				sty = "fail"
			} else if w, ok := num(col["warn"]); ok && f >= w {
				sty = "chg"
			}
		}
		return FmtNum(n) + unit, sty, cellExtra{}
	case "bar":
		mx, _ := num(vm["max"])
		if !isMap || mx == 0 {
			if isMap {
				if x, ok := vm["text"]; ok {
					return PyNum(x), "dim", cellExtra{}
				}
				return "—", "dim", cellExtra{}
			}
			return PyNum(v), "dim", cellExtra{}
		}
		cur, _ := num(vm["v"])
		frac := cur / mx
		unit, _ := vm["unit"].(string)
		bs := "teal"
		if frac >= 0.85 {
			bs = "chg"
		}
		return FmtNum(vm["v"]) + "/" + FmtNum(mx) + unit, "text", cellExtra{kind: "bar", frac: frac, style: bs}
	case "ratio":
		if !isMap {
			return PyNum(v), "text", cellExtra{}
		}
		n, _ := num(vm["v"])
		m, _ := num(vm["max"])
		sty := "chg"
		if n == m && m != 0 {
			sty = "ok"
		}
		return PyNum(n) + "/" + PyNum(m), sty, cellExtra{kind: "ratio", n: int(n), m: int(m)}
	}
	return PyNum(v), "text", cellExtra{}
}

func cells(r any) probes.Obj {
	rm, _ := r.(map[string]any)
	c, _ := rm["cells"].(map[string]any)
	return c
}

func colWidth(col probes.Obj, rows []any) int {
	t := probes.Str(col, "type", "text")
	id := probes.Str(col, "id", "")
	label := probes.Str(col, "label", id)
	maxV := 0
	for _, r := range rows {
		txt, _, _ := cellText(col, cells(r)[id])
		maxV = max(maxV, Width(txt))
	}
	w := max(Width(label), maxV)
	switch t {
	case "bar":
		w = max(Width(label), 9+maxV)
	case "ratio":
		mx := 0
		for _, r := range rows {
			if vm, ok := cells(r)[id].(map[string]any); ok {
				if m, ok := num(vm["max"]); ok {
					mx = max(mx, int(m))
				}
			}
		}
		w = max(Width(label), maxV+1+min(12, mx))
	}
	if cw, ok := num(col["width"]); ok && cw != 0 {
		w = min(w, int(cw))
	}
	return w
}

func itemsLines(items []any, w int) int {
	if len(items) == 0 {
		return 0
	}
	lines, x := 1, 0
	for _, it := range items {
		im, _ := it.(map[string]any)
		n := Width(PyNum(orEmpty(im["label"]))) + Width(PyNum(orEmpty(im["value"]))) + 6
		if x > 0 && x+n > w {
			lines++
			x = 0
		}
		x += n
	}
	return lines
}

func orEmpty(v any) any {
	if v == nil {
		return ""
	}
	return v
}

// ProbeHeight : hauteur idéale du panneau (cadre compris).
func ProbeHeight(obj probes.Obj, err string, w int) int {
	if obj == nil {
		return 3
	}
	rows := probes.List(obj, "rows")
	h := 2
	if len(rows) > 0 {
		h += 1 + len(rows)
	} else if len(probes.List(obj, "columns")) > 0 && probes.Str(obj, "empty", "") != "" {
		h++
	}
	h += itemsLines(probes.List(obj, "items"), w-4)
	if len(probes.List(obj, "series")) > 0 {
		h++
	}
	h += len(probes.List(obj, "lines"))
	if err != "" {
		h++
	}
	return max(3, h)
}

// DrawProbe dessine un panneau de sonde dans la zone donnée.
func DrawProbe(c *Canvas, y, x, h, w int, name string, obj probes.Obj, err string, history map[probes.HKey][]float64) {
	if obj == nil {
		Box(c, y, x, h, w, name, "")
		if err != "" {
			c.Put(y+1, x+2, Clip("✘ "+err, w-4), "fail")
		} else {
			c.Put(y+1, x+2, Clip("Collecte en cours…", w-4), "dim")
		}
		return
	}
	stale := err != ""
	st := probes.Str(obj, "status", "unknown")
	title := "● " + probes.Str(obj, "title", name)
	note := probes.Str(obj, "note", "")
	if stale {
		note = "périmé · " + err
	}
	Box(c, y, x, h, w, title, note)
	if stale {
		c.Put(y, x+3, "●", "dim")
	} else {
		c.Put(y, x+3, "●", StatusStyleOf(st))
	}
	innerW := w - 4
	yy, bottom := y+1, y+h-1
	sty := func(s string) string {
		if stale {
			return "dim"
		}
		return s
	}
	var cols []probes.Obj
	for _, c := range probes.List(obj, "columns") {
		if cm, ok := c.(map[string]any); ok {
			cols = append(cols, cm)
		}
	}
	rows := probes.List(obj, "rows")
	if len(cols) > 0 && len(rows) > 0 && yy < bottom {
		widths := make([]int, len(cols))
		xs := make([]int, len(cols))
		cx := x + 4
		for i, col := range cols {
			widths[i] = colWidth(col, rows)
			xs[i] = cx
			cx += widths[i] + 2
		}
		for i, col := range cols {
			if xs[i] < x+w-2 {
				id := probes.Str(col, "id", "")
				c.Put(yy, xs[i], Clip(probes.Str(col, "label", id), min(widths[i], x+w-2-xs[i])), "dim")
			}
		}
		yy++
		for _, r := range rows {
			if yy >= bottom {
				break
			}
			rm, _ := r.(map[string]any)
			c.Put(yy, x+2, "●", sty(StatusStyleOf(probes.Str(rm, "status", "ok"))))
			for i, col := range cols {
				cx0 := xs[i]
				room := (x + w - 2) - cx0
				if room <= 0 {
					break
				}
				wd := room
				if i < len(cols)-1 {
					wd = widths[i]
				}
				wd = min(wd, room)
				txt, s, extra := cellText(col, cells(r)[probes.Str(col, "id", "")])
				switch extra.kind {
				case "bar":
					c.Put(yy, cx0, Bar(extra.frac, 8), sty(extra.style))
					c.Put(yy, cx0+9, Clip(txt, wd-9), sty(s))
				case "ratio":
					k := c.Put(yy, cx0, Clip(txt, wd), sty(s))
					room2 := wd - k - 1
					if room2 > 0 && extra.m != 0 {
						on := max(0, min(extra.n, room2))
						c.Put(yy, cx0+k+1, strings.Repeat("▮", on), sty("ok"))
						c.Put(yy, cx0+k+1+on, strings.Repeat("▯", max(0, min(extra.m-extra.n, room2-on))), "dim")
					}
				default:
					c.Put(yy, cx0, Clip(txt, wd), sty(s))
				}
			}
			yy++
		}
	} else if len(cols) > 0 && len(rows) == 0 && yy < bottom {
		c.Put(yy, x+2, Clip(probes.Str(obj, "empty", "Rien à afficher."), innerW), "dim")
		yy++
	}
	if items := probes.List(obj, "items"); len(items) > 0 && yy < bottom {
		cx := x + 2
		for _, it := range items {
			im, _ := it.(map[string]any)
			label, val := PyNum(orEmpty(im["label"])), PyNum(orEmpty(im["value"]))
			n := Width(label) + Width(val) + 6
			if cx > x+2 && cx+n > x+w-2 {
				yy++
				cx = x + 2
				if yy >= bottom {
					break
				}
			}
			cx += c.Put(yy, cx, "● ", sty(StatusStyleOf(probes.Str(im, "status", "unknown"))))
			cx += c.Put(yy, cx, label+" ", "dim")
			cx += c.Put(yy, cx, Clip(val, x+w-2-cx), sty(probes.Str(im, "style", "label"))) + 3
		}
		yy++
	}
	if series := probes.List(obj, "series"); len(series) > 0 && yy < bottom {
		cx := x + 2
		sw := max(8, min(30, innerW/max(1, len(series))-18))
		for _, s := range series {
			sm, _ := s.(map[string]any)
			label := PyNum(orEmpty(sm["label"]))
			hist := history[probes.HKey{Name: name, Label: label}]
			if len(hist) == 0 {
				v, _ := num(sm["v"])
				hist = []float64{v}
			}
			cx += c.Put(yy, cx, label+" ", "dim")
			cx += c.Put(yy, cx, SparkLine(hist, sw), sty(probes.Str(sm, "style", "teal")))
			cx += c.Put(yy, cx, " "+FmtNum(sm["v"]), "label") + 4
			if cx >= x+w-4 {
				break
			}
		}
		yy++
	}
	for _, line := range probes.List(obj, "lines") {
		if yy >= bottom {
			break
		}
		c.Put(yy, x+2, Clip(PyNum(line), innerW), "dim")
		yy++
	}
}
