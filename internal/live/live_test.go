package live

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jgsqware/preview/internal/cassette"
	"github.com/jgsqware/preview/internal/config"
)

func project(t *testing.T) *config.Config {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "s.sh"), []byte(`step "préparer"
step "vérifier"
`), 0o644)
	os.WriteFile(filepath.Join(root, "preview.toml"), []byte(`
[project]
name = "Démo"
[live]
interval = 0.2
[[scenario]]
name = "sc"
plan = "s.sh"
run = "echo '::step préparer'; echo \"secret $MY_TOKEN\"; echo '::ok base prête'; echo '::step vérifier'; echo '::metric n=3'; sleep 0.3; echo '::ok fini'"
[[probe]]
name = "p"
run = "echo '{\"title\": \"Sonde\", \"status\": \"ok\", \"lines\": [\"tout va bien\"]}'"
place = "side"
`), 0o644)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestRunRecordReplay(t *testing.T) {
	t.Setenv("MY_TOKEN", "tres-secret-123")
	cfg := project(t)
	sc, _ := cfg.Scenario("")
	a := New(cfg, "run", sc, "", 1, "")
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	a.WaitMain(10 * time.Second)
	a.Stop()
	if a.Run.Verdict() != "VERT" {
		t.Fatal(a.Run.Verdict(), a.Run.Log)
	}
	raw, _ := os.ReadFile(a.Rec.Path)
	if bytes.Contains(raw, []byte("tres-secret-123")) {
		t.Fatal("secret enregistré")
	}
	ok := cassette.Latest(cfg.Recordings(), "sc", true)
	if ok != a.Rec.Path {
		t.Fatal(ok)
	}
	r := New(cfg, "replay", nil, ok, 1e9, "")
	r.ReplayLoop()
	var buf bytes.Buffer
	r.PrintOnce(&buf, 120, 40)
	out := buf.String()
	for _, want := range []string{"✔ VERT", "✔ préparer", "✔ vérifier", "base prête", "n ▅ 3", "Sonde", "bilan · sc"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q absent :\n%s", want, out)
		}
	}
}

func TestWatchFollow(t *testing.T) {
	cfg := project(t)
	log := filepath.Join(cfg.Root, "run.log")
	os.WriteFile(log, []byte("=== étape suivie\n✔ ok\n"), 0o644)
	ptr := filepath.Join(cfg.Root, "last")
	os.WriteFile(ptr, []byte("run.log\n"), 0o644)
	cfg.Follow = ptr
	a := New(cfg, "watch", nil, "", 1, "")
	a.WatchStep(NewWatchState())
	var buf bytes.Buffer
	a.PrintOnce(&buf, 120, 40)
	for _, want := range []string{"OBSERVATION", "run.log", "=== étape suivie"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("%q absent :\n%s", want, buf.String())
		}
	}
}

func TestDecodeKeys(t *testing.T) {
	got := strings.Join(DecodeKeys("\x1b[Aq\x1b[6~é\x1bOB"), ",")
	if got != "up,q,pgdn,é,down" {
		t.Fatal(got)
	}
}
