# Choreo

Le tableau de bord du foyer : tout ce qu'il y a à faire à la maison, qui s'en occupe et pour quand.
Aussi simple qu'un post-it sur le frigo, aussi fiable qu'un outil professionnel, et hébergé chez nous.

Pour les familles, les colocations, les couples et les personnes seules. **Gratuit pour les
utilisateurs, pour toujours** : ni publicité, ni offre payante, ni revente de données, ni traqueur.

> **Statut : Phase 0, fondations.** Rien n'est utilisable pour l'instant.
> Voir [docs/PROGRESS.md](docs/PROGRESS.md).

## Ce que c'est

Une application mobile **hors-ligne d'abord** : le téléphone détient une base locale complète qui se
synchronise, pas un client d'API qui met en cache. Supermarché, cave, métro, avion — tout fonctionne.

- **Application** : Flutter / Dart, Android d'abord
- **Serveur** : Go, PostgreSQL 17, auto-hébergé dans l'UE
- **Aucun service géré** : ni Firebase, ni Supabase, ni Auth0. Aucun traqueur tiers.

Le serveur n'est qu'un relais de synchronisation, pas le cerveau. Chaque téléphone est une réplique
complète : si le serveur disparaît, personne ne perd ses données. Et l'adresse du serveur est un
réglage — un foyer qui le souhaite héberge le sien.

## Démarrer

```sh
# Préparer la machine : Go, Flutter, SDK Android, variables d'environnement.
# Rejouable sans risque, n'a besoin d'aucun sudo.
./scripts/bootstrap-dev.sh

# Ouvrir un nouveau terminal, puis vérifier
flutter doctor
go version
```

Les cibles `make` arriveront avec le lot L0.6.

## Documentation

| Fichier | Contenu |
|---|---|
| [docs/CAHIER_DES_CHARGES.md](docs/CAHIER_DES_CHARGES.md) | La source de vérité du produit |
| [docs/PLAN.md](docs/PLAN.md) | Les phases, et la phase en cours en détail |
| [docs/DECISIONS.md](docs/DECISIONS.md) | Tous les arbitrages, avec leurs raisons |
| [docs/PROGRESS.md](docs/PROGRESS.md) | Fait / en cours / suivant |

## Licence

[AGPL-3.0](LICENSE). Ce choix n'est pas décoratif : il garantit que personne ne pourra exploiter une
version fermée de ce service en gardant le code pour soi. Ce qui est gratuit le reste.
