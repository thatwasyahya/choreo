# 0003 — Le serveur est auto-hébergé, et la dépense est différée

- **Statut** : accepté
- **Date** : 20 septembre 2026
- **Décision** : `D-005`, liée à `D-017` et `D-018`

## Contexte

« Gratuit pour les utilisateurs » ne veut plus dire « sans coût » : une application mobile
auto-hébergée déplace la charge sur l'opérateur. La question posée était binaire — assumer
un coût d'environ 60 à 100 € par an, ou tenir le « zéro euro absolu » et revenir à la v1
en PWA.

Trois portes menaient au zéro euro. Toutes se ferment :

| Porte | Pourquoi elle se ferme |
|---|---|
| Offres gratuites d'un hébergeur | Interdites par le §0 du cahier, et structurellement fragiles : Heroku a tué la sienne en 2022, Railway en 2023, et un compte jugé inactif est récupéré sans recours. On bâtirait les données de familles sur un compte qu'un tiers peut supprimer |
| Pair-à-pair, sans serveur | Tombe sur le besoin n°1 — voir plus bas |
| Auto-hébergement à domicile | Ni gratuit (matériel, électricité), ni tenable : le réseau domestique devient le service, pour des données de tiers |

**Le besoin n°1** : deux téléphones d'un même foyer ne sont presque jamais sur le même
Wi-Fi au même moment. Camille assigne une tâche depuis le bureau ; la coloc des Lilas, ce
sont quatre personnes avec du turn-over. Les CRDT règlent la **fusion** des modifications,
pas leur **acheminement**. Il faut un point de rendez-vous. Un point de rendez-vous, c'est
un serveur.

## Décision

**Un VPS OVH — mais pas tout de suite.**

| Quand | Dépense |
|---|---|
| Phases 0 et 1 | **0 €** : la même pile `docker compose` tourne sur la machine de développement |
| Phase 2, premier foyer extérieur | VPS OVH, ~5 €/mois |
| Domaine | reporté jusqu'au premier APK fonctionnel (`D-018`) |

Le premier euro part quand part le premier vrai utilisateur.

## Pourquoi c'est si peu cher

Parce que le principe « hors-ligne d'abord » retourne l'économie du projet. Le téléphone
détient les échéances, donc **les rappels datés sont calculés et déclenchés sur
l'appareil** (`D-010`). Le serveur ne fait plus que quatre choses : authentifier, empiler
les mutations, servir les changements depuis un curseur, émettre un réveil. Un VPS à
2 vCPU et 4 Go tient des milliers de foyers.

## Ce qui rend ce « oui » réversible

1. **Chaque téléphone est une réplique complète.** Perdre le serveur n'est pas perdre les
   données.
2. **`CHOREO_SERVER_URL` est un paramètre de compilation.** Un foyer déménage vers une
   autre instance.
3. **`docker compose up` suffit** à héberger la sienne — et l'AGPL-3.0 en fait un droit.

C'est ce qui permet de promettre « gratuit pour toujours » sans mentir : la promesse ne
dépend plus de notre capacité à payer.

## Sur OVH plutôt qu'un autre

Pas d'élément solide contre la fiabilité des VPS d'entrée de gamme concurrents, mais la
sévérité des détections de fraude est un risque réel pour un opérateur solo. OVH est de
toute façon un bon choix : UE, RGPD natif, domaine et VPS sur une seule facture.

Un point à retenir les concernant : **l'incendie de Strasbourg en mars 2021** a détruit
les données des clients sans sauvegarde hors site. La sauvegarde ira donc chez un autre
fournisseur, jamais dans un second espace du même.
