// Package builtin : sondes intégrées, utilisables telles quelles dans preview.toml :
//
//	[[probe]]  name = "docker"  run = "preview builtin docker [--all] [--filter label=x]"
//	[[probe]]  name = "system"  run = "preview builtin system [/ /var …]"
//	[[probe]]  name = "http"    run = "preview builtin http https://a/health [nom=https://b] …"
package builtin

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jgsqware/preview/internal/cassette"
)

type obj = map[string]any

func out(o obj) int {
	o["v"] = 1
	fmt.Println(string(cassette.Marshal(o)))
	return 0
}

func round(v float64, n int) float64 {
	p := math.Pow(10, float64(n))
	return math.RoundToEven(v*p) / p
}

// Main : exécute la sonde `name`.
func Main(name string, argv []string) int {
	switch name {
	case "docker":
		return docker(argv)
	case "system":
		return system(argv)
	case "http":
		return httpProbe(argv)
	}
	fmt.Fprintf(os.Stderr, "preview : sonde intégrée inconnue : %s (docker, system, http)\n", name)
	return 2
}

func docker(argv []string) int {
	args := []string{"ps", "--format", "{{json .}}"}
	for i, a := range argv {
		if a == "--all" || a == "-a" {
			args = append([]string{"ps", "-a"}, args[1:]...)
		}
		if a == "--filter" && i+1 < len(argv) {
			args = append([]string{args[0], "--filter", argv[i+1]}, args[1:]...)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok || ctx.Err() != nil {
			return out(obj{"title": "Conteneurs Docker", "status": "unknown", "note": "docker indisponible : " + err.Error(),
				"lines": []any{}})
		}
		note := "docker ps en échec"
		lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
		if l := lines[len(lines)-1]; l != "" {
			note = l
		}
		return out(obj{"title": "Conteneurs Docker", "status": "unknown", "note": note})
	}
	var rows []obj
	running := 0
	sc := bufio.NewScanner(strings.NewReader(string(stdout)))
	for sc.Scan() {
		var c map[string]any
		if json.Unmarshal(sc.Bytes(), &c) != nil {
			continue
		}
		s := func(k, def string) string {
			if v, ok := c[k].(string); ok {
				return v
			}
			return def
		}
		state := s("State", "")
		up := state == "running"
		unhealthy := strings.Contains(s("Status", ""), "unhealthy")
		if up {
			running++
		}
		st := "warn"
		switch {
		case unhealthy || state == "exited" || state == "dead":
			st = "fail"
		case up:
			st = "ok"
		}
		rows = append(rows, obj{"key": s("Names", "?"), "status": st,
			"cells": obj{"name": s("Names", "?"), "image": s("Image", ""), "state": obj{"status": st, "text": state},
				"since": s("RunningFor", "")}})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i]["key"].(string) < rows[j]["key"].(string) })
	worst := "ok"
	for _, r := range rows {
		if r["status"] == "warn" && worst == "ok" {
			worst = "warn"
		}
		if r["status"] == "fail" {
			worst = "fail"
		}
	}
	if len(rows) == 0 {
		worst = "unknown"
	}
	return out(obj{"title": "Conteneurs Docker", "status": worst,
		"note": fmt.Sprintf("%d/%d en marche", running, len(rows)), "empty": "Aucun conteneur",
		"columns": []obj{{"id": "name", "label": "conteneur", "type": "text", "style": "label", "width": 28},
			{"id": "image", "label": "image", "type": "text", "style": "dim", "width": 36},
			{"id": "state", "label": "état", "type": "badge"},
			{"id": "since", "label": "créé", "type": "text", "style": "dim"}},
		"rows": nonNilRows(rows), "series": []obj{{"label": "en marche", "v": running}}})
}

func nonNilRows(r []obj) []obj {
	if r == nil {
		return []obj{}
	}
	return r
}

func meminfo() map[string]float64 {
	info := map[string]float64{}
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return info
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseFloat(f[0], 64)
		if err == nil {
			info[k] = n * 1024
		}
	}
	return info
}

func statusOf(frac float64) string {
	switch {
	case frac > .95:
		return "fail"
	case frac > .85:
		return "warn"
	}
	return "ok"
}

func system(argv []string) int {
	var paths []string
	for _, a := range argv {
		if strings.HasPrefix(a, "/") {
			paths = append(paths, a)
		}
	}
	if len(paths) == 0 {
		paths = []string{"/"}
	}
	const G = 1 << 30
	var rows []obj
	load := loadAvg()
	cpus := runtime.NumCPU()
	mem := meminfo()
	if total := mem["MemTotal"]; len(mem) > 0 {
		used := total - mem["MemAvailable"]
		frac := 0.0
		if total > 0 {
			frac = used / total
		}
		rows = append(rows, obj{"key": "ram", "status": statusOf(frac),
			"cells": obj{"name": "mémoire", "use": obj{"v": round(used/G, 1), "max": round(total/G, 1), "unit": " Go"}}})
	}
	for _, p := range paths {
		total, free, ok := diskUsage(p)
		if !ok {
			continue
		}
		used := total - free
		frac := 0.0
		if total > 0 {
			frac = used / total
		}
		rows = append(rows, obj{"key": p, "status": statusOf(frac),
			"cells": obj{"name": "disque " + p, "use": obj{"v": round(used/G, 1), "max": round(total/G, 1), "unit": " Go"}}})
	}
	worst := "ok"
	rank := map[string]int{"ok": 0, "warn": 1, "fail": 2}
	for _, r := range rows {
		if s := r["status"].(string); rank[s] > rank[worst] {
			worst = s
		}
	}
	status := worst
	if load > float64(cpus) && worst == "ok" {
		status = "warn"
	}
	return out(obj{"title": "Système", "status": status, "note": fmt.Sprintf("charge %.2f / %d CPU", load, cpus),
		"columns": []obj{{"id": "name", "label": "ressource", "type": "text", "style": "label"},
			{"id": "use", "label": "utilisation", "type": "bar"}},
		"rows": nonNilRows(rows), "series": []obj{{"label": "charge", "v": round(load, 2)}}})
}

func loadAvg() float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err == nil {
		if f := strings.Fields(string(data)); len(f) > 0 {
			v, _ := strconv.ParseFloat(f[0], 64)
			return v
		}
	}
	// macOS et autres : sysctl
	if o, err := exec.Command("sysctl", "-n", "vm.loadavg").Output(); err == nil {
		f := strings.Fields(strings.Trim(strings.TrimSpace(string(o)), "{}"))
		if len(f) > 0 {
			v, _ := strconv.ParseFloat(f[0], 64)
			return v
		}
	}
	return 0
}

func httpProbe(argv []string) int {
	type target struct{ name, url string }
	var targets []target
	insecure := false
	for _, a := range argv {
		if a == "--insecure" || a == "-k" {
			insecure = true
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		if name, url, ok := strings.Cut(a, "="); ok && !strings.HasPrefix(a, "http") {
			targets = append(targets, target{name, url})
		} else {
			host := a
			if i := strings.Index(host, "//"); i >= 0 {
				host = host[i+2:]
			}
			host, _, _ = strings.Cut(host, "/")
			targets = append(targets, target{host, a})
		}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // option explicite --insecure
	}
	client := &http.Client{Timeout: 4 * time.Second, Transport: tr}
	rows := make([]obj, len(targets))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t target) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			t0 := time.Now()
			resp, err := client.Get(t.url)
			if err != nil {
				for u := errors.Unwrap(err); u != nil; u = errors.Unwrap(u) {
					err = u // cause racine : « connection refused » plutôt que toute la chaîne
				}
				reason := err.Error()
				if r := []rune(reason); len(r) > 60 {
					reason = string(r[:60])
				}
				rows[i] = obj{"key": t.name, "status": "fail", "cells": obj{"name": t.name,
					"code": obj{"status": "fail", "text": "injoignable"}, "ms": nil, "url": reason}}
				return
			}
			resp.Body.Close()
			ms := float64(time.Since(t0).Milliseconds())
			st := "fail"
			switch {
			case resp.StatusCode < 400:
				st = "ok"
			case resp.StatusCode < 500:
				st = "warn"
			}
			rows[i] = obj{"key": t.name, "status": st, "cells": obj{"name": t.name,
				"code": obj{"status": st, "text": strconv.Itoa(resp.StatusCode)}, "ms": obj{"v": math.Round(ms)}, "url": t.url}}
		}(i, t)
	}
	wg.Wait()
	ok, fail := 0, false
	for _, r := range rows {
		switch r["status"] {
		case "ok":
			ok++
		case "fail":
			fail = true
		}
	}
	worst := "ok"
	switch {
	case len(rows) == 0:
		worst = "unknown"
	case fail:
		worst = "fail"
	case ok < len(rows):
		worst = "warn"
	}
	return out(obj{"title": "HTTP", "status": worst, "note": fmt.Sprintf("%d/%d OK", ok, len(rows)),
		"empty": "Aucune URL (arguments : nom=https://… )",
		"columns": []obj{{"id": "name", "label": "service", "type": "text", "style": "label", "width": 24},
			{"id": "code", "label": "code", "type": "badge"},
			{"id": "ms", "label": "ms", "type": "num", "warn": 800, "crit": 2000},
			{"id": "url", "label": "url", "type": "text", "style": "dim"}},
		"rows": rows, "series": []obj{{"label": "OK", "v": ok, "style": "ok"}}})
}
