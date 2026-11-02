package probes

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jgsqware/preview/internal/config"
)

const Sample = `{"v": 1, "title": "Inst", "status": "warn", "note": "n",
 "columns": [{"id": "n", "type": "text"}, {"id": "r", "type": "bar"}, {"id": "c", "type": "ratio"},
             {"id": "b", "type": "badge"}, {"id": "u", "type": "num", "unit": "%", "warn": 80}],
 "rows": [{"key": "a", "status": "ok", "cells": {"n": "a", "r": {"v": 1, "max": 4, "unit": "G"},
          "c": {"v": 2, "max": 3}, "b": "fail", "u": 90}}],
 "items": [{"label": "Prom", "value": "1/2", "status": "warn"}],
 "series": [{"label": "s", "v": 3}], "lines": ["libre"]}`

func sample() map[string]any {
	var m map[string]any
	json.Unmarshal([]byte(Sample), &m)
	return m
}

func TestValidate(t *testing.T) {
	if e := Validate(sample()); len(e) != 0 {
		t.Fatal(e)
	}
	bad := sample()
	bad["status"] = "nope"
	bad["rows"] = []any{map[string]any{"cells": map[string]any{"zz": 1.0}}}
	errs := strings.Join(Validate(bad), "|")
	if !strings.Contains(errs, "status") || !strings.Contains(errs, "zz") {
		t.Fatal(errs)
	}
}

func TestRunOnceErrors(t *testing.T) {
	cfg := config.New(".", "x")
	ok := RunOnce(&config.Probe{Name: "p", Run: "echo '" + strings.ReplaceAll(Sample, "\n", " ") + "'", Timeout: 8}, cfg)
	if !ok.OK {
		t.Fatal(ok.Error)
	}
	for run, want := range map[string]string{"exit 3": "code 3", "echo nope": "JSON"} {
		if r := RunOnce(&config.Probe{Name: "p", Run: run, Timeout: 8}, cfg); !strings.Contains(r.Error, want) {
			t.Fatal(run, r.Error)
		}
	}
	if r := RunOnce(&config.Probe{Name: "p", Run: "sleep 2", Timeout: 0.3}, cfg); !strings.Contains(r.Error, "timeout") || r.MS > 1000 {
		t.Fatal(r)
	}
}
