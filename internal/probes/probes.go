// Package probes : exécution des sondes, validation du JSON (SPEC.md §4), historique des séries.
package probes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jgsqware/preview/internal/config"
)

var (
	Statuses  = []string{"ok", "warn", "fail", "unknown"}
	cellTypes = []string{"text", "badge", "bar", "num", "ratio"}
	Rank      = map[string]int{"ok": 0, "unknown": 1, "warn": 2, "fail": 3}
)

func in(s string, list []string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Obj : objet JSON générique.
type Obj = map[string]any

// Str : champ chaîne (def si absent ou d'un autre type).
func Str(o Obj, k, def string) string {
	if v, ok := o[k].(string); ok {
		return v
	}
	return def
}

// List : champ liste (nil si absent).
func List(o Obj, k string) []any {
	v, _ := o[k].([]any)
	return v
}

// Map : champ objet (nil si absent).
func Map(o Obj, k string) Obj {
	v, _ := o[k].(map[string]any)
	return v
}

// Validate : erreurs de schéma (vide = valide).
func Validate(raw any) []string {
	obj, ok := raw.(map[string]any)
	if !ok {
		return []string{"la sortie n'est pas un objet JSON"}
	}
	var errs []string
	if v, ok := obj["v"]; ok && v != 1.0 {
		errs = append(errs, fmt.Sprintf("version de schéma inconnue : %v", pyRepr(v)))
	}
	if t, ok := obj["title"]; ok {
		if _, isStr := t.(string); !isStr {
			errs = append(errs, "title doit être une chaîne")
		}
	}
	if st, ok := obj["status"]; ok {
		if s, isStr := st.(string); !isStr || !in(s, Statuses) {
			errs = append(errs, fmt.Sprintf("status invalide : %v", pyRepr(st)))
		}
	}
	var cols []any
	if c, ok := obj["columns"]; ok && c != nil {
		if l, isList := c.([]any); isList {
			cols = l
		} else {
			errs = append(errs, "columns doit être une liste")
		}
	}
	ids := map[string]bool{}
	for i, c := range cols {
		cm, ok := c.(map[string]any)
		id, _ := cm["id"].(string)
		if !ok || id == "" {
			errs = append(errs, fmt.Sprintf("columns[%d] : id requis", i))
			continue
		}
		ids[id] = true
		if t, ok := cm["type"]; ok {
			if s, isStr := t.(string); !isStr || !in(s, cellTypes) {
				errs = append(errs, fmt.Sprintf("columns[%d] : type inconnu %v", i, pyRepr(t)))
			}
		}
	}
	rows, _ := obj["rows"].([]any)
	for i, r := range rows {
		rm, ok := r.(map[string]any)
		var cells map[string]any
		if ok {
			if c, has := rm["cells"]; has {
				cells, ok = c.(map[string]any)
			}
		}
		if !ok {
			errs = append(errs, fmt.Sprintf("rows[%d] : objet avec cells attendu", i))
			continue
		}
		if st, has := rm["status"]; has {
			if s, isStr := st.(string); !isStr || !in(s, Statuses) {
				errs = append(errs, fmt.Sprintf("rows[%d] : status invalide", i))
			}
		}
		var extra []string
		for k := range cells {
			if !ids[k] {
				extra = append(extra, k)
			}
		}
		if len(extra) > 0 {
			sort.Strings(extra)
			errs = append(errs, fmt.Sprintf("rows[%d] : cellules sans colonne : %s", i, strings.Join(extra, ", ")))
		}
	}
	for _, key := range []string{"items", "series", "lines"} {
		if v, ok := obj[key]; ok {
			if _, isList := v.([]any); !isList {
				errs = append(errs, key+" doit être une liste")
			}
		}
	}
	series, _ := obj["series"].([]any)
	for i, s := range series {
		sm, ok := s.(map[string]any)
		if !ok {
			errs = append(errs, fmt.Sprintf("series[%d] : v numérique requis", i))
			continue
		}
		if _, num := sm["v"].(float64); !num {
			errs = append(errs, fmt.Sprintf("series[%d] : v numérique requis", i))
		}
	}
	return errs
}

func pyRepr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprint(int64(x))
		}
	}
	return fmt.Sprint(v)
}

// Result : une exécution de sonde.
type Result struct {
	OK    bool    `json:"ok"`
	Data  Obj     `json:"data"`
	Error string  `json:"error"`
	T     float64 `json:"t"`
	MS    float64 `json:"ms"`
}

// RunOnce exécute une sonde.
func RunOnce(p *config.Probe, cfg *config.Config) Result {
	t := time.Now()
	res := Result{T: float64(t.UnixNano()) / 1e9}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.Timeout*float64(time.Second)))
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", p.Run)
	cmd.Dir = cfg.Root
	cmd.Env = append(os.Environ(), "PREVIEW_PROJECT_DIR="+cfg.Root, "PREVIEW_STATE_DIR="+cfg.StateDir)
	setPgid(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 500 * time.Millisecond
	err := cmd.Run()
	res.MS = float64(time.Since(t).Microseconds()) / 1000
	if ctx.Err() == context.DeadlineExceeded {
		res.Error = fmt.Sprintf("timeout (%s s)", trimFloat(p.Timeout))
		res.MS = p.Timeout * 1000
		return res
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > 2 {
		lines = lines[len(lines)-2:]
	}
	tail := strings.Join(lines, " · ")
	suffix := ""
	if tail != "" {
		suffix = " : " + tail
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.Error = fmt.Sprintf("code %d%s", ee.ExitCode(), suffix)
		return res
	}
	if err != nil {
		res.Error = err.Error()
		res.MS = 0
		return res
	}
	var data any
	if json.Unmarshal(stdout.Bytes(), &data) != nil {
		res.Error = "sortie JSON invalide" + suffix
		return res
	}
	if errs := Validate(data); len(errs) > 0 {
		if len(errs) > 3 {
			errs = errs[:3]
		}
		res.Error = "schéma : " + strings.Join(errs, " ; ")
		return res
	}
	res.OK, res.Data = true, data.(map[string]any)
	return res
}

func trimFloat(f float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.6f", f), "0"), ".")
}

// HKey : clé d'historique (sonde, libellé de série).
type HKey struct{ Name, Label string }

// State : dernier état connu de chaque sonde + historique des séries.
type State struct {
	Mu      sync.RWMutex
	Last    map[string]Obj
	Err     map[string]string
	Order   []string
	History map[HKey][]float64
}

func NewState(names []string) *State {
	return &State{Last: map[string]Obj{}, Err: map[string]string{}, Order: append([]string(nil), names...),
		History: map[HKey][]float64{}}
}

// Update : résultat d'exécution (res) ou objet tel quel (data, res = nil).
func (s *State) Update(name string, res *Result, data Obj) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if !in(name, s.Order) {
		s.Order = append(s.Order, name)
	}
	if res != nil && !res.OK {
		s.Err[name] = res.Error
		return
	}
	obj := data
	if obj == nil && res != nil {
		obj = res.Data
	}
	s.Last[name] = obj
	delete(s.Err, name)
	for _, x := range List(obj, "series") {
		sm, _ := x.(map[string]any)
		v, _ := sm["v"].(float64)
		k := HKey{name, Str(sm, "label", "")}
		h := append(s.History[k], v)
		if len(h) > 200 {
			h = h[len(h)-200:]
		}
		s.History[k] = h
	}
}

// LoadSnap : snap d'enregistrement {nom: objet sonde}. Les anciens formats sont ignorés.
func (s *State) LoadSnap(snap any) {
	m, ok := snap.(map[string]any)
	if !ok {
		return
	}
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		obj, ok := m[name].(map[string]any)
		if !ok {
			continue
		}
		_, a := obj["title"]
		_, b := obj["rows"]
		_, c := obj["items"]
		if (a || b || c) && len(Validate(obj)) == 0 {
			s.Update(name, nil, obj)
		}
	}
}

// Snap : copie des derniers objets.
func (s *State) Snap() map[string]any {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	out := make(map[string]any, len(s.Last))
	for k, v := range s.Last {
		out[k] = v
	}
	return out
}

// Worst : pire statut connu, ou "".
func (s *State) Worst() string {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	worst, best := "", -1
	consider := func(st string) {
		r, ok := Rank[st]
		if !ok {
			r = 1
		}
		if r > best {
			worst, best = st, r
		}
	}
	for _, o := range s.Last {
		consider(Str(o, "status", "unknown"))
	}
	if len(s.Err) > 0 {
		consider("fail")
	}
	return worst
}

// StartPoller : une goroutine par sonde, chacune à son intervalle, jusqu'à la fermeture de stop.
func StartPoller(cfg *config.Config, st *State, stop <-chan struct{}) {
	for _, p := range cfg.Probes {
		go func(p *config.Probe) {
			for {
				t := time.Now()
				r := RunOnce(p, cfg)
				select {
				case <-stop:
					return
				default:
				}
				st.Update(p.Name, &r, nil)
				wait := time.Duration(p.Interval*float64(time.Second)) - time.Since(t)
				if wait < 200*time.Millisecond {
					wait = 200 * time.Millisecond
				}
				select {
				case <-stop:
					return
				case <-time.After(wait):
				}
			}
		}(p)
	}
}
