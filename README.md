# preview

Présenter un projet sans dupliquer : un **deck** écrit une fois en Markdown (terminal et HTML autonome),
une **démo live** qui lance un vrai scénario et montre l'état du système, et des **enregistrements**
qui rejouent un vrai run quand le live n'est pas possible.

Un seul binaire Go, sans dépendance à l'exécution (le gabarit HTML est embarqué).

```bash
mise run install          # ou : go install github.com/jgsqware/preview@latest
cd mon-projet && preview init
preview deck              # deck dans le terminal (← →)
preview live              # scénario en direct + sondes, enregistré
preview rehearse          # rejoue tout sans TUI avant une présentation
preview html              # deck HTML autonome, avec le dernier run VERT embarqué
```

| Commande | Rôle |
|---|---|
| `preview deck [deck\|demo.md] [--all]` | deck terminal ; sélecteur s'il y a plusieurs `[[deck]]` ; `d` / `w` / `r` sur une diapositive live |
| `preview html [deck\|demo.md] [-o f.html] [--all]` | un seul fichier HTML, hors ligne, clair / sombre ; `--all` : tous les decks |
| `preview live [scénario] [--fallback]` | TUI : étapes, journal, compteurs Ansible, sondes ; enregistre |
| `preview watch [--once]` | sondes + journal suivi, lecture seule |
| `preview replay [scénario\|fichier] [--speed X] [--once]` | rejoue un enregistrement |
| `preview rehearse [scénarios] [--check]` | joue sans TUI ; `--check` : enregistrements périmés ? |
| `preview probe [nom] [--json]` / `preview check` | mise au point des sondes / de la config |
| `preview builtin docker\|system\|http` | sondes intégrées |
| `preview skill install` | skill Claude Code pour outiller un projet |

## Comment ça tient

- **Un projet = `preview.toml` + un ou plusieurs decks** (`[[deck]]`, chacun avec son HTML).
  Un scénario peut limiter ses panneaux : `probes = ["relay", "tunnels"]`.
- **Sondes et scénarios.** Les scénarios sont des commandes ; les sondes sont des
  exécutables qui impriment un JSON typé (lignes, barres, ratios, badges, séries) : preview ne connaît
  ni Docker, ni Incus, ni Prometheus.
- **Lecture de la sortie** (regex à la syntaxe Python, lookbehind compris) : protocole `::step` / `::ok` / `::fail` / `::metric` / `::result`, sortie
  `ansible-playbook` reconnue d'office, et regex par projet pour une sortie existante.
- **Enregistrements** : chaque run live est enregistré (JSONL), **masqué à l'écriture** (tokens, mots de
  passe, clés). Le HTML embarque le dernier run VERT et le rejoue dans le navigateur.

Spécification : [`SPEC.md`](SPEC.md) · exemple : [`examples/demo.md`](examples/demo.md).

## Licence

MIT, voir [LICENSE](LICENSE).
