// Package config charge preview.toml (SPEC.md §1).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const Name = "preview.toml"

// RuleKeys : clés de [live.rules], dans l'ordre.
var RuleKeys = []string{"step", "ok", "fail", "result_ok", "result_fail", "ignore_step", "command", "plan"}

// DefaultRules : règles de lecture par défaut.
func DefaultRules() map[string]string {
	return map[string]string{
		"step":        `^=== (.+)$`,
		"ok":          `^[✔✓] (.+)$`,
		"fail":        `^[✘✗] (.+)$`,
		"result_ok":   "",
		"result_fail": "",
		"ignore_step": "",
		"command":     "",
		"plan":        `^\s*step "(.*)"\s*$`,
	}
}

// Error : erreur de configuration, affichée telle quelle.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func errf(f string, a ...any) error { return &Error{fmt.Sprintf(f, a...)} }

// IsError : vrai pour une erreur de configuration.
func IsError(err error) bool {
	var e *Error
	return errors.As(err, &e)
}

type Scenario struct {
	Name      string
	Run       string
	Title     string
	Plan      string // chemin absolu ou ""
	Preflight string
	Timeout   float64 // 0 = aucun
	Env       map[string]string
	Probes    []string // sondes affichées pendant ce scénario ; vide = toutes
}

// Deck : un deck du projet ([[deck]] ou [deck]).
type Deck struct {
	Name   string
	Source string // chemin absolu
	HTML   string // chemin absolu
}

type Probe struct {
	Name     string
	Run      string
	Interval float64
	Timeout  float64
	Place    string // side | bottom
}

type Config struct {
	Root       string // dossier projet (celui de preview.toml)
	Name       string
	Subtitle   string
	StateDir   string
	DeckSource string // premier deck
	DeckHTML   string
	Decks      []*Deck
	Default    string
	Interval   float64
	Redact     []string
	Follow     string // "" = aucun
	Rules      map[string]string
	Scenarios  map[string]*Scenario
	Order      []string // ordre des scénarios
	Probes     []*Probe
}

// New : configuration minimale (sans preview.toml).
func New(root, name string) *Config {
	return &Config{
		Root: root, Name: name, StateDir: filepath.Join(root, ".preview"),
		DeckSource: filepath.Join(root, "demo.md"), DeckHTML: filepath.Join(root, "demo.html"),
		Interval: 3, Rules: DefaultRules(), Scenarios: map[string]*Scenario{},
	}
}

func (c *Config) Recordings() string { return filepath.Join(c.StateDir, "recordings") }

// Deck : par nom ; nil si inconnu.
func (c *Config) Deck(name string) *Deck {
	for _, d := range c.Decks {
		if d.Name == name {
			return d
		}
	}
	return nil
}

// DeckNames : noms des decks, dans l'ordre de preview.toml.
func (c *Config) DeckNames() []string {
	out := make([]string, len(c.Decks))
	for i, d := range c.Decks {
		out[i] = d.Name
	}
	return out
}

// ProbesFor : sondes à afficher pour un scénario (toutes si sc est nil ou sans liste).
func (c *Config) ProbesFor(sc *Scenario) []*Probe {
	if sc == nil || len(sc.Probes) == 0 {
		return c.Probes
	}
	var out []*Probe
	for _, p := range c.Probes {
		for _, n := range sc.Probes {
			if p.Name == n {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// Scenario : par nom, ou le défaut, ou le premier.
func (c *Config) Scenario(name string) (*Scenario, error) {
	if name == "" {
		name = c.Default
	}
	if name == "" && len(c.Order) > 0 {
		name = c.Order[0]
	}
	if s, ok := c.Scenarios[name]; ok {
		return s, nil
	}
	known := strings.Join(c.Order, ", ")
	if known == "" {
		known = "aucun"
	}
	if name == "" {
		name = "(aucun)"
	}
	return nil, errf("scénario inconnu : %s (connus : %s)", name, known)
}

func (c *Config) addScenario(s *Scenario) {
	if _, ok := c.Scenarios[s.Name]; !ok {
		c.Order = append(c.Order, s.Name)
	}
	c.Scenarios[s.Name] = s
}

// FindRoot : premier dossier (courant puis parents) qui contient preview.toml.
func FindRoot(start string) string {
	if start == "" {
		start, _ = os.Getwd()
	}
	p, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		if st, err := os.Stat(filepath.Join(p, Name)); err == nil && st.Mode().IsRegular() {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return ""
		}
		p = parent
	}
}

type rawScenario struct {
	Name      string         `toml:"name"`
	Run       string         `toml:"run"`
	Title     string         `toml:"title"`
	Plan      *string        `toml:"plan"`
	Preflight string         `toml:"preflight"`
	Timeout   float64        `toml:"timeout"`
	Env       map[string]any `toml:"env"`
	Glob      string         `toml:"glob"`
	Probes    []string       `toml:"probes"`
}

type rawDeck struct {
	Name   string `toml:"name"`
	Source string `toml:"source"`
	HTML   string `toml:"html"`
}

type rawProbe struct {
	Name     string   `toml:"name"`
	Run      string   `toml:"run"`
	Interval *float64 `toml:"interval"`
	Timeout  *float64 `toml:"timeout"`
	Place    string   `toml:"place"`
}

type raw struct {
	Project struct {
		Name     string `toml:"name"`
		Subtitle string `toml:"subtitle"`
		StateDir string `toml:"state_dir"`
	} `toml:"project"`
	Deck any `toml:"deck"` // [deck] (un seul) ou [[deck]] (liste)
	Live struct {
		Default  string            `toml:"default"`
		Interval *float64          `toml:"interval"`
		Redact   []string          `toml:"redact"`
		Follow   string            `toml:"follow"`
		Rules    map[string]string `toml:"rules"`
	} `toml:"live"`
	Scenarios *rawScenario  `toml:"scenarios"`
	Scenario  []rawScenario `toml:"scenario"`
	Probe     []rawProbe    `toml:"probe"`
}

// Load : charge preview.toml depuis root (dossier ou fichier) ; root vide = recherche vers le haut.
func Load(root string) (*Config, error) {
	if root == "" {
		root = FindRoot("")
		if root == "" {
			return nil, errf("aucun %s trouvé ici ni dans les dossiers parents (preview init pour en créer un)", Name)
		}
	}
	root, _ = filepath.Abs(root)
	path := root
	if st, err := os.Stat(root); err == nil && st.IsDir() {
		path = filepath.Join(root, Name)
	}
	root = filepath.Dir(path)
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, errf("%s introuvable", path)
	}
	var r raw
	if _, err := toml.Decode(string(text), &r); err != nil {
		return nil, errf("%s : %v", path, err)
	}
	if r.Project.Name == "" {
		return nil, errf("%s : [project] name est requis", path)
	}
	rel := func(p string) string {
		if p == "" {
			return ""
		}
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(root, p)
	}
	or := func(a, b string) string {
		if a != "" {
			return a
		}
		return b
	}
	cfg := New(root, r.Project.Name)
	cfg.Subtitle = r.Project.Subtitle
	cfg.StateDir = rel(or(r.Project.StateDir, ".preview"))
	decks, err := rawDecks(r.Deck)
	if err != nil {
		return nil, errf("%s : %v", path, err)
	}
	if len(decks) == 0 {
		decks = []rawDeck{{}}
	}
	for _, d := range decks {
		src := rel(or(d.Source, "demo.md"))
		name := d.Name
		if name == "" {
			if len(decks) > 1 {
				return nil, errf("%s : chaque [[deck]] demande name", path)
			}
			name = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
		}
		if cfg.Deck(name) != nil {
			return nil, errf("%s : deck en double : %s", path, name)
		}
		out := strings.TrimSuffix(src, filepath.Ext(src)) + ".html"
		if d.HTML != "" {
			out = rel(d.HTML)
		}
		for _, o := range cfg.Decks {
			if o.HTML == out {
				return nil, errf("%s : decks %s et %s : même fichier HTML %s", path, o.Name, name, out)
			}
		}
		cfg.Decks = append(cfg.Decks, &Deck{Name: name, Source: src, HTML: out})
	}
	cfg.DeckSource, cfg.DeckHTML = cfg.Decks[0].Source, cfg.Decks[0].HTML
	cfg.Default = r.Live.Default
	if r.Live.Interval != nil {
		cfg.Interval = *r.Live.Interval
	}
	cfg.Redact = r.Live.Redact
	cfg.Follow = rel(r.Live.Follow)
	for k, v := range r.Live.Rules {
		if _, ok := cfg.Rules[k]; ok {
			cfg.Rules[k] = v
		}
	}
	env := func(m map[string]any) map[string]string {
		out := map[string]string{}
		for k, v := range m {
			out[k] = fmt.Sprint(v)
		}
		return out
	}

	// scénarios découverts, puis explicites (qui gagnent)
	if d := r.Scenarios; d != nil {
		if d.Glob == "" || d.Run == "" {
			return nil, errf("%s : [scenarios] demande glob et run", path)
		}
		files, _ := filepath.Glob(filepath.Join(root, d.Glob))
		sort.Strings(files)
		for _, f := range files {
			base := filepath.Base(f)
			name := strings.TrimSuffix(base, filepath.Ext(base))
			relp, _ := filepath.Rel(root, f)
			fmtS := func(s string) string {
				return strings.NewReplacer("{name}", name, "{path}", relp).Replace(s)
			}
			plan := "{path}"
			if d.Plan != nil {
				plan = *d.Plan
			}
			cfg.addScenario(&Scenario{Name: name, Run: fmtS(d.Run), Title: fmtS(d.Title), Plan: rel(fmtS(plan)),
				Preflight: d.Preflight, Timeout: d.Timeout, Env: env(d.Env), Probes: d.Probes})
		}
	}
	for _, s := range r.Scenario {
		if s.Name == "" || s.Run == "" {
			return nil, errf("%s : chaque [[scenario]] demande name et run", path)
		}
		plan := ""
		if s.Plan != nil {
			plan = rel(*s.Plan)
		}
		cfg.addScenario(&Scenario{Name: s.Name, Run: s.Run, Title: s.Title, Plan: plan, Preflight: s.Preflight,
			Timeout: s.Timeout, Env: env(s.Env), Probes: s.Probes})
	}
	seen := map[string]bool{}
	for _, p := range r.Probe {
		if p.Name == "" || p.Run == "" {
			return nil, errf("%s : chaque [[probe]] demande name et run", path)
		}
		if seen[p.Name] {
			return nil, errf("%s : sonde en double : %s", path, p.Name)
		}
		seen[p.Name] = true
		place := or(p.Place, "bottom")
		if place != "side" && place != "bottom" {
			return nil, errf("%s : sonde %s : place doit valoir side ou bottom", path, p.Name)
		}
		pr := &Probe{Name: p.Name, Run: p.Run, Interval: cfg.Interval, Timeout: 8, Place: place}
		if p.Interval != nil {
			pr.Interval = *p.Interval
		}
		if p.Timeout != nil {
			pr.Timeout = *p.Timeout
		}
		cfg.Probes = append(cfg.Probes, pr)
	}
	for _, n := range cfg.Order {
		for _, pn := range cfg.Scenarios[n].Probes {
			if !seen[pn] {
				return nil, errf("%s : scénario %s : sonde inconnue dans probes : %s", path, n, pn)
			}
		}
	}
	return cfg, nil
}

// rawDecks : [deck] (table) ou [[deck]] (liste de tables).
func rawDecks(v any) ([]rawDeck, error) {
	str := func(m map[string]any, k string) (string, error) {
		x, ok := m[k]
		if !ok {
			return "", nil
		}
		s, ok := x.(string)
		if !ok {
			return "", fmt.Errorf("deck : %s doit être une chaîne", k)
		}
		return s, nil
	}
	one := func(m map[string]any) (rawDeck, error) {
		var d rawDeck
		var err error
		for k := range m {
			if k != "name" && k != "source" && k != "html" {
				return d, fmt.Errorf("deck : clé inconnue %s (name, source, html)", k)
			}
		}
		if d.Name, err = str(m, "name"); err != nil {
			return d, err
		}
		if d.Source, err = str(m, "source"); err != nil {
			return d, err
		}
		d.HTML, err = str(m, "html")
		return d, err
	}
	switch t := v.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		d, err := one(t)
		return []rawDeck{d}, err
	case []map[string]any:
		var out []rawDeck
		for _, m := range t {
			d, err := one(m)
			if err != nil {
				return nil, err
			}
			out = append(out, d)
		}
		return out, nil
	}
	return nil, fmt.Errorf("deck : [deck] ou [[deck]] attendu")
}
