// Package cassette : enregistrements des runs — écriture masquée, lecture, réduction pour l'export (SPEC.md §3).
package cassette

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jgsqware/preview/internal/rx"
)

const (
	RedactionVersion = 1
	Mask             = "‹masqué›"
)

var (
	secretEnv = rx.MustCompile(`(?i)TOKEN|SECRET|PASS|KEY|VAULT|CREDENTIAL`)
	detectors = []string{
		`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`,
		`glpat-[\w-]{10,}`,
		`gh[pousr]_[A-Za-z0-9]{20,}`,
		`eyJ[\w-]{8,}\.eyJ[\w-]{8,}\.[\w-]{8,}`,
		`AKIA[0-9A-Z]{16}`,
		`(?i:(?<=password[=:] )\S+)`,
		`(?i:(?<=password": ")[^"]+)`,
		`(?i:(?<=passwd[=:] )\S+)`,
	}
)

// Redactor masque les secrets.
type Redactor struct{ re *rx.Re }

// NewRedactor : motifs supplémentaires + valeurs des variables d'environnement secrètes (env nil = os.Environ).
func NewRedactor(extra []string, env map[string]string) *Redactor {
	if env == nil {
		env = map[string]string{}
		for _, kv := range os.Environ() {
			k, v, _ := strings.Cut(kv, "=")
			env[k] = v
		}
	}
	set := map[string]bool{}
	for k, v := range env {
		if secretEnv.MatchString(k) && len([]rune(v)) >= 6 {
			set[v] = true
		}
	}
	values := make([]string, 0, len(set))
	for v := range set {
		values = append(values, v)
	}
	sort.Slice(values, func(i, j int) bool {
		if len(values[i]) != len(values[j]) {
			return len(values[i]) > len(values[j])
		}
		return values[i] < values[j]
	})
	var pats []string
	for _, v := range values {
		pats = append(pats, rx.Escape(v))
	}
	pats = append(pats, detectors...)
	for _, e := range extra {
		if _, err := rx.Compile(e); err == nil {
			pats = append(pats, e)
		}
	}
	parts := make([]string, len(pats))
	for i, p := range pats {
		parts[i] = "(?:" + p + ")"
	}
	return &Redactor{re: rx.MustCompile(strings.Join(parts, "|"))}
}

func (r *Redactor) Text(s string) string {
	if r == nil || r.re == nil || s == "" {
		return s
	}
	return r.re.Sub(s, Mask)
}

// Obj : masque toutes les chaînes d'une valeur JSON générique.
func (r *Redactor) Obj(o any) any {
	switch v := o.(type) {
	case string:
		return r.Text(v)
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = r.Obj(x)
		}
		return out
	case []string:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = r.Text(x)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			out[k] = r.Obj(x)
		}
		return out
	}
	// autres types (structures) : passage par JSON
	if o == nil {
		return nil
	}
	switch o.(type) {
	case bool, int, int64, float64:
		return o
	}
	b, err := json.Marshal(o)
	if err != nil {
		return o
	}
	var g any
	if json.Unmarshal(b, &g) != nil {
		return o
	}
	return r.Obj(g)
}

// GitSHA : commit court du dossier, ou "".
func GitSHA(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = dir
	out, err := runTimeout(cmd, 3*time.Second)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func runTimeout(cmd *exec.Cmd, d time.Duration) (string, error) {
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return buf.String(), err
	case <-time.After(d):
		_ = cmd.Process.Kill()
		return "", fmt.Errorf("timeout")
	}
}

// Marshal : JSON sans échappement HTML, caractères non ASCII gardés.
func Marshal(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// Recorder écrit une cassette ; tout passe par le masquage avant d'atteindre le disque.
type Recorder struct {
	Dir, Path string
	redactor  *Redactor
	t0        time.Time
	mu        sync.Mutex
	fh        *os.File
}

// NewRecorder : meta = champs supplémentaires de la ligne meta.
func NewRecorder(recordings, scenario string, meta map[string]any, red *Redactor) (*Recorder, error) {
	dir := filepath.Join(recordings, scenario)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	now := time.Now()
	path := filepath.Join(dir, now.Format("20060102-150405")+".jsonl")
	fh, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	m := map[string]any{"preview": 1, "redaction": RedactionVersion, "scenario": scenario,
		"started": now.Format("2006-01-02T15:04:05"), "host": host}
	for k, v := range meta {
		m[k] = v
	}
	r := &Recorder{Dir: dir, Path: path, redactor: red, t0: now, fh: fh}
	r.write(0, "meta", m)
	r.link("latest.jsonl")
	return r, nil
}

func (r *Recorder) write(t float64, k string, v any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fh == nil {
		return
	}
	line := Marshal(map[string]any{"t": math.Round(t*100) / 100, "k": k, "v": r.redactor.Obj(v)})
	_, _ = r.fh.Write(append(line, '\n'))
}

// Event : un événement horodaté depuis le début du run.
func (r *Recorder) Event(k string, v any) {
	r.write(time.Since(r.t0).Seconds(), k, v)
}

// Close : écrit la fin ; un run VERT devient latest-ok.
func (r *Recorder) Close(rc int, ok bool) {
	r.Event("end", rc)
	r.mu.Lock()
	if r.fh != nil {
		_ = r.fh.Close()
		r.fh = nil
	}
	r.mu.Unlock()
	if ok {
		r.link("latest-ok.jsonl")
	}
}

func (r *Recorder) link(name string) {
	l := filepath.Join(r.Dir, name)
	_ = os.Remove(l)
	_ = os.Symlink(filepath.Base(r.Path), l)
}

// Ev : un événement lu ({"t","k","v"}).
type Ev struct {
	T float64
	K string
	V any
}

func evFrom(m map[string]any) Ev {
	e := Ev{K: fmt.Sprint(m["k"]), V: m["v"]}
	switch t := m["t"].(type) {
	case float64:
		e.T = t
	}
	return e
}

func lines(path string, fn func(map[string]any) bool) error {
	fh, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) != nil || m == nil {
			continue
		}
		if !fn(m) {
			break
		}
	}
	return sc.Err()
}

// Read : (meta, événements hors meta). meta = {} pour un vieil enregistrement sans meta.
func Read(path string) (map[string]any, []Ev, error) {
	meta := map[string]any{}
	var evs []Ev
	first := true
	err := lines(path, func(m map[string]any) bool {
		_, hasK := m["k"]
		if first {
			first = false
			if m["k"] == "meta" {
				if v, ok := m["v"].(map[string]any); ok {
					meta = v
				}
				return true
			}
		}
		if hasK && m["k"] != "meta" {
			evs = append(evs, evFrom(m))
		}
		return true
	})
	return meta, evs, err
}

// List : cassettes (hors liens) triées de la plus ancienne à la plus récente.
func List(recordings, scenario string) []string {
	base := recordings
	if scenario != "" {
		base = filepath.Join(recordings, scenario)
	}
	type fi struct {
		p string
		t time.Time
	}
	var files []fi
	_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if st, err := os.Stat(p); err == nil {
			files = append(files, fi{p, st.ModTime()})
		}
		return nil
	})
	sort.SliceStable(files, func(i, j int) bool { return files[i].t.Before(files[j].t) })
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.p
	}
	return out
}

// Latest : dernière cassette (ok : dernier run VERT) ; "" si aucune.
func Latest(recordings, scenario string, ok bool) string {
	if scenario != "" {
		name := "latest.jsonl"
		if ok {
			name = "latest-ok.jsonl"
		}
		link := filepath.Join(recordings, scenario, name)
		if _, err := os.Stat(link); err == nil {
			if p, err := filepath.EvalSymlinks(link); err == nil {
				return p
			}
		}
		if ok {
			return ""
		}
	}
	recs := List(recordings, scenario)
	if len(recs) == 0 {
		return ""
	}
	return recs[len(recs)-1]
}

// Outcome : (code de sortie ou nil, meta).
func Outcome(path string) (*int, map[string]any) {
	meta, evs, _ := Read(path)
	var rc *int
	for _, e := range evs {
		if e.K == "end" {
			if v, ok := e.V.(float64); ok {
				n := int(v)
				rc = &n
			}
		}
	}
	return rc, meta
}

// -- réduction pour l'export HTML --------------------------------------------------------------

var (
	keepRe  = rx.MustCompile(`^(::|=== |PLAY |TASK |RUNNING HANDLER|PLAY RECAP|changed:|failed:|fatal:|unreachable:|[✔✓✘✗] |ERROR|\[ERROR)`)
	recapRe = rx.MustCompile(`^\S+\s+:\s+ok=\d+`)
)

// TrimOpts : réglages de Trim (zéro = défauts).
type TrimOpts struct {
	MaxBytes, MaxSnaps, Tail int
	StepRe, ExtraKeep        string
}

// Trimmed : cassette réduite.
type Trimmed struct {
	Meta   map[string]any
	Events []Ev
}

// ErrUnredacted : cassette sans masquage, refusée à l'export.
type ErrUnredacted struct{ Path string }

func (e *ErrUnredacted) Error() string {
	return e.Path + " : cassette sans masquage (ancien format), refusée à l'export ; refaire le run avec preview"
}

// Trim : cassette réduite pour l'export. Refuse une cassette non masquée.
func Trim(path string, o TrimOpts) (*Trimmed, error) {
	if o.MaxBytes == 0 {
		o.MaxBytes = 250_000
	}
	if o.MaxSnaps == 0 {
		o.MaxSnaps = 120
	}
	if o.Tail == 0 {
		o.Tail = 40
	}
	meta, events, err := Read(path)
	if err != nil {
		return nil, err
	}
	if r, ok := meta["redaction"]; !ok || r == nil || r == false || r == 0.0 || r == "" {
		return nil, &ErrUnredacted{path}
	}
	var step, keep *rx.Re
	if o.StepRe != "" {
		step, _ = rx.Compile(o.StepRe)
	}
	if o.ExtraKeep != "" {
		keep, _ = rx.Compile(o.ExtraKeep)
	}
	var out []Ev
	budget := o.MaxBytes
	var pending []Ev
	flush := func() {
		from := 0
		if len(pending) > o.Tail {
			from = len(pending) - o.Tail
		}
		for _, ev := range pending[from:] {
			s, _ := ev.V.(string)
			budget -= len([]rune(s))
			out = append(out, ev)
		}
		pending = pending[:0]
	}
	var snaps []int
	for i, e := range events {
		if e.K == "snap" {
			snaps = append(snaps, i)
		}
	}
	stride := len(snaps) / o.MaxSnaps
	if stride < 1 {
		stride = 1
	}
	snapKeep := map[int]bool{}
	for k := 0; k < len(snaps); k += stride {
		snapKeep[snaps[k]] = true
	}
	if len(snaps) > 0 {
		snapKeep[snaps[len(snaps)-1]] = true
	}
	for i, ev := range events {
		if ev.K == "snap" {
			if snapKeep[i] {
				out = append(out, ev)
			}
			continue
		}
		if ev.K != "line" {
			out = append(out, ev)
			continue
		}
		s, _ := ev.V.(string)
		stripped := strings.TrimSpace(s)
		isStep := step != nil && step.Match(stripped) != nil
		important := keepRe.Match(stripped) != nil || recapRe.Match(stripped) != nil || isStep ||
			(keep != nil && keep.MatchString(stripped))
		if isStep {
			flush()
		}
		if important {
			if budget > 0 {
				budget -= len([]rune(s))
				out = append(out, ev)
			}
		} else {
			pending = append(pending, ev)
		}
	}
	flush()
	sort.SliceStable(out, func(i, j int) bool { return out[i].T < out[j].T })
	return &Trimmed{Meta: meta, Events: out}, nil
}
