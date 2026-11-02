package deck

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

const demo = "../../examples/demo.md"
const golden = "testdata/demo_steps.json"

func P(t *testing.T, text string) *Deck {
	t.Helper()
	d, err := ParseText(text, "")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func perr(t *testing.T, text string) {
	t.Helper()
	_, err := ParseText(text, "")
	if _, ok := err.(*Error); !ok {
		t.Fatalf("erreur de deck attendue, obtenu %v", err)
	}
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("\n got: %#v\nwant: %#v", got, want)
	}
}

func titles(d *Deck) []string {
	var out []string
	for _, s := range d.Slides {
		out = append(out, s.Title)
	}
	return out
}

func TestFrontMatter(t *testing.T) {
	d := P(t, "+++\ntitle = \"Projet\"\naccent = \"#123456\"\n+++\n# Un\n")
	eq(t, d.Meta["accent"], "#123456")
	eq(t, d.Title, "Projet")
	eq(t, len(d.Slides), 1)
}

func TestFrontMatterErrors(t *testing.T) {
	perr(t, "+++\ntitle = 'x'\n# jamais refermé\n")
	perr(t, "+++\ntitle = \n+++\n# x\n")
}

func TestSplitIgnoresFences(t *testing.T) {
	d := P(t, "# Un\n```yaml\na: 1\n---\nb: 2\n```\n---\n# Deux\n~~~\n---\n~~~\n")
	eq(t, titles(d), []string{"Un", "Deux"})
	eq(t, d.Slides[0].Blocks[0].Text, "a: 1\n---\nb: 2")
	eq(t, *d.Slides[1].Blocks[0], Block{T: "text", Text: "---"})
}

func TestEmptySlidesDroppedAndSeparatorStrict(t *testing.T) {
	d := P(t, "---\n# Un\n\n---\n\n---\n# Deux\ntexte --- pas un séparateur\n----\n")
	eq(t, titles(d), []string{"Un", "Deux"})
}

func TestTitleLeadFlagsNotes(t *testing.T) {
	d := P(t, "<!-- cover -->\n# Titre\n## Accroche\nTexte\n???\nNote **orateur**\n---\n<!-- appendix -->\n# Annexe\n"+
		"---\n# Trois\n<!-- un commentaire\nsur deux lignes -->\nfin\n")
	a, b, c := d.Slides[0], d.Slides[1], d.Slides[2]
	eq(t, a.Cover, true)
	eq(t, []string{a.Title, a.Lead}, []string{"Titre", "Accroche"})
	eq(t, a.Blocks, []*Block{{T: "p", Text: "Texte"}})
	eq(t, a.Notes, []*Block{{T: "p", Text: "Note **orateur**"}})
	eq(t, b.Appendix, true)
	var labels []string
	for _, s := range d.Slides {
		labels = append(labels, s.Label)
	}
	eq(t, labels, []string{"1", "A", "2"})
	eq(t, d.Main(), 2)
	eq(t, c.Blocks, []*Block{{T: "p", Text: "fin"}})
}

func TestTable(t *testing.T) {
	d := P(t, "# T\n| a | b | c |\n|:--|:-:|--:|\n| `x|y` | 2 \\| 3 | 🚀 |\n| court |\n")
	tb := d.Slides[0].Blocks[0]
	eq(t, tb.T, "table")
	eq(t, tb.Align, []string{"left", "center", "right"})
	eq(t, tb.Rows[0], []string{"`x|y`", "2 | 3", "🚀"})
	eq(t, tb.Rows[1], []string{"court", "", ""})
}

func TestListsNested(t *testing.T) {
	d := P(t, "# L\n- un\n  suite\n- deux\n  - a\n  - b\n    1. x\n- trois\n\n3. trois\n4. quatre\n")
	ul, ol := d.Slides[0].Blocks[0], d.Slides[0].Blocks[1]
	texts := func(b *Block) []string {
		var out []string
		for _, it := range b.Items {
			out = append(out, it.Text)
		}
		return out
	}
	eq(t, texts(ul), []string{"un suite", "deux", "trois"})
	sub := ul.Items[1].Sub
	eq(t, texts(sub), []string{"a", "b"})
	eq(t, sub.Items[1].Sub.Ordered, true)
	eq(t, []any{ol.Ordered, ol.Start}, []any{true, 3})
}

func TestCalloutAndQuote(t *testing.T) {
	d := P(t, "# Q\n> [!warning] Attention\n> corps\n\n> simple\n> citation\n")
	c, q := d.Slides[0].Blocks[0], d.Slides[0].Blocks[1]
	eq(t, []string{c.T, c.Kind, c.Title}, []string{"callout", "warning", "Attention"})
	eq(t, c.Blocks, []*Block{{T: "p", Text: "corps"}})
	eq(t, *q, Block{T: "quote", Blocks: []*Block{{T: "p", Text: "simple citation"}}})
}

func TestTabsStarAndSteps(t *testing.T) {
	d := P(t, "# T\n````tabs\n## A\nun\n## B *\n```bash\n## pas un onglet\n```\n## C\ntrois\n````\n")
	s := d.Slides[0]
	tb := s.Blocks[0]
	var names []string
	for _, x := range tb.Tabs {
		names = append(names, x.Name)
	}
	eq(t, names, []string{"A", "B", "C"})
	eq(t, tb.Active, 1)
	eq(t, tb.Tabs[1].Blocks[0].Text, "## pas un onglet")
	eq(t, s.Steps, []map[string]int{{"s0w0": 1}, {"s0w0": 2}, {"s0w0": 0}})
}

func TestTabsWithoutSections(t *testing.T) {
	perr(t, "# T\n```tabs\nrien\n```\n")
}

func TestPipelineTermLive(t *testing.T) {
	d := P(t, "# P\n```pipeline\n$ make all\nun | premier\ndeux\n```\n```term titre\n$ ls\n# c\n= h\n✔ ok\n✘ ko\nsortie\n```\n"+
		"```live\nscenario = \"x\"\ntitle = \"X\"\n```\n")
	pipe, term, live := d.Slides[0].Blocks[0], d.Slides[0].Blocks[1], d.Slides[0].Blocks[2]
	eq(t, pipe.Cmd, "make all")
	eq(t, pipe.Stages, []Stage{{"un", "premier"}, {"deux", ""}})
	eq(t, term.Title, "titre")
	var kinds []string
	for _, l := range term.Lines {
		kinds = append(kinds, l[0])
	}
	eq(t, kinds, []string{"$", "c", "h", "g", "r", "o"})
	eq(t, []string{live.Scenario, live.Title}, []string{"x", "X"})
	eq(t, d.Slides[0].Steps, []map[string]int{{"s0w0": 1}, {"s0w0": 2}})
	eq(t, LiveScenarios(d), []string{"x"})
	perr(t, "# L\n```live\ntitle = 'sans scénario'\n```\n")
}

func TestTwoWidgetsChain(t *testing.T) {
	d := P(t, "# W\n```pipeline\na\nb\n```\n```tabs\n## x\n## y\n```\n")
	eq(t, d.Slides[0].Steps, []map[string]int{{"s0w0": 1, "s0w1": 0}, {"s0w0": 2, "s0w1": 0}, {"s0w0": 2, "s0w1": 1}})
}

func TestInline(t *testing.T) {
	segs := Segments("a **b *c*** `d*e` [l](http://x) \\*f\\* snake_case_word")
	has := func(want Seg) {
		t.Helper()
		for _, s := range segs {
			if reflect.DeepEqual(s, want) {
				return
			}
		}
		t.Fatalf("%#v absent de %#v", want, segs)
	}
	has(Seg{"b ", []string{"b"}, ""})
	has(Seg{"c", []string{"b", "em"}, ""})
	has(Seg{"d*e", []string{"code"}, ""})
	has(Seg{"l", []string{"link"}, "http://x"})
	eq(t, Plain("\\*f\\* snake_case_word"), "*f* snake_case_word")
}

func TestBoxDiagramKeptVerbatim(t *testing.T) {
	art := "┌──┐\n│é │ ───►\n└──┘"
	d := P(t, "# D\n```text\n"+art+"\n```\n")
	eq(t, d.Slides[0].Blocks[0].Text, art)
}

func roundtrip(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func TestGoldenDemoSteps(t *testing.T) {
	d, err := Parse(demo)
	if err != nil {
		t.Fatal(err)
	}
	got := d.StepsJSON()
	if os.Getenv("UPDATE_GOLDEN") != "" {
		b, _ := json.MarshalIndent(got, "", " ")
		_ = os.MkdirAll("testdata", 0o755)
		_ = os.WriteFile(golden, append(b, '\n'), 0o644)
	}
	raw, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	var want any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	eq(t, roundtrip(got), want)
}

func TestDemoCoversEveryBlock(t *testing.T) {
	d, err := Parse(demo)
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]bool{}
	cover, app := false, false
	for _, s := range d.Slides {
		Walk(s.Blocks, func(b *Block) { types[b.T] = true })
		cover = cover || s.Cover
		app = app || s.Appendix
	}
	for _, k := range []string{"p", "list", "h2", "callout", "quote", "table", "text", "code", "term", "tabs", "pipeline", "live"} {
		if !types[k] {
			t.Fatalf("type de bloc absent de la démo : %s", k)
		}
	}
	if !cover || !app {
		t.Fatal("la démo doit avoir une couverture et une annexe")
	}
}

func TestWidth(t *testing.T) {
	eq(t, dwidth("aé"), 2)
	eq(t, dwidth("🚀漢"), 4)
	eq(t, dwidth("é"), 1)
}

func maxWidth(out string) int {
	m := 0
	for _, x := range strings.Split(out, "\n") {
		m = max(m, dwidth(x))
	}
	return m
}

func TestRenderAllFitsAndTablesAlign(t *testing.T) {
	d, err := Parse(demo)
	if err != nil {
		t.Fatal(err)
	}
	out := RenderAll(d, false, WIDTH)
	if m := maxWidth(out); m > 100 {
		t.Fatalf("ligne de %d colonnes", m)
	}
	// chaque tableau en traits a des lignes de même largeur
	var blocks [][]int
	var cur []int
	for _, x := range strings.Split(out, "\n") {
		s := strings.TrimSpace(x)
		if strings.HasPrefix(s, "┌") || strings.HasPrefix(s, "│") || strings.HasPrefix(s, "├") || strings.HasPrefix(s, "└") {
			cur = append(cur, dwidth(strings.TrimRight(x, " ")))
		} else if len(cur) > 0 {
			blocks = append(blocks, cur)
			cur = nil
		}
	}
	tables := 0
	for _, b := range blocks {
		if len(b) <= 3 {
			continue
		}
		tables++
		for _, w := range b {
			if w != b[0] {
				t.Fatalf("tableau mal aligné : %v", b)
			}
		}
	}
	if tables < 2 {
		t.Fatalf("%d tableaux trouvés", tables)
	}
	// --all déplie tous les onglets et garde les notes
	for _, name := range []string{"── Markdown", "── Terminal", "── HTML", "── notes"} {
		if !strings.Contains(out, name) {
			t.Fatalf("%q absent", name)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatal("codes ANSI sans couleur")
	}
}

func TestWideTableWraps(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("mot ", 60))
	d := P(t, "# W\n| a | b |\n|---|---|\n| "+long+" | "+long+" |\n")
	if m := maxWidth(RenderAll(d, false, WIDTH)); m > 100 {
		t.Fatalf("ligne de %d colonnes", m)
	}
}

func TestColorCodes(t *testing.T) {
	d, err := Parse(demo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(RenderAll(d, true, WIDTH), "\x1b[1m") {
		t.Fatal("pas de gras en couleur")
	}
}
