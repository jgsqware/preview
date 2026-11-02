package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

const sample = `{"v": 1, "title": "Inst", "status": "warn", "note": "n",
 "columns": [{"id": "n", "type": "text"}, {"id": "r", "type": "bar"}, {"id": "c", "type": "ratio"},
             {"id": "b", "type": "badge"}, {"id": "u", "type": "num", "unit": "%", "warn": 80}],
 "rows": [{"key": "a", "status": "ok", "cells": {"n": "a", "r": {"v": 1, "max": 4, "unit": "G"},
          "c": {"v": 2, "max": 3}, "b": "fail", "u": 90}}],
 "items": [{"label": "Prom", "value": "1/2", "status": "warn"}],
 "series": [{"label": "s", "v": 3}], "lines": ["libre"]}`

func TestRenderProbe(t *testing.T) {
	var obj map[string]any
	json.Unmarshal([]byte(sample), &obj)
	h := ProbeHeight(obj, "", 90)
	c := NewCanvas(h, 90)
	DrawProbe(c, 0, 0, h, 90, "inst", obj, "", nil)
	txt := c.Text()
	for _, s := range []string{"Inst", "1/4G", "2/3", "▮▮▯", "90%", "Prom", "libre"} {
		if !strings.Contains(txt, s) {
			t.Fatalf("%q absent :\n%s", s, txt)
		}
	}
}

func TestWidths(t *testing.T) {
	if Width("aé") != 2 || Width("🚀漢") != 4 || Width("é") != 1 {
		t.Fatal(Width("aé"), Width("🚀漢"), Width("é"))
	}
	if Clip("abcdef", 4) != "abc…" || FmtNum(12345.0) != "12.3k" || FmtNum(2.5) != "2.5" || MMSS(65) != "01:05" {
		t.Fatal(Clip("abcdef", 4), FmtNum(12345.0), FmtNum(2.5), MMSS(65))
	}
}
