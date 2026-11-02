// Package cli : point d'entrée, preview <commande>.
package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/jgsqware/preview/assets"
	"github.com/jgsqware/preview/internal/builtin"
	"github.com/jgsqware/preview/internal/cassette"
	"github.com/jgsqware/preview/internal/config"
	"github.com/jgsqware/preview/internal/deck"
	"github.com/jgsqware/preview/internal/events"
	"github.com/jgsqware/preview/internal/html"
	"github.com/jgsqware/preview/internal/live"
	"github.com/jgsqware/preview/internal/probes"
	"github.com/jgsqware/preview/internal/rx"
	"github.com/jgsqware/preview/internal/scaffold"
	"github.com/jgsqware/preview/internal/tui"
)

// Version : remplacée au build (-ldflags "-X …cli.Version=x").
var Version = "0.3.0"

var B, D, R, G, Y, RED string

func initColors() {
	if term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == "" {
		B, D, R = "\033[1m", "\033[2m", "\033[0m"
		G, Y, RED = "\033[32m", "\033[33m", "\033[31m"
	}
}

// exit : sortie anticipée avec un code (die).
type exit struct{ code int }

func die(msg string, code int) {
	fmt.Fprintf(os.Stderr, "preview : %s\n", msg)
	panic(exit{code})
}

// -- arguments ---------------------------------------------------------------------------------

type args struct {
	C     string
	pos   []string
	flags map[string]string // nom -> valeur ("1" pour un booléen)
}

func (a *args) has(k string) bool   { _, ok := a.flags[k]; return ok }
func (a *args) get(k string) string { return a.flags[k] }
func (a *args) arg(i int) string {
	if i < len(a.pos) {
		return a.pos[i]
	}
	return ""
}

func (a *args) float(k string, def float64) float64 {
	if v, ok := a.flags[k]; ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			die(fmt.Sprintf("--%s : nombre attendu, reçu %q", k, v), 2)
		}
		return f
	}
	return def
}

type command struct {
	name, help, usage string
	bools             []string          // drapeaux sans valeur
	values            []string          // drapeaux avec valeur
	short             map[string]string // -o -> output
	maxPos            int               // -1 = illimité
	fn                func(*args) int
}

var commands []*command

func init() {
	commands = []*command{
		{name: "deck", help: "deck dans le terminal (sélecteur s'il y en a plusieurs)", usage: "[deck|demo.md] [--all] [--slide N]",
			bools: []string{"all"}, values: []string{"slide"}, maxPos: 1, fn: cmdDeck},
		{name: "html", help: "export HTML autonome", usage: "[deck|demo.md] [-o fichier.html] [--all]",
			bools: []string{"all"}, values: []string{"output"}, short: map[string]string{"-o": "output"}, maxPos: 1, fn: cmdHTML},
		{name: "live", help: "lance un scénario en direct (TUI), enregistré", usage: "[scénario] [--fallback] [--force] [--speed X]",
			bools: []string{"fallback", "force"}, values: []string{"speed"}, maxPos: 1, fn: cmdLive},
		{name: "watch", help: "tableau de bord seul (sondes + journal suivi)", usage: "[--once]",
			bools: []string{"once"}, fn: cmdWatch},
		{name: "replay", help: "rejoue un enregistrement", usage: "[fichier.jsonl|scénario] [--speed X] [--once]",
			bools: []string{"once"}, values: []string{"speed"}, maxPos: 1, fn: cmdReplay},
		{name: "rehearse", help: "joue les scénarios sans TUI et rafraîchit les enregistrements",
			usage: "[scénario…] [--check]", bools: []string{"check"}, maxPos: -1, fn: cmdRehearse},
		{name: "probe", help: "exécute les sondes une fois", usage: "[nom…] [--json]", bools: []string{"json"},
			maxPos: -1, fn: cmdProbe},
		{name: "check", help: "valide preview.toml, le deck et les sondes", fn: cmdCheck},
		{name: "init", help: "crée preview.toml + demo.md dans le projet", usage: "[--force]",
			bools: []string{"force"}, fn: cmdInit},
		{name: "skill", help: "skill Claude Code (install | show)", usage: "install|show [--dest DIR]",
			values: []string{"dest"}, maxPos: 1, fn: cmdSkill},
		{name: "builtin", help: "sondes intégrées : docker, system, http", usage: "docker|system|http [arguments…]",
			maxPos: -1, fn: cmdBuiltin},
	}
}

func usage() {
	fmt.Println("usage : preview [-C DIR] <commande> [options]")
	fmt.Println()
	fmt.Println("Présenter un projet : deck, démo live, enregistrements.")
	fmt.Println()
	fmt.Println("commandes :")
	for _, c := range commands {
		fmt.Printf("  %-9s %s\n", c.name, c.help)
	}
	fmt.Println()
	fmt.Println("options :")
	fmt.Println("  -C DIR     dossier du projet (défaut : le premier parent qui contient preview.toml)")
	fmt.Println("  --version  affiche la version")
	fmt.Println()
	fmt.Println("preview <commande> --help pour le détail d'une commande.")
}

func parse(c *command, argv []string, a *args) {
	isBool := func(k string) bool { return contains(c.bools, k) }
	isVal := func(k string) bool { return contains(c.values, k) }
	for i := 0; i < len(argv); i++ {
		s := argv[i]
		if c.name == "builtin" && len(a.pos) >= 1 { // le reste est passé tel quel à la sonde
			a.pos = append(a.pos, argv[i:]...)
			break
		}
		switch {
		case s == "-h" || s == "--help":
			fmt.Printf("usage : preview %s %s\n\n%s\n", c.name, c.usage, c.help)
			panic(exit{0})
		case s == "-C" && i+1 < len(argv):
			a.C = argv[i+1]
			i++
		case s == "--":
			a.pos = append(a.pos, argv[i+1:]...)
			i = len(argv)
		case strings.HasPrefix(s, "-") && len(s) > 1 && !isNumber(s):
			name, val, hasVal := strings.Cut(strings.TrimLeft(s, "-"), "=")
			if full, ok := c.short[s]; ok {
				name = full
			}
			switch {
			case isBool(name):
				a.flags[name] = "1"
			case isVal(name):
				if !hasVal {
					if i+1 >= len(argv) {
						die(fmt.Sprintf("%s : valeur attendue après %s", c.name, s), 2)
					}
					val = argv[i+1]
					i++
				}
				a.flags[name] = val
			default:
				die(fmt.Sprintf("%s : option inconnue %s (preview %s --help)", c.name, s, c.name), 2)
			}
		default:
			a.pos = append(a.pos, s)
		}
	}
	if c.maxPos >= 0 && len(a.pos) > c.maxPos {
		die(fmt.Sprintf("%s : argument en trop : %s", c.name, a.pos[c.maxPos]), 2)
	}
}

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Main : exécute la ligne de commande ; renvoie le code de sortie.
func Main(argv []string) (code int) {
	initColors()
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(exit); ok {
				code = e.code
				return
			}
			panic(r)
		}
	}()
	a := &args{flags: map[string]string{}}
	for len(argv) > 0 && strings.HasPrefix(argv[0], "-") {
		switch {
		case argv[0] == "-C" && len(argv) > 1:
			a.C = argv[1]
			argv = argv[2:]
		case strings.HasPrefix(argv[0], "-C") && len(argv[0]) > 2:
			a.C = argv[0][2:]
			argv = argv[1:]
		case argv[0] == "--version":
			fmt.Println("preview " + Version)
			return 0
		case argv[0] == "-h" || argv[0] == "--help":
			usage()
			return 0
		default:
			die("option inconnue "+argv[0]+" (preview --help)", 2)
		}
	}
	if len(argv) == 0 {
		usage()
		return 0
	}
	for _, c := range commands {
		if c.name == argv[0] {
			parse(c, argv[1:], a)
			return c.fn(a)
		}
	}
	die(fmt.Sprintf("commande inconnue : %s (preview --help)", argv[0]), 2)
	return 2
}

func loadCfg(a *args) *config.Config {
	cfg, err := config.Load(a.C)
	if err != nil {
		die(err.Error(), 2)
	}
	return cfg
}

func ask(prompt string) bool {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false
	}
	fmt.Printf("%s o/N ", prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "o", "oui", "y", "yes":
		return true
	}
	return false
}

func rel(p string) string {
	wd, _ := os.Getwd()
	if r, err := filepath.Rel(wd, p); err == nil {
		return r
	}
	return p
}

func mmss(s float64) string {
	n := int(math.Max(0, s))
	return fmt.Sprintf("%dm%02ds", n/60, n%60)
}

func stem(p string) string {
	b := filepath.Base(p)
	return strings.TrimSuffix(b, filepath.Ext(b))
}

// -- live / watch / replay ---------------------------------------------------------------------

func preflightOK(cfg *config.Config, sc *config.Scenario) bool {
	if sc.Preflight == "" {
		return true
	}
	cmd := exec.Command("bash", "-c", sc.Preflight)
	cmd.Dir = cfg.Root
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return false
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err == nil
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return false
	}
}

func cmdLive(a *args) int {
	cfg := loadCfg(a)
	sc, err := cfg.Scenario(a.arg(0))
	if err != nil {
		die(err.Error(), 2)
	}
	speed := a.float("speed", 2.0)
	if !preflightOK(cfg, sc) {
		rec := cassette.Latest(cfg.Recordings(), sc.Name, true)
		fmt.Printf("%s⚠ pré-requis en échec pour %s :%s %s\n", Y, sc.Name, R, sc.Preflight)
		if rec != "" && (a.has("fallback") || ask(fmt.Sprintf("Rejouer le dernier run VERT (%s) à la place ?", stem(rec)))) {
			return play(cfg, rec, speed, false, "⚠ live indisponible : enregistrement")
		}
		if !a.has("force") {
			die("live impossible (--force pour lancer quand même)", 1)
		}
	}
	app := live.New(cfg, "run", sc, "", speed, "")
	if err := live.RunTUI(app); err != nil {
		die(err.Error(), 1)
	}
	res := app.Run.Verdict()
	if res == "" {
		res = "arrêté"
	}
	fmt.Printf("%s · %s : %s en %s\n", cfg.Name, sc.Name, res, mmss(app.Elapsed()))
	if app.Rec != nil {
		fmt.Printf("enregistrement : %s · à rejouer : preview replay %s\n", rel(app.Rec.Path), sc.Name)
	}
	if app.Run.RC != nil {
		return *app.Run.RC & 0xff
	}
	return 130
}

func resolveRecording(cfg *config.Config, what string) string {
	if what != "" {
		if st, err := os.Stat(what); err == nil && st.Mode().IsRegular() {
			return what
		}
		_, known := cfg.Scenarios[what]
		if st, err := os.Stat(filepath.Join(cfg.Recordings(), what)); known || (err == nil && st.IsDir()) {
			if rec := cassette.Latest(cfg.Recordings(), what, false); rec != "" {
				return rec
			}
			die(fmt.Sprintf("aucun enregistrement pour %s (%s)", what, filepath.Join(cfg.Recordings(), what)), 2)
		}
		die(what+" : ni fichier, ni scénario connu", 2)
	}
	rec := cassette.Latest(cfg.Recordings(), "", false)
	if rec == "" {
		die(fmt.Sprintf("aucun enregistrement dans %s (preview live ou preview rehearse d'abord)", cfg.Recordings()), 2)
	}
	return rec
}

func termSize() (int, int) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		if c, e := strconv.Atoi(os.Getenv("COLUMNS")); e == nil {
			w = c
		} else {
			w = 120
		}
		if l, e := strconv.Atoi(os.Getenv("LINES")); e == nil {
			h = l
		} else {
			h = 40
		}
	}
	return w, h
}

func play(cfg *config.Config, path string, speed float64, once bool, banner string) int {
	if once {
		speed = 1e9
	} else {
		speed = math.Max(0.1, speed)
	}
	app := live.New(cfg, "replay", nil, path, speed, banner)
	if once {
		app.ReplayLoop()
		w, h := termSize()
		app.PrintOnce(os.Stdout, w, h)
		return 0
	}
	if err := live.RunTUI(app); err != nil {
		die(err.Error(), 1)
	}
	return 0
}

func cmdReplay(a *args) int {
	cfg := loadCfg(a)
	return play(cfg, resolveRecording(cfg, a.arg(0)), a.float("speed", 2.0), a.has("once"), "")
}

func cmdWatch(a *args) int {
	cfg := loadCfg(a)
	app := live.New(cfg, "watch", nil, "", 1, "")
	if a.has("once") {
		for _, p := range cfg.Probes {
			r := probes.RunOnce(p, cfg)
			app.Probes.Update(p.Name, &r, nil)
		}
		app.WatchStep(live.NewWatchState())
		w, h := termSize()
		app.PrintOnce(os.Stdout, w, h)
		return 0
	}
	if err := live.RunTUI(app); err != nil {
		die(err.Error(), 1)
	}
	return 0
}

// -- rehearse ----------------------------------------------------------------------------------

func gitOut(dir string, args ...string) (string, int, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode(), nil
	}
	return string(out), 0, err
}

// staleness : (« à jour » | « périmé » | « inconnu », détail).
func staleness(cfg *config.Config, rec string) (string, string) {
	_, meta := cassette.Outcome(rec)
	sha, _ := meta["git"].(string)
	started, _ := meta["started"].(string)
	when := strings.ReplaceAll(started, "T", " ")
	if r := []rune(when); len(r) > 16 {
		when = string(r[:16])
	}
	if sha == "" {
		return "inconnu", fmt.Sprintf("du %s, sans commit enregistré", when)
	}
	diff, rc, err := gitOut(cfg.Root, "diff", "--name-only", sha, "--", ".")
	if err != nil {
		return "inconnu", fmt.Sprintf("du %s (%s)", when, sha)
	}
	if rc != 0 {
		return "périmé", fmt.Sprintf("du %s, commit %s introuvable", when, sha)
	}
	dirty, _, err := gitOut(cfg.Root, "status", "--porcelain", "--", ".")
	if err != nil {
		return "inconnu", fmt.Sprintf("du %s (%s)", when, sha)
	}
	relState, _ := filepath.Rel(cfg.Root, cfg.StateDir)
	files := map[string]bool{}
	for _, l := range strings.Split(diff, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files[l] = true
		}
	}
	for _, l := range strings.Split(dirty, "\n") {
		if len(l) <= 3 {
			continue
		}
		f := strings.TrimSpace(l[3:])
		if f != "" && !strings.HasPrefix(l[3:], relState) {
			files[f] = true
		}
	}
	if len(files) > 0 {
		return "périmé", fmt.Sprintf("du %s (%s), %d fichier(s) modifié(s) depuis", when, sha, len(files))
	}
	return "à jour", fmt.Sprintf("du %s (%s)", when, sha)
}

func padRight(s string, n int) string {
	if w := tui.Width(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

func padLeft(s string, n int) string {
	if w := tui.Width(s); w < n {
		return strings.Repeat(" ", n-w) + s
	}
	return s
}

func cmdRehearse(a *args) int {
	cfg := loadCfg(a)
	names := a.pos
	if len(names) == 0 {
		names = deckScenarios(cfg)
	}
	if len(names) == 0 {
		sc, err := cfg.Scenario("")
		if err != nil {
			die(err.Error(), 2)
		}
		names = []string{sc.Name}
	}
	for _, n := range names {
		if _, ok := cfg.Scenarios[n]; !ok {
			die("scénario inconnu : "+n, 2)
		}
	}
	if a.has("check") {
		allOK := true
		for _, n := range names {
			rec := cassette.Latest(cfg.Recordings(), n, true)
			var st, mark, detail string
			if rec == "" {
				last := cassette.Latest(cfg.Recordings(), n, false)
				st, mark, detail = "jamais joué", RED+"✘"+R, "preview rehearse "+n
				if last != "" {
					st, detail = "jamais VERT", "dernier run : "+stem(last)
				}
			} else {
				st, detail = staleness(cfg, rec)
				mark = Y + "⚠" + R
				if st == "à jour" {
					mark = G + "✔" + R
				}
			}
			allOK = allOK && st == "à jour"
			fmt.Printf("%s %s%s%s %s %s%s%s\n", mark, B, padRight(n, 16), R, padRight(st, 12), D, detail, R)
		}
		if allOK {
			return 0
		}
		return 1
	}
	type result struct {
		name, res string
		dur       float64
		path      string
	}
	var results []result
	intr := make(chan os.Signal, 1)
	signal.Notify(intr, os.Interrupt)
	defer signal.Stop(intr)
loop:
	for _, n := range names {
		sc := cfg.Scenarios[n]
		fmt.Printf("%s▶ %s%s %s%s%s\n", B, n, R, D, sc.Run, R)
		if !preflightOK(cfg, sc) {
			fmt.Printf("  %s✘ pré-requis en échec : %s%s\n", RED, sc.Preflight, R)
			results = append(results, result{n, "pré-requis", 0, ""})
			continue
		}
		app := live.New(cfg, "run", sc, "", 1, "")
		if err := app.Start(); err != nil {
			die(err.Error(), 1)
		}
		shown := map[int]string{}
		tick := time.NewTicker(500 * time.Millisecond)
		for !finished(app) {
			select {
			case <-intr:
				tick.Stop()
				app.Kill()
				app.WaitMain(10 * time.Second)
				reportSteps(app, shown)
				results = append(results, result{n, "interrompu", app.Elapsed(), app.Rec.Path})
				app.Stop()
				break loop
			case <-tick.C:
				reportSteps(app, shown)
			}
		}
		tick.Stop()
		app.WaitMain(10 * time.Second)
		reportSteps(app, shown)
		results = append(results, result{n, app.Run.Verdict(), app.Elapsed(), app.Rec.Path})
		app.Stop()
	}
	fmt.Println()
	allOK := len(results) > 0
	for _, r := range results {
		mark := RED + "✘" + R
		if r.res == "VERT" {
			mark = G + "✔" + R
		} else {
			allOK = false
		}
		where := ""
		if r.path != "" {
			where = " " + D + rel(r.path) + R
		}
		fmt.Printf("%s %s%s%s %s %s%s\n", mark, B, padRight(r.name, 16), R, padRight(r.res, 11), padLeft(mmss(r.dur), 7), where)
	}
	if allOK {
		return 0
	}
	return 1
}

func finished(app *live.App) bool {
	app.Mu.Lock()
	defer app.Mu.Unlock()
	return app.Finished
}

func reportSteps(app *live.App, shown map[int]string) {
	app.Mu.Lock()
	defer app.Mu.Unlock()
	for i, st := range app.Run.Steps {
		if (st.State == "done" || st.State == "failed" || st.State == "skipped") && shown[i] != st.State {
			shown[i] = st.State
			mark := map[string]string{"done": G + "✔" + R, "failed": RED + "✘" + R, "skipped": D + "–" + R}[st.State]
			dur := ""
			if st.T0 != nil {
				t1 := 0.0
				if st.T1 != nil {
					t1 = *st.T1
				}
				dur = " " + D + mmss(t1-*st.T0) + R
			}
			fmt.Printf("  %s %s%s\n", mark, st.Name, dur)
			for _, ck := range st.Checks {
				if ck.OK {
					fmt.Printf("      %s✔%s %s\n", G, R, ck.Text)
				} else {
					fmt.Printf("      %s✘%s %s\n", RED, R, ck.Text)
				}
			}
		}
	}
}

// deckScenarios : scénarios cités par les blocs ```live de tous les decks.
func deckScenarios(cfg *config.Config) []string {
	var out []string
	for _, dk := range cfg.Decks {
		d, err := deck.Parse(dk.Source)
		if err != nil {
			continue
		}
		for _, s := range deck.LiveScenarios(d) {
			if _, ok := cfg.Scenarios[s]; ok && !contains(out, s) {
				out = append(out, s)
			}
		}
	}
	return out
}

// -- deck / html -------------------------------------------------------------------------------

// hooks : touches d / w / r du deck terminal ; relance preview dans un sous-processus.
type hooks struct{ cfg *config.Config }

func (h hooks) sub(argv ...string) {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	cmd := exec.Command(exe, append([]string{"-C", h.cfg.Root}, argv...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	_ = cmd.Run()
}

func (h hooks) Run(scenario string)    { h.sub(withArg("live", scenario)...) }
func (h hooks) Watch()                 { h.sub("watch") }
func (h hooks) Replay(scenario string) { h.sub(withArg("replay", scenario)...) }

func withArg(cmd, arg string) []string {
	if arg == "" {
		return []string{cmd}
	}
	return []string{cmd, arg}
}

// deckOf : arg = nom d'un deck de preview.toml, ou fichier ; vide = premier deck. nil si arg est un fichier hors config.
func deckOf(cfg *config.Config, arg string) *config.Deck {
	if cfg != nil {
		if arg == "" {
			return cfg.Decks[0]
		}
		if d := cfg.Deck(arg); d != nil && !isFile(arg) {
			return d
		}
		abs, _ := filepath.Abs(arg)
		for _, d := range cfg.Decks {
			if d.Source == abs {
				return d
			}
		}
	}
	if arg == "" {
		die("pas de preview.toml : donne le fichier du deck", 2)
	}
	return nil
}

func deckPath(cfg *config.Config, arg string) string {
	if d := deckOf(cfg, arg); d != nil {
		return d.Source
	}
	return arg
}

func deckChoices(cfg *config.Config) []deck.Choice {
	out := make([]deck.Choice, len(cfg.Decks))
	for i, dk := range cfg.Decks {
		r, _ := filepath.Rel(cfg.Root, dk.Source)
		detail := r
		if d, err := deck.Parse(dk.Source); err != nil {
			detail += " · introuvable ou illisible"
		} else {
			if t := deck.Plain(d.Title); t != "" {
				detail = t + " · " + detail
			}
			detail += fmt.Sprintf(" · %d diapositive(s)", d.Main())
		}
		out[i] = deck.Choice{Name: dk.Name, Detail: detail}
	}
	return out
}

func tryCfg(a *args) *config.Config {
	cfg, err := config.Load(a.C)
	if err != nil {
		if a.C != "" {
			die(err.Error(), 2)
		}
		return nil
	}
	return cfg
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func cmdDeck(a *args) int {
	cfg := tryCfg(a)
	if cfg != nil && a.arg(0) == "" && len(cfg.Decks) > 1 && !a.has("all") &&
		term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
		// plusieurs decks : sélecteur, on y revient en quittant un deck
		items, cur := deckChoices(cfg), 0
		for {
			cur = deck.Choose(cfg.Name+" · decks", items, cur)
			if cur < 0 {
				return 0
			}
			showDeck(a, cfg, cfg.Decks[cur].Source)
		}
	}
	return showDeck(a, cfg, deckPath(cfg, a.arg(0)))
}

func showDeck(a *args, cfg *config.Config, path string) int {
	if !isFile(path) {
		die(path+" introuvable", 2)
	}
	d, err := deck.Parse(path)
	if err != nil {
		die(err.Error(), 2)
	}
	start := 0
	if a.has("slide") {
		n, err := strconv.Atoi(a.get("slide"))
		if err != nil {
			die("--slide : entier attendu", 2)
		}
		start = max(0, n-1)
	}
	var lh deck.LiveHooks
	if cfg != nil {
		lh = hooks{cfg}
	}
	deck.RunTerminal(d, start, a.has("all"), lh)
	return 0
}

func cmdHTML(a *args) int {
	cfg := tryCfg(a)
	if a.has("all") {
		if cfg == nil {
			die("html --all : pas de preview.toml", 2)
		}
		if a.arg(0) != "" || a.has("output") {
			die("html --all : ni deck ni -o (chaque deck va dans son html)", 2)
		}
		rc := 0
		for _, dk := range cfg.Decks {
			if !isFile(dk.Source) {
				fmt.Fprintf(os.Stderr, "%s✘%s %s : %s introuvable\n", RED, R, dk.Name, rel(dk.Source))
				rc = 2
				continue
			}
			exportHTML(cfg, dk.Source, dk.HTML)
		}
		return rc
	}
	dk := deckOf(cfg, a.arg(0))
	path := a.arg(0)
	if dk != nil {
		path = dk.Source
	}
	if !isFile(path) {
		die(path+" introuvable", 2)
	}
	out := a.get("output")
	if out == "" {
		if dk != nil {
			out = dk.HTML
		} else {
			out = strings.TrimSuffix(path, filepath.Ext(path)) + ".html"
		}
	}
	if cfg != nil && a.arg(0) == "" && len(cfg.Decks) > 1 {
		fmt.Fprintf(os.Stderr, "%s%d autre(s) deck(s) : preview html --all%s\n", D, len(cfg.Decks)-1, R)
	}
	if cfg == nil {
		dir, _ := filepath.Abs(filepath.Dir(path))
		cfg = config.New(dir, stem(path))
		cfg.StateDir = filepath.Join(filepath.Dir(path), ".preview")
		cfg.DeckSource, cfg.DeckHTML = path, out
	}
	exportHTML(cfg, path, out)
	return 0
}

func exportHTML(cfg *config.Config, path, out string) {
	res, err := html.Export(cfg, path, out, func(m string) { fmt.Fprintf(os.Stderr, "⚠ %s\n", m) })
	if err != nil {
		die(err.Error(), 2)
	}
	size := int64(0)
	if st, err := os.Stat(res); err == nil {
		size = st.Size()
	}
	fmt.Printf("✔ %s (%d Ko)\n", rel(res), size/1024)
}

// -- probe / check -----------------------------------------------------------------------------

func cmdProbe(a *args) int {
	cfg := loadCfg(a)
	var list []*config.Probe
	for _, p := range cfg.Probes {
		if len(a.pos) == 0 || contains(a.pos, p.Name) {
			list = append(list, p)
		}
	}
	if len(list) == 0 {
		msg := "aucune sonde dans preview.toml"
		if len(a.pos) > 0 {
			msg = "aucune sonde nommée " + strings.Join(a.pos, ", ")
		}
		die(msg, 2)
	}
	rc := 0
	w, _ := termSize()
	w = max(80, w)
	color := tui.ColorOn(term.IsTerminal(int(os.Stdout.Fd())))
	attrs := tui.Attrs(color)
	for _, p := range list {
		r := probes.RunOnce(p, cfg)
		if a.has("json") {
			obj := map[string]any{"name": p.Name, "ok": r.OK, "data": r.Data, "error": r.Error, "t": r.T, "ms": r.MS}
			b, _ := json.MarshalIndent(obj, "", "  ")
			b = []byte(strings.NewReplacer(`<`, "<", `>`, ">", `&`, "&").Replace(string(b)))
			fmt.Println(string(b))
			continue
		}
		errS := ""
		if !r.OK {
			errS = r.Error
		}
		h := tui.ProbeHeight(r.Data, errS, w)
		c := tui.NewCanvas(h, w)
		st := probes.NewState(nil)
		if r.OK {
			st.Update(p.Name, &r, nil)
		}
		tui.DrawProbe(c, 0, 0, h, w, p.Name, st.Last[p.Name], errS, st.History)
		if color {
			fmt.Println(strings.TrimPrefix(c.ANSI(attrs), "\x1b[H"))
		} else {
			fmt.Println(c.Text())
		}
		fmt.Printf("%s  %s · %.0f ms%s\n", D, p.Run, r.MS, R)
		if !r.OK {
			rc = 1
		}
	}
	return rc
}

func cmdCheck(a *args) int {
	cfg := loadCfg(a)
	ok := true
	line := func(good bool, text string) {
		ok = ok && good
		mark := RED + "✘"
		if good {
			mark = G + "✔"
		}
		fmt.Printf("%s%s %s\n", mark, R, text)
	}
	line(true, fmt.Sprintf("preview.toml : %s · %d deck(s) · %d scénario(s) · %d sonde(s)", cfg.Name, len(cfg.Decks), len(cfg.Scenarios), len(cfg.Probes)))
	for _, k := range []string{"step", "ok", "fail", "result_ok", "result_fail", "ignore_step", "command", "plan"} {
		if v := cfg.Rules[k]; v != "" {
			if _, err := rx.Compile(v); err != nil {
				line(false, fmt.Sprintf("live.rules.%s : regex invalide : %v", k, err))
			}
		}
	}
	for _, n := range cfg.Order {
		sc := cfg.Scenarios[n]
		plan := events.PlannedSteps(sc.Plan, cfg.Rules["plan"])
		good := sc.Plan == "" || isFile(sc.Plan)
		extra := ""
		if !good {
			extra = " · plan introuvable " + sc.Plan
		}
		if len(sc.Probes) > 0 {
			extra += " · sondes : " + strings.Join(sc.Probes, ", ")
		}
		line(good, fmt.Sprintf("scénario %s : %d étape(s) prévue(s)%s", sc.Name, len(plan), extra))
	}
	for _, dk := range cfg.Decks {
		if !isFile(dk.Source) {
			line(false, fmt.Sprintf("deck %s introuvable : %s", dk.Name, dk.Source))
			continue
		}
		d, err := deck.Parse(dk.Source)
		if err != nil {
			line(false, "deck "+dk.Name+" : "+err.Error())
		} else {
			var unknown []string
			for _, s := range deck.LiveScenarios(d) {
				if _, ok := cfg.Scenarios[s]; !ok {
					unknown = append(unknown, s)
				}
			}
			r, _ := filepath.Rel(cfg.Root, dk.Source)
			o, _ := filepath.Rel(cfg.Root, dk.HTML)
			extra := ""
			if len(unknown) > 0 {
				extra = " · scénarios inconnus : " + strings.Join(unknown, ", ")
			}
			line(len(unknown) == 0, fmt.Sprintf("deck %s (%s → %s) : %d diapositive(s)%s", dk.Name, r, o, len(d.Slides), extra))
		}
	}
	for _, p := range cfg.Probes {
		r := probes.RunOnce(p, cfg)
		if r.OK {
			line(true, fmt.Sprintf("sonde %s : %s en %.0f ms", p.Name, probes.Str(r.Data, "status", "?"), r.MS))
		} else {
			line(false, fmt.Sprintf("sonde %s : %s", p.Name, r.Error))
		}
	}
	if ok {
		return 0
	}
	return 1
}

// -- init / skill / builtin --------------------------------------------------------------------

func cmdInit(a *args) int {
	root := a.C
	if root == "" {
		root = "."
	}
	abs, _ := filepath.Abs(root)
	return scaffold.Init(abs, a.has("force"))
}

func cmdSkill(a *args) int {
	switch a.arg(0) {
	case "show":
		fmt.Print(assets.Skill)
		return 0
	case "install":
	default:
		die("skill : action attendue : install ou show", 2)
	}
	dest := a.get("dest")
	if dest == "" {
		home, _ := os.UserHomeDir()
		dest = filepath.Join(home, ".claude", "skills", "preview")
	} else if strings.HasPrefix(dest, "~/") {
		home, _ := os.UserHomeDir()
		dest = filepath.Join(home, dest[2:])
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		die(err.Error(), 1)
	}
	target := filepath.Join(dest, "SKILL.md")
	if err := os.WriteFile(target, []byte(assets.Skill), 0o644); err != nil {
		die(err.Error(), 1)
	}
	fmt.Printf("✔ skill installé : %s\n", target)
	return 0
}

func cmdBuiltin(a *args) int {
	name := a.arg(0)
	if !contains([]string{"docker", "system", "http"}, name) {
		die("builtin : sonde attendue : docker, system ou http", 2)
	}
	return builtin.Main(name, a.pos[1:])
}
