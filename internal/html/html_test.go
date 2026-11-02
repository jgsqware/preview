package html

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/jgsqware/preview/internal/config"
	"github.com/jgsqware/preview/internal/deck"
)

const demo = "../../examples/demo.md"

func writeCassette(t *testing.T, state, scenario string, evs []map[string]any, metaExtra map[string]any, ok bool) {
	t.Helper()
	d := filepath.Join(state, "recordings", scenario)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "20261028-134341.jsonl"
	meta := map[string]any{"preview": 1, "redaction": 1, "scenario": scenario, "started": "2026-10-28T13:43:41",
		"git": "8646bae", "host": "orb", "env_secret": "NE PAS EXPORTER"}
	for k, v := range metaExtra {
		meta[k] = v
	}
	var b strings.Builder
	line := func(v any) {
		j, _ := json.Marshal(v)
		b.Write(j)
		b.WriteByte('\n')
	}
	line(map[string]any{"t": 0, "k": "meta", "v": meta})
	for _, e := range evs {
		line(e)
	}
	if err := os.WriteFile(filepath.Join(d, name), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok {
		if err := os.Symlink(name, filepath.Join(d, "latest-ok.jsonl")); err != nil {
			t.Fatal(err)
		}
	}
}

func setup(t *testing.T) (*config.Config, string, string) {
	dir := t.TempDir()
	state := filepath.Join(dir, ".preview")
	cfg := config.New(dir, "Démo")
	cfg.StateDir = state
	cfg.Probes = []*config.Probe{{Name: "inst", Run: "true", Place: "side"}}
	return cfg, dir, state
}

func TestExportSelfContainedWithPlayer(t *testing.T) {
	cfg, dir, state := setup(t)
	evil := "</script><img src=x onerror=alert(1)>"
	writeCassette(t, state, "platform", []map[string]any{
		{"t": 0.1, "k": "line", "v": "=== préparation"},
		{"t": 0.2, "k": "line", "v": "\x1b[32m✔ base prête\x1b[0m"},
		{"t": 0.3, "k": "line", "v": evil},
		{"t": 0.4, "k": "snap", "v": map[string]any{
			"inst": map[string]any{"v": 1, "title": "Instances", "status": "ok",
				"columns": []any{map[string]any{"id": "n", "label": "nom", "type": "text"}},
				"rows":    []any{map[string]any{"key": "a", "cells": map[string]any{"n": evil}}}},
			"ancien": []any{1, 2, 3}}},
		{"t": 0.5, "k": "snap", "v": []any{1, 2}},
		{"t": 9.0, "k": "line", "v": "=== vérifications"},
		{"t": 9.5, "k": "line", "v": "✘ une alerte"},
		{"t": 10, "k": "end", "v": 0},
	}, map[string]any{"plan": []any{"préparation", "vérifications", "jamais"}}, true)
	src := filepath.Join(dir, "demo.md")
	_ = os.WriteFile(src, []byte("+++\ntitle = \"Titre <b>\"\naccent = \"red;}</style>\"\n+++\n# Un <script>alert(1)</script>\n"+
		"[x](javascript:alert(1)) et `code`\n---\n# Démo\n```live\nscenario = \"platform\"\n```\n"+
		"```live\nscenario = \"absent\"\n```\n"), 0o644)
	var warns []string
	out, err := Export(cfg, src, filepath.Join(dir, "out", "demo.html"), func(m string) { warns = append(warns, m) })
	if err != nil {
		t.Fatal(err)
	}
	if out != filepath.Join(dir, "out", "demo.html") {
		t.Fatal(out)
	}
	raw, _ := os.ReadFile(out)
	page := string(raw)
	must := func(cond bool, msg string) {
		t.Helper()
		if !cond {
			t.Fatal(msg)
		}
	}
	must(!strings.Contains(page, "<script src"), "script externe")
	must(strings.Count(page, "</script>") == strings.Count(page, "<script"), "scripts déséquilibrés")
	must(!strings.Contains(page, evil), "charge brute présente")
	must(strings.Contains(page, "\\u003c/script\\u003e\\u003cimg"), "charge non échappée")
	must(!strings.Contains(page, "<script>alert(1)"), "titre non échappé")
	must(strings.Contains(page, "&lt;script&gt;alert(1)&lt;/script&gt;"), "titre échappé absent")
	must(!strings.Contains(page, "javascript:"), "lien javascript:")
	must(!strings.Contains(page, "</style>\""), "accent invalide gardé")
	must(!strings.Contains(page, "NE PAS EXPORTER"), "meta non filtrée")
	must(strings.Contains(page, "Titre &lt;b&gt;"), "titre du deck")
	found := false
	for _, w := range warns {
		found = found || strings.Contains(w, "absent")
	}
	must(found, "pas d'avertissement pour « absent »")

	datas := map[string]string{}
	for _, m := range regexp.MustCompile(`(?s)<script type="application/json" id="([\w-]+)">(.*?)</script>`).FindAllStringSubmatch(page, -1) {
		datas[m[1]] = m[2]
	}
	keys := []string{}
	for k := range datas {
		keys = append(keys, k)
	}
	must(len(datas) == 3 && datas["deck-data"] != "" && datas["live-0"] != "" && datas["live-1"] != "", strings.Join(keys, ","))
	var p0 map[string]any
	_ = json.Unmarshal([]byte(datas["live-0"]), &p0)
	must(p0["verdict"] == "ROUGE", "verdict : code 0 mais une vérification ✘")
	var plan []string
	for _, x := range p0["plan"].([]any) {
		plan = append(plan, x.(map[string]any)["name"].(string))
	}
	must(reflect.DeepEqual(plan, []string{"préparation", "vérifications", "jamais"}), "plan")
	var lines, snaps []map[string]any
	for _, e := range p0["events"].([]any) {
		em := e.(map[string]any)
		switch em["k"] {
		case "line":
			lines = append(lines, em)
		case "snap":
			snaps = append(snaps, em)
		}
	}
	want := map[string]any{"t": 0.2, "k": "line", "v": "✔ base prête", "e": []any{[]any{"check", true, "base prête"}}, "s": "ok"}
	must(reflect.DeepEqual(lines[1], want), "ligne ✔")
	must(reflect.DeepEqual(lines[0]["e"], []any{[]any{"step", "préparation"}}), "ligne d'étape")
	must(len(snaps) == 1, "un seul snap")
	v := snaps[0]["v"].(map[string]any)
	_, hasInst := v["inst"]
	must(len(v) == 1 && hasInst, "snap filtré")
	must(reflect.DeepEqual(p0["place"], map[string]any{"inst": "side"}), "place")
	must(reflect.DeepEqual(p0["summary"].(map[string]any)["failed"], []any{"vérifications"}), "étapes en échec")
	var p1 map[string]any
	_ = json.Unmarshal([]byte(datas["live-1"]), &p1)
	must(p1["missing"] == true, "live-1 manquant")
}

func TestFallbackToLatestAndRefusal(t *testing.T) {
	cfg, _, state := setup(t)
	writeCassette(t, state, "rouge", []map[string]any{{"t": 1, "k": "line", "v": "x"}, {"t": 2, "k": "end", "v": 1}}, nil, false)
	p := LivePayload(cfg, "rouge")
	if p["verdict"] != "ROUGE" {
		t.Fatal(p["verdict"])
	}
	if len(p["plan"].([]any)) != 0 {
		t.Fatal(p["plan"])
	}
	d := filepath.Join(state, "recordings", "vieux")
	_ = os.MkdirAll(d, 0o755)
	_ = os.WriteFile(filepath.Join(d, "20260101-000000.jsonl"), []byte(`{"t":0,"k":"meta","v":{"scenario":"vieux"}}`+"\n"), 0o644)
	p = LivePayload(cfg, "vieux")
	if e, _ := p["error"].(string); !strings.Contains(e, "masquage") {
		t.Fatal(p)
	}
}

func TestRenderDemo(t *testing.T) {
	cfg, _, _ := setup(t)
	d, err := deck.Parse(demo)
	if err != nil {
		t.Fatal(err)
	}
	page := Render(d, cfg, map[string]map[string]any{})
	for _, s := range []string{`data-widget="tabs"`, `data-widget="pipeline"`, `data-widget="term"`, `class="rack"`,
		"--accent-src: #B8600C"} {
		if !strings.Contains(page, s) {
			t.Fatalf("%q absent", s)
		}
	}
	if n := strings.Count(page, `<section class="slide`); n != len(d.Slides) {
		t.Fatalf("%d sections pour %d diapositives", n, len(d.Slides))
	}
}
