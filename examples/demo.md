+++
title = "Preview"
subtitle = "Présenter un projet, depuis son propre dépôt."
footer = "exemple · examples/demo.md"
accent = "#B8600C"
+++

<!-- cover -->
# Preview

Un **deck** écrit une seule fois en Markdown, une *démo live* qui suit un vrai run, et des
**enregistrements** qui la rejouent hors ligne.

- **deck** terminal et HTML autonome `.md`
- **live** scénario + sondes, en direct `tui`
- **replay** le dernier run VERT `jsonl`

???
Se présenter, puis annoncer le plan : le problème, le format, la démo.

---

# Le problème
## Trois outils de démo écrits à la main

Chaque projet finit avec ses diapositives, son script de démo et ses captures, qui vieillissent
séparément. Voir [la spécification](SPEC.md) pour le détail.

## Aujourd'hui

- Un deck HTML de **1 000 lignes**, copié de projet en projet
- Un script bash pour le terminal
  - même contenu, réécrit
  - couleurs codées en dur
- Une démo live qui ne tourne que *sur le poste de l'orateur*

## Ce qu'on veut

1. Un seul fichier source, relu en MR
2. Une démo qui **prouve** que ça marche
3. Un export qui s'ouvre partout, même hors ligne

---

# Comment ça marche
## Une source, trois sorties

```text
                 ┌──────────────┐
   demo.md  ───► │   preview    │ ───► terminal (100 colonnes)
                 │  deck · live │ ───► index.html (autonome)
 preview.toml ─► └──────┬───────┘
                        │ run réel
                        ▼
              .preview/recordings/*.jsonl  ───►  lecteur HTML
```

| sortie | commande | hors ligne | contenu |
|:-------|:---------|:----------:|--------:|
| Terminal | `preview deck` | ✅ | diapositives |
| HTML | `preview html` | ✅ | + dernier run 🟢 |
| Live | `preview live` | ❌ | run + sondes |

> [!tip] Pas de dépendance
> Bibliothèque standard Python uniquement : `tomllib`, `json`, `curses` n'est même pas nécessaire.

---

# Un deck, deux cibles

```tabs
## Markdown
Les diapositives sont séparées par une ligne `---`, **jamais** à l'intérieur d'un bloc :

~~~yaml
# un document YAML multiple
---
name: premier
---
name: second
~~~

## Terminal *
- Écran alterné, curseur masqué, largeur **100 colonnes** centrée
- Barre de progression `●·` en bas, `--all` sans TTY
- Les tableaux deviennent des tableaux en traits

## HTML
| touche | action |
|--------|--------|
| → espace | étape suivante |
| ← | étape précédente |
| `n` | notes de l'orateur |
| `t` | thème clair / sombre |
```

> Chaque widget se réduit à une suite d'**étapes** : la flèche parcourt d'abord les onglets.

---

# Un run, de l'intérieur
## Cinq étapes, un enregistrement

```pipeline
$ preview live platform
préflight | `incus info` répond, sinon on propose le dernier run VERT
scénario | `./scripts/test.sh platform`, sortie lue ligne par ligne
sondes | toutes les 3 s : un objet JSON par sonde
masquage | jetons, mots de passe et clés remplacés avant l'écriture
cassette | `.preview/recordings/platform/*.jsonl`
```

> [!warning] Masquage à l'écriture
> Un enregistrement sans champ `redaction` est **refusé** à l'export HTML.

???
Insister sur le masquage : rien ne part dans le HTML sans être passé par le Redactor.

---

# Démo

```term preview — bash
# 1. vérifier la configuration
$ preview check
✔ preview.toml : Preview · 1 scénario(s) · 2 sonde(s)
✔ deck demo.md : 7 diapositive(s)
# 2. répéter la démo, sans écran
$ preview rehearse platform
= platform
✔ VERT en 04:41
✘ obs : 1 alerte en feu (tolérée)
$ preview html -o demo/index.html
```

```live
scenario = "platform"
title = "Plateforme + observabilité"
```

---

<!-- appendix -->
# Configuration minimale

```toml
[project]
name = "Mon projet"

[[scenario]]
name = "platform"
run = "./scripts/test.sh platform"
```

> [!note]
> Les sondes sont facultatives : sans elles, le lecteur affiche les étapes et le journal.

???
Annexe : à montrer seulement si on pose la question.
