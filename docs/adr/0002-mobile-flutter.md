# 0002 — L'application mobile est écrite en Flutter

- **Statut** : accepté, et non rediscutable en cours de route
- **Date** : 20 septembre 2026
- **Décision** : `D-003`, liée à `D-004`

## Contexte

Le cahier des charges propose Flutter + Dart, avec Kotlin Multiplatform + Compose
Multiplatform en alternative, et précise que le choix est **tranché au cadrage, jamais en
cours de route** — changer de socle d'interface à mi-parcours reviendrait à réécrire
l'application.

Android est la cible des phases 1 à 4. iOS est reporté jusqu'à ce que le projet dégage un
revenu (`D-004`), mais **reporté n'est pas annulé** : il n'existe aucune voie gratuite de
distribution iOS, c'est une dépense de 99 $/an, pas un obstacle technique.

## Décision

**Flutter + Dart**, avec Drift (SQLite) comme base locale.

## Conséquences

- Le jour où iOS arrive, l'interface n'est pas à réécrire. C'est l'argument central.
- Une seule base de code à tester, à traduire et à faire vieillir.
- Drift donne des requêtes typées et des migrations de schéma côté téléphone — ce qui
  compte, puisque **le téléphone est la base de données** et non un cache.
- Coût assumé : l'accès aux API Android passe par des greffons, et le rendu n'est pas
  natif. Sur un tableau de tâches, aucun des deux ne se voit.
- Le plafond de version de `drift` et `drift_dev` doit bouger d'un seul geste : les
  raisons sont écrites dans `app/pubspec.yaml`.

## Alternative écartée : Kotlin Multiplatform + Compose

Si iOS avait été écarté **définitivement**, Compose natif aurait été le meilleur choix :
moins de couches, meilleur accès à `WorkManager`, aux notifications et aux widgets — qui
sont précisément les briques dont dépendent les rappels locaux (`D-010`).

Ce n'est pas le cas. Le report d'iOS est économique et temporaire, donc le socle commun
garde sa valeur.
