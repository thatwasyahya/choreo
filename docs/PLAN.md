# Plan — Choreo

Le §18 du cahier des charges n'ayant jamais été transmis (`D-015`), le découpage en phases ci-dessous
est rédigé par l'équipe et remplace ce §18 une fois validé par le product owner.

---

## Découpage en phases

| Phase | Objet | Sortie observable | Coût engagé |
|---|---|---|---|
| **0** | Fondations. Aucune règle métier | `make dev` monte tout, un écran Flutter affiche une donnée venue du Go, CI verte | domaine seul |
| **1** | **Le noyau** : foyer, membres, tâches, base locale, boîte d'envoi, synchronisation | Tâche créée en mode avion sur un téléphone, retrouvée sur un autre après reconnexion | 0 € |
| **2** | Vie quotidienne : séries et occurrences, catégories, étiquettes, rappels locaux, listes à cocher. **Première mise en ligne** | L'app tourne d'abord sur le minipc, puis sur le VPS OVH ; APK distribué à ≤ 20 appareils ; sauvegarde restaurée pour de vrai | VPS ~5 €/mois |
| **3** | Le foyer vivant : rôles, rotation, mode enfant, points, mémos, invités, multi-foyers | Une coloc et une famille s'en servent sans explication | — |
| **4** | Ouverture : Google Play, export et suppression RGPD en libre-service, page « pourquoi c'est gratuit », push si démontré | Installable par quelqu'un qu'on ne connaît pas | 25 $ une fois |
| **5** | iOS | Conditionnée à un revenu (`D-004`) | 99 $/an |

**La Phase 1 est celle qui fait ou casse le produit.** Tout le reste est du travail ordinaire ; la
synchronisation hors-ligne, non. C'est pourquoi sa conception (`L0.9`) est tirée en Phase 0.

---

## Phase 0 — Fondations

**Objectif** : que la machine, le dépôt, la chaîne de construction et la conception de la
synchronisation soient prêts, pour que la Phase 1 ne fasse que du métier.

**Hors périmètre, explicitement** : toute règle métier, tout écran réel, tout schéma de données de
domaine, tout déploiement sur un serveur distant. Une seule table triviale, un seul écran de
vérification.

### Lots

#### L0.1 — Remettre l'environnement de développement d'aplomb
*Premier, parce que rien ne fonctionne avant.*

État constaté le 20/09/2026 dans `ma-dev-box` (Ubuntu 24.04, 6 cœurs, 7,8 Go, 796 Go libres) :

| Élément | État | Action |
|---|---|---|
| Docker 28.1.1 | présent | rien |
| JDK 21 | présent | rien |
| SDK Android (`~/Android/Sdk`) | présent, complet | définir `ANDROID_HOME`, ajouter `platform-tools` au `PATH` |
| Go | **absent** | installer la dernière version stable |
| Flutter | **cassé** — le `PATH` pointe vers `/mnt/c/.../flutter_windows_3.29.2-stable`, un SDK **Windows** référencé depuis Linux | retirer cette entrée, installer le SDK Flutter **Linux** nativement |
| `adb` | hors `PATH` | ajouter `~/Android/Sdk/platform-tools` |

- Tout se fait **dans WSL2**, jamais via `\\wsl.localhost` depuis un outil Windows (`D-011`).
- **Pas d'émulateur** : 7,8 Go de RAM partagés avec Docker et Gradle, c'est trop juste. Téléphone
  physique en **débogage sans fil** (`adb connect`), ce qui évite aussi `usbipd-win`. C'est de toute
  façon le bon banc d'essai pour du hors-ligne d'abord.
- Le tout dans un script `scripts/bootstrap-dev.sh` rejouable, pas dans un historique de shell.

*Critère de sortie* : `flutter doctor` sans erreur bloquante, `go version` répond, un téléphone
apparaît dans `flutter devices`.

#### L0.2 — Dépôt et garde-fous

- Arborescence : `server/` (Go), `app/` (Flutter), `deploy/` (Compose, Ansible), `docs/`, `scripts/`.
- `.gitignore`, `.editorconfig`, `README.md`, `.env.example`.
- **`pre-commit` avec gitleaks**, testé avec un faux secret qui doit être refusé.
- Conventional Commits. Une branche par lot. Identité déjà épinglée (`D-016`).
- **`LICENSE` = AGPL-3.0**, en place **avant la première poussée** — le dépôt est public (`D-008`).
- Adresse de commit basculée sur `users.noreply.github.com` avant la première poussée.

*Critère de sortie* : un commit contenant une fausse clé est bloqué par le crochet.

#### L0.3 — Serveur Go, squelette
- Go, `net/http` de la bibliothèque standard (`D-012`), pas de cadriciel.
- `cmd/choreod/`, `internal/{config,httpapi,store,domain,obs}`.
- Configuration par variables d'environnement uniquement, aucune valeur en dur.
- `/healthz` (vivant) et `/readyz` (base joignable).
- `golangci-lint` en échec bloquant, erreurs enveloppées, contexte propagé.

*Critère de sortie* : `make test` et `make lint` verts, `/healthz` répond.

#### L0.4 — PostgreSQL et migrations
- PostgreSQL 17 en conteneur.
- **goose**, migrations SQL embarquées dans le binaire, en avant uniquement (`D-013`).
- Une seule migration en Phase 0, triviale, pour éprouver la mécanique.
- `make db-reset` reconstruit de zéro.

*Critère de sortie* : migration rejouable sur base vierge, `/readyz` vire au rouge quand la base est
arrêtée.

#### L0.5 — Flutter, squelette
- `very_good_analysis`, `flutter analyze` **sans le moindre avertissement**.
- **Internationalisation dès le premier écran** (ARB, `fr` et `en`). Aucune chaîne en dur, jamais —
  y compris sur l'écran de vérification.
- **Drift** câblé dès maintenant (`D-014`), avec une table triviale : l'app est une base locale, pas
  un client d'API.
- Un écran unique qui lit la base locale et affiche le résultat de `/healthz`.
- `APP_NAME` et l'URL du serveur sont des paramètres de construction, pas des constantes (§8.6).

*Critère de sortie* : APK de débogage installé sur le téléphone, écran affiché en français et en
anglais selon la langue du système.

#### L0.6 — Docker Compose et Makefile
- `compose.yaml` : postgres, serveur, Caddy.
- `Makefile` : `dev`, `test`, `lint`, `migrate`, `db-reset`, `apk`, `deploy`.
- Le serveur est joignable depuis le téléphone sur le réseau local.
- `deploy` est écrit pour une cible distante quelconque, mais **n'est pas exercé en Phase 0** : le
  minipc reste réservé jusqu'à ce que l'application soit fonctionnelle (`D-017`).

*Critère de sortie* : `make dev` sur une machine vierge monte la pile complète sans intervention.

#### L0.7 — Intégration continue (GitHub Actions)
- Postes : lint et tests Go · `flutter analyze` et tests · construction d'un APK de débogage en
  artefact · gitleaks.
- Dépôt public (`D-008`) : minutes de CI sans plafond, pas d'arbitrage à faire sur le quota.

*Critère de sortie* : CI verte sur la première pull request.

#### L0.8 — Observabilité
*Dès maintenant, pas après le premier incident (§1.1 n°5).*
- `log/slog` en JSON, identifiant de requête propagé.
- `/metrics` au format Prometheus.
- Pas de Grafana en Phase 0 : la RAM ne le justifie pas encore (§1.1 n°4).

*Critère de sortie* : une requête produit une ligne de journal corrélée et incrémente un compteur.

#### L0.9 — `docs/SYNC_PROTOCOL.md` — conception, aucun code
*Le lot le plus important de la phase.*

Le §10.3 n'existe pas (`D-015`), et c'est le cœur du produit. À trancher et écrire noir sur blanc :

- Format du **journal de changements** par foyer, et du **curseur** client.
- **Résolution des conflits** : quelle granularité, quelle règle de convergence, qui gagne et pourquoi.
- **Boîte d'envoi** : idempotence, rejeu, ordre, que faire d'une mutation refusée par le serveur
  après trois jours hors-ligne.
- **Pierres tombales** : durée de conservation, et ce qui arrive au téléphone qui revient après.
- **Compatibilité N-2** (§15.4) : comment le serveur sert deux versions d'app à la fois.
- Horloges : le téléphone d'un enfant est à l'heure qu'il veut.

*Critère de sortie* : document relu et **validé explicitement par le product owner**. La Phase 1 ne
commence pas avant.

#### L0.10 — Mémoire du projet
`CLAUDE.md` (150 lignes maximum, commandes et invariants, aucune copie du cahier),
`docs/PROGRESS.md`, `docs/ARCHITECTURE.md`, `docs/SETUP_COMPTES.md`, et les ADR des décisions
structurantes : `0001-go`, `0002-flutter`, `0003-hebergement-differe`, `0004-auth-sans-email`,
`0005-notifications-locales`.

*Critère de sortie* : quelqu'un qui clone le dépôt lance `make dev` en suivant le README, sans poser
de question.

### Ordre

```
L0.1 ──► L0.2 ──┬──► L0.3 ──► L0.4 ──┐
                │                     ├──► L0.6 ──► L0.7 ──► L0.10
                └──► L0.5 ────────────┘
                                 L0.8 (avec L0.3)
L0.9 ─── en parallèle de tout, du début à la fin de la phase
```

### Risques

| Risque | Gravité | Parade |
|---|---|---|
| **Entamer la Phase 1 sans protocole de synchronisation arrêté** | Élevée — c'est la façon classique de perdre trois mois | L0.9 est un critère de sortie bloquant |
| Environnement WSL bancal, Flutter pointant vers un SDK Windows | Élevée, mais connue | L0.1 en premier, script rejouable |
| 7,8 Go de RAM partagés entre Docker, Gradle et l'analyseur Dart | Moyenne | Pas d'émulateur, téléphone physique |
| `D-008` non tranché, blocage sur la CI | Moyenne | Ne bloque que L0.7 ; tout le reste avance |
| Élargissement du périmètre : « tant qu'on y est, ajoutons les tâches » | Moyenne | Le hors-périmètre est écrit plus haut |
| Transcription du cahier des charges par l'équipe, non relue | Faible | À écraser si l'original réapparaît |

### Critères de sortie de la Phase 0

- [ ] `make dev` monte la pile complète sur une machine vierge
- [ ] `make test` et `make lint` verts sur le serveur comme sur l'app
- [ ] APK de débogage installé sur un téléphone physique, écran de vérification affiché
- [ ] L'écran lit la base locale **et** l'API — les deux chemins fonctionnent
- [ ] Interface affichée en `fr` et en `en`, aucune chaîne en dur
- [ ] CI verte sur une pull request
- [ ] gitleaks bloque un faux secret
- [ ] `/healthz`, `/readyz`, `/metrics` répondent ; les journaux sont structurés et corrélés
- [ ] **`docs/SYNC_PROTOCOL.md` validé par le product owner**
- [ ] `CLAUDE.md`, `PROGRESS.md`, `ARCHITECTURE.md`, `SETUP_COMPTES.md` et les 5 ADR écrits

---

## Actions attendues du product owner

1. **Basculer l'adresse de commit** sur `users.noreply.github.com` avant la première poussée —
   le dépôt est public (`D-008`).
2. **Activer le débogage sans fil** sur un téléphone Android, et le laisser accessible sur le réseau
   local pendant les séances.
3. **Relire et valider `docs/SYNC_PROTOCOL.md`** quand il arrivera. C'est la validation la plus
   importante de tout le projet — elle mérite une vraie lecture, pas un GO de politesse.

*Fait* : accès au minipc débloqué le 20/09/2026, machine inspectée et documentée (`D-017`).

*Reporté par décision du product owner* : l'achat de `choreo.eu`, jusqu'au premier APK fonctionnel
(`D-018`). Sans incidence sur les Phases 0 et 1.
