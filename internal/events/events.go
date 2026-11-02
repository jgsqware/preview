// Package events lit la sortie d'un scénario : adaptateurs -> événements -> état du run (SPEC.md §2).
package events

import (
	"os"
	"strconv"
	"strings"

	"github.com/dlclark/regexp2"
	"github.com/jgsqware/preview/internal/rx"
)

var (
	ansiRe   = rx.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07|\r`)
	starsRe  = rx.MustCompile(`\s\*{3,}$`)
	rePlay   = rx.MustCompile(`^PLAY \[(.*)\]`)
	reTask   = rx.MustCompile(`^(?:TASK|RUNNING HANDLER) \[(.*)\]`)
	reRes    = rx.MustCompile(`^(ok|changed|failed|fatal|skipping|unreachable): \[`)
	reRecap  = rx.MustCompile(`^(\S+)\s+:\s+ok=(\d+)\s+changed=(\d+)\s+unreachable=(\d+)\s+failed=(\d+)\s+skipped=(\d+)`)
	reMetric = rx.MustCompile(`([\w.-]+)=(-?[\d.]+)`)
	reStepLn = rx.MustCompile(`::step ([^"'\n]+)`)
	reVar    = rx.MustCompile(`\\\$\\\{?\w+\\\}?|\\\$\w+|\$\\\{\w+\\\}|\$\w+`)
)

// Clean : ANSI retiré, fin de ligne nettoyée, « ***** » d'Ansible ôtés.
func Clean(raw string) string {
	s := strings.TrimRightFunc(ansiRe.Sub(raw, ""), isPySpace)
	return starsRe.Sub(s, "")
}

func isPySpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x1f, 0x85, 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// SplitLines : comme str.splitlines() (sans les fins de ligne).
func SplitLines(s string) []string {
	var out []string
	start := 0
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		switch rs[i] {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			out = append(out, string(rs[start:i]))
			start = i + 1
		case '\r':
			out = append(out, string(rs[start:i]))
			if i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	if start < len(rs) {
		out = append(out, string(rs[start:]))
	}
	return out
}

// Event : un événement. Kind : step, check, metric, result, note, recap_start, play, task, host_result,
// recap, command.
type Event struct {
	Kind   string
	Text   string         // step, note, play, task, command, check (texte), metric (clé), host_result, recap (hôte)
	OK     bool           // check, result
	Value  float64        // metric
	Counts map[string]int // recap
}

// JSON : forme liste, comme le tuple Python (export HTML).
func (e Event) JSON() []any {
	switch e.Kind {
	case "step", "note", "play", "task", "command", "host_result":
		return []any{e.Kind, e.Text}
	case "check":
		return []any{e.Kind, e.OK, e.Text}
	case "metric":
		return []any{e.Kind, e.Text, e.Value}
	case "result":
		return []any{e.Kind, e.OK}
	case "recap":
		return []any{e.Kind, e.Text, e.Counts}
	}
	return []any{e.Kind}
}

// Parser : ligne nettoyée -> événements. Sans état, sauf « dans le PLAY RECAP ».
type Parser struct {
	r       map[string]*rx.Re
	inRecap bool
}

// NewParser : les règles invalides sont ignorées (preview check les signale).
func NewParser(rules map[string]string) *Parser {
	p := &Parser{r: map[string]*rx.Re{}}
	for _, k := range []string{"step", "ok", "fail", "result_ok", "result_fail", "ignore_step", "command"} {
		if rules[k] != "" {
			if re, err := rx.Compile(rules[k]); err == nil {
				p.r[k] = re
			}
		}
	}
	return p
}

// Clone : même règles, état remis à zéro.
func (p *Parser) Clone() *Parser { return &Parser{r: p.r} }

var keys5 = []string{"ok", "changed", "unreachable", "failed", "skipped"}

func (p *Parser) Parse(s string) []Event {
	r := p.r
	// 1. protocole
	if strings.HasPrefix(s, "::") {
		head, rest, _ := strings.Cut(s[2:], " ")
		rest = strings.TrimSpace(rest)
		switch head {
		case "step":
			return []Event{{Kind: "step", Text: rest}}
		case "ok", "fail":
			return []Event{{Kind: "check", OK: head == "ok", Text: rest}}
		case "metric":
			var out []Event
			for _, m := range reMetric.FindAll(rest) {
				v, err := strconv.ParseFloat(m.Groups[2], 64)
				if err != nil {
					continue
				}
				out = append(out, Event{Kind: "metric", Text: m.Groups[1], Value: v})
			}
			return out
		case "result":
			switch strings.ToLower(rest) {
			case "ok", "vert", "pass", "0", "true":
				return []Event{{Kind: "result", OK: true}}
			}
			return []Event{{Kind: "result", OK: false}}
		case "note":
			return []Event{{Kind: "note", Text: rest}}
		}
	}
	// 2. résultat déclaré (règles), avant les étapes : « === Résultat … » ressemble à une étape
	if re := r["result_ok"]; re != nil && re.MatchString(s) {
		return []Event{{Kind: "result", OK: true}}
	}
	if re := r["result_fail"]; re != nil && re.MatchString(s) {
		return []Event{{Kind: "result", OK: false}}
	}
	// 3. ansible
	if strings.HasPrefix(s, "PLAY RECAP") {
		p.inRecap = true
		return []Event{{Kind: "recap_start"}}
	}
	if m := rePlay.Match(s); m != nil {
		p.inRecap = false
		return []Event{{Kind: "play", Text: m[1]}}
	}
	if m := reTask.Match(s); m != nil {
		return []Event{{Kind: "task", Text: m[1]}}
	}
	if m := reRes.Match(s); m != nil {
		k := m[1]
		switch k {
		case "fatal":
			k = "failed"
		case "skipping":
			k = "skipped"
		}
		return []Event{{Kind: "host_result", Text: k}}
	}
	if p.inRecap {
		if m := reRecap.Match(s); m != nil {
			c := map[string]int{}
			for i, k := range keys5 {
				c[k], _ = strconv.Atoi(m[i+2])
			}
			return []Event{{Kind: "recap", Text: m[1], Counts: c}}
		}
	}
	// 4. règles
	for _, k := range []string{"step", "ok", "fail", "command"} {
		re := r[k]
		if re == nil {
			continue
		}
		m := re.Match(s)
		if m == nil {
			continue
		}
		if k == "step" && r["ignore_step"] != nil && r["ignore_step"].MatchString(s) {
			continue
		}
		text := s
		if len(m) > 1 {
			text = m[1]
		}
		text = strings.TrimSpace(text)
		switch k {
		case "step":
			p.inRecap = false
			return []Event{{Kind: "step", Text: text}}
		case "command":
			return []Event{{Kind: "command", Text: text}}
		}
		return []Event{{Kind: "check", OK: k == "ok", Text: text}}
	}
	return nil
}

// Classify : style d'une ligne du journal.
func Classify(s string, evs []Event) string {
	for _, e := range evs {
		switch e.Kind {
		case "step":
			return "step"
		case "play", "task", "recap_start":
			return "task"
		case "check", "result":
			if e.OK {
				return "ok"
			}
			return "fail"
		case "host_result":
			switch e.Text {
			case "ok":
				return "ok"
			case "changed":
				return "chg"
			case "skipped":
				return "dim"
			}
			return "fail"
		case "command", "note":
			return "acc"
		}
	}
	for _, p := range []string{"ERROR", "[ERROR", "fatal"} {
		if strings.HasPrefix(s, p) {
			return "fail"
		}
	}
	return "text"
}

// Display : texte affiché d'une ligne ; le protocole « :: » est rendu lisible.
func Display(s string, evs []Event) string {
	if !strings.HasPrefix(s, "::") || len(evs) == 0 {
		return s
	}
	e := evs[0]
	switch e.Kind {
	case "step":
		return "=== " + e.Text
	case "check":
		if e.OK {
			return "✔ " + e.Text
		}
		return "✘ " + e.Text
	case "result":
		if e.OK {
			return "=== Résultat : VERT"
		}
		return "=== Résultat : ROUGE"
	case "note":
		return "» " + e.Text
	case "metric":
		parts := make([]string, len(evs))
		for i, x := range evs {
			parts[i] = x.Text + " = " + G(x.Value)
		}
		return "· " + strings.Join(parts, "  ")
	}
	return s
}

// G : format « :g » de Python.
func G(v float64) string {
	return strconv.FormatFloat(v, 'g', 6, 64)
}

// PlannedSteps : regex `plan` + lignes `::step` littérales du fichier plan ; $VAR deviendra joker.
func PlannedSteps(plan, planRegex string) []string {
	if plan == "" {
		return nil
	}
	data, err := os.ReadFile(plan)
	if err != nil {
		return nil
	}
	var re *rx.Re
	if planRegex != "" {
		re, _ = rx.Compile(planRegex, regexp2.Multiline)
	}
	var names []string
	for _, line := range SplitLines(strings.ToValidUTF8(string(data), "�")) {
		if re != nil {
			if m := re.Match(line); m != nil {
				g := ""
				if len(m) > 1 {
					g = m[1]
				}
				names = append(names, strings.ReplaceAll(g, `\$`, "$"))
				continue
			}
		}
		if m := reStepLn.Search(line); m != nil {
			names = append(names, strings.ReplaceAll(strings.TrimSpace(m[1]), `\$`, "$"))
		}
	}
	return names
}

// StepPatternSource : motif (syntaxe Python/JS) d'une étape prévue ; $VAR et ${VAR} deviennent « .+ ».
func StepPatternSource(name string) string {
	return "^" + reVar.Sub(rx.Escape(name), ".+") + "$"
}

// StepPattern : motif compilé.
func StepPattern(name string) *rx.Re {
	re, err := rx.Compile(StepPatternSource(name))
	if err != nil {
		return rx.MustCompile("^" + rx.Escape(name) + "$")
	}
	return re
}
