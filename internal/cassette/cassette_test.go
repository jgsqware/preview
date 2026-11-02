package cassette

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactionAndRoundtrip(t *testing.T) {
	red := NewRedactor([]string{`lab-secret-\w+`}, map[string]string{"MY_TOKEN": "s3cr3t-value", "HOME": "/home/x"})
	d := t.TempDir()
	rec, err := NewRecorder(d, "sc", map[string]any{"cmd": "x"}, red)
	if err != nil {
		t.Fatal(err)
	}
	rec.Event("line", "token s3cr3t-value and glpat-abcdefghijklmnop and lab-secret-42 /home/x password: hunter22")
	rec.Event("snap", map[string]any{"p": map[string]any{"title": "t", "lines": []any{"s3cr3t-value"}}})
	rec.Close(0, true)
	raw, _ := os.ReadFile(rec.Path)
	for _, bad := range []string{"s3cr3t", "glpat-", "lab-secret-42", "hunter22"} {
		if strings.Contains(string(raw), bad) {
			t.Fatalf("%s non masqué : %s", bad, raw)
		}
	}
	if !strings.Contains(string(raw), "/home/x") || !strings.Contains(string(raw), Mask) {
		t.Fatal(string(raw))
	}
	meta, evs, _ := Read(rec.Path)
	if meta["redaction"] != float64(RedactionVersion) {
		t.Fatal(meta)
	}
	var ks []string
	for _, e := range evs {
		ks = append(ks, e.K)
	}
	if strings.Join(ks, ",") != "line,snap,end" {
		t.Fatal(ks)
	}
	if got := Latest(d, "sc", true); got != rec.Path {
		t.Fatal(got, rec.Path)
	}
}

func TestTrimRefusesUnredactedAndKeepsImportant(t *testing.T) {
	d := t.TempDir()
	old := filepath.Join(d, "old.jsonl")
	os.WriteFile(old, []byte(`{"t":0,"k":"meta","v":{"scenario":"x"}}`+"\n"), 0o644)
	if _, err := Trim(old, TrimOpts{}); err == nil {
		t.Fatal("accepté")
	}
	rec, _ := NewRecorder(d, "sc", nil, NewRedactor(nil, map[string]string{}))
	rec.Event("line", "=== étape")
	for i := 0; i < 500; i++ {
		rec.Event("line", fmt.Sprintf("bruit %d", i))
	}
	rec.Event("line", "changed: [h]")
	rec.Close(0, true)
	out, err := Trim(rec.Path, TrimOpts{Tail: 10, StepRe: `^=== (.+)$`})
	if err != nil {
		t.Fatal(err)
	}
	lines := map[string]bool{}
	for _, e := range out.Events {
		if e.K == "line" {
			lines[e.V.(string)] = true
		}
	}
	if !lines["=== étape"] || !lines["changed: [h]"] || !lines["bruit 499"] || lines["bruit 10"] {
		t.Fatal(lines)
	}
}
