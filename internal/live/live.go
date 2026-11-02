// Package live : démo live — lance un scénario, suit sa sortie, montre les sondes ; modes watch et replay.
package live

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jgsqware/preview/internal/cassette"
	"github.com/jgsqware/preview/internal/config"
	"github.com/jgsqware/preview/internal/events"
	"github.com/jgsqware/preview/internal/probes"
	"github.com/jgsqware/preview/internal/tui"
)

// App : état partagé entre les goroutines (processus, sondes, relecture) et le rendu.
type App struct {
	Cfg      *config.Config
	Mode     string // run | watch | replay
	Speed    float64
	Banner   string
	Scenario *config.Scenario
	Replay   string

	Mu       sync.Mutex
	stop     chan struct{}
	stopOnce sync.Once
	paused   atomic.Bool

	parser *events.Parser
	plan   []string
	Run    *events.Run
	Probes *probes.State
	place  map[string]string

	probeList []*config.Probe // sondes de ce scénario (toutes hors scénario)

	t0       time.Time
	clock    float64
	Finished bool
	scroll   int
	fullLog  bool
	quitArm  bool
	frame    int
	proc     *exec.Cmd
	Rec      *cassette.Recorder
	meta     map[string]any
	watchLog string
	timedOut bool
	mainDone chan struct{} // fermé quand la goroutine principale (processus / relecture) a fini
}

// New : mode run (scenario), watch, ou replay (fichier).
func New(cfg *config.Config, mode string, sc *config.Scenario, replay string, speed float64, banner string) *App {
	a := &App{Cfg: cfg, Mode: mode, Speed: speed, Banner: banner, Scenario: sc, Replay: replay,
		stop: make(chan struct{}), parser: events.NewParser(cfg.Rules), place: map[string]string{},
		t0: time.Now(), meta: map[string]any{}, mainDone: make(chan struct{})}
	if sc != nil {
		a.plan = events.PlannedSteps(sc.Plan, cfg.Rules["plan"])
	}
	a.Run = events.NewRun(a.parser, a.plan)
	a.probeList = cfg.ProbesFor(sc)
	names := make([]string, len(a.probeList))
	for i, p := range a.probeList {
		names[i] = p.Name
		a.place[p.Name] = p.Place
	}
	a.Probes = probes.NewState(names)
	return a
}

// Stop : arrête toutes les goroutines.
func (a *App) Stop() { a.stopOnce.Do(func() { close(a.stop) }) }

func (a *App) stopped() bool {
	select {
	case <-a.stop:
		return true
	default:
		return false
	}
}

// wait : attend d ou l'arrêt ; vrai si arrêté.
func (a *App) wait(d time.Duration) bool {
	select {
	case <-a.stop:
		return true
	case <-time.After(d):
		return false
	}
}

func (a *App) now() float64 { return time.Since(a.t0).Seconds() }

// -- goroutines ----------------------------------------------------------------------------------

func (a *App) snapLoop() {
	for !a.wait(time.Duration(a.Cfg.Interval * float64(time.Second))) {
		if a.Rec != nil {
			a.Rec.Event("snap", a.Probes.Snap())
		}
	}
}

func (a *App) processLoop() {
	defer close(a.mainDone)
	sc := a.Scenario
	cmd := exec.Command("bash", "-c", sc.Run)
	cmd.Dir = a.Cfg.Root
	env := append(os.Environ(), "PYTHONUNBUFFERED=1", "PREVIEW_PROJECT_DIR="+a.Cfg.Root,
		"PREVIEW_STATE_DIR="+a.Cfg.StateDir)
	for k, v := range sc.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	pr, pw, err := os.Pipe()
	if err == nil {
		cmd.Stdout, cmd.Stderr = pw, pw
		newSession(cmd)
		err = cmd.Start()
		pw.Close()
	}
	if err != nil {
		a.Mu.Lock()
		a.Run.Line(fmt.Sprintf("✘ impossible de lancer « %s » : %v", sc.Run, err), 0)
		a.Run.Finish(1, 0)
		a.Finished = true
		a.Mu.Unlock()
		if a.Rec != nil {
			a.Rec.Close(1, false)
		}
		return
	}
	a.Mu.Lock()
	a.proc = cmd
	a.Mu.Unlock()
	if sc.Timeout > 0 {
		go a.watchdog(sc.Timeout)
	}
	rd := bufio.NewReaderSize(pr, 64*1024)
	for {
		line, err := rd.ReadString('\n')
		if line != "" {
			line = strings.ToValidUTF8(line, "�")
			a.Mu.Lock()
			a.Run.Feed(line, a.now())
			a.Mu.Unlock()
			if a.Rec != nil {
				a.Rec.Event("line", events.Clean(line))
			}
		}
		if err != nil {
			break
		}
	}
	pr.Close()
	rc := 0
	if err := cmd.Wait(); err != nil {
		rc = exitCode(cmd, err)
	}
	a.Mu.Lock()
	if a.timedOut {
		a.Run.Line(fmt.Sprintf("✘ délai dépassé (%s s) : scénario arrêté", events.G(sc.Timeout)), a.now())
	}
	a.Run.Finish(rc, a.now())
	a.Finished = true
	ok := a.Run.Result != nil && *a.Run.Result
	a.Mu.Unlock()
	if a.Rec != nil {
		a.Rec.Event("snap", a.Probes.Snap())
		a.Rec.Close(rc, ok)
	}
}

func (a *App) watchdog(timeout float64) {
	if !a.wait(time.Duration(timeout*float64(time.Second))) && !a.isFinished() {
		a.Mu.Lock()
		a.timedOut = true
		a.Mu.Unlock()
		a.Kill()
	}
}

func (a *App) isFinished() bool {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	return a.Finished
}

// watchState : journal suivi en mode watch.
type watchState struct {
	path string
	fh   *os.File
	rd   *bufio.Reader
	part string
}

func (a *App) watchLoop() {
	st := &watchState{}
	for !a.stopped() {
		a.WatchStep(st)
		if a.wait(time.Second) {
			return
		}
	}
}

// NewWatchState : état de suivi pour un appel unique de WatchStep.
func NewWatchState() *watchState { return &watchState{} }

// WatchStep : cfg.follow est soit le journal, soit un fichier qui contient son chemin.
func (a *App) WatchStep(st *watchState) {
	f := a.Cfg.Follow
	if f == "" {
		return
	}
	info, err := os.Stat(f)
	if err != nil {
		return
	}
	target := ""
	if info.Size() < 4096 {
		if data, err := os.ReadFile(f); err == nil {
			txt := strings.TrimSpace(strings.ToValidUTF8(string(data), "�"))
			if txt != "" && !strings.Contains(txt, "\n") {
				cand := txt
				if !filepath.IsAbs(cand) {
					cand = filepath.Join(filepath.Dir(f), cand)
				}
				if ci, err := os.Stat(cand); err == nil && ci.Mode().IsRegular() {
					target = cand
				}
			}
		}
	}
	if target == "" {
		target = f
	}
	if target != st.path {
		if st.fh != nil {
			st.fh.Close()
		}
		fh, err := os.Open(target)
		if err != nil {
			return
		}
		st.fh, st.rd, st.path, st.part = fh, bufio.NewReader(fh), target, ""
		rel, _ := filepath.Rel(a.Cfg.Root, target)
		a.Mu.Lock()
		a.Run = events.NewRun(a.parser.Clone(), nil)
		a.watchLog = rel
		a.Mu.Unlock()
	}
	for {
		line, err := st.rd.ReadString('\n')
		if err != nil {
			st.part += line // ligne incomplète : on attend la suite
			return
		}
		line = st.part + line
		st.part = ""
		a.Mu.Lock()
		a.Run.Feed(strings.ToValidUTF8(line, "�"), a.now())
		a.Mu.Unlock()
	}
}

// ReplayLoop : rejoue l'enregistrement à la vitesse choisie.
func (a *App) ReplayLoop() {
	defer close(a.mainDone)
	prev := 0.0
	meta, evs, err := cassette.Read(a.Replay)
	if err != nil {
		a.Mu.Lock()
		a.Run.Line("✘ enregistrement illisible : "+err.Error(), 0)
		a.Finished = true
		a.Mu.Unlock()
		return
	}
	var plan []string
	hasPlan := false
	if p, ok := meta["plan"].([]any); ok {
		hasPlan = true
		for _, x := range p {
			plan = append(plan, tui.PyNum(x))
		}
	}
	if !hasPlan {
		if name, ok := meta["scenario"].(string); ok {
			if sc, ok := a.Cfg.Scenarios[name]; ok {
				plan = events.PlannedSteps(sc.Plan, a.Cfg.Rules["plan"])
			}
		}
	}
	a.Mu.Lock()
	a.meta = meta
	a.plan = plan
	a.Run = events.NewRun(a.parser, plan)
	a.Mu.Unlock()
	for _, ev := range evs {
		if a.stopped() {
			return
		}
		t := ev.T
		delay := math.Min(math.Max(0, t-prev), 2.0) / a.Speed
		end := time.Now().Add(time.Duration(delay * float64(time.Second)))
		for time.Now().Before(end) || a.paused.Load() {
			if a.wait(50 * time.Millisecond) {
				return
			}
		}
		prev = t
		a.Mu.Lock()
		a.clock = t
		switch ev.K {
		case "line":
			s, _ := ev.V.(string)
			a.Run.Feed(s, t)
		case "end":
			rc := 0
			if v, ok := ev.V.(float64); ok {
				rc = int(v)
			}
			a.Run.Finish(rc, t)
			a.Finished = true
		}
		a.Mu.Unlock()
		if ev.K == "snap" {
			a.Probes.LoadSnap(ev.V)
		}
	}
	a.Mu.Lock()
	if !a.Finished {
		a.Run.Finish(1, prev)
	}
	a.Finished = true
	a.Mu.Unlock()
}

// Start : lance les goroutines du mode.
func (a *App) Start() error {
	if (a.Mode == "run" || a.Mode == "watch") && len(a.probeList) > 0 {
		probes.StartPoller(a.Cfg, a.probeList, a.Probes, a.stop)
	}
	switch a.Mode {
	case "run":
		sc := a.Scenario
		rec, err := cassette.NewRecorder(a.Cfg.Recordings(), sc.Name, map[string]any{
			"title": sc.Title, "cmd": sc.Run, "git": cassette.GitSHA(a.Cfg.Root), "plan": nonNil(a.plan),
			"project": a.Cfg.Name}, cassette.NewRedactor(a.Cfg.Redact, nil))
		if err != nil {
			return err
		}
		a.Rec = rec
		go a.processLoop()
		go a.snapLoop()
	case "watch":
		close(a.mainDone)
		go a.watchLoop()
	default:
		go a.ReplayLoop()
	}
	return nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// WaitMain : attend la fin du processus ou de la relecture (au plus d).
func (a *App) WaitMain(d time.Duration) {
	select {
	case <-a.mainDone:
	case <-time.After(d):
	}
}

// Elapsed : durée affichée.
func (a *App) Elapsed() float64 {
	if a.Finished && len(a.Run.Steps) > 0 {
		end, any := 0.0, false
		for _, s := range a.Run.Steps {
			if s.T1 != nil {
				end, any = math.Max(end, *s.T1), true
			}
		}
		if any {
			return end
		}
	}
	if a.Mode == "replay" {
		return a.clock
	}
	return a.now()
}

// Kill : SIGTERM au groupe du scénario.
func (a *App) Kill() {
	a.Mu.Lock()
	p := a.proc
	done := a.Finished
	a.Mu.Unlock()
	if p != nil && p.Process != nil && !done {
		killGroup(p)
	}
}

// -- rendu ---------------------------------------------------------------------------------------

// Render : image complète (h lignes × w colonnes).
func (a *App) Render(h, w int) *tui.Canvas {
	c := tui.NewCanvas(h, w)
	a.Mu.Lock()
	defer a.Mu.Unlock()
	a.Probes.Mu.RLock()
	defer a.Probes.Mu.RUnlock()
	if w < 80 || h < 20 {
		c.Put(0, 0, fmt.Sprintf("Terminal trop petit : 80 × 20 minimum (actuel %d × %d). q pour quitter.", w, h), "acc")
		return c
	}
	a.drawHeader(c)
	top, bot := 1, h-1
	if a.fullLog {
		a.drawLog(c, top, 0, bot-top, w)
		a.drawFooter(c)
		return c
	}
	var side, bottom []string
	for _, n := range a.Probes.Order {
		if a.placeOf(n) == "side" {
			side = append(side, n)
		} else {
			bottom = append(bottom, n)
		}
	}
	if a.Mode == "replay" { // seulement les sondes présentes dans l'enregistrement
		var s2, b2 []string
		for _, n := range side {
			if _, ok := a.Probes.Last[n]; ok {
				s2 = append(s2, n)
			}
		}
		for _, n := range a.Probes.Order {
			if _, ok := a.Probes.Last[n]; ok && !contains(s2, n) {
				b2 = append(b2, n)
			}
		}
		side, bottom = s2, b2
	}
	avail := bot - top
	lw := w
	if len(side) > 0 {
		lw = max(40, int(float64(w)*0.40))
	}
	sideH := make([]int, len(side))
	sum := 0
	for i, n := range side {
		sideH[i] = tui.ProbeHeight(a.Probes.Last[n], a.Probes.Err[n], w-lw)
		sum += sideH[i]
	}
	topH := max(len(a.stepLines())+2, sum, 6)
	topH = min(topH, max(6, int(float64(avail)*0.45)))
	botH := make([]int, len(bottom))
	for i, n := range bottom {
		botH[i] = tui.ProbeHeight(a.Probes.Last[n], a.Probes.Err[n], w)
	}
	room := max(0, avail-topH-6)
	for total(botH) > room && len(botH) > 0 {
		k := 0
		for i := range botH {
			if botH[i] > botH[k] {
				k = i
			}
		}
		if botH[k] <= 3 {
			bottom, botH = bottom[:len(bottom)-1], botH[:len(botH)-1]
		} else {
			botH[k]--
		}
	}
	midH := avail - topH - total(botH)
	a.drawSteps(c, top, 0, topH, lw)
	if len(side) > 0 {
		y, remaining := top, topH
		for i, n := range side {
			hh := remaining
			if i != len(side)-1 {
				hh = min(sideH[i], max(3, remaining-3*(len(side)-i-1)))
			}
			a.probe(c, y, lw, hh, w-lw, n)
			y += hh
			remaining -= hh
		}
	}
	a.drawLog(c, top+topH, 0, midH, w)
	y := top + topH + midH
	for i, n := range bottom {
		a.probe(c, y, 0, botH[i], w, n)
		y += botH[i]
	}
	a.drawFooter(c)
	return c
}

func total(xs []int) int {
	s := 0
	for _, x := range xs {
		s += x
	}
	return s
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func (a *App) placeOf(n string) string {
	if p, ok := a.place[n]; ok {
		return p
	}
	return "bottom"
}

func (a *App) probe(c *tui.Canvas, y, x, h, w int, name string) {
	if h < 3 {
		return
	}
	tui.DrawProbe(c, y, x, h, w, name, a.Probes.Last[name], a.Probes.Err[name], a.Probes.History)
}

func (a *App) status() (string, string) {
	if a.Mode == "watch" {
		worst := a.worstLocked()
		label := "◉ OBSERVATION"
		if worst != "" && worst != "ok" {
			label += " · " + strings.ToUpper(worst)
		}
		return label, "bar_watch"
	}
	if !a.Finished {
		if a.paused.Load() {
			return "❚❚ PAUSE", "bar_run"
		}
		return tui.SpinFrame(a.frame) + " EN COURS", "bar_run"
	}
	if a.Run.Result != nil && *a.Run.Result {
		return "✔ VERT", "bar_ok"
	}
	return "✘ ROUGE", "bar_ko"
}

// worstLocked : comme probes.State.Worst, verrou déjà pris.
func (a *App) worstLocked() string {
	worst, best := "", -1
	consider := func(st string) {
		r, ok := probes.Rank[st]
		if !ok {
			r = 1
		}
		if r > best {
			worst, best = st, r
		}
	}
	for _, o := range a.Probes.Last {
		consider(probes.Str(o, "status", "unknown"))
	}
	if len(a.Probes.Err) > 0 {
		consider("fail")
	}
	return worst
}

func (a *App) drawHeader(c *tui.Canvas) {
	style := "bar"
	if a.Mode == "replay" {
		style = "bar_replay"
	}
	c.Fill(0, style)
	var what string
	switch a.Mode {
	case "run":
		what = a.Scenario.Name
		if a.Scenario.Title != "" {
			what += " · " + a.Scenario.Title
		}
	case "watch":
		what = "lecture seule"
	default:
		speed := "REPLAY ×" + events.G(a.Speed)
		if a.Speed > 1000 {
			speed = "bilan"
		}
		started := strings.ReplaceAll(str(a.meta["started"]), "T", " ")
		if r := []rune(started); len(r) > 16 {
			started = string(r[:16])
		}
		sc := str(a.meta["scenario"])
		if sc == "" {
			sc = "?"
		}
		what = speed + " · " + sc + " · " + started
		if git := str(a.meta["git"]); git != "" {
			what += " · " + git
		}
	}
	left := fmt.Sprintf(" %s live │ %s │ ⏱ %s ", a.Cfg.Name, what, tui.MMSS(a.Elapsed()))
	if a.Banner != "" {
		left += "│ " + a.Banner + " "
	}
	label, st := a.status()
	c.Put(0, 0, tui.Clip(left, c.W-tui.Width(label)-4), style)
	c.Put(0, c.W-tui.Width(label)-2, " "+label+" ", st)
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return tui.PyNum(v)
}

type stepLine struct {
	style, text, dur string
	cur, main        bool // main : ligne d'étape (pas une vérification)
}

func (a *App) stepLines() []stepLine {
	if a.Mode == "watch" {
		follow := a.watchLog
		if follow == "" && a.Cfg.Follow != "" {
			follow, _ = filepath.Rel(a.Cfg.Root, a.Cfg.Follow)
		}
		head := "Aucun journal suivi (live.follow)."
		if follow != "" {
			head = "Journal suivi :"
		}
		return []stepLine{{style: "dim", text: "Lecture seule : rien n'est lancé."}, {style: "text", text: head},
			{style: "acc", text: follow}, {style: "text"},
			{style: "dim", text: strings.TrimRight("Démo complète : preview live "+a.Cfg.Default, " ")}}
	}
	var out []stepLine
	for i, st := range a.Run.Steps {
		mark, sty := tui.SpinFrame(a.frame), "acc"
		switch st.State {
		case "done":
			mark, sty = "✔", "ok"
		case "failed":
			mark, sty = "✘", "fail"
		case "skipped":
			mark, sty = "–", "dim"
		case "pending":
			mark, sty = "·", "dim"
		}
		dur := ""
		if st.T0 != nil {
			end := a.Elapsed()
			if st.T1 != nil {
				end = *st.T1
			}
			dur = tui.MMSS(end - *st.T0)
		}
		out = append(out, stepLine{style: sty, text: mark + " " + st.Name, dur: dur, cur: i == a.Run.Cur, main: true})
		if (st.State == "running" || st.State == "failed" || st.State == "done") && len(st.Checks) > 0 {
			for _, ck := range st.Checks {
				if ck.OK {
					out = append(out, stepLine{style: "ok", text: "   ✔ " + ck.Text})
				} else {
					out = append(out, stepLine{style: "fail", text: "   ✘ " + ck.Text})
				}
			}
		}
	}
	return out
}

func (a *App) drawSteps(c *tui.Canvas, y, x, h, w int) {
	title := "Étapes"
	if a.Mode == "watch" {
		title = "Mode observation"
	}
	done := 0
	for _, s := range a.Run.Steps {
		if s.State == "done" || s.State == "failed" {
			done++
		}
	}
	note := ""
	if a.Mode != "watch" && len(a.Run.Steps) > 0 {
		note = fmt.Sprintf("%d/%d", done, len(a.Run.Steps))
	}
	tui.Box(c, y, x, h, w, title, note)
	lines := a.stepLines()
	inner := h - 2
	if len(lines) == 0 {
		c.Put(y+1, x+2, "En attente de la sortie du scénario…", "dim")
		return
	}
	cur := 0
	for k, l := range lines {
		if l.cur {
			cur = k
			break
		}
	}
	last := cur
	for last+1 < len(lines) && !lines[last+1].main {
		last++
	}
	first := max(0, min(len(lines)-inner, last-inner+2))
	for r := 0; r < inner && first+r < len(lines); r++ {
		l := lines[first+r]
		room := w - 4
		if l.dur != "" {
			room -= len(l.dur) + 1
		}
		sty := l.style
		if l.cur {
			sty = "label"
		}
		c.Put(y+1+r, x+2, tui.Clip(l.text, room), sty)
		if l.cur {
			c.Put(y+1+r, x+2, string([]rune(l.text)[:1]), l.style)
		}
		if l.dur != "" {
			c.Put(y+1+r, x+w-2-len(l.dur), l.dur, "dim")
		}
	}
}

func (a *App) drawLog(c *tui.Canvas, y, x, h, w int) {
	run := a.Run
	follow := ""
	if a.scroll != 0 {
		follow = fmt.Sprintf("↑ %d lignes · f : suivre", a.scroll)
	}
	title := "Journal"
	if run.Ansible {
		title = "Journal · Ansible"
	}
	note := follow
	if note == "" && a.fullLog {
		note = "· plein écran (l : revenir)"
	}
	tui.Box(c, y, x, h, w, title, note)
	inner := h - 2
	if inner <= 0 {
		return
	}
	type pair struct{ l, r string }
	var head []pair
	if run.Command != "" || run.Play != "" || run.Task != "" {
		cnt := run.Counts
		line1 := ""
		if run.Command != "" {
			line1 = "▸ " + run.Command
			if run.Commands > 1 {
				line1 += fmt.Sprintf("  (%d)", run.Commands)
			}
		}
		stats := fmt.Sprintf("ok %d · changed %d · failed %d", cnt["ok"], cnt["changed"], cnt["failed"])
		head = append(head, pair{line1, stats})
		if run.Play != "" || run.Task != "" {
			l := "  " + run.Play
			if run.Task != "" {
				l += " › " + run.Task
			}
			head = append(head, pair{l, ""})
		}
	}
	if len(run.MetricOrder) > 0 {
		var parts []string
		for i, k := range run.MetricOrder {
			if i >= 4 {
				break
			}
			v := run.Metrics[k]
			parts = append(parts, k+" "+tui.SparkLine(v.Vals, 12)+" "+events.G(v.Last()))
		}
		head = append(head, pair{strings.Join(parts, "   "), ""})
	}
	if n := max(0, inner-2); len(head) > n {
		head = head[:n]
	}
	for i, p := range head {
		sty := "task"
		if i == 0 {
			sty = "acc"
		}
		c.Put(y+1+i, x+2, tui.Clip(p.l, w-6-tui.Width(p.r)), sty)
		if p.r != "" {
			c.Put(y+1+i, x+w-2-tui.Width(p.r), p.r, "dim")
		}
	}
	bodyTop := y + 1 + len(head)
	bodyH := y + h - 1 - bodyTop
	logs := run.Log
	end := max(0, len(logs)-a.scroll)
	start := max(0, end-bodyH)
	for r, l := range logs[start:end] {
		c.Put(bodyTop+r, x+2, tui.Clip(l.Text, w-4), l.Style)
	}
}

func (a *App) drawFooter(c *tui.Canvas) {
	y := c.H - 1
	keys := "q quitter · ↑↓ PgUp PgDn défiler · f suivre · l journal plein écran"
	if a.Mode == "replay" {
		keys += " · espace pause"
	}
	c.Put(y, 1, tui.Clip(keys, c.W-2), "dim")
	if a.quitArm {
		msg := "Le scénario tourne encore : q à nouveau pour l'arrêter"
		c.Put(y, c.W-tui.Width(msg)-1, msg, "fail")
	} else if a.Rec != nil {
		rel, _ := filepath.Rel(a.Cfg.Root, a.Rec.Path)
		msg := "enregistré : " + rel
		c.Put(y, max(tui.Width(keys)+4, c.W-tui.Width(msg)-1), tui.Clip(msg, c.W-tui.Width(keys)-5), "dim")
	}
}

// Key : touche décodée -> false pour quitter.
func (a *App) Key(k string) bool {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	if k == "q" || k == "Q" || k == "\x03" {
		if k != "\x03" && a.Mode == "run" && !a.Finished && !a.quitArm {
			a.quitArm = true
			return true
		}
		return false
	}
	a.quitArm = false
	switch k {
	case "up":
		a.scroll++
	case "down":
		a.scroll = max(0, a.scroll-1)
	case "pgup":
		a.scroll += 10
	case "pgdn":
		a.scroll = max(0, a.scroll-10)
	case "f", "end":
		a.scroll = 0
	case "l":
		a.fullLog = !a.fullLog
	case " ":
		if a.Mode == "replay" {
			a.paused.Store(!a.paused.Load())
		}
	}
	return true
}

// PrintOnce : une image texte (sans TUI).
func (a *App) PrintOnce(w io.Writer, cols, rows int) {
	fmt.Fprintln(w, a.Render(max(rows, 30), max(cols, 100)).Text())
}
