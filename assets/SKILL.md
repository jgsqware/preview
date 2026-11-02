---
name: preview
description: Mettre en place ou faire évoluer « preview » dans un projet — deck de présentation (terminal + HTML autonome) écrit une fois en Markdown, démo live qui lance un vrai scénario avec des sondes d'état, enregistrements rejouables. À utiliser quand on demande de présenter un projet, préparer une démo, une visite guidée, un tableau de bord live d'un scénario, ou d'écrire une sonde preview.
---

# preview

Outil CLI (binaire Go, `preview --help`). Un projet se décrit avec deux fichiers à sa racine :
`preview.toml` (scénarios, sondes, règles de lecture de la sortie) et `demo.md` (le deck).
La spécification complète est dans `SPEC.md` du dépôt preview ; l'essentiel est ci-dessous.

## Mettre preview dans un projet

1. `preview init` : crée `preview.toml` + `demo.md` à partir du README, des tâches mise et de `tests/*.sh`.
2. **Écrire le deck** (`demo.md`) à partir de ce que fait vraiment le projet : lis le README, la structure,
   les scripts. Une idée par diapositive, des faits vérifiables (chiffres, noms réels), pas de remplissage.
3. **Scénarios** : une commande qui démontre le projet de bout en bout (un test, un déploiement, une
   migration). Pour que la TUI montre des étapes, le script imprime le protocole :
   `::step <nom>`, `::ok <vérif>`, `::fail <vérif>`, `::metric k=v`, `::result ok|fail`.
   Une sortie existante se lit aussi sans modifier le script, avec `[live.rules]` (regex : step, ok, fail,
   result_ok, result_fail, command) ; la sortie `ansible-playbook` est reconnue d'office.
4. **Sondes** : commencer par les intégrées (`preview builtin docker|system|http`), puis écrire des sondes
   propres au projet (voir ci-dessous).
5. Vérifier : `preview check`, `preview probe`, `preview deck --all`, `preview rehearse`, `preview html --all`.

Plusieurs publics (version courte / longue) : une liste `[[deck]] name, source, html` à la place de
`[deck]` ; `preview deck` sans argument ouvre alors un sélecteur, `preview check` et `preview html --all`
couvrent tous les decks.

## Écrire une sonde

Un exécutable (lecture seule, sans état, < 8 s) qui imprime **un objet JSON** et sort en 0, même quand
le système observé est absent (le dire dans `status` / `note`). Code ≠ 0 = bug de la sonde.

```json
{"v": 1, "title": "Instances", "status": "ok", "note": "3/3 en marche",
 "empty": "Rien de monté",
 "columns": [{"id": "name", "label": "instance", "type": "text", "style": "label"},
             {"id": "ram", "label": "RAM", "type": "bar"},
             {"id": "cpu", "label": "CPU", "type": "num", "unit": "%", "warn": 80, "crit": 95},
             {"id": "ctr", "label": "conteneurs", "type": "ratio"},
             {"id": "health", "label": "santé", "type": "badge"}],
 "rows": [{"key": "web1", "status": "ok", "cells": {"name": "web1", "ram": {"v": 1.2, "max": 4, "unit": "G"},
           "cpu": {"v": 12}, "ctr": {"v": 3, "max": 3}, "health": "ok"}}],
 "items": [{"label": "Prometheus", "value": "12/14 up", "status": "warn"}],
 "series": [{"label": "conteneurs", "v": 18}],
 "lines": ["texte libre"]}
```

Statuts : `ok | warn | fail | unknown`. L'historique des `series` (sparklines) est tenu par preview.
Déclarer dans `preview.toml` : `[[probe]] name, run, interval, timeout, place = "side"|"bottom"`.
Pour n'afficher que les panneaux utiles à un scénario : `probes = ["relay", "tunnels"]` dans son `[[scenario]]`.
Environnement reçu : `PREVIEW_PROJECT_DIR`, `PREVIEW_STATE_DIR` (pour garder un échantillon précédent).

## Syntaxe du deck (`demo.md`)

- En-tête TOML entre `+++` (title, footer, accent), diapositives séparées par `---`, titre = premier `# `.
- `<!-- cover -->` couverture, `<!-- appendix -->` annexe, `???` puis texte = notes de l'orateur.
- Markdown courant : paragraphes, listes, **gras**, `code`, liens, tableaux GFM, `> [!warning] Titre`.
- Blocs : ```` ```text ```` (schémas verbatim, ≤ 96 colonnes), ```` ```term ```` (`$ cmd` tapé,
  `# commentaire`, `= titre`, `✔`/`✘`), ```` ```tabs ```` (sections `## Nom`, `## Nom *` = actif),
  ```` ```pipeline ```` (`nom | description` par ligne), ```` ```live ```` (`scenario = "x"`).
- Tout widget se parcourt avec → : un onglet ou une étape de pipeline par pression.

## Règles

- Les enregistrements sont masqués à l'écriture (variables `*TOKEN*`, `*PASS*`…, clés, JWT) ; ajouter les
  motifs propres au projet dans `live.redact`. Ne jamais committer `.preview/` (ou le `state_dir`).
- Avant une présentation : `preview rehearse` puis `preview html` (le HTML embarque le dernier run VERT).
  `preview rehearse --check` dit si les enregistrements sont périmés (fichiers modifiés depuis leur commit).
- Si le live est impossible le jour J : `preview live --fallback` rejoue le dernier run VERT.
