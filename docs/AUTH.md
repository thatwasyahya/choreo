# Authentification, foyers et invitations

> Lot **L1.2** de la Phase 1. Ni le cahier des charges ni
> [SYNC_PROTOCOL.md](SYNC_PROTOCOL.md) ne spécifiaient de point de terminaison
> d'authentification : le protocole se contente d'exiger un `Authorization: Bearer`.
> Ce document comble ce vide. `D-009` en avait déjà tranché le mécanisme.

Document séparé, et pas une section du protocole : l'authentification et la synchronisation
changent pour des raisons différentes, et celle-ci doit pouvoir être relue seule.

**Le principe qui commande tout le reste** : on crée un foyer et on s'en sert **sans rien
donner à personne**. Pas d'adresse e-mail, pas de numéro, pas de compte chez un tiers. Ce
n'est pas une posture — c'est ce qui rend le §2.3 tenable, puisqu'un code à usage unique
devrait être acheminé, et qu'acheminer veut dire un relais SMTP ou des SMS.

---

## Les choix, et pourquoi

### Des jetons opaques, pas des JWT

Un jeton est **32 octets aléatoires**, rendus en base64url, stockés **hachés** côté serveur.

Le JWT a un seul avantage : vérifier sans toucher la base. Cet avantage vaut **zéro** ici,
parce que le protocole exige déjà une lecture de base à chaque requête : l'appartenance au
foyer est vérifiée contre une ligne `members` vivante **à chaque appel**, jamais à l'émission
du jeton (`SYNC_PROTOCOL.md`, isolation, mesure 2). On paierait donc une bibliothèque de
crypto, une classe de failles bien connue — confusion d'algorithme, `alg: none`, clés mal
tournées — et une cérémonie de rotation de clés, pour un gain nul.

En échange, la révocation est immédiate : supprimer la ligne suffit. C'est exactement ce
qu'exige `devices.revoked_at`.

### Deux jetons, et le jeton de rafraîchissement tourne

| | Durée | Envoyé |
|---|---|---|
| `access` | **15 minutes** | à chaque requête |
| `refresh` | **180 jours**, glissants | seulement pour renouveler |

Cent-quatre-vingts jours et non soixante : le modèle de menace est un téléphone perdu, et
c'est la révocation d'appareil qui y répond, pas l'expiration du jeton. Une tablette murale
qu'on ne touche qu'au passage ne doit pas se déconnecter — le principe 2 ne survit pas à un
grand-parent devant un écran de connexion (`D-021`, A1).

Pourquoi deux, alors que la révocation est déjà immédiate : l'`access` passe dans chaque
requête et peut finir dans le journal d'un intermédiaire ; le `refresh` passe rarement.

**Le `refresh` est à usage unique et tourne à chaque emploi.** Réutiliser un `refresh` déjà
consommé est le signal d'un vol — soit le voleur s'en sert, soit la victime. On ne peut pas
savoir lequel, donc on **révoque tout l'appareil**.

**Avec une exception, et elle n'est pas un confort.** La règle stricte ci-dessus se retourne
contre l'utilisateur dans un cas banal : le téléphone envoie son `refresh`, le serveur fait
tourner la chaîne, et **la réponse est perdue** en route. Le client détient toujours l'ancien
jeton, il retente, et il se verrouille lui-même — c'est-à-dire exactement la reconnexion que
`A1` a été tranchée pour éviter. Sur un réseau mobile, ce n'est pas un cas limite.

La règle exacte est donc :

| Situation | Réponse |
|---|---|
| Jeton inconnu ou expiré | `401`, rien d'autre |
| Jeton consommé, **et un successeur a déjà servi** | `401 token_reuse` + **révocation de l'appareil** |
| Jeton consommé depuis moins de **60 secondes**, successeur jamais servi | **accordé** : le successeur orphelin est jeté et une paire neuve est émise |

La détection de vol survit intacte : rejouer un jeton dont le successeur a déjà servi reste
une révocation, et c'est la vraie signature de deux parties qui alternent sur une même
chaîne. Ce que la fenêtre enlève, c'est le faux positif — pas le vrai.

Ce que ça coûte : pendant au plus 60 secondes, un voleur qui aurait intercepté l'ancien jeton
peut le présenter avant que le propriétaire n'utilise le nouveau. Soixante secondes et un vol
déjà en cours, contre une famille déconnectée pour un paquet perdu — l'échange est net.

### argon2id, avec les paramètres dans le haché

```
argon2id  m = 19456 KiB   t = 2   p = 1   sel 16 octets   sortie 32 octets
```

C'est la configuration minimale recommandée par l'OWASP. 19 Mio par calcul tient sans effort
sur les 4 Go du §0.2, même avec plusieurs connexions simultanées — là où 64 Mio exposerait le
serveur à une saturation mémoire par simple répétition de connexions.

Le haché est stocké au **format PHC** (`$argon2id$v=19$m=19456,t=2,p=1$<sel>$<haché>`), donc
les paramètres voyagent avec lui. Les relever plus tard n'invalide aucun compte existant : on
re-hache au prochain succès de connexion.

**Et un plafond global de 4 calculs simultanés.** Sans lui, le plafond de coût de `D-005`
était une fiction : les seaux ci-dessous sont par identifiant et par adresse, donc une
tentative répartie sur beaucoup d'identifiants distincts les passe tous les deux et arrive
quand même d'un coup. Quatre cents tentatives simultanées réclameraient près de 8 Go sur une
machine qui en a 4. Avec le plafond, le serveur **ralentit** sous inondation au lieu de
mourir. Le calcul à vide servi aux identifiants inexistants prend un jeton lui aussi : sinon
il suffirait d'envoyer des identifiants faux pour contourner la file.

### Rien d'énumérable, sauf là où c'est inévitable

- **Échec de connexion** : toujours la même réponse, `401 invalid_credentials`, que
  l'identifiant existe ou non. Et quand il n'existe pas, le serveur **calcule quand même un
  argon2id** sur un haché factice : sans cela, la différence de temps de réponse révèle quels
  identifiants existent.
- **Création de compte** : un identifiant déjà pris doit être annoncé, sinon l'inscription est
  impossible. C'est le seul oracle d'énumération assumé, et il est limité en débit.
- **Un foyer dont on n'est pas membre répond `404`, jamais `403`** — repris du protocole.
  Aucun oracle d'appartenance.

### `device_id` est frappé par le serveur

Le client ne choisit pas son identifiant d'appareil. Il est émis à l'enregistrement et le
jeton y est lié.

La raison est concrète : `device_id` porte le curseur de synchronisation. Un client qui
pourrait revendiquer le `device_id` d'un autre appareil corromprait **son** curseur, donc sa
réplique. Un identifiant frappé par le serveur et lié au jeton rend le geste impossible.

### Limitation de débit en mémoire, et c'est assumé

Un seau à jetons par processus, par route et par clé — l'identifiant pour la connexion,
l'adresse pour le reste.

| Route | Limite |
|---|---|
| `POST /v1/sessions` | 10 / 15 min par identifiant, 60 / 15 min par adresse |
| `POST /v1/accounts` | 5 / heure par adresse |
| `POST /v1/invites/redeem` | 10 / heure par compte |
| `POST /v1/sessions/recover` | 5 / heure par identifiant |

Pas de Redis : une pièce mobile de plus devrait justifier sa RAM (règle d'exploitation 4), et
il n'y a qu'un processus. **Les limites se réinitialisent au redémarrage et ne franchissent
pas une seconde instance.** C'est écrit ici pour que ce soit un choix et pas une surprise, et
c'est à revoir le jour où il y a deux serveurs — pas avant.

---

## Les tables ajoutées

Migration `00003`. Les comptes, appareils et membres existent déjà (`00002`).

```sql
-- Un jeton de rafraîchissement vivant. L'access n'est jamais stocké : il est
-- vérifié en recalculant son haché contre cette ligne, puis oublié.
CREATE TABLE sessions (
    id            uuid PRIMARY KEY,
    account_id    uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    device_id     uuid NOT NULL REFERENCES devices(id)  ON DELETE CASCADE,
    access_hash   bytea NOT NULL,                  -- sha256 du jeton d'accès
    refresh_hash  bytea NOT NULL,                  -- sha256 du jeton de rafraîchissement
    access_expires_at  timestamptz NOT NULL,
    refresh_expires_at timestamptz NOT NULL,
    -- Chaîne de rotation : détecte le rejeu d'un refresh déjà consommé.
    rotated_from  uuid NULL,
    consumed_at   timestamptz NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX sessions_access  ON sessions (access_hash);
CREATE UNIQUE INDEX sessions_refresh ON sessions (refresh_hash);
CREATE INDEX sessions_device ON sessions (device_id);

-- sha256 et non argon2id : un jeton de 32 octets aléatoires n'a pas besoin
-- d'être ralenti, il n'y a rien à devenir par force brute. Un mot de passe, si.

CREATE TABLE invites (
    id           uuid PRIMARY KEY,
    household_id uuid NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    code_hash    bytea NOT NULL,
    -- Non nul = ce code sert à REVENDIQUER un membre existant sans compte
    -- (le « membre sans compte » du glossaire).
    member_id    uuid NULL,
    role         text NOT NULL CHECK (role IN ('adult', 'child', 'guest')),
    created_by   uuid NOT NULL,
    expires_at   timestamptz NOT NULL,
    redeemed_at  timestamptz NULL,
    redeemed_by  uuid NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX invites_code ON invites (code_hash);
CREATE INDEX invites_live ON invites (household_id)
    WHERE redeemed_at IS NULL;
```

Un code d'invitation ne crée **jamais** un `admin` : on ne devient administrateur qu'en créant
le foyer, ou par promotion explicite par un administrateur en place. Un code qui fuite ne peut
donc pas livrer les clés du foyer.

---

## Les formats lisibles à voix haute

Les deux codes sont en **base32 Crockford** — alphabet sans `I`, `L`, `O` ni `U`, donc sans
confusion entre 1 et I, 0 et O. La saisie est insensible à la casse et aux tirets.

| | Format | Entropie | Vie |
|---|---|---|---|
| Code d'invitation | `ABCD-EFGH-JK` (10 caractères) | ~50 bits | **48 h**, usage unique |
| Code de secours | `ABCD-EFGH-JKMN-PQRS-TUVW-XYZ2` (24 caractères) | 120 bits | jusqu'au prochain |

50 bits se devinent en théorie ; en pratique le code est à usage unique, expire en 48 heures,
et sa présentation exige d'être déjà authentifié et passe par un seau de 10 par heure. Il n'y
a pas de force brute anonyme possible.

**Le QR code porte le code *et* l'adresse du serveur.** Sans cela, un foyer qui auto-héberge
son instance (§8.6) ne pourrait pas inviter : la personne scannerait un code valide en le
présentant au mauvais serveur.

```
choreo://join?s=https%3A%2F%2Fchoreo.example&c=ABCDEFGHJK
```

---

## Les points de terminaison

Tout sous `/v1`. Comme pour la synchronisation, `protocol`, `device_id` et `app_version`
voyagent **dans le corps** — plus faciles à journaliser, à `grep` et à falsifier dans un test
qu'un en-tête.

### `POST /v1/accounts` — créer un compte

```json
{ "login": "yahya", "password": "…", "protocol": 1,
  "app_version": "1.0.0", "platform": "android" }
```

`201` :

```json
{ "account_id": "018f…", "device_id": "018g…",
  "access": "…", "access_expires_in": 900,
  "refresh": "…", "refresh_expires_in": 5184000,
  "recovery_code": "ABCD-EFGH-JKMN-PQRS-TUVW-XYZ2" }
```

**`recovery_code` n'apparaît qu'ici, une seule fois.** Le serveur n'en garde qu'un haché et ne
peut pas le réafficher. L'interface doit obliger à le noter avant de continuer — c'est le seul
filet quand il n'y a pas d'adresse e-mail.

Identifiant : 3 à 32 caractères, lettres, chiffres, `.`, `-`, `_`, commençant par une lettre
ou un chiffre. Insensible à la casse (`citext`). Mot de passe : 10 caractères minimum, aucune
règle de composition — elles produisent `Password1!` et rien de mieux.

Erreurs : `409 login_taken`, `422 weak_password`, `429 rate_limited`.

### `POST /v1/sessions` — se connecter

```json
{ "login": "yahya", "password": "…", "protocol": 1,
  "app_version": "1.0.0", "platform": "android",
  "device_id": "018g…" }
```

`device_id` est **optionnel** : absent, le serveur en frappe un nouveau. Présent, il doit
appartenir à ce compte et ne pas être révoqué, sinon `401`. C'est ce qui permet de se
reconnecter sans perdre son curseur, donc sans retélécharger la réplique.

`200` : même forme que ci-dessus, sans `recovery_code`.

Erreur unique en cas d'échec : `401 invalid_credentials`. Jamais « ce compte n'existe pas ».

### `POST /v1/sessions/refresh` — renouveler

```json
{ "refresh": "…" }
```

`200` : une paire neuve. L'ancienne est consommée dans la même transaction.

Si le `refresh` présenté est **déjà consommé** : `401 token_reuse`, et **toutes les sessions de
cet appareil sont révoquées**. Voir plus haut : on ne sait pas qui rejoue, donc on ferme.

### `DELETE /v1/sessions` — se déconnecter

Authentifié. Révoque la session courante. **La réplique locale est conservée** et la
synchronisation s'arrête — se déconnecter n'est pas se faire effacer.

### `POST /v1/sessions/recover` — mot de passe oublié

```json
{ "login": "yahya", "recovery_code": "ABCD-…", "new_password": "…" }
```

`200` : une paire de jetons, et un **nouveau code de secours** — l'ancien est consommé.

**Tous les autres appareils du compte sont révoqués.** Le cas courant est « j'ai perdu mon
téléphone et oublié mon mot de passe » : y révoquer le téléphone perdu est exactement ce qu'on
veut. L'interface doit le dire avant de valider.

### `POST /v1/accounts/password` — changer de mot de passe

Authentifié, demande l'ancien. Les autres appareils **ne sont pas** révoqués : changer son mot
de passe par hygiène ne doit pas déconnecter la tablette de la cuisine.

### `GET /v1/me` — qui je suis, et quels foyers

```json
{ "account_id": "018f…", "login": "yahya", "device_id": "018g…",
  "households": [
    { "household_id": "018h…", "member_id": "018i…", "name": "Maison",
      "role": "admin", "tz": "Europe/Paris" } ] }
```

C'est ce que le client appelle au démarrage pour savoir **quoi** synchroniser. La liste ne
contient que les foyers où une ligne `members` vivante existe : un membre retiré voit le foyer
disparaître au prochain appel, sans attendre l'expiration de son jeton.

### `POST /v1/households` — créer un foyer

```json
{ "name": "Maison", "tz": "Europe/Paris", "display_name": "Yahya" }
```

`201` → `household_id`, `member_id`, rôle `admin`. Le créateur est le premier membre.

### `POST /v1/households/{hid}/invites` — inviter

Réservé aux `admin`. `404` si on n'est pas membre.

```json
{ "role": "adult", "member_id": null, "ttl_hours": 48 }
```

`member_id` non nul **revendique un membre existant sans compte** : les tâches déjà assignées
à « Lina » restent les siennes quand Lina finit par avoir un téléphone. C'est l'épisode E02 du
cahier des charges, et c'est la raison pour laquelle `members.account_id` est nullable.

`201` → `{ "code": "ABCD-EFGH-JK", "qr": "choreo://join?s=…&c=…", "expires_at": "…" }`

### `POST /v1/invites/redeem` — rejoindre

Authentifié. Un compte est donc nécessaire d'abord — ce qui supprime toute force brute
anonyme.

```json
{ "code": "abcd-efgh-jk", "display_name": "Camille" }
```

`200` → `{ "household_id": "…", "member_id": "…", "role": "adult" }`

Erreurs : `404 invite_unknown` (inconnu, expiré **ou** déjà utilisé — la même réponse pour les
trois, pas d'oracle), `409 already_member`.

### `DELETE /v1/devices/{id}` — retirer un appareil

Authentifié. Ne peut viser qu'un appareil de son propre compte. Pose `revoked_at` et supprime
ses sessions.

L'appareil visé reçoit `401 device_revoked` à son appel suivant. **Celui-là efface sa réplique
locale**, contrairement à une simple déconnexion. C'est la différence entre « je me déconnecte »
et « ce téléphone n'est plus à moi ».

---

## Ce qui ne doit jamais apparaître dans un journal

Mot de passe, jeton d'accès, jeton de rafraîchissement, code de secours, code d'invitation.
Ni en clair, ni tronqué — un préfixe de code d'invitation réduit l'espace de recherche.

Le journal d'accès existant ne consigne que méthode, route, statut et durée : **aucun en-tête,
aucun corps**. Ce document est la raison pour laquelle il doit rester ainsi.

Et le `payload` **verbatim** stocké dans `ops` ne concerne que les opérations de
synchronisation : aucun corps d'authentification n'y entre jamais.

---

## Les deux points qui restaient, tranchés

Décidés par la même délégation que les neuf questions du protocole, et consignés en `D-021`.

**A1 — Rafraîchissement : 180 jours** glissants, et non 60. Détaillé plus haut.

**A2 — La récupération révoque bien les autres appareils.** L'asymétrie décide : se tromper en
révoquant coûte une reconnexion, se tromper en **ne** révoquant **pas** laisse un voleur de
code de secours cohabiter en silence avec le propriétaire. L'interface doit l'annoncer avant
de valider, pas après.
