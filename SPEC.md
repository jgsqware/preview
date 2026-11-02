# Preview — spécification v1

Preview présente un projet : un **deck** (terminal et HTML autonome) écrit une seule fois en Markdown,
une **démo live** (TUI qui lance un scénario, suit sa sortie et montre l'état du système via des sondes),
et des **enregistrements** qui rejouent un vrai run hors ligne.

Un **binaire Go unique** (Go ≥ 1.26 pour le construire), sans dépendance à l'exécution. Interface en français.
Les regex de `preview.toml` (`live.rules`, `live.redact`) suivent la syntaxe de Python `re` (lookbehind compris).

```
preview deck [demo.md] [--all] [--slide N]      deck dans le terminal
preview html [demo.md] [-o fichier.html]        export HTML autonome (enregistrements inclus)
preview live [scénario]                         lance le scénario + TUI, enregistre le run
preview watch                                   tableau de bord seul (sondes + journal suivi), lecture seule
preview replay [fichier|scénario] [--speed X] [--once]
preview rehearse [scénario…]                    joue sans TUI, enregistre, rapport OK / ROUGE / PÉRIMÉ
preview probe [nom] [--json]                    exécute les sondes une fois (mise au point)
preview check                                   valide preview.toml, le deck et les sondes
preview init                                    crée preview.toml + demo.md à partir du dépôt
preview skill install                           installe le skill Claude Code dans ~/.claude/skills/
```

Option globale `-C DIR` : dossier du projet (défaut : premier dossier parent contenant `preview.toml`).

---

## 1. `preview.toml`

Tous les chemins sont relatifs au dossier qui contient `preview.toml` (le « dossier projet »), qui est
aussi le dossier courant de toutes les commandes lancées.

```toml
[project]
name = "Boutique"                # requis
subtitle = "Une boutique en ligne, montée de bout en bout."
state_dir = ".preview"          # enregistrements ; défaut ".preview"

[deck]
source = "demo.md"               # défaut "demo.md"
html = "demo/index.html"         # défaut "<source sans .md>.html"

[live]
default = "platform"             # scénario par défaut de `preview live`
interval = 3                     # secondes entre deux collectes des sondes ; défaut 3
redact = ['stub-token-[\w-]+']   # regex supplémentaires masquées dans les enregistrements
follow = "../.logs/last"         # watch : fichier qui contient le chemin du journal à suivre
                                 #         (ou directement un fichier journal)

# Règles de lecture de la sortie (adaptateur « rules »). Valeurs par défaut ci-dessous.
[live.rules]
step = '^=== (.+)$'
ok = '^[✔✓] (.+)$'
fail = '^[✘✗] (.+)$'
result_ok = ''                   # regex : le run est déclaré VERT
result_fail = ''                 # regex : le run est déclaré ROUGE
ignore_step = ''                 # regex : ligne qui matche `step` mais n'est pas une étape
command = ''                     # regex, groupe 1 : sous-commande en cours (ex. playbook)
plan = '^\s*step "(.*)"\s*$'     # regex appliquée au fichier `plan` pour les étapes prévues

# Scénarios explicites
[[scenario]]
name = "platform"
title = "Plateforme + observabilité"
run = "./scripts/test.sh platform"  # commande shell (bash -c)
plan = "tests/platform.sh"       # fichier scanné pour les étapes prévues (optionnel)
preflight = "incus info >/dev/null"  # si échec : `preview live` propose le dernier enregistrement VERT
timeout = 1800                   # secondes ; défaut aucun
env = { KEEP_ENV = "1" }

# … ou découverts : un scénario par fichier
[scenarios]
glob = "tests/*.sh"              # {name} = nom du fichier sans extension
run = "./scripts/test.sh {name}"
plan = "{path}"                  # défaut : le fichier lui-même
env = { KEEP_ENV = "1" }

[[probe]]
name = "instances"               # requis, unique
run = "probes/incus.py"          # commande shell ; stdout = un objet JSON (§4)
interval = 3                     # défaut : live.interval
timeout = 8                      # défaut 8
place = "side"                   # "side" (à droite des étapes) | "bottom" (sous le journal) ; défaut "bottom"
```

Les `[[scenario]]` explicites passent avant ceux de `[scenarios]` en cas de nom identique.

---

## 2. Lecture de la sortie : événements

Chaque ligne de sortie (stdout + stderr fusionnés, ANSI retiré) passe par les adaptateurs dans cet ordre ;
le premier qui reconnaît la ligne gagne.

1. **protocol** — lignes émises exprès par n'importe quel script :
   - `::step <nom>` début d'une étape de premier niveau
   - `::ok <texte>` / `::fail <texte>` vérification dans l'étape courante
   - `::metric clé=valeur [clé=valeur…]` valeur numérique, gardée en historique (sparkline)
   - `::result ok|fail` verdict final
   - `::note <texte>` ligne mise en avant dans le journal
2. **ansible** — `PLAY [x]`, `TASK [x]`, `RUNNING HANDLER [x]`, `ok:|changed:|failed:|fatal:|skipping:|unreachable:`,
   `PLAY RECAP` + lignes `hôte : ok=N changed=N unreachable=N failed=N skipped=N` → play, tâche courante,
   compteurs du playbook en cours et totaux.
3. **rules** — les regex de `[live.rules]` (groupe 1 = texte).

Événements produits (internes, recalculés à chaque relecture d'un enregistrement) :
`step(name)`, `check(ok: bool, text)`, `metric(key, value)`, `result(ok: bool)`, `command(text)`,
`play(name)`, `task(name)`, `host_result(kind)`, `recap(host, counters)`.

**Étapes prévues** : regex `live.rules.plan` appliquée au fichier `plan` du scénario, plus les lignes
`::step <nom>` littérales qu'il contient. Dans un nom, `$VAR` / `${VAR}` devient un joker `.+`.
Une étape reçue est rapprochée de la prochaine étape prévue qui matche ; les étapes prévues sautées
passent en « sautée » ; une étape non prévue est insérée.

**Repli** : si aucun `step` n'arrive, le scénario entier est une étape unique. Sans `result`, le verdict
vient du code de sortie (0 = VERT).

---

## 3. Enregistrements (« cassettes »)

`<state_dir>/recordings/<scénario>/<AAAAMMJJ-HHMMSS>.jsonl`, plus un lien `latest-ok.jsonl` vers le
dernier run VERT et `latest.jsonl` vers le dernier run. Une ligne JSON par événement :

```json
{"t": 0, "k": "meta", "v": {"preview": 1, "redaction": 1, "scenario": "platform", "title": "…",
  "started": "2026-10-28T13:43:41", "cmd": "./scripts/test.sh platform", "git": "8646bae", "host": "orb",
  "plan": ["doctor + up", "…"]}}
{"t": 1.52, "k": "line", "v": "TASK [docker : install] ***"}
{"t": 3.01, "k": "snap", "v": {"instances": {…objet sonde §4…}, "obs": {…}}}
{"t": 281.4, "k": "end", "v": 0}
```

- `t` = secondes depuis le début du run.
- Les lignes `line` sont **brutes** (sans ANSI) : les adaptateurs sont rejoués à la lecture.
- **Masquage à l'écriture**, pas à l'export : valeurs des variables d'environnement dont le nom matche
  `TOKEN|SECRET|PASS|KEY|VAULT|CREDENTIAL` (longueur ≥ 6), blocs PEM, `glpat-…`, JWT, `AKIA…`,
  `password[=:] …`, + `live.redact`. Remplacé par `‹masqué›`. Appliqué aux lignes et aux chaînes des snaps.
  `meta.redaction` = version du masquage ; l'export HTML refuse une cassette sans ce champ.
- Compatibilité : les anciens enregistrements (`{"t","k":"meta"|"line"|"snap"|"end"}` sans
  `preview` dans meta) se rejouent ; un snap qui n'est pas un dict de sondes est ignoré.

**Export HTML** : cassette réduite — garder toutes les lignes step/check/result/fail/changed/command/play,
les 40 dernières lignes de chaque étape, au plus 250 Ko de lignes ; snaps échantillonnés (≤ 120).

---

## 4. Sondes

Une sonde est une commande qui écrit **un objet JSON** sur stdout puis se termine. Elle ne connaît pas
l'interface : elle décrit des données typées. Elle est sans état (l'historique est tenu par Preview).
Code de sortie ≠ 0, timeout ou JSON invalide → panneau « indisponible » avec la fin de stderr, et
le dernier snapshot valide affiché grisé (« périmé »).

```json
{
  "v": 1,
  "title": "Instances Incus",
  "status": "ok",                       // ok | warn | fail | unknown   (pire statut → en-tête)
  "note": "3/3 en marche · 18 conteneurs · RAM 5.1 Go",
  "empty": "Aucune instance démarrée",  // texte si rows est vide
  "columns": [
    {"id": "name", "label": "instance", "type": "text", "style": "label", "width": 18},
    {"id": "ip",   "label": "IP",       "type": "text", "style": "teal"},
    {"id": "ram",  "label": "RAM",      "type": "bar"},
    {"id": "cpu",  "label": "CPU",      "type": "num", "unit": "%", "warn": 80, "crit": 95},
    {"id": "ctr",  "label": "conteneurs", "type": "ratio"}
  ],
  "rows": [
    {"key": "web-1", "status": "ok",
     "cells": {"name": "web-1", "ip": "10.0.0.11",
               "ram": {"v": 1.2, "max": 4, "unit": "G"}, "cpu": {"v": 12.5},
               "ctr": {"v": 10, "max": 12}}}
  ],
  "items": [                            // paires libellé / valeur (panneau « tableau de bord »)
    {"label": "Prometheus", "value": "12/14 cibles up", "status": "warn"}
  ],
  "series": [                           // valeurs suivies dans le temps → sparkline
    {"label": "conteneurs", "v": 18, "style": "teal"}
  ],
  "lines": ["texte libre, une ligne par entrée"]
}
```

Types de cellule (une cellule peut aussi être `null` → « — ») :

| type    | valeur                                  | rendu                                          |
|---------|-----------------------------------------|------------------------------------------------|
| `text`  | chaîne                                  | texte (style de colonne : `label`, `teal`, `dim`, `acc`) |
| `badge` | `"ok"\|"warn"\|"fail"\|"unknown"` ou `{"status","text"}` | `●` coloré + texte              |
| `bar`   | `{"v","max","unit"?}`                   | barre ████░░ + `v/max unit` ; ≥ 85 % = warn     |
| `num`   | `{"v","unit"?}` ou nombre               | nombre, coloré selon `warn` / `crit` de la colonne |
| `ratio` | `{"v","max"}`                           | `v/max` + ▮▮▮▯ ; vert si v == max              |

Le statut d'une ligne (`row.status`) colore sa puce `●` en tête de ligne.
Environnement passé aux sondes : `PREVIEW_PROJECT_DIR`, `PREVIEW_STATE_DIR`.

---

## 5. Deck : `demo.md`

En-tête TOML optionnel entre deux lignes `+++`, puis des diapositives séparées par une ligne `---`.

```markdown
+++
title = "Boutique"
footer = "Équipe plateforme · dossier demo/"
accent = "#B8600C"      # couleur d'accent HTML (optionnel)
+++

# Le problème

Paragraphe avec **gras**, *italique*, `code` et [lien](https://…).

- puce
  - sous-puce

> [!warning] Titre facultatif
> Encadré (note, tip, warning, danger).

| colonne | colonne |
|---------|---------|
| a       | b       |

---

# Une autre diapositive
```

Premier `# ` d'une diapositive = son titre. `<!-- cover -->` sur une diapositive = couverture (titre en
grand, centré). `<!-- appendix -->` = annexe (après « Fin », hors compte principal).
`## ` = sous-titre dans la diapositive. Lignes `???` puis texte = notes de l'orateur (jusqu'à la fin de
la diapositive ; HTML : touche `n`).

Blocs clôturés :

| bloc | contenu | terminal | HTML |
|------|---------|----------|------|
| ```` ```text ```` ou sans langage | verbatim (schémas, arbres) | tel quel | `<pre>` |
| ```` ```bash ```` etc. | code | tel quel, en couleur d'accent | `<pre><code>` |
| ```` ```term ```` | `$ cmd` tapé, `# commentaire`, `= titre`, `✔ ok`, `✘ ko`, autre = sortie | affiché d'un coup | terminal animé (frappe), rejouable |
| ```` ```tabs ```` | sections `## Nom` (`## Nom *` = actif au départ), corps Markdown | → parcourt les onglets avant la diapositive suivante | onglets cliquables, → aussi |
| ```` ```pipeline ```` | une étape par ligne : `nom \| description` | → révèle une étape à la fois | étapes qui s'allument, rejouable |
| ```` ```live ```` | `scenario = "platform"` (TOML) + `title = "…"` | touches `d` lancer live, `w` watch, `r` rejouer | lecteur de la dernière cassette VERTE (étapes, journal, panneaux de sondes), lecture/pause/×4 |

Règle : **tout widget se réduit à une suite d'étapes** que → / ← parcourent, dans les deux cibles.

Navigation commune : → espace l j = suivant, ← h k = précédent, 1-9 = diapositive, g / G = début / fin,
q = quitter (terminal). HTML : swipe, `#N` dans l'URL, thème clair / sombre suivant le système.
