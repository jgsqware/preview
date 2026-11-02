package events

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jgsqware/preview/internal/config"
)

func labRules() map[string]string {
	r := config.DefaultRules()
	r["result_ok"] = `^=== Résultat .*: VERT`
	r["result_fail"] = `^=== Résultat .*: ROUGE`
	r["command"] = `^\[lab\] ansible-playbook (\S+)`
	return r
}

func feed(r *Run, lines ...string) {
	for i, l := range lines {
		r.Feed(l, float64(i))
	}
}

func states(r *Run) []string {
	var out []string
	for _, s := range r.Steps {
		out = append(out, s.State)
	}
	return out
}

func TestProtocolAndPlan(t *testing.T) {
	run := NewRun(NewParser(config.DefaultRules()), []string{"préparer", "déployer $HOST", "vérifier"})
	feed(run, "::step préparer", "::ok a", "::step déployer web1", "::metric n=3 m=4.5", "::fail b", "::result fail")
	if got := states(run); !reflect.DeepEqual(got, []string{"done", "failed", "skipped"}) {
		t.Fatal(got)
	}
	if run.Steps[1].Name != "déployer web1" {
		t.Fatal(run.Steps[1].Name)
	}
	if v := run.Metrics["m"].Vals; !reflect.DeepEqual(v, []float64{4.5}) {
		t.Fatal(v)
	}
	if run.Result == nil || *run.Result {
		t.Fatal("result")
	}
	if run.Log[0] != (LogLine{"step", "=== préparer"}) {
		t.Fatal(run.Log[0])
	}
}

func TestLabRulesAndAnsible(t *testing.T) {
	run := NewRun(NewParser(labRules()), []string{"up"})
	feed(run, "\x1b[1;35m=== up\x1b[0m", "[lab] ansible-playbook servers/docker/deploy_full.yaml",
		"PLAY [all] ****", "TASK [docker : install] ****", "changed: [web-1]", "ok: [db-1]",
		"PLAY RECAP ****", "web-1 : ok=5 changed=2 unreachable=0 failed=0 skipped=1",
		"✔ caddy répond", "=== Résultat mvp : VERT en 1m02s")
	if run.Command != "servers/docker/deploy_full.yaml" {
		t.Fatal(run.Command)
	}
	if run.Counts["changed"] != 1 || run.Totals["ok"] != 5 {
		t.Fatal(run.Counts, run.Totals)
	}
	if !reflect.DeepEqual(run.Steps[0].Checks, []Check{{true, "caddy répond"}}) {
		t.Fatal(run.Steps[0].Checks)
	}
	if run.Verdict() != "VERT" || len(run.Steps) != 1 { // « === Résultat » n'est pas une étape
		t.Fatal(run.Verdict(), len(run.Steps))
	}
}

func TestFallbackSingleStep(t *testing.T) {
	run := NewRun(NewParser(config.DefaultRules()), nil)
	feed(run, "hello")
	run.Finish(3, 1)
	if run.Steps[0].State != "failed" || run.Verdict() != "ROUGE" {
		t.Fatal(run.Steps[0].State, run.Verdict())
	}
}

func TestPlannedSteps(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.sh")
	os.WriteFile(p, []byte("step \"un\"\n  step \"deux $X\"\necho \"::step trois\"\n"), 0o644)
	got := PlannedSteps(p, config.DefaultRules()["plan"])
	if !reflect.DeepEqual(got, []string{"un", "deux $X", "trois"}) {
		t.Fatal(got)
	}
}

func TestStepPatternAndDisplay(t *testing.T) {
	if s := StepPatternSource("deploy ${HOST} ok"); s != `^deploy\ .+\ ok$` {
		t.Fatal(s)
	}
	if !StepPattern("déployer $HOST").MatchString("déployer web1") {
		t.Fatal("joker")
	}
	evs := NewParser(config.DefaultRules()).Parse("::metric a=1 b=2.50")
	if d := Display("::metric a=1 b=2.50", evs); d != "· a = 1  b = 2.5" {
		t.Fatal(d)
	}
	if c := Clean("\x1b[32mTASK [x] *****\x1b[0m  \r"); c != "TASK [x]" {
		t.Fatalf("%q", c)
	}
}
