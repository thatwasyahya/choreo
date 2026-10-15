# Architecture

Ce document décrit la forme du système et les raisons de cette forme. Les décisions
elles-mêmes, avec leur date et leur statut, sont dans [DECISIONS.md](DECISIONS.md).

---

## L'idée à retenir

> **Le téléphone est la base de données. Le serveur est un relais.**

Ce n'est pas une formule : c'est la contrainte dont découle tout le reste. L'application
n'est pas un client d'API qui met en cache, c'est une base locale qui se synchronise
(principe 9 du cahier des charges). Trois conséquences directes :

1. **Le réseau ne conditionne aucun écran.** Tout ce qui s'affiche est lu dans la base
   SQLite du téléphone. Supermarché, cave, métro, avion.
2. **Perdre le serveur n'est pas perdre les données.** Chaque téléphone du foyer en
   détient une réplique complète. C'est ce qui rend la promesse « gratuit pour toujours »
   tenable sans dépendre de notre capacité à payer un VPS (`D-005`).
3. **Les rappels n'ont besoin de personne.** Les échéances sont déjà sur l'appareil, donc
   les notifications datées sont calculées et déclenchées localement. Aucun service de
   push, aucun Firebase (`D-010`).

---

## Les trois morceaux

```
   ┌─────────────────────────────┐
   │  app/   Flutter + Dart      │
   │                             │
   │   écrans ──lisent──► Drift  │   la base locale est la source de vérité
   │                      (SQLite)│   de ce qui s'affiche
   │                        │     │
   │                   boîte d'envoi
   └────────────────────────┼─────┘
                            │  HTTPS, quand il y a du réseau
   ┌────────────────────────▼─────┐
   │  deploy/  Caddy (TLS ACME)   │   seule porte ouverte en production
   └────────────────────────┼─────┘
   ┌────────────────────────▼─────┐
   │  server/  Go, monolithe      │   authentifie, journalise les mutations,
   │                              │   sert les changements depuis un curseur
   │           PostgreSQL 17      │   aucun port publié
   └──────────────────────────────┘
```

Un monolithe modulaire, pas des services. Sur une machine à 4 Go, six conteneurs qui se
parlent coûtent plus de RAM et de pannes qu'ils n'apportent (règle d'exploitation 4).

### `server/` — Go

| Paquet | Rôle |
|---|---|
| `cmd/choreod` | montage et arrêt propre. Rien d'autre |
| `internal/config` | lecture de l'environnement, et **rien que** l'environnement |
| `internal/httpapi` | la surface HTTP. `net/http` de la bibliothèque standard (`D-012`) |
| `internal/obs` | journaux structurés, corrélation, métriques |
| `internal/store` | PostgreSQL et les migrations, embarquées dans le binaire |

Les interfaces sont déclarées **là où elles sont consommées**, pas là où elles sont
implémentées : `httpapi.Pinger` décrit la tranche de `store` dont l'API a besoin, ce qui
évite que la couche HTTP dépende du magasin concret.

### `app/` — Flutter

| Dossier | Rôle |
|---|---|
| `lib/core` | base locale (Drift), client HTTP, configuration de compilation |
| `lib/features/<domaine>` | un dossier par domaine fonctionnel |
| `lib/l10n` | les chaînes, en `fr` et `en`, dès le premier écran |

**Aucune chaîne d'interface en dur, jamais.** Un test vérifie qu'un libellé français ne
fuit pas en locale anglaise — c'est le seul garde-fou qui résiste à la fatigue.

---

## Ce qui est délibérément absent

- **Aucun service géré.** Ni Firebase, ni Supabase, ni Auth0. Pas par idéologie : chacun
  serait un tiers capable de fermer le service, de changer ses prix, ou d'observer les
  tâches ménagères de familles qui ne lui ont rien demandé.
- **Aucun traqueur.** Les indicateurs du §2.4 sont calculés en base, agrégés.
- **Aucune adresse e-mail obligatoire.** L'inscription est un identifiant et un mot de
  passe ; rejoindre un foyer se fait par code d'invitation (`D-009`). Il n'y a donc pas de
  relais SMTP à auto-héberger, et pas de connexion impossible le jour où le courrier
  tombe.
- **Aucun service de push en v1.** Voir plus haut : les rappels sont locaux.

---

## Le serveur est remplaçable, et ça se vérifie

Trois propriétés, à tenir à chaque phase :

1. **`CHOREO_SERVER_URL` est un paramètre de compilation**, jamais une constante. Un foyer
   technicien pointe l'app vers son instance.
2. **`docker compose up` suffit** à héberger la sienne. Le dépôt est public sous AGPL-3.0
   précisément pour que ce soit un droit et pas une tolérance (`D-008`).
3. **L'export et la suppression sont en libre-service** (phase 4).

---

## Les frontières à ne pas franchir

- **Le foyer est le tenant.** L'isolation entre foyers est une frontière de sécurité, pas
  une commodité d'affichage. Elle se teste, comme le reste.
- **`/healthz` ne dépend de rien. `/readyz` dépend de la base.** Confondre les deux fait
  redémarrer par un orchestrateur un serveur qui n'avait qu'à attendre que PostgreSQL
  démarre.
- **Un message d'erreur de pilote ne sort jamais vers le client.** Une sonde est non
  authentifiée, et une erreur de pilote nomme des hôtes, des ports, parfois des comptes.
- **Migrations en avant uniquement** (`D-013`), et le serveur sert toujours **les deux
  dernières versions de l'app**. Un téléphone met des semaines à se mettre à jour.
- **Aucune adresse IP privée ni nom d'hôte interne dans un fichier versionné.**

---

## Ce qui n'est pas encore décidé

Le **protocole de synchronisation** — journal de changements, curseur, résolution des
conflits, pierres tombales — fait l'objet de [SYNC_PROTOCOL.md](SYNC_PROTOCOL.md). Le §10.3
du cahier des charges n'a jamais existé (`D-015`), ce document le remplace. **Il doit être
validé par le product owner avant la première ligne de phase 1** : c'est la pièce dont
dépendent le schéma de données, l'API et le comportement hors-ligne, c'est-à-dire le
produit.
