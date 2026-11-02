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
