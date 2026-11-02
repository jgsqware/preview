// Package scaffold : preview init — preview.toml + demo.md de départ, déduits du dépôt (README, mise.toml, tests).
package scaffold

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

var paraSplit = regexp.MustCompile(`\n\s*\n`)

func readme(root string) (string, string) {
	for _, name := range []string{"README.md", "readme.md", "README"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		text := strings.ToValidUTF8(string(data), "�")
		title := ""
		for _, l := range strings.Split(text, "\n") {
			if strings.HasPrefix(l, "# ") {
				title = strings.TrimSpace(l[2:])
				break
			}
		}
		for _, b := range paraSplit.Split(text, -1) {
			b = strings.TrimSpace(b)
			if b == "" {
				continue
			}
			lead := strings.TrimLeft(b, " \t\n")
			skip := false
			for _, p := range []string{"#", "```", "|", "<", "!", ">"} {
				if strings.HasPrefix(lead, p) {
					skip = true
				}
			}
			if !skip {
				return title, strings.ReplaceAll(b, "\n", " ")
			}
		}
		return title, ""
	}
	return "", ""
}

type task struct{ name, desc string }

// miseTasks : tâches de mise.toml, dans l'ordre du fichier.
func miseTasks(root string) []task {
	data, err := os.ReadFile(filepath.Join(root, "mise.toml"))
	if err != nil {
		return nil
	}
	var raw map[string]any
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil
	}
	tasks, _ := raw["tasks"].(map[string]any)
	var out []task
	seen := map[string]bool{}
	for _, key := range md.Keys() {
		if len(key) < 2 || key[0] != "tasks" || seen[key[1]] {
			continue
		}
		name := key[1]
		seen[name] = true
		desc := ""
		if t, ok := tasks[name].(map[string]any); ok {
			desc, _ = t["description"].(string)
		}
		out = append(out, task{name, desc})
	}
	return out
}

func scenarios(root string, tasks []task) ([]string, string) {
	for _, d := range []string{"tests", "test", "scenarios"} {
		if m, _ := filepath.Glob(filepath.Join(root, d, "*.sh")); len(m) > 0 {
			return []string{"[scenarios]", fmt.Sprintf(`glob = "%s/*.sh"`, d), `run = "bash {path}"`, ""}, ""
		}
	}
	var tests []task
	for _, t := range tasks {
		if strings.HasPrefix(t.name, "test") {
			tests = append(tests, t)
		}
	}
	if len(tests) > 0 {
		var out []string
		for i, t := range tests {
			if i >= 6 {
				break
			}
			name := strings.ReplaceAll(t.name, ":", "-")
			title := t.desc
			if title == "" {
				title = t.name
			}
			out = append(out, "[[scenario]]", fmt.Sprintf(`name = "%s"`, name), fmt.Sprintf(`title = "%s"`, title),
				fmt.Sprintf(`run = "mise run %s"`, t.name), "")
		}
		return out, strings.ReplaceAll(tests[0].name, ":", "-")
	}
	return []string{"[[scenario]]", `name = "demo"`, `title = "Démo"`,
		`run = "echo '::step bonjour'; echo '::ok ça marche'"`, ""}, "demo"
}

var nameRe = regexp.MustCompile(`(?m)^name = "(.+)"`)

// Init : crée preview.toml + demo.md dans root.
func Init(root string, force bool) int {
	cfgPath, deckPath := filepath.Join(root, "preview.toml"), filepath.Join(root, "demo.md")
	for _, p := range []string{cfgPath, deckPath} {
		if _, err := os.Stat(p); err == nil && !force {
			fmt.Printf("preview : %s existe déjà (--force pour l'écraser)\n", filepath.Base(p))
			return 1
		}
	}
	title, intro := readme(root)
	if title == "" {
		title = filepath.Base(root)
	}
	tasks := miseTasks(root)
	scLines, def := scenarios(root, tasks)
	var probes []string
	if _, err := exec.LookPath("docker"); err == nil {
		probes = append(probes, "[[probe]]", `name = "docker"`, `run = "preview builtin docker --all"`, "")
	}
	probes = append(probes, "[[probe]]", `name = "system"`, `run = "preview builtin system /"`, `place = "side"`, "")
	subtitle := `subtitle = ""`
	if intro != "" {
		r := []rune(intro)
		if len(r) > 120 {
			r = r[:120]
		}
		subtitle = fmt.Sprintf(`subtitle = "%s"`, string(r))
	}
	defLine := `# default = "<scénario>"`
	if def != "" {
		defLine = fmt.Sprintf(`default = "%s"`, def)
	}
	cfg := []string{
		"# preview : deck + démo live du projet. Doc : preview skill show",
		"[project]",
		fmt.Sprintf(`name = "%s"`, title),
		subtitle,
		"",
		"[deck]",
		`source = "demo.md"`,
		`html = "demo.html"`,
		"",
		"[live]",
		defLine,
		"interval = 3",
		"",
	}
	cfg = append(cfg, scLines...)
	cfg = append(cfg, probes...)
	if err := os.WriteFile(cfgPath, []byte(strings.TrimRight(strings.Join(cfg, "\n"), " \n")+"\n"), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "preview : %v\n", err)
		return 1
	}

	var rows []string
	for i, t := range tasks {
		if i >= 12 {
			break
		}
		rows = append(rows, fmt.Sprintf("| `mise run %s` | %s |", t.name, t.desc))
	}
	liveSc := def
	if liveSc == "" {
		if m := nameRe.FindStringSubmatch(strings.Join(scLines, "\n")); m != nil {
			liveSc = m[1]
		}
	}
	lead := intro
	if lead == "" {
		lead = "Une phrase qui dit ce que fait le projet."
	}
	slides := []string{
		"+++", fmt.Sprintf(`title = "%s"`, title), `footer = ""`, "+++", "",
		"<!-- cover -->", "# " + title, "", lead, "",
		"---", "", "# Le problème", "", "- Ce qui fait mal aujourd'hui", "- Pourquoi ça compte", "",
		"---", "", "# L'idée", "", "```pipeline", "entrée | d'où ça part", "traitement | ce que fait le projet",
		"résultat | ce qu'on obtient", "```", "",
	}
	if len(rows) > 0 {
		slides = append(slides, "---", "", "# Commandes", "", "| commande | rôle |", "|---|---|",
			strings.Join(rows, "\n"), "")
	}
	slides = append(slides,
		"---", "", "# Démo", "", "```term", "# lancer la démo",
		strings.TrimRight("$ preview live "+liveSc, " "), "```", "",
		"---", "", "# Démo live", "", "```live", fmt.Sprintf(`scenario = "%s"`, liveSc), `title = "En direct"`,
		"```", "",
		"---", "", "# Merci", "", "Questions ?", "")
	if err := os.WriteFile(deckPath, []byte(strings.Join(slides, "\n")), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "preview : %v\n", err)
		return 1
	}
	fmt.Printf("✔ %s et %s créés dans %s\n", filepath.Base(cfgPath), filepath.Base(deckPath), root)
	fmt.Println("  ensuite : preview check · preview deck · preview rehearse · preview html")
	return 0
}
