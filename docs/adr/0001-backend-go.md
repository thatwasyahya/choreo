# 0001 — Le back-end est écrit en Go

- **Statut** : accepté
- **Date** : 20 septembre 2026
- **Décision** : `D-002`

## Contexte

Le cahier des charges propose Go, avec Rust (Axum + SQLx) en alternative explicite. Le
serveur tournera sur un VPS à 2 vCPU et 4 Go, et son travail réel est modeste : il
authentifie, empile des mutations dans un journal par foyer, sert les changements depuis un
curseur. Quelques kilo-octets par foyer et par jour.

L'opérateur est une seule personne, qui écrit déjà du Go au quotidien.

## Décision

**Go**, avec `net/http` de la bibliothèque standard et PostgreSQL via `pgx`.

## Conséquences

- Un binaire statique, sans dépendance système : l'image de production est une
  `distroless/static`, sans shell ni gestionnaire de paquets.
- Le profil mémoire est prévisible, ce qui compte sur une machine partagée avec PostgreSQL.
- La tenue de l'exigence « erreurs enveloppées, contexte propagé, aucune variable globale
  mutable » est mécanisée par `golangci-lint`, qui doit rester à zéro.
- Le ramasse-miettes existe. À ce volume, c'est sans objet.

## Alternative écartée : Rust

Rust aurait apporté une empreinte mémoire plus basse et l'absence de ramasse-miettes.
Aucun des deux n'est une contrainte ici : le serveur est un relais, pas un moteur de
calcul. En échange, il aurait coûté un temps d'apprentissage et de compilation que le
projet n'a pas à dépenser, sur un périmètre où le risque réel est ailleurs — dans le
protocole de synchronisation.

Le facteur décisif n'est pas technique : **la langue que l'unique mainteneur lit le mieux
à deux heures du matin**.
