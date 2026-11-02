// Package html : export HTML autonome du deck (SPEC.md §5), lecteur d'enregistrements inclus (§3).
package html

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jgsqware/preview/assets"
	"github.com/jgsqware/preview/internal/cassette"
	"github.com/jgsqware/preview/internal/config"
	"github.com/jgsqware/preview/internal/deck"
	"github.com/jgsqware/preview/internal/events"
	"github.com/jgsqware/preview/internal/rx"
)

var (
	liveMeta = []string{"scenario", "title", "started", "cmd", "git", "host"}
	colorRe  = rx.MustCompile(`^(#[0-9a-fA-F]{3,8}|[a-zA-Z]{3,20}|(rgb|rgba|hsl|hsla|oklch|oklab)\([\d\s.,%/+-]+\))$`)
	schemeRe = rx.MustCompile(`^([a-zA-Z][\w+.-]*):`)
	escRepl  = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#x27;")
)

// esc : comme html.escape(quote=True) de Python.
func esc(s string) string { return escRepl.Replace(s) }

// jsonScript : JSON sûr dans <script> (<, > et & deviennent des échappements \u).
func jsonScript(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// jsonAttr : JSON lisible, non échappé (l'attribut est échappé ensuite).
func jsonAttr(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimRight(buf.String(), "\n")
}

func href(url string) string {
	if m := schemeRe.Match(url); m != nil {
		switch strings.ToLower(m[1]) {
		case "http", "https", "mailto":
		default:
			return "#"
		}
	}
	return url
}

// Inline : Markdown inline -> HTML échappé.
func Inline(text string) string {
	var out strings.Builder
	open := ""
	isOpen := false
	for _, s := range deck.Segments(text) {
		piece := esc(s.Text)
		if s.Has("code") {
			piece = "<code>" + piece + "</code>"
		}
		if s.Has("em") {
			piece = "<em>" + piece + "</em>"
		}
		if s.Has("b") {
			piece = "<strong>" + piece + "</strong>"
		}
		link, hasLink := "", false
		if s.Has("link") {
			link, hasLink = s.URL, true
		}
		if hasLink != isOpen || link != open {
			if isOpen {
				out.WriteString("</a>")
			}
			if hasLink {
				h := href(link)
				ext := ""
				if strings.HasPrefix(h, "http:") || strings.HasPrefix(h, "https:") {
					ext = ` target="_blank" rel="noopener"`
				}
				out.WriteString(`<a href="` + esc(h) + `"` + ext + ">")
			}
			open, isOpen = link, hasLink
		}
		out.WriteString(piece)
	}
	if isOpen {
		out.WriteString("</a>")
	}
	return out.String()
}

func codeHTML(text, lang string) string {
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		s := strings.TrimLeft(ln, " \t")
		if (strings.HasPrefix(s, "#") || strings.HasPrefix(s, "//")) && lang != "markdown" && lang != "md" && lang != "text" {
			lines[i] = `<span class="c">` + esc(ln) + "</span>"
		} else {
			lines[i] = esc(ln)
		}
	}
	return strings.Join(lines, "\n")
}

func termLine(k, s string) string {
	if k == "$" {
		return `<span class="p">$</span> ` + esc(s)
	}
	switch k {
	case "c", "h", "g", "r":
		return `<span class="` + k + `">` + esc(s) + "</span>"
	}
	return esc(s)
}

type htmlR struct {
	cfg   *config.Config
	live  map[string]map[string]any
	nLive int
	data  []string
}

func (h *htmlR) blocks(blocks []*deck.Block, state map[string]int) string {
	var parts []string
	for _, b := range blocks {
		if b.Only != "terminal" {
			parts = append(parts, h.block(b, state))
		}
	}
	return strings.Join(parts, "\n")
}

func (h *htmlR) block(b *deck.Block, state map[string]int) string {
	switch b.T {
	case "p":
		return "<p>" + Inline(b.Text) + "</p>"
	case "h1", "h2":
		return `<h3 class="sub">` + Inline(b.Text) + "</h3>"
	case "h3":
		return `<h4 class="sub2">` + Inline(b.Text) + "</h4>"
	case "list":
		return h.list(b)
	case "callout":
		kind := "note"
		switch b.Kind {
		case "note", "info", "tip", "important", "warning", "caution", "danger":
			kind = b.Kind
		}
		title := ""
		if b.Title != "" {
			title = ` <span class="ct">` + Inline(b.Title) + "</span>"
		}
		return `<aside class="callout k-` + kind + `"><div class="ch"><b>` + esc(deck.CalloutLabel(b.Kind)) + "</b>" +
			title + "</div>" + h.blocks(b.Blocks, nil) + "</aside>"
	case "quote":
		return "<blockquote>" + h.blocks(b.Blocks, nil) + "</blockquote>"
	case "table":
		al := func(c int) string {
			if a := b.Align[c]; a != "" {
				return ` style="text-align:` + a + `"`
			}
			return ""
		}
		var head, rows strings.Builder
		for c, x := range b.Head {
			head.WriteString("<th" + al(c) + ">" + Inline(x) + "</th>")
		}
		for _, r := range b.Rows {
			rows.WriteString("<tr>")
			for c, x := range r {
				rows.WriteString("<td" + al(c) + ">" + Inline(x) + "</td>")
			}
			rows.WriteString("</tr>")
		}
		cls := ""
		if len(b.Head) > 1 {
			cls = " multi"
		}
		return `<div class="tbl"><table class="t` + cls + `"><thead><tr>` + head.String() + "</tr></thead><tbody>" +
			rows.String() + "</tbody></table></div>"
	case "text":
		return `<div class="scroll-x"><pre class="tree">` + esc(b.Text) + "</pre></div>"
	case "code":
		capt := ""
		if b.Title != "" || b.Lang != "" {
			t := b.Title
			if t == "" {
				t = b.Lang
			}
			capt = `<div class="code-cap">` + esc(t) + "</div>"
		}
		return `<figure class="codebox">` + capt + `<pre class="code"><code class="lang-` + esc(b.Lang) + `">` +
			codeHTML(b.Text, b.Lang) + "</code></pre></figure>"
	case "term":
		lines := make([]string, len(b.Lines))
		raw := make([][]string, len(b.Lines))
		for i, l := range b.Lines {
			lines[i] = termLine(l[0], l[1])
			raw[i] = []string{l[0], l[1]}
		}
		title := b.Title
		if title == "" {
			title = "terminal"
		}
		return `<div class="term" id="` + esc(b.ID) + `" data-widget="term" data-lines="` + esc(jsonAttr(raw)) + `">` +
			`<div class="term-top"><i></i><i></i><i></i><span>` + esc(title) + `</span>` +
			`<button class="replay mini" type="button">↻ Rejouer</button></div>` +
			`<pre class="term-out" aria-live="off">` + strings.Join(lines, "\n") + "</pre></div>"
	case "tabs":
		return h.tabs(b, state)
	case "pipeline":
		return h.pipeline(b, state)
	case "live":
		return h.player(b)
	}
	return ""
}

func (h *htmlR) list(b *deck.Block) string {
	tag := "ul"
	start := ""
	if b.Ordered {
		tag = "ol"
		if b.Start != 1 {
			start = fmt.Sprintf(` start="%d"`, b.Start)
		}
	}
	var items strings.Builder
	for _, it := range b.Items {
		items.WriteString("<li>" + Inline(it.Text))
		if it.Sub != nil {
			items.WriteString(h.list(it.Sub))
		}
		items.WriteString("</li>")
	}
	return "<" + tag + ` class="md"` + start + ">" + items.String() + "</" + tag + ">"
}

func stateGet(state map[string]int, id string, def int) int {
	if v, ok := state[id]; ok {
		return v
	}
	return def
}

func (h *htmlR) tabs(b *deck.Block, state map[string]int) string {
	wid := b.ID
	act := stateGet(state, wid, b.Active)
	var bar, panels strings.Builder
	for k, tab := range b.Tabs {
		sel, ti, hidden := "false", -1, " hidden"
		if k == act {
			sel, ti, hidden = "true", 0, ""
		}
		fmt.Fprintf(&bar, `<button class="tab" role="tab" type="button" id="%s-t%d" data-k="%d" aria-selected="%s" aria-controls="%s-p%d" tabindex="%d">%s</button>`,
			wid, k, k, sel, wid, k, ti, Inline(tab.Name))
		fmt.Fprintf(&panels, `<div class="tabpanel" role="tabpanel" id="%s-p%d" aria-labelledby="%s-t%d" data-k="%d"%s>%s</div>`,
			wid, k, wid, k, k, hidden, h.blocks(tab.Blocks, nil))
	}
	return `<div class="w-tabs" id="` + wid + `" data-widget="tabs"><div class="tabs" role="tablist">` + bar.String() +
		`</div><div class="tabpanels">` + panels.String() + "</div></div>"
}

func (h *htmlR) pipeline(b *deck.Block, state map[string]int) string {
	wid := b.ID
	n := len(b.Stages)
	lit := stateGet(state, wid, n)
	cmd := ""
	if b.Cmd != "" {
		cmd = `<div class="cmdline">` + esc(b.Cmd) + "</div>"
	}
	var items strings.Builder
	for k, s := range b.Stages {
		cls := ""
		if k < lit {
			cls = "lit"
		}
		items.WriteString(`<li class="` + cls + `"><b>` + Inline(s.Name) + "</b>")
		if s.Desc != "" {
			items.WriteString("<span>" + Inline(s.Desc) + "</span>")
		}
		items.WriteString("</li>")
	}
	vert := ""
	if n > 6 {
		vert = " vert"
	}
	return fmt.Sprintf(`<div class="w-pipe" id="%s" data-widget="pipeline" data-n="%d">%s<ol class="pipe%s" style="--n:%d">%s</ol><button class="replay" type="button">↻ Rejouer</button></div>`,
		wid, n, cmd, vert, max(1, n), items.String())
}

func (h *htmlR) player(b *deck.Block) string {
	key := fmt.Sprintf("live-%d", h.nLive)
	h.nLive++
	payload := map[string]any{}
	if src := h.live[b.Scenario]; len(src) > 0 {
		for k, v := range src {
			payload[k] = v
		}
	} else {
		payload = map[string]any{"missing": true, "scenario": b.Scenario}
	}
	if b.Title != "" {
		payload["title"] = b.Title
	}
	h.data = append(h.data, `<script type="application/json" id="`+key+`">`+jsonScript(payload)+"</script>")
	msg := "Lecteur d'enregistrement (JavaScript requis)."
	if m, _ := payload["missing"].(bool); m {
		msg = "Aucun enregistrement : lancer <code>preview rehearse " + esc(b.Scenario) + "</code> puis réexporter."
	}
	id := b.ID
	if id == "" {
		id = key
	}
	return `<div class="player" id="` + esc(id) + `" data-widget="live" data-src="` + key + `">` +
		`<noscript><p class="muted">` + msg + "</p></noscript></div>"
}

// -- diapositives -------------------------------------------------------------------------------

type pageMeta struct{ name, subtitle, footer string }

func (h *htmlR) slide(s *deck.Slide, m pageMeta) string {
	state := map[string]int{}
	if len(s.Steps) > 0 {
		state = s.Steps[0]
	}
	cls := "slide"
	if s.Cover {
		cls += " cover-slide"
	}
	if s.Appendix {
		cls += " appendix"
	}
	notes := ""
	if len(s.Notes) > 0 {
		notes = `<aside class="notes" hidden>` + h.blocks(s.Notes, nil) + "</aside>"
	}
	head := `data-title="` + esc(deck.Plain(s.Title)) + `" data-label="` + esc(s.Label) + `"`
	var body string
	if s.Cover {
		body = h.cover(s, m, state)
	} else {
		num := "Annexe " + s.Label
		if !s.Appendix {
			n, _ := strconv.Atoi(s.Label)
			num = fmt.Sprintf("%02d", n)
		}
		var eyebrow, h2 string
		if s.Lead != "" {
			eyebrow = `<span class="eyebrow"><span class="num">` + esc(num) + "</span>" + Inline(s.Title) + "</span>"
			h2 = "<h2>" + Inline(s.Lead) + "</h2>"
		} else {
			eyebrow = `<span class="eyebrow"><span class="num">` + esc(num) + "</span>" + esc(m.name) + "</span>"
			h2 = "<h2>" + Inline(s.Title) + "</h2>"
		}
		body = `<div class="inner">` + eyebrow + h2 + h.layout(s.Blocks, state) + "</div>"
	}
	return `<section class="` + cls + `" ` + head + ` aria-roledescription="diapositive">` + body + notes + "</section>"
}

// layout : sections `##` courtes côte à côte (colonnes) ; le reste à la suite.
func (h *htmlR) layout(blocks []*deck.Block, state map[string]int) string {
	var pre []*deck.Block
	var sections [][]*deck.Block
	for _, b := range blocks {
		switch {
		case b.T == "h2":
			sections = append(sections, []*deck.Block{b})
		case len(sections) > 0:
			sections[len(sections)-1] = append(sections[len(sections)-1], b)
		default:
			pre = append(pre, b)
		}
	}
	narrow := func(b *deck.Block) bool {
		switch b.T {
		case "tabs", "pipeline", "live":
			return false
		case "table":
			return len(b.Head) <= 3
		case "text", "code":
			for _, x := range strings.Split(b.Text, "\n") {
				if len([]rune(x)) > 60 {
					return false
				}
			}
		}
		return true
	}
	if len(sections) >= 2 && len(sections) <= 3 {
		ok := true
		for _, sec := range sections {
			for _, b := range sec {
				ok = ok && narrow(b)
			}
		}
		if ok {
			var cols strings.Builder
			for _, sec := range sections {
				cols.WriteString(`<div class="col">` + h.blocks(sec, state) + "</div>")
			}
			return h.blocks(pre, state) + `<div class="cols">` + cols.String() + "</div>"
		}
	}
	return h.blocks(blocks, state)
}

func (h *htmlR) cover(s *deck.Slide, m pageMeta, state map[string]int) string {
	blocks := append([]*deck.Block(nil), s.Blocks...)
	var rack *deck.Block
	for k, b := range blocks {
		if b.T == "list" && !b.Ordered {
			rack = b
			blocks = append(blocks[:k], blocks[k+1:]...)
			break
		}
	}
	var lead, rest []*deck.Block
	for _, b := range blocks {
		if b.T == "p" && len(rest) == 0 && len(lead) < 1 {
			lead = append(lead, b)
		} else {
			rest = append(rest, b)
		}
	}
	eyebrow := ""
	if m.subtitle != "" {
		eyebrow = `<span class="eyebrow">` + esc(m.subtitle) + "</span>"
	}
	var leads strings.Builder
	for _, b := range lead {
		leads.WriteString(`<p class="lead">` + Inline(b.Text) + "</p>")
	}
	var foot strings.Builder
	for _, x := range strings.Split(m.footer, "·") {
		if x = strings.TrimSpace(x); x != "" {
			foot.WriteString("<span>" + Inline(x) + "</span>")
		}
	}
	footS := ""
	if foot.Len() > 0 {
		footS = `<div class="meta">` + foot.String() + "</div>"
	}
	main := `<div class="cover-main">` + eyebrow + "<h1>" + Inline(s.Title) + "</h1>" + leads.String() +
		h.blocks(rest, state) + footS + "</div>"
	if rack == nil {
		return `<div class="inner"><div class="cover solo">` + main + "</div></div>"
	}
	var units strings.Builder
	for _, it := range rack.Items {
		segs := deck.Segments(it.Text)
		var name strings.Builder
		var tail []string
		for _, sg := range segs {
			if sg.Has("b") {
				name.WriteString(sg.Text)
			}
			if sg.Has("code") {
				tail = append(tail, sg.Text)
			}
		}
		ip := ""
		if len(segs) == 0 {
			segs = []deck.Seg{{}}
		}
		if last := segs[len(segs)-1]; len(tail) > 0 && len(last.Styles) == 1 && last.Styles[0] == "code" {
			ip = tail[len(tail)-1]
		}
		var desc strings.Builder
		for _, sg := range segs {
			if !sg.Has("b") && !(ip != "" && sg.Text == ip && sg.Has("code")) {
				desc.WriteString(sg.Text)
			}
		}
		n, d := strings.TrimSpace(name.String()), strings.Trim(desc.String(), " ·—-:")
		if n == "" {
			n, d = d, ""
		}
		units.WriteString(`<div class="unit"><span class="led"></span><span><b>` + esc(n) + "</b><small>" + esc(d) +
			`</small></span><span class="ip">` + esc(ip) + "</span></div>")
	}
	cnt := len(rack.Items)
	plural := ""
	if cnt > 1 {
		plural = "s"
	}
	rackHTML := `<div class="rack" aria-label="` + esc(deck.Plain(s.Title)) + `"><div class="rack-head"><span>` +
		esc(m.name) + "</span><span>" + fmt.Sprintf("%d unité%s", cnt, plural) + "</span></div>" + units.String() + "</div>"
	return `<div class="inner"><div class="cover">` + main + rackHTML + "</div></div>"
}

// -- enregistrements ----------------------------------------------------------------------------

func pyStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e16 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		if x {
			return "True"
		}
		return "False"
	case nil:
		return "None"
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
	case float64:
		return x != 0
	case int64:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// LivePayload : dernière cassette VERTE (sinon la dernière) du scénario, réduite et pré-analysée pour le lecteur.
func LivePayload(cfg *config.Config, scenario string) map[string]any {
	rec := cassette.Latest(cfg.Recordings(), scenario, true)
	if rec == "" {
		rec = cassette.Latest(cfg.Recordings(), scenario, false)
	}
	if rec == "" {
		return map[string]any{"missing": true, "scenario": scenario}
	}
	data, err := cassette.Trim(rec, cassette.TrimOpts{StepRe: cfg.Rules["step"]})
	if err != nil {
		return map[string]any{"error": err.Error(), "scenario": scenario}
	}
	meta := data.Meta
	sc := cfg.Scenarios[scenario]
	var plan []string
	if l, ok := meta["plan"].([]any); ok {
		for _, x := range l {
			plan = append(plan, pyStr(x))
		}
	} else if sc != nil {
		plan = events.PlannedSteps(sc.Plan, cfg.Rules["plan"])
	}
	run := events.NewRun(events.NewParser(cfg.Rules), plan)
	out := []any{}
	blank, tEnd := true, 0.0
	var rc any
	hasRC := false
	for _, ev := range data.Events {
		t := ev.T
		tEnd = math.Max(tEnd, t)
		switch ev.K {
		case "line":
			v := ""
			if ev.V != nil {
				v = pyStr(ev.V)
			}
			parts := events.SplitLines(v)
			if len(parts) == 0 {
				parts = []string{""}
			}
			for _, part := range parts {
				s := events.Clean(part)
				if s == "" && blank {
					continue
				}
				blank = s == ""
				evs := run.Line(s, t)
				e := []any{}
				for _, x := range evs {
					e = append(e, x.JSON())
				}
				out = append(out, map[string]any{"t": t, "k": "line", "v": s, "e": e, "s": events.Classify(s, evs)})
			}
		case "snap":
			if v, ok := ev.V.(map[string]any); ok {
				probes := map[string]any{}
				for n, p := range v {
					if pm, ok := p.(map[string]any); ok {
						probes[n] = pm
					}
				}
				if len(probes) > 0 {
					out = append(out, map[string]any{"t": t, "k": "snap", "v": probes})
				}
			}
		case "end":
			rc, hasRC = ev.V, true
			out = append(out, map[string]any{"t": t, "k": "end", "v": rc})
		}
	}
	if hasRC && rc != nil {
		if f, ok := rc.(float64); ok {
			run.Finish(int(f), tEnd)
		} else {
			run.Finish(1, tEnd)
		}
	}
	title := ""
	switch {
	case truthy(meta["title"]):
		title = pyStr(meta["title"])
	case sc != nil && sc.Title != "":
		title = sc.Title
	default:
		title = scenario
	}
	m := map[string]any{}
	for _, k := range liveMeta {
		if v, ok := meta[k]; ok && v != nil && v != "" {
			m[k] = pyStr(v)
		}
	}
	date := ""
	if s, ok := meta["started"]; ok {
		date = fmtDate(pyStr(s))
	}
	if date == "" {
		date = dateFromName(filepath.Base(rec))
	}
	var verdict any
	if (hasRC && rc != nil) || run.Result != nil {
		if v := run.Verdict(); v != "" {
			verdict = v
		}
	}
	planOut := []any{}
	for _, n := range plan {
		planOut = append(planOut, map[string]any{"name": n, "re": events.StepPatternSource(n)})
	}
	place := map[string]any{}
	for _, p := range cfg.Probes {
		place[p.Name] = p.Place
	}
	return map[string]any{
		"scenario": scenario,
		"title":    title,
		"meta":     m,
		"file":     filepath.Base(rec),
		"date":     date,
		"verdict":  verdict,
		"duration": math.Round(tEnd*100) / 100,
		"plan":     planOut,
		"place":    place,
		"summary":  run.Summary(),
		"events":   out,
	}
}

func fmtDate(iso string) string {
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04:05.999999", "2006-01-02T15:04",
		"2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05.999999Z07:00", "2006-01-02"} {
		if t, err := time.Parse(layout, iso); err == nil {
			return t.Format("02/01/2006 à 15:04")
		}
	}
	return ""
}

func dateFromName(name string) string {
	if len(name) < 15 {
		return ""
	}
	t, err := time.Parse("20060102-150405", name[:15])
	if err != nil {
		return ""
	}
	return t.Format("02/01/2006 à 15:04")
}

// -- export -------------------------------------------------------------------------------------

func firstTruthy(vs ...any) string {
	for _, v := range vs {
		if truthy(v) {
			return pyStr(v)
		}
	}
	return ""
}

// Render : deck -> page HTML complète. live : {scénario: payload} déjà calculés.
func Render(d *deck.Deck, cfg *config.Config, live map[string]map[string]any) string {
	fm := d.Meta
	var cfgName, cfgSub any
	if cfg != nil {
		cfgName, cfgSub = cfg.Name, cfg.Subtitle
	}
	m := pageMeta{
		name:     firstTruthy(fm["title"], cfgName, d.Title),
		subtitle: firstTruthy(fm["subtitle"], cfgSub),
		footer:   firstTruthy(fm["footer"]),
	}
	h := &htmlR{cfg: cfg, live: live}
	slides := make([]string, len(d.Slides))
	for i, s := range d.Slides {
		slides[i] = h.slide(s, m)
	}
	data := d.StepsJSON()
	data["name"], data["subtitle"] = m.name, m.subtitle
	accent := strings.TrimSpace(firstTruthy(fm["accent"]))
	accentCSS := ""
	if accent != "" && colorRe.Match(accent) != nil {
		accentCSS = ":root { --accent-src: " + accent + "; --amber: var(--accent-src); }\n" +
			"@media (prefers-color-scheme: dark) { :root:not([data-theme=\"light\"]) " +
			"{ --amber: color-mix(in srgb, var(--accent-src) 62%, #fff); } }\n" +
			":root[data-theme=\"dark\"] { --amber: color-mix(in srgb, var(--accent-src) 62%, #fff); }"
	}
	footer := ""
	if m.footer != "" {
		footer = "<span>" + Inline(m.footer) + "</span>"
	}
	rep := [][2]string{
		{"<!--@TITLE@-->", esc(m.name)},
		{"/*@ACCENT@*/", accentCSS},
		{"<!--@NAME@-->", esc(m.name)},
		{"<!--@SUBTITLE@-->", esc(m.subtitle)},
		{"<!--@FOOTER@-->", footer},
		{"<!--@SLIDES@-->", strings.Join(slides, "\n")},
		{"<!--@COUNT@-->", fmt.Sprintf("1 / %d", d.Main())},
		{"<!--@DATA@-->", `<script type="application/json" id="deck-data">` + jsonScript(data) + "</script>\n" +
			strings.Join(h.data, "\n")},
	}
	page := assets.Template
	for _, r := range rep {
		page = strings.ReplaceAll(page, r[0], r[1])
	}
	return page
}

// Export écrit le deck en un seul fichier HTML autonome ; renvoie son chemin.
func Export(cfg *config.Config, deckPath, outPath string, warn func(string)) (string, error) {
	if warn == nil {
		warn = func(m string) { fmt.Fprintln(os.Stderr, "⚠ "+m) }
	}
	if deckPath == "" {
		deckPath = cfg.DeckSource
	}
	if outPath == "" {
		outPath = cfg.DeckHTML
	}
	d, err := deck.Parse(deckPath)
	if err != nil {
		return "", err
	}
	live := map[string]map[string]any{}
	for _, sc := range deck.LiveScenarios(d) {
		p := LivePayload(cfg, sc)
		if m, _ := p["missing"].(bool); m {
			warn("aucun enregistrement pour « " + sc + " » : preview rehearse " + sc)
		} else if e, ok := p["error"].(string); ok {
			warn(e)
		}
		live[sc] = p
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(outPath, []byte(Render(d, cfg, live)), 0o644); err != nil {
		return "", err
	}
	return outPath, nil
}
