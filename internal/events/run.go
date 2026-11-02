package events

import (
	"github.com/jgsqware/preview/internal/rx"
)

// Check : une vérification ✔ / ✘ dans une étape.
type Check struct {
	OK   bool
	Text string
}

// Step : une étape prévue ou reçue. State : pending, running, done, failed, skipped.
type Step struct {
	Name   string
	re     *rx.Re
	State  string
	T0, T1 *float64
	Checks []Check
	Failed bool
}

// LogLine : (style, texte) du journal.
type LogLine struct {
	Style, Text string
}

// Ring : historique borné de valeurs.
type Ring struct {
	Max  int
	Vals []float64
}

func (r *Ring) Add(v float64) {
	r.Vals = append(r.Vals, v)
	if len(r.Vals) > r.Max {
		r.Vals = r.Vals[len(r.Vals)-r.Max:]
	}
}

func (r *Ring) Last() float64 { return r.Vals[len(r.Vals)-1] }

const maxLog = 5000

// Run : état d'un run, alimenté ligne par ligne (live ou relecture). Heures t en secondes.
type Run struct {
	parser      *Parser
	Steps       []*Step
	Cur         int // -1 = aucune
	Command     string
	Play        string
	Task        string
	Counts      map[string]int // playbook / commande en cours
	Totals      map[string]int // PLAY RECAP cumulés
	Commands    int
	Tasks       int
	Metrics     map[string]*Ring
	MetricOrder []string
	Log         []LogLine
	Result      *bool
	RC          *int
	sawStep     bool
	Ansible     bool
}

func f(t float64) *float64 { return &t }

// NewRun : un run avec ses étapes prévues.
func NewRun(p *Parser, plan []string) *Run {
	r := &Run{parser: p, Cur: -1, Counts: map[string]int{}, Totals: map[string]int{}, Metrics: map[string]*Ring{}}
	for _, n := range plan {
		r.Steps = append(r.Steps, &Step{Name: n, re: StepPattern(n), State: "pending"})
	}
	return r
}

// Feed : texte brut (éventuellement plusieurs lignes, avec ANSI).
func (r *Run) Feed(raw string, t float64) {
	parts := SplitLines(raw)
	if len(parts) == 0 {
		parts = []string{""}
	}
	for _, part := range parts {
		r.Line(Clean(part), t)
	}
}

// Line : une ligne nettoyée ; renvoie ses événements.
func (r *Run) Line(s string, t float64) []Event {
	if s == "" && (len(r.Log) == 0 || r.Log[len(r.Log)-1].Text == "") {
		return nil
	}
	evs := r.parser.Parse(s)
	r.Log = append(r.Log, LogLine{Classify(s, evs), Display(s, evs)})
	if len(r.Log) > maxLog {
		r.Log = r.Log[len(r.Log)-maxLog:]
	}
	for _, e := range evs {
		r.Apply(e, t)
	}
	return evs
}

func (r *Run) Apply(e Event, t float64) {
	switch e.Kind {
	case "step":
		r.step(e.Text, t)
	case "check":
		if r.Cur < 0 {
			r.step("(sans étape)", t)
		}
		st := r.Steps[r.Cur]
		st.Checks = append(st.Checks, Check{e.OK, e.Text})
		if !e.OK {
			st.Failed = true
		}
	case "metric":
		m := r.Metrics[e.Text]
		if m == nil {
			m = &Ring{Max: 200}
			r.Metrics[e.Text] = m
			r.MetricOrder = append(r.MetricOrder, e.Text)
		}
		m.Add(e.Value)
	case "result":
		ok := e.OK
		r.Result = &ok
		r.close(t)
	case "command":
		r.Command, r.Play, r.Task = e.Text, "", ""
		r.Counts = map[string]int{}
		r.Commands++
	case "play":
		r.Play = e.Text
		r.Ansible = true
	case "task":
		r.Task = e.Text
		r.Tasks++
		r.Ansible = true
	case "host_result":
		r.Counts[e.Text]++
	case "recap":
		for k, v := range e.Counts {
			r.Totals[k] += v
		}
	}
}

func (r *Run) step(name string, t float64) {
	r.sawStep = true
	if r.Cur >= 0 {
		st := r.Steps[r.Cur]
		if st.State == "running" {
			st.State = "done"
			if st.Failed {
				st.State = "failed"
			}
			st.T1 = f(t)
		}
	}
	start := r.Cur + 1
	for k := start; k < len(r.Steps); k++ {
		s := r.Steps[k]
		if s.State == "pending" && s.re != nil && s.re.Match(name) != nil {
			for j := start; j < k; j++ {
				if r.Steps[j].State == "pending" {
					r.Steps[j].State = "skipped"
				}
			}
			s.Name, s.State, s.T0 = name, "running", f(t)
			r.Cur = k
			return
		}
	}
	st := &Step{Name: name, State: "running", T0: f(t)}
	r.Steps = append(r.Steps[:start], append([]*Step{st}, r.Steps[start:]...)...)
	r.Cur = start
}

func (r *Run) close(t float64) {
	if r.Cur >= 0 && r.Steps[r.Cur].State == "running" {
		st := r.Steps[r.Cur]
		st.State = "done"
		if st.Failed || (r.Result != nil && !*r.Result) {
			st.State = "failed"
		}
		st.T1 = f(t)
	}
	for _, st := range r.Steps {
		if st.State == "pending" {
			st.State = "skipped"
		}
	}
}

// Finish : fin du processus (code de sortie rc).
func (r *Run) Finish(rc int, t float64) {
	r.RC = &rc
	if !r.sawStep && len(r.Steps) == 0 {
		r.Steps = append(r.Steps, &Step{Name: "scénario", State: "running", T0: f(0)})
		r.Cur = 0
	}
	if r.Result == nil { // sans verdict déclaré : code de sortie, et aucune vérification ✘
		ok := rc == 0
		for _, s := range r.Steps {
			if s.Failed {
				ok = false
			}
		}
		r.Result = &ok
	}
	r.close(t)
}

// Verdict : "VERT", "ROUGE" ou "".
func (r *Run) Verdict() string {
	if r.Result == nil {
		return ""
	}
	if *r.Result {
		return "VERT"
	}
	return "ROUGE"
}

// Summary : bilan pour l'export.
func (r *Run) Summary() map[string]any {
	done := 0
	failed := []string{}
	for _, s := range r.Steps {
		if s.State == "done" || s.State == "failed" {
			done++
		}
		if s.State == "failed" {
			failed = append(failed, s.Name)
		}
	}
	var verdict any
	if v := r.Verdict(); v != "" {
		verdict = v
	}
	return map[string]any{"result": verdict, "steps": done, "planned": len(r.Steps), "failed": failed,
		"totals": r.Totals, "tasks": r.Tasks}
}
