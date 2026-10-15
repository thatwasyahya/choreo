# 0004 — L'authentification n'exige aucune adresse e-mail

- **Statut** : accepté
- **Date** : 20 septembre 2026
- **Décision** : `D-009`

## Contexte

Le §0 du cahier des charges interdit tout service géré : ni Firebase, ni Auth0. Le §2.3
range les SMS parmi les non-objectifs. Le glossaire prévoit des **membres sans compte**, et
le principe 5 veut que l'application serve dès le premier utilisateur.

Restaient trois mécanismes : mot de passe, code à usage unique (OTP), ou clés d'accès.

## Décision

**Identifiant + mot de passe**, haché en argon2id. Jetons d'accès courts et jeton de
rafraîchissement. **Aucune adresse e-mail obligatoire.**

- Rejoindre un foyer se fait par **code d'invitation ou QR code**, transmis de la main à la
  main, ou par le canal que l'utilisateur choisit lui-même. Aucun envoi côté serveur.
- La récupération passe par un **code de secours affiché à l'inscription** (modèle Signal,
  Bitwarden) et par la ré-invitation par un administrateur du foyer.
- L'e-mail devient **optionnel**, ajouté plus tard, derrière l'interface qu'exige la
  règle d'exploitation 8.

## Pourquoi pas l'OTP

Pour une raison mécanique : **un code à usage unique doit être acheminé.**

- Par SMS : non-objectif explicite du §2.3, et payant.
- Par e-mail : il faut un relais SMTP, c'est-à-dire précisément le service externe qu'on
  voulait éviter. S'y ajoutent la délivrabilité catastrophique du courrier auto-hébergé, et
  une connexion qui devient **impossible** le jour où le courrier tombe.

Un mot de passe ne dépend de personne.

## Conséquences

- Aucune donnée personnelle n'est demandée pour créer un foyer et s'en servir. C'est
  aligné avec la souveraineté du principe 8, et ça réduit la surface RGPD à presque rien.
- Pas de relais SMTP à exploiter, donc une pièce mobile de moins (règle 4).
- **Coût assumé** : un mot de passe oublié sans code de secours et sans autre
  administrateur dans le foyer est un compte perdu. Atténué par trois choses — le code de
  secours, la ré-invitation par un administrateur, et le fait que les données locales
  survivent sur l'appareil.
- Le jour où l'e-mail arrive, il arrive comme une **option de récupération**, pas comme un
  identifiant obligatoire.

## Alternative à reconsidérer plus tard : les clés d'accès

Les *passkeys* supprimeraient le mot de passe sans relais d'aucune sorte. Écartées en v1
pour leur complexité côté Flutter et le besoin d'un mécanisme de repli de toute façon —
mais c'est l'évolution naturelle de cette décision, pas une porte fermée.
