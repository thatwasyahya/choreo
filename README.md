# Choreo

[![CI](https://github.com/thatwasyahya/choreo/actions/workflows/ci.yml/badge.svg)](https://github.com/thatwasyahya/choreo/actions/workflows/ci.yml)

Le tableau de bord du foyer : tout ce qu'il y a à faire à la maison, qui s'en occupe et pour quand.
Aussi simple qu'un post-it sur le frigo, aussi fiable qu'un outil professionnel, et hébergé chez nous.

Pour les familles, les colocations, les couples et les personnes seules. **Gratuit pour les
utilisateurs, pour toujours** : ni publicité, ni offre payante, ni revente de données, ni traqueur.

> **Statut : le noyau est là, rien n'est encore installable.**
>
> Ce qui marche : créer un compte et un foyer, inviter, créer et cocher des tâches hors-ligne,
> la synchronisation entre appareils, et le plateau « À revoir » quand le serveur n'a pas pu
> faire ce qu'on lui demandait. Les sept cas du protocole passent en automatique, deux clients
> simulés contre un vrai PostgreSQL.
>
> Ce qui manque : les séries et la récurrence, les listes, aucun serveur déployé, et **aucune
> version publiée** — la CI produit un APK de débogage, pas une application signée. Détail dans
> [docs/PROGRESS.md](docs/PROGRESS.md).

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

Ensuite, tout passe par `make` :

| | |
|---|---|
| `make help` | la liste à jour |
| `make dev` | monte PostgreSQL et le serveur |
| `make test-db` | publie une base de test, **avant** les tests d'intégration |
| `make test` | les tests Go **et** Flutter |
| `make lint` | `go vet`, `golangci-lint`, `flutter analyze` |
| `make generate` | le code Drift et les traductions, qui ne sont pas versionnés |
| `make apk` | un APK de débogage |

Après un clone, `make generate` avant toute analyse : le code généré n'est pas dans le dépôt.

## Documentation

| Fichier | Contenu |
|---|---|
| [docs/CAHIER_DES_CHARGES.md](docs/CAHIER_DES_CHARGES.md) | La source de vérité du produit |
| [docs/PLAN.md](docs/PLAN.md) | Les phases, et la phase en cours en détail |
| [docs/DECISIONS.md](docs/DECISIONS.md) | Tous les arbitrages, avec leurs raisons |
| [docs/PROGRESS.md](docs/PROGRESS.md) | Fait / en cours / suivant |
| [docs/SYNC_PROTOCOL.md](docs/SYNC_PROTOCOL.md) | Le protocole de synchronisation, et ses **sept cas** — la section qui décrit ce que les gens ressentiront |
| [docs/AUTH.md](docs/AUTH.md) | Comptes, jetons, codes de secours |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | La forme du système |

## Licence

[AGPL-3.0](LICENSE). Ce choix n'est pas décoratif : il garantit que personne ne pourra exploiter une
version fermée de ce service en gardant le code pour soi. Ce qui est gratuit le reste.
