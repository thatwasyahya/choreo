# Choreo — notes de travail

Source de vérité produit : `docs/CAHIER_DES_CHARGES.md`. Arbitrages et leurs raisons :
`docs/DECISIONS.md`. Où on en est : `docs/PROGRESS.md`. Phases : `docs/PLAN.md`.
**Ne recopie rien de ces fichiers ici.** Ce fichier ne contient que ce qui sert à agir.

## Environnement

- **Tout se fait dans WSL2 (`ma-dev-box`, Ubuntu 24.04)**, jamais via `\\wsl.localhost`
  depuis un outil Windows : Gradle et Flutter y sont d'une lenteur rédhibitoire (`D-011`).
- Préparer ou réparer la machine : `./scripts/bootstrap-dev.sh` — rejouable, sans `sudo`.
- **`bash -lc` ne lit pas `.bashrc`** (sortie anticipée en non-interactif). Tout script
  automatisé doit commencer par `. ~/.config/choreo/dev-env.sh`, sinon ni `go`, ni
  `flutter`, ni `golangci-lint` ne sont dans le `PATH`.
- Pas d'émulateur : 7,8 Go de RAM partagés avec Docker et Gradle. Téléphone physique en
  débogage sans fil, ou la cible `linux` de Flutter pour un essai rapide.
- `ma-dev-box` n'a pas systemd et fait tourner un *runner* de CI : la charge monte à 15+.
  Les commandes longues doivent partir en tâche de fond.

## Commandes

| | |
|---|---|
| `make help` | la liste à jour |
| `make dev` | monte postgres + serveur (crée `.env` au besoin) |
| `make test-db` | **avant les tests d'intégration** : publie PostgreSQL et crée `choreo_test` |
| `make test` | tests Go **et** Flutter |
| `make lint` | `go vet` + `golangci-lint` + `flutter analyze` |
| `make generate` | régénère le code Drift et les traductions |
| `make migrate` | applique les migrations et sort |
| `make db-reset` | **détruit** la base locale et la reconstruit |
| `make apk` | APK de débogage |
| `make build` | binaire du serveur dans `server/bin/` |

## Pièges qui ont déjà coûté du temps

- **`build_runner` exige `--force-jit`.** Il compile son propre script avec
  `dart compile`, qui refuse tout paquet portant un *build hook* — et `sqlite3`, sous
  drift, en porte un. Sans ce drapeau : « does not support build hooks ».
  À l'inverse, `--delete-conflicting-outputs` a été **supprimé** de build_runner : le
  passer ne fait qu'émettre un avertissement.
- **`drift` est plafonné sous 2.35** et `drift_dev` sous 2.34.6, appariés. Remonter l'un
  sans l'autre casse la résolution. Les raisons sont dans `app/pubspec.yaml`.
- **Le code généré n'est pas versionné** (`*.g.dart`, `lib/l10n/generated/`). Après un
  clone : `make generate` avant `flutter analyze`.
- Le partage `\\wsl.localhost` échoue parfois en `fsync` sous charge. Écrire côté Linux.
- **Les tests de `internal/store` s'ignorent silencieusement** sans
  `CHOREO_TEST_DATABASE_URL`. Un `ok ... 0.01s` sur ce paquet veut dire « rien n'a tourné ».
  `make test-db` d'abord, puis `make test-server` qui passe la variable.
- **`go test` sans `-p 1` est instable** dès que deux paquets touchent la base : il lance
  les *paquets* en parallèle, et chacun tronque `choreo_test` entre ses propres tests.
  L'échec est intermittent, donc trompeur. `make test-server` pose le drapeau ; n'appelle
  pas `go test ./...` à la main sans lui.
- **`compose.yaml` ne publie aucun port PostgreSQL** (refus par défaut). Les surcouches
  `compose.test.yaml` (port 5433) et `compose.prod.yaml` (Caddy) sont les exceptions
  nommées. Un profil ne suffirait pas : Compose interpole les variables de tous les
  services, profil inactif compris.

## Conventions

- **Commits** : Conventional Commits, atomiques, une branche par lot.
  Auteur `thatwasyahya` (épinglé en config locale). **Aucune mention d'un outil
  d'assistance**, ni dans les messages, ni en `Co-Authored-By`, ni dans les PR (`D-016`).
- **Langues** : le code, les commentaires et les messages de commit en **anglais** ;
  la documentation de `docs/` en **français**. Le dépôt est public (`D-008`).
- **Go** : `golangci-lint` doit rester à zéro. Erreurs enveloppées, contexte propagé,
  aucune variable globale mutable — les trois exceptions portent un `//nolint` motivé.
- **Dart** : `flutter analyze --fatal-infos --fatal-warnings` doit rester à zéro.
  **Aucune chaîne d'interface en dur** : tout passe par `lib/l10n/*.arb`, dès le
  premier écran. Un test vérifie qu'aucun libellé français ne fuit en locale anglaise.
- **Secrets** : jamais dans le dépôt. Le crochet `pre-commit` (gitleaks) refuse le
  commit. Pas de `--no-verify` ; un faux positif se documente dans `.gitleaksignore`.
  **Aucune adresse IP privée ni nom d'hôte interne** dans un fichier versionné.

## Invariants d'architecture

- **Le téléphone détient une réplique complète de son foyer.** L'app *est* une base
  locale qui se synchronise, pas un client d'API qui met en cache. Tout ce qui est à
  l'écran est lu dans Drift, jamais attendu du réseau.
- **Le serveur est un relais de synchronisation et l'arbitre de cinq invariants** — la
  séquence du foyer, la provenance des champs, l'unicité de l'occurrence ouverte d'une
  série, les rôles et l'appartenance, la borne sur `occurred_on`. Cinq, et pas plus.
- **Aucun moteur de fusion sur le téléphone.** Le serveur renvoie des états de lignes
  déjà fusionnés ; le client remplace la ligne si la révision entrante est supérieure.
  La fusion n'existe qu'une fois, en Go. Voir `docs/SYNC_PROTOCOL.md`.
- **Les rappels datés sont calculés et déclenchés sur l'appareil** — zéro service de
  push, et ça marche en avion (`D-010`). Firebase est exclu.
- **L'adresse du serveur est un paramètre de compilation** (`CHOREO_SERVER_URL`), jamais
  une constante : un foyer doit pouvoir héberger le sien.
- **Le foyer est le tenant.** L'isolation entre foyers est une frontière de sécurité.
- **Migrations en avant uniquement**, et le serveur sert toujours les deux dernières
  versions de l'app.
- `/healthz` ne dépend de rien ; `/readyz` dépend de la base. Ne jamais les confondre :
  un orchestrateur redémarrerait un serveur qui n'avait qu'à attendre.

## Arborescence

```
server/   Go — cmd/choreod, internal/{config,httpapi,obs,store}
app/      Flutter — lib/{core,features,l10n}
deploy/   compose.yaml, Dockerfile, Caddyfile
scripts/  bootstrap-dev.sh
docs/     cahier des charges, plan, décisions, ADR
```
