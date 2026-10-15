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

## Phase 1 — Le noyau

**C'est la phase qui fait ou casse le produit.** Tout le reste du projet est du travail
ordinaire ; la synchronisation hors-ligne, non.

**Objectif** : une tâche créée en mode avion sur un téléphone se retrouve sur un autre après
reconnexion, et deux modifications concurrentes convergent selon les règles écrites.

La conception est faite : [SYNC_PROTOCOL.md](SYNC_PROTOCOL.md) fixe le modèle de données, les
points de terminaison et les règles de résolution, et `D-020` tranche ses neuf questions.
Cette phase est un exercice d'**exécution**, pas de conception — sauf sur un point, voir L1.2.

**Hors périmètre, explicitement** : récurrences et séries au-delà de l'invariant, catégories,
étiquettes, rappels, listes de courses, rôles, rotation, mode enfant, points, mémos, invités,
multi-foyers à l'écran. Tout cela a son schéma prêt et attend les phases 2 et 3.

### Lots

#### L1.1 — Le schéma serveur
Les 13 tables de `SYNC_PROTOCOL.md` en migrations goose : `accounts`, `households`, `members`,
`devices`, `device_cursors`, `tasks`, `series`, `completions`, `lists`, `list_items`, `tags`,
`memos`, `notices`, `ops`. L'enveloppe de synchronisation (`rev`, `created_rev`, `field_revs`,
`deleted_rev`, `updated_by`) est identique sur chaque table synchronisée — une seule forme, pas
treize variantes.

Les tables des phases ultérieures sont créées **maintenant**, vides : `household_id` partout et
la frontière d'isolation posée dès le départ. L'ajouter après la mise en service voudrait dire
reconstruire une frontière de sécurité en production.

*Critère de sortie* : migration rejouable sur base vierge ; un test prouve qu'aucune requête du
code ne peut lire une ligne d'un autre foyer.

#### L1.2 — L'authentification : spécifier, puis implémenter
**Le seul endroit où il reste à concevoir.** `D-009` tranche le mécanisme — identifiant et mot
de passe argon2id, codes d'invitation, code de secours, aucun e-mail — mais **aucun point de
terminaison n'est spécifié**, ni dans le cahier des charges ni dans le protocole, qui se
contente d'exiger un `Authorization: Bearer`.

À écrire avant de coder : création de compte, session et rafraîchissement, création d'un foyer,
émission et acceptation d'un code d'invitation, enregistrement d'un appareil, parcours de
récupération. Plus les paramètres argon2id, les durées de vie des jetons, et ce qui arrive à un
appareil révoqué.

*Critère de sortie* : les endpoints documentés puis implémentés, un test par parcours, et
**aucun secret dans un journal** — ni mot de passe, ni jeton, ni code de secours.

#### L1.3 — `POST /sync` côté serveur
**Le lot central.** Verrou de ligne sur le foyer, `change_seq` incrémenté dans la transaction
qui écrit, **une transaction par opération** et non par lot, le vocabulaire d'opérations
(`create`, `set_fields`, `delete`, `restore`, `complete`, `uncomplete`, et les transitions de
série), la table des règles de résolution appliquée telle qu'elle est écrite, et un résultat
par opération dans l'ordre de la requête.

Le vocabulaire d'opérations est un **engagement de protocole** : en ajouter un plus tard est un
incrément de version. Le fixer ici, pas en chemin.

*Critère de sortie* : un test par ligne de la table des règles de résolution. Pas « des tests
sur la fusion » — un par règle, nommé d'après elle.

#### L1.4 — Les notices et les mutations refusées
Une opération refusée ne disparaît jamais en silence : elle devient une ligne `notices` côté
serveur, donc elle suit l'utilisateur sur ses autres appareils et survit à une réinstallation.
Et le client ne réécrit **jamais** une `base_rev` périmée vers la valeur du serveur.

*Critère de sortie* : une opération refusée après trois jours hors-ligne produit une notice
lisible, et aucune donnée saisie n'est perdue.

#### L1.5 — `GET /snapshot` et le chemin `410`
Reconstruction complète de la réplique quand un curseur est tombé sous
`purge_horizon_seq`. **La remise à zéro ne touche pas à la boîte d'envoi** : « ton curseur a
expiré » ne peut pas vouloir dire « tes quatorze modifications hors-ligne sont perdues ».

*Critère de sortie* : un client dont le curseur a expiré se rebâtit **et** conserve sa boîte
d'envoi.

#### L1.6 — Le long poll
`wait_ms`, un `LISTEN choreo_changes` dédié pour tout le processus, `NOTIFY` dans la
transaction qui valide. **Le gestionnaire relâche sa connexion `pgx` avant de se garer** : un
appel en attente est une goroutine, jamais une connexion `idle in transaction`. C'est ce qui
tient le plafond de coût de `D-005`.

*Critère de sortie* : cent appels garés simultanément ne consomment aucune connexion du pool.

#### L1.7 — Le schéma client Drift
Les miroirs du domaine, plus `outbox`, `sync_cursors`, `device_identity`, `notices`,
`quarantine`. **Aucun moteur de fusion** : appliquer un changement, c'est remplacer la ligne si
la révision entrante est supérieure.

*Critère de sortie* : appliquer deux fois la même page de changements donne le même état, et
l'ordre d'arrivée n'a aucune incidence.

#### L1.8 — La boucle de synchronisation côté client
Vidange de la boîte d'envoi, avance du curseur **uniquement sur ce qui est engagé dans
SQLite**, adoption des états autoritatifs, vidage de `pending_fields`.

*Critère de sortie* : un plantage au milieu d'une page reprend où il s'était arrêté, sans page
partielle à l'écran.

#### L1.9 — Les écrans
Créer ou rejoindre un foyer, la liste des membres, la liste des tâches, et créer, cocher,
renommer, supprimer une tâche. Plus le plateau « À revoir ».

Le principe 1 est un critère, pas un souhait : **dix secondes pour créer une tâche, seul le
titre obligatoire.** Et tout est lu dans Drift, jamais attendu du réseau.

*Critère de sortie* : créer une tâche en moins de dix secondes, à une main, en mode avion.

#### L1.10 — La recette des sept cas
Un test automatisé par cas du protocole, pilotant **deux clients simulés** contre un vrai
PostgreSQL. Puis le même essai à la main sur deux appareils.

*Critère de sortie* : les sept cas passent sans intervention.

### Ordre

```
L1.1 ──► L1.2 ──► L1.3 ──┬──► L1.4 ──┐
                         ├──► L1.5    ├──► L1.9 ──► L1.10
                         └──► L1.6    │
L1.7 ──► L1.8 ───────────────────────┘
   (en parallèle de L1.3 dès que le schéma serveur est posé)
```

### Risques

| Risque | Gravité | Parade |
|---|---|---|
| **Toute la complexité se concentre dans L1.3.** Un handler de synchronisation écrit « au feeling » dérive de la table des règles en une semaine | Élevée | Un test par règle, nommé d'après la règle. La table est la spécification, pas une illustration |
| Le vocabulaire d'opérations grandit en chemin | Élevée | Il est figé en L1.3. En ajouter un est un incrément de version de protocole, pas une retouche |
| **Pas de téléphone** : le critère de sortie réel demande deux appareils | Moyenne | L1.10 pilote deux clients simulés contre un vrai PostgreSQL ; l'essai à la main confirme, il ne découvre pas |
| La frontière d'isolation ajoutée après coup | Moyenne | Posée en L1.1, testée en L1.1 |
| Dérive de périmètre vers les séries, les listes, les rôles | Moyenne | Le hors-périmètre est écrit plus haut ; leur schéma existe et reste vide |
| La CI n'a toujours jamais tourné | Faible | Dépend du dépôt distant, côté product owner |

### Critères de sortie de la Phase 1

- [ ] **L'essai décisif** : tâche créée en mode avion sur un client, retrouvée sur un autre
      après reconnexion
- [ ] Les **sept cas** du protocole passent en automatique
- [ ] Un test par ligne de la table des règles de résolution
- [ ] L'invariant « une seule occurrence ouverte par série » tient quand deux clients
      hors-ligne ouvrent la suivante
- [ ] Isolation entre foyers prouvée par un test
- [ ] Compatibilité N-2 : un client simulé N-1 applique une ligne contenant un champ qu'il ne
      connaît pas
- [ ] Un curseur expiré se rebâtit sans perdre la boîte d'envoi
- [ ] Cent long polls garés ne consomment aucune connexion du pool
- [ ] Aucun secret dans les journaux
- [ ] `make test`, `make lint` verts ; analyse Dart et `golangci-lint` à zéro

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
