# Où en est le projet

Mis à jour le 20 septembre 2026. À lire en premier pour reprendre à froid.

**Phase en cours : 0 — Fondations.** Plan détaillé dans [PLAN.md](PLAN.md), arbitrages dans
[DECISIONS.md](DECISIONS.md).

Lots faits : **L0.1**, **L0.2**. Reste : L0.3 à L0.10.

---

## Fait

### Cadrage
- 18 décisions arrêtées, aucune en attente (`D-001` à `D-018`).
- Constat : les §5 à §20 du cahier des charges n'existent pas. Documents de remplacement identifiés
  (`D-015`).

### L0.1 — Environnement de développement
- Dépôt git local sur `main`, identité épinglée en configuration locale (`D-016`).
- `scripts/bootstrap-dev.sh` : rejouable, sans aucun `sudo`, ne touche à aucun fichier système.
- Bonne surprise : le SDK **Flutter Linux** et le **SDK Android** étaient déjà installés.

| Outil | Version |
|---|---|
| Go | 1.27.1 (installé dans `~/.local/go`) |
| Flutter | 3.38.9 stable |
| Dart | 3.10.8 |
| Android SDK | 35.0.0, licences acceptées |
| adb | 1.0.41 |
| Docker | 28.1.1, démon actif |
| JDK | 21.0.11 |
| gitleaks | 8.30.1 |

`flutter doctor` ne signale qu'un point, **non bloquant** : Chrome absent, donc pas de cible web —
sans objet pour un projet mobile.

### L0.2 — Garde-fous du dépôt
- `.gitignore`, `.editorconfig`, `README.md`.
- `LICENSE` : AGPL-3.0, texte canonique de la FSF (`D-008`).
- **Crochet `pre-commit` gitleaks, éprouvé** : un commit portant un jeton a bien été refusé.

## En cours

Rien. Prochain lot à démarrer.

## Suivant

1. **L0.3** — squelette du serveur Go : `net/http` de la bibliothèque standard, `/healthz`,
   `/readyz`, `golangci-lint`.
2. **L0.5** — squelette Flutter : `very_good_analysis`, i18n dès le premier écran, Drift câblé.
3. **L0.4**, **L0.6**, **L0.7**, **L0.8** — Postgres, Compose, CI, observabilité.
4. **L0.9, le lot qui compte** : `docs/SYNC_PROTOCOL.md`, à concevoir de zéro et à faire valider
   avant la moindre ligne de Phase 1.

## En attente du product owner

- **Téléphone Android en débogage sans fil.** `adb devices` est vide, `flutter devices` ne voit que
  la cible Linux. Non bloquant jusqu'à L0.5 : la cible Linux suffit pour les premiers essais.
- **Adresse de commit.** Les commits portent l'adresse Gmail personnelle. Le dépôt sera public, et
  rien n'est encore poussé : la réécriture est gratuite maintenant, coûteuse après.

## Particularités de la machine, à ne pas redécouvrir

- **`ma-dev-box` n'a pas systemd** (PID 1 = `init`), et son `/etc/wsl.conf` lance un *runner* de CI
  au démarrage. Docker tourne quand même. Sans conséquence : Compose n'a pas besoin de systemd.
- Le `PATH` de WSL hérite du `PATH` Windows, qui contient un SDK **Flutter pour Windows**
  (`/mnt/c/Users/.../Downloads/flutter_windows_3.29.2-stable`), incapable de s'exécuter sous Linux.
  Le script ne le supprime pas — il n'en a pas le droit — il place le nôtre devant.
- Le shell du product owner est **zsh**. Les variables du projet vivent dans
  `~/.config/choreo/dev-env.sh`, sourcé par une ligne gardée depuis `.zshrc` et `.bashrc`.
- **`bash -lc` ne lit pas `.bashrc`** (sortie anticipée en non-interactif) : tout script automatisé
  doit sourcer `~/.config/choreo/dev-env.sh` explicitement.
- `core.hooksPath` est une configuration **locale** : elle ne voyage pas avec un `push`. Qui clone
  doit lancer `scripts/bootstrap-dev.sh`.
- `git` refuse les chemins `\\wsl.localhost` depuis Windows sans `safe.directory` ; l'exception est
  posée pour ce dépôt uniquement.
