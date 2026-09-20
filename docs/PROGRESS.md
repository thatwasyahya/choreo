# Où en est le projet

Mis à jour le 20 septembre 2026. À lire en premier pour reprendre à froid.

**Phase en cours : 0 — Fondations.** Plan détaillé dans [PLAN.md](PLAN.md), arbitrages dans
[DECISIONS.md](DECISIONS.md).

---

## Fait

- Cadrage complet. 18 décisions arrêtées, aucune en attente (`D-001` à `D-018`).
- Dépôt git local initialisé sur `main`, identité épinglée en configuration locale (`D-016`).
- Constat : les §5 à §20 du cahier des charges n'existent pas. Documents de remplacement identifiés
  (`D-015`).
- Environnement de développement cartographié. Meilleure surprise que prévu : le SDK Flutter Linux
  et le SDK Android sont **déjà installés** dans WSL.
- `scripts/bootstrap-dev.sh` écrit : installe Go, câble `ANDROID_HOME`, et force le SDK Flutter
  Linux devant celui que le `PATH` Windows fait fuiter dans WSL.

## En cours — L0.1, environnement de développement

| Élément | État |
|---|---|
| Docker 28.1.1 (démon actif), JDK 21 | déjà là |
| SDK Android `~/Android/Sdk` | déjà là, `ANDROID_HOME` câblé par le script |
| SDK Flutter Linux `~/flutter` | déjà là et fonctionnel, mise à jour disponible |
| Go 1.27.1 | en cours d'installation par le script |
| Téléphone en débogage sans fil | **à faire par le product owner** |

## Suivant

1. Finir L0.1 : `flutter doctor` sans erreur bloquante, un téléphone visible dans `flutter devices`.
2. L0.2 : garde-fous du dépôt — `.gitignore`, `.editorconfig`, `LICENSE`, `pre-commit` gitleaks.
3. L0.3 et L0.5 en parallèle : squelettes Go et Flutter.
4. **L0.9, le lot qui compte** : `docs/SYNC_PROTOCOL.md`. À concevoir de zéro puisque le §10.3
   n'existe pas, et à faire valider avant la moindre ligne de Phase 1.

## Particularités de la machine, à ne pas redécouvrir

- **`ma-dev-box` n'a pas systemd** (PID 1 = `init`), et son `/etc/wsl.conf` lance un *runner* de CI
  au démarrage. Docker tourne quand même. Sans conséquence pour la Phase 0 : Compose n'a pas besoin
  de systemd.
- Le `PATH` de WSL hérite du `PATH` Windows, qui contient un SDK **Flutter pour Windows**
  (`/mnt/c/Users/.../Downloads/flutter_windows_3.29.2-stable`). Il ne peut pas s'exécuter sous Linux.
  Le script ne le supprime pas — il n'en a pas le droit — il place le nôtre devant.
- Le shell du product owner est **zsh**. Les variables d'environnement du projet vivent dans
  `~/.config/choreo/dev-env.sh`, sourcé depuis `.zshrc` et `.bashrc` par une ligne unique et gardée.
- `git` refuse les chemins `\\wsl.localhost` depuis Windows sans `safe.directory` ; l'exception est
  posée pour ce dépôt uniquement.
