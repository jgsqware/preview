package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoad(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "tests"), 0o755)
	os.WriteFile(filepath.Join(root, "tests", "a.sh"), []byte("step \"x\"\n"), 0o644)
	os.WriteFile(filepath.Join(root, "preview.toml"), []byte(`
[project]
name = "P"
[scenarios]
glob = "tests/*.sh"
run = "./run {name}"
[[scenario]]
name = "b"
run = "echo b"
[[probe]]
name = "p"
run = "echo {}"
place = "side"
[live]
interval = 2
[live.rules]
ok = '^OK (.+)$'
`), 0o644)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Order, []string{"a", "b"}) {
		t.Fatal(cfg.Order)
	}
	if cfg.Scenarios["a"].Run != "./run a" || cfg.Scenarios["a"].Plan != filepath.Join(root, "tests", "a.sh") {
		t.Fatal(cfg.Scenarios["a"])
	}
	if cfg.Rules["ok"] != "^OK (.+)$" || cfg.Probes[0].Place != "side" || cfg.Probes[0].Interval != 2 {
		t.Fatal(cfg.Rules["ok"], cfg.Probes[0])
	}
	if sc, _ := cfg.Scenario(""); sc.Name != "a" {
		t.Fatal(sc.Name)
	}
	if _, err := cfg.Scenario("zz"); !IsError(err) {
		t.Fatal(err)
	}
	if cfg.DeckHTML != filepath.Join(root, "demo.html") {
		t.Fatal(cfg.DeckHTML)
	}
}

func TestErrors(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "preview.toml"), []byte("[project]\n"), 0o644)
	if _, err := Load(root); !IsError(err) {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "preview.toml"), []byte("[project]\nname='x'\n[[probe]]\nname='p'\nrun='x'\nplace='top'\n"), 0o644)
	if _, err := Load(root); err == nil {
		t.Fatal("place")
	}
}

func TestDecksAndScenarioProbes(t *testing.T) {
	root := t.TempDir()
	write := func(s string) {
		os.WriteFile(filepath.Join(root, "preview.toml"), []byte("[project]\nname = \"P\"\n"+s), 0o644)
	}
	write(`
[[deck]]
name = "court"
source = "court.md"
[[deck]]
name = "long"
source = "decks/long.md"
html = "out/long.html"
[[probe]]
name = "relay"
run = "echo {}"
[[probe]]
name = "tunnels"
run = "echo {}"
[[probe]]
name = "disk"
run = "echo {}"
[[scenario]]
name = "a"
run = "true"
probes = ["tunnels", "relay"]
[[scenario]]
name = "b"
run = "true"
`)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.DeckNames(), []string{"court", "long"}) || cfg.DeckSource != filepath.Join(root, "court.md") {
		t.Fatal(cfg.DeckNames(), cfg.DeckSource)
	}
	if d := cfg.Deck("long"); d.HTML != filepath.Join(root, "out", "long.html") || cfg.Deck("court").HTML != filepath.Join(root, "court.html") {
		t.Fatal(d, cfg.Deck("court"))
	}
	names := func(ps []*Probe) (out []string) {
		for _, p := range ps {
			out = append(out, p.Name)
		}
		return
	}
	if got := names(cfg.ProbesFor(cfg.Scenarios["a"])); !reflect.DeepEqual(got, []string{"relay", "tunnels"}) {
		t.Fatal(got)
	}
	if got := names(cfg.ProbesFor(cfg.Scenarios["b"])); len(got) != 3 {
		t.Fatal(got)
	}
	if got := names(cfg.ProbesFor(nil)); len(got) != 3 {
		t.Fatal(got)
	}

	// [deck] seul : toujours accepté, nommé d'après le fichier
	write("[deck]\nsource = \"talk.md\"\n")
	if cfg, err = Load(root); err != nil || !reflect.DeepEqual(cfg.DeckNames(), []string{"talk"}) {
		t.Fatal(err, cfg)
	}
	// sans deck : demo.md
	write("")
	if cfg, err = Load(root); err != nil || cfg.Decks[0].Name != "demo" {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"[[probe]]\nname = \"p\"\nrun = \"x\"\n[[scenario]]\nname = \"a\"\nrun = \"x\"\nprobes = [\"nope\"]\n",
		"[[deck]]\nsource = \"a.md\"\n[[deck]]\nsource = \"b.md\"\n",
		"[[deck]]\nname = \"x\"\n[[deck]]\nname = \"x\"\nsource = \"b.md\"\n",
		"[[deck]]\nname = \"x\"\nsource = \"a.md\"\n[[deck]]\nname = \"y\"\nsource = \"b.md\"\nhtml = \"a.html\"\n",
		"[deck]\nsrc = \"a.md\"\n",
	} {
		write(bad)
		if _, err := Load(root); err == nil || !IsError(err) {
			t.Fatalf("attendu une erreur pour :\n%s", bad)
		}
	}
}
