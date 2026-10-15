# Où en est le projet

Mis à jour le 15 octobre 2026. À lire en premier pour reprendre à froid.

**Phase en cours : 0 — Fondations.** Plan : [PLAN.md](PLAN.md). Arbitrages :
[DECISIONS.md](DECISIONS.md). Forme du système : [ARCHITECTURE.md](ARCHITECTURE.md).

| Lot | État |
|---|---|
| L0.1 environnement | **fait** |
| L0.2 garde-fous du dépôt | **fait** |
| L0.3 squelette Go | **fait** |
| L0.4 PostgreSQL et migrations | **fait** |
| L0.5 squelette Flutter | **fait** |
| L0.6 Compose et Makefile | **fait** |
| L0.7 intégration continue | **écrite**, pas encore exercée — il manque le dépôt distant |
| L0.8 observabilité | **fait** |
| L0.9 protocole de synchronisation | **en cours de rédaction** |
| L0.10 mémoire du projet | **fait** |

---

## Ce qui est vérifié, pas seulement écrit

La pile complète a été montée et éprouvée de bout en bout :

```
/healthz  → 200  {"status":"ok","version":"c973d73-dirty"}   + en-tête X-Request-Id
/readyz   → 200  {"status":"ready"}
/metrics  → choreo_http_requests_total{method,route,status}   étiqueté par motif de route
base      → goose_db_version + schema_bootstrap présentes
```

Et l'invariant inverse, celui qui compte, **postgres arrêté** :

```
/healthz  → 200          la vivacité ne dépend de rien
/readyz   → 503          {"reason":"database unreachable"}
journaux  → l'erreur de pilote, qui nomme l'hôte et le compte, reste DANS les journaux
```

| Vérification | Résultat |
|---|---|
| `go test -race ./...` | vert |
| `golangci-lint run` | **0 issue** |
| `flutter analyze --fatal-infos --fatal-warnings` | **0 issue** |
| `flutter test` | **8 tests verts** |
| `make dev` sur machine vierge | monte postgres + serveur, sans intervention |
| `make prod-config` | composition de production valide |
| `make apk` | **`app-debug.apk` produit** (153 Mo : débogage, toutes ABI) |
| `.github/workflows/ci.yml` | se parse — 3 postes, mais **jamais exécutée** faute de dépôt distant |
| crochet `pre-commit` | refuse un commit portant un jeton |

## Outils installés

Go 1.27.1 · Flutter 3.38.9 · Dart 3.10.8 · Android SDK 35 · adb 1.0.41 ·
Docker 28.1.1 · JDK 21.0.11 · gitleaks 8.30.1 · golangci-lint 2.13.2

## Suivant

1. **Finir L0.9** : `SYNC_PROTOCOL.md`, puis **le faire valider**. La phase 1 ne démarre
   pas avant. C'est la validation la plus lourde du projet.
2. Pousser sur le dépôt distant pour que la CI tourne enfin (L0.7).
3. Installer l'APK sur un vrai téléphone.

## En attente du product owner

- **Dépôt distant.** Pas encore créé, donc rien n'est poussé et la CI n'a jamais tourné.
  Marche à suivre exacte dans [SETUP_COMPTES.md](SETUP_COMPTES.md).
- **Adresse de commit.** Les 12 commits portent l'adresse personnelle. Le dépôt sera
  public : la réécriture est gratuite tant que rien n'est publié, coûteuse après.
- **Téléphone Android.** `adb devices` est vide. Non bloquant : la cible `linux` de Flutter
  suffit pour les essais, et la CI produit un APK en artefact.
- **Validation de `SYNC_PROTOCOL.md`** quand il arrive.

## Particularités de la machine, à ne pas redécouvrir

Les pièges d'outillage sont dans [CLAUDE.md](../CLAUDE.md) — `--force-jit`, le plafond
`drift`, `bash -lc` qui ne lit pas `.bashrc`. À retenir ici :

- **`ma-dev-box` n'a pas systemd** et lance un *runner* de CI au démarrage : la charge
  monte à 15+ sur 6 cœurs. Les commandes longues partent en tâche de fond.
- Le `PATH` de WSL hérite du `PATH` Windows, qui contient un SDK **Flutter pour Windows**
  incapable de s'exécuter sous Linux. `bootstrap-dev.sh` ne le supprime pas — il n'en a
  pas le droit — il place le nôtre devant.
- **Les profils Compose ne protègent pas l'interpolation** : une variable `${VAR:?}` d'un
  service en profil inactif casse quand même `docker compose up`. D'où deux fichiers,
  `compose.yaml` et `compose.prod.yaml`, et non un profil.
- `core.hooksPath` est une configuration **locale** : elle ne voyage pas avec un `push`.
  Qui clone doit lancer `scripts/bootstrap-dev.sh`.
- La pile de développement tourne encore en tâche de fond. `make down` pour l'arrêter.
