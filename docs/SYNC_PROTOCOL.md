# Protocole de synchronisation

> Remplace le §10.3 du cahier des charges, qui n'a jamais été transmis (`D-015`).
> Lot **L0.9** de la Phase 0. **Ce document doit être validé explicitement par le product
> owner avant la première ligne de Phase 1.** C'est lui qui fixe le schéma de données,
> l'API et le comportement hors-ligne — c'est-à-dire le produit.

Ce document **tranche**. Il ne compare pas des options : quatre protocoles ont été conçus
puis démontés, les mécanismes qui ont survécu sont ici, et ce qui a été écarté l'est en une
ligne. Les identifiants, tables et colonnes restent en anglais, comme le reste du code.

Ce qu'il décide, en une phrase : **les intentions montent, les états de lignes descendent,
un compteur par foyer ordonne tout, et aucune horloge de téléphone n'arbitre jamais quoi que
ce soit.**

---

## Le modèle en une page

Cinq pièces. Si vous ne retenez que celles-là, le reste se lit en diagonale.

**1. Le foyer a un compteur, et c'est la seule horloge du système.**
`households.change_seq`, incrémenté sous un verrou de ligne à l'intérieur de la transaction
qui écrit. Ordre d'attribution = ordre de validation, donc un client qui demande « tout ce
qui a changé après 48 210 » ne peut rien manquer. Pas de séquence PostgreSQL (elle laisse
des trous et valide dans le désordre), pas d'horloge logique hybride, pas d'horodatage.

**2. Les intentions montent.** Le téléphone n'envoie pas « voici le nouvel état de la
tâche », il envoie « coche cette tâche », « renomme ce champ, j'avais vu la révision
48 102 ». Douze types d'opérations, pas plus. Le serveur est le seul à décider ce que
l'intention devient, **une fois**, sous le verrou du foyer.

**3. Les états descendent.** Le serveur ne renvoie pas les opérations des autres : il
renvoie **l'état de la ligne après fusion**. Trois conséquences, toutes décisives :

- rattraper dix jours coûte ce qui a changé, pas ce qui s'est passé — une tâche éditée
  quarante fois, c'est une ligne ;
- **il n'y a aucun moteur de fusion sur le téléphone.** Appliquer un état, c'est
  `INSERT OR REPLACE` si la `rev` entrante est supérieure à la `rev` locale. Idempotent,
  insensible à l'ordre d'arrivée, impossible à désynchroniser. Une seule implémentation de
  la fusion existe, en Go, sur le serveur. C'est le coût que les quatre propositions
  payaient le plus cher — deux réducteurs, deux langages, qui doivent rester d'accord pour
  toujours — et il est **supprimé**, pas atténué ;
- une app N-1 qui reçoit une ligne contenant un champ qu'elle ne connaît pas l'applique
  quand même. Une ligne est compatible en avant par nature ; un vocabulaire d'opérations,
  non. C'est ce qui rend la compatibilité N-2 (§15.4) gratuite dans le sens descendant.

**4. La détection de conflit est causale, jamais chronologique.** Chaque écriture de champ
porte la `rev` que l'auteur avait réellement sous les yeux. « Concurrent » veut dire
« n'avait pas vu la valeur actuelle », ce qui reste vrai qu'un combiné soit réglé sur 1997
ou sur 2031.

**5. Rien ne disparaît sans qu'un humain l'ait vu.** Trois règles mécaniques, pas des
intentions :

- une opération refusée n'est jamais supprimée en silence : elle devient une ligne
  `notices` **côté serveur**, donc elle apparaît sur les autres appareils de son auteur et
  survit à une réinstallation ;
- le client ne réécrit **jamais** une `base_rev` périmée vers la valeur actuelle du
  serveur. Seul un humain qui a vu les deux valeurs a le droit d'en écraser une ;
- une remise à zéro de la réplique locale (`410`) rebâtit l'état venu du serveur et **ne
  touche pas à la boîte d'envoi**. « Ton curseur a expiré » ne peut pas vouloir dire « tes
  quatorze modifications hors-ligne sont perdues ».

```
  TÉLÉPHONE                                  SERVEUR
  ─────────                                  ───────
  écran ──lit──► Drift (réplique complète)
                    │
             écriture locale optimiste
                    │
                 outbox ──── POST /sync ────► verrou du foyer
                  (intentions)                     │ arbitre UNE fois
                                                   │ change_seq += 1
                 Drift ◄─── états de lignes ───────┘ écrit la ligne (rev = seq)
                   (après fusion)
```

**Écarté en une ligne, pour que la question ne revienne pas.** CRDT généralisé : converge,
mais ne sait pas exprimer « une seule occurrence ouverte par série », qui est la première
vraie contrainte du produit, et personne seul ne le débogue à 1 h du matin. Horloges
logiques hybrides : remplacent une horloge fausse par une horloge fausse plafonnée, et le
plafond récompense le tricheur qui arrive en dernier. Journal d'opérations rejoué par le
client : impose un réducteur en Go **et** en Dart, identiques pour toujours.
Dernière-écriture-gagne globale : c'est exactement le mécanisme par lequel un téléphone qui
refait surface écrase la matinée du foyer.

---

## Modèle de données

### Serveur — PostgreSQL 17

Principe de découpage : **le serveur a des colonnes typées pour ce qu'il doit arbitrer, et
du `jsonb` qu'il n'interprète pas pour le reste.** C'est « le serveur est un relais, pas le
cerveau » écrit en DDL, et c'est ce qui fait qu'ajouter un champ de tâche en app v3 ne
demande aucune migration serveur.

#### Identité, foyer, appareils

```sql
CREATE TABLE accounts (
  id            uuid PRIMARY KEY,
  login         citext UNIQUE NOT NULL,
  pw_hash       text NOT NULL,                   -- argon2id (D-009)
  recovery_hash text NOT NULL,                   -- code de secours (D-009)
  created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE households (
  id                uuid PRIMARY KEY,
  name              text NOT NULL,
  tz                text NOT NULL,               -- IANA, 'Europe/Paris' par défaut
  change_seq        bigint NOT NULL DEFAULT 0,   -- L'HORLOGE DU FOYER
  purge_horizon_seq bigint NOT NULL DEFAULT 0,   -- en dessous, un curseur est mort
  settings          jsonb NOT NULL DEFAULT '{}', -- opaque au serveur
  created_at        timestamptz NOT NULL DEFAULT now()
);

-- Un membre = une personne du foyer, AVEC OU SANS COMPTE (glossaire §4).
-- account_id NULL = membre sans compte. L'appartenance à plusieurs foyers, c'est
-- simplement plusieurs lignes members pour un même account_id : pas de table
-- d'appartenance séparée, et pas de « foyer courant » nulle part.
CREATE TABLE members (
  id           uuid NOT NULL,
  household_id uuid NOT NULL REFERENCES households(id) ON DELETE CASCADE,
  account_id   uuid NULL REFERENCES accounts(id) ON DELETE SET NULL,
  role         text NOT NULL CHECK (role IN ('admin','adult','child','guest')),
  removed_rev  bigint NULL,                      -- un départ ne réécrit pas l'historique
  -- enveloppe de synchronisation, identique sur toutes les tables synchronisées
  rev         bigint NOT NULL,
  created_rev bigint NOT NULL,
  field_revs  jsonb  NOT NULL DEFAULT '{}',
  deleted_rev bigint NULL,
  updated_by  uuid   NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  fields      jsonb  NOT NULL DEFAULT '{}',      -- display_name, colour, …
  PRIMARY KEY (household_id, id)
);
CREATE INDEX members_account ON members (account_id) WHERE account_id IS NOT NULL;
CREATE INDEX members_seq     ON members (household_id, rev);

CREATE TABLE devices (
  id          uuid PRIMARY KEY,                  -- une par installation
  account_id  uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  app_version text NOT NULL,
  protocol    int  NOT NULL,
  platform    text NOT NULL,
  revoked_at  timestamptz NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);

-- Un curseur PAR (appareil, foyer). Pas un curseur par appareil : Paul l'aidant et la
-- garde alternée du §3.1 sont des faits du domaine, pas une option de Phase 3. Ajouter
-- cette colonne plus tard voudrait dire reconstruire la frontière de sécurité et le
-- moteur de synchronisation après la mise en service.
CREATE TABLE device_cursors (
  device_id     uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  household_id  uuid NOT NULL REFERENCES households(id) ON DELETE CASCADE,
  member_id     uuid NOT NULL,
  cursor_seq    bigint NOT NULL DEFAULT 0,       -- 0 = prendre un snapshot d'abord
  outbox_age_s  int    NOT NULL DEFAULT 0,       -- rapporté par le client, §2.4
  clock_skew_ms bigint NULL,                     -- observé, jamais appliqué
  last_seen_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (device_id, household_id)
);
```

#### L'enveloppe de synchronisation

Sept colonnes, **identiques sur chaque table synchronisée**. Il n'y a **pas de table
`change_log`** : le journal du foyer, c'est l'ensemble des lignes du domaine triées par
`rev`. Une ligne modifiée quarante fois est une ligne, pas quarante. C'est la plus grosse
simplification du document : aucune table de journal à écrire, à indexer, à purger, à
désynchroniser du modèle, et aucune copie intégrale d'une note longue à chaque frappe.

| Colonne | Rôle |
|---|---|
| `id` | uuidv7, **frappé par le client** — un téléphone en cave doit créer une ligne sans serveur |
| `household_id` | le tenant. Première colonne de chaque clé et de chaque index |
| `rev` | `change_seq` à la dernière écriture. Sert de révision **et** de position dans le journal |
| `created_rev` | `rev` à la création. Sert à la règle du successeur (séries) |
| `field_revs` | `{"<nom de champ logique>": <rev>}`. La détection de conflit, par champ |
| `deleted_rev` | pierre tombale. Les colonnes de contenu sont **conservées** |
| `updated_by` / `updated_at` | membre et heure **serveur**. Jamais fournis par le client |

`field_revs` est indexé par **nom de champ logique**, jamais par emplacement de stockage :
`priority` est `priority`, que la valeur vive dans une colonne typée ou dans `ext`. C'est ce
qui permettra de faire « graduer » un champ `ext` en colonne sans qu'une valeur porte deux
provenances que l'arbitre ne sait pas comparer.

#### Les tables du domaine

```sql
CREATE TABLE tasks (
  id uuid NOT NULL, household_id uuid NOT NULL,
  rev bigint NOT NULL, created_rev bigint NOT NULL, field_revs jsonb NOT NULL DEFAULT '{}',
  deleted_rev bigint NULL, updated_by uuid NOT NULL, updated_at timestamptz NOT NULL,
  closed_rev  bigint NULL,                       -- dérivée : non nulle dès qu'une validation existe
  -- contenu
  kind        text NOT NULL CHECK (kind IN ('task','event')),
  title       text NOT NULL CHECK (length(title) BETWEEN 1 AND 500),
  notes       text NULL CHECK (notes IS NULL OR length(notes) <= 8000),
  due_on      date NULL,                         -- une DATE, pas un instant
  due_time    time NULL,
  all_day     boolean NOT NULL DEFAULT true,
  assignee_id uuid NULL, category_id uuid NULL, priority smallint NULL,
  -- série
  series_id    uuid NULL,
  series_index int  NULL,                        -- un ENTIER, pas une date. Voir l'invariant
  overrides    text[] NOT NULL DEFAULT '{}',     -- champs qu'un humain a épinglés
  ext          jsonb  NOT NULL DEFAULT '{}',     -- champs d'une app plus récente
  PRIMARY KEY (household_id, id)
);
CREATE INDEX tasks_seq ON tasks (household_id, rev);
CREATE UNIQUE INDEX tasks_series_slot ON tasks (household_id, series_id, series_index)
  WHERE series_id IS NOT NULL;
-- Le garde-fou de dernier recours contre MES propres bugs, jamais le mécanisme :
CREATE UNIQUE INDEX tasks_one_open_per_series ON tasks (household_id, series_id)
  WHERE series_id IS NOT NULL AND deleted_rev IS NULL AND closed_rev IS NULL;

-- Les validations sont un ENSEMBLE, pas un booléen. Ajoutées par membre, jamais écrasées.
-- Même mécanisme pour cocher une tâche et pour cocher un article de liste : une règle de
-- moins à tenir en tête, et c'est la plus utilisée des deux.
CREATE TABLE completions (
  household_id  uuid NOT NULL,
  row_id        uuid NOT NULL,                   -- tasks.id ou list_items.id
  member_id     uuid NOT NULL,
  completion_id uuid NOT NULL,                   -- frappé par le client : clé de dédoublonnage des points
  done_rev      bigint NOT NULL,
  occurred_on   date   NOT NULL,                 -- PROPOSÉ par le client, BORNÉ par le serveur
  client_ts     timestamptz NULL,                -- donnée d'affichage, jamais autorité
  clock_suspect boolean NOT NULL DEFAULT false,
  PRIMARY KEY (household_id, row_id, member_id)
);

CREATE TABLE series (
  id uuid NOT NULL, household_id uuid NOT NULL,
  rev bigint NOT NULL, created_rev bigint NOT NULL, field_revs jsonb NOT NULL DEFAULT '{}',
  deleted_rev bigint NULL, updated_by uuid NOT NULL, updated_at timestamptz NOT NULL,
  title    text NOT NULL,
  mode     text NOT NULL CHECK (mode IN ('calendar','relative')),
  rule     text NOT NULL,                        -- sous-ensemble RRULE, mode calendar
  rel_days int  NULL,                            -- « X jours après la dernière fois »
  anchor_on date NOT NULL,
  rotation uuid[] NOT NULL DEFAULT '{}', rotation_offset int NOT NULL DEFAULT 0,
  paused   boolean NOT NULL DEFAULT false,
  catch_up text NOT NULL DEFAULT 'skip' CHECK (catch_up IN ('skip','accumulate')),
  -- LES DEUX COLONNES ARBITRÉES. Un client ne les écrit jamais.
  next_index         int  NOT NULL DEFAULT 0,
  open_occurrence_id uuid NULL,                  -- NULL seulement si paused ou deleted
  ext jsonb NOT NULL DEFAULT '{}',
  PRIMARY KEY (household_id, id)
);
CREATE INDEX series_seq ON series (household_id, rev);

CREATE TABLE lists      ( /* enveloppe + name, kind, ext */ );
CREATE TABLE list_items ( /* enveloppe + list_id, label, qty, unit, order_key, ext */ );
CREATE TABLE memos      ( /* enveloppe + title, body, pinned, ext */ );
CREATE TABLE tags       (household_id uuid, task_id uuid, tag citext,
                         add_rev bigint NOT NULL,
                         PRIMARY KEY (household_id, task_id, tag));
```

`tasks.closed_rev` est une colonne dérivée, tenue par le serveur, non nulle dès que
`completions` n'est pas vide pour cette ligne. Elle existe uniquement pour rendre l'index
partiel ci-dessus possible — l'invariant est tenu par `open_occurrence_id`, pas par l'index.

Les `completions` et les `tags` **voyagent dans l'état de leur tâche** et n'ont pas de `rev`
propre : cocher une tâche incrémente la `rev` de la tâche. Sept entités de premier niveau
seulement sont donc interrogées par un `pull` : `members`, `tasks`, `series`, `lists`,
`list_items`, `memos`, `notices`.

#### Le registre des opérations — la table de 2 h du matin

```sql
CREATE TABLE ops (
  household_id uuid NOT NULL,
  op_id        uuid NOT NULL,                    -- frappé par le client au moment du geste
  payload_hash bytea NOT NULL,                   -- sha256(payload canonique)
  device_id    uuid NOT NULL,
  member_id    uuid NOT NULL,                    -- VENU DU JETON, jamais du corps
  kind         text NOT NULL,
  row_id       uuid NULL,
  payload      jsonb NOT NULL,                   -- VERBATIM ce que le client a envoyé
  outcome      text NOT NULL CHECK (outcome IN
                 ('applied','absorbed','receipted','superseded','refused')),
  reason       text NULL,
  result_rev   bigint NULL,
  result       jsonb NULL,                       -- la réponse rendue, rejouée à l'identique
  client_ts    timestamptz NULL,                 -- l'horloge du téléphone. Stockée. Jamais crue
  app_version  text NOT NULL, protocol int NOT NULL,
  received_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (household_id, op_id)
);
CREATE INDEX ops_recent ON ops (household_id, received_at DESC);
CREATE INDEX ops_by_row ON ops (household_id, row_id, received_at DESC);
```

`PRIMARY KEY (household_id, op_id)` est la clé d'idempotence : un envoi rejoué rend le
résultat stocké au lieu de s'appliquer deux fois. **Et `payload_hash` est comparé à chaque
rejeu** : un bug client qui réutilise un `op_id` pour une autre opération obtient
`malformed`, pas la réponse de l'autre opération. Sans ce hachage, une mutation disparaît
sans aucune trace — la panne la plus difficile à diagnostiquer de toutes celles examinées.

C'est aussi tout le journal d'audit dont un exploitant seul a besoin. Dans
`docs/RUNBOOK.md` :

```sql
-- Tout ce qui est arrivé à une ligne contestée, et ce que le serveur en a fait
SELECT received_at, device_id, app_version, kind, payload, outcome, reason
  FROM ops WHERE household_id = $1 AND row_id = $2 ORDER BY received_at;
```

#### Les notices — une seule surface, côté serveur

```sql
CREATE TABLE notices (
  id uuid NOT NULL, household_id uuid NOT NULL,
  for_member uuid NOT NULL,                      -- ADRESSÉE. Jamais « au foyer »
  kind   text NOT NULL,                          -- receipt | decision | refusal
  code   text NOT NULL,                          -- clé de localisation, jamais une phrase
  entity text NOT NULL, row_id uuid NULL,
  mine   jsonb NOT NULL,                         -- le contenu de l'utilisateur, VERBATIM
  theirs jsonb NULL,                             -- la valeur qui a tenu
  by_member uuid NULL, op_id uuid NULL,
  rev bigint NOT NULL, created_rev bigint NOT NULL,
  resolved_rev bigint NULL, resolved_choice text NULL,
  PRIMARY KEY (household_id, id)
);
CREATE INDEX notices_seq ON notices (household_id, rev);
```

Une notice vit **sur le serveur**, donc : elle apparaît sur le téléphone et sur la tablette
du même membre (cas 7), elle survit à une réinstallation, et elle survit à la perte de
l'appareil qui l'a provoquée. Les quatre propositions examinées gardaient le contenu refusé
dans une table locale — c'est-à-dire exactement sur l'appareil qui peut disparaître.

#### Isolation entre foyers — c'est une frontière de sécurité

Quatre mesures, dont trois gratuites. Aucune ne suppose les autres.

1. **`household_id` est la première colonne de chaque clé primaire et de chaque index.**
2. **Il est lu de l'appartenance, à chaque requête, jamais du corps de la requête.** Le
   `hid` du chemin est vérifié contre une ligne `members` vivante liée au compte du jeton,
   **à chaque appel** — pas à l'émission du jeton. Un membre retiré du foyer cesse de lire
   et d'écrire à la requête suivante, sans attendre l'expiration de son jeton d'accès.
3. **Un foyer dont on n'est pas membre répond `404`, jamais `403`.** Pas d'oracle
   d'appartenance. Tous les identifiants sont des UUID : un identifiant deviné ne révèle
   rien.
4. **Un `id` frappé par le client n'est jamais une clé globale.** Toutes les clés primaires
   et tous les index d'unicité portent `household_id` en tête. Un ancien colocataire qui a
   gardé une réplique connaît des UUID du foyer ; il ne peut pas pré-réserver un
   identifiant dans un autre foyer pour en bloquer la génération, parce qu'aucune unicité
   n'est globale. C'est la faille qui a tué une des propositions examinées, et la parade est
   une colonne dans chaque index.

**Pas de RLS, et c'est assumé.** Le rôle applicatif est propriétaire des tables et applique
les migrations (`store.Migrate`, `D-013`) : une politique RLS est donc contournée par
défaut, sauf à ajouter `FORCE ROW LEVEL SECURITY` **et** un second rôle **et** une exemption
pour la purge nocturne et le snapshot. Une seconde ceinture décorative est pire que pas de
seconde ceinture, parce qu'on relâche la première en croyant être couvert. À la place, ce qui
est mécaniquement vérifiable :

> **Classe de tests obligatoire, exigée par le §1.1 « Qualité ».** Pour chaque endpoint et
> chaque type d'entité : un membre du foyer A reçoit `404` pour chaque identifiant du foyer B.
> Plus un test qui échoue si une requête SQL existe hors de `internal/store`, et un test qui
> échoue si une méthode de `store` ne prend pas `householdID` en premier argument.

### Client — Drift / SQLite

Les tables de domaine sont les miroirs des précédentes, **`household_id` compris**, avec
deux colonnes locales en plus sur chaque ligne :

| Colonne locale | Rôle |
|---|---|
| `pending_fields TEXT` | tableau JSON des champs modifiés par l'utilisateur et pas encore acquittés |
| `provisional INTEGER` | 1 pour une occurrence prédite localement, pas encore confirmée |

**La règle qui empêche la synchronisation de révoquer la phrase que l'utilisateur est en
train de lire** : quand un état serveur arrive, le client écrit tous les champs **sauf** ceux
listés dans `pending_fields`. Une colonne, aucun couplage au protocole, et c'est le seul
mécanisme fiable contre ce symptôme.

```sql
-- L'ordre de rejeu est un entier autoincrémenté. JAMAIS un horodatage. Même
-- raisonnement que celui déjà écrit dans app/lib/core/database.dart : sur un
-- téléphone, l'id autoincrémenté est la seule chose monotone.
CREATE TABLE outbox (
  lseq         INTEGER PRIMARY KEY AUTOINCREMENT,
  household_id TEXT NOT NULL,
  op_id        TEXT NOT NULL UNIQUE,
  kind         TEXT NOT NULL,
  entity       TEXT NOT NULL,
  row_id       TEXT NOT NULL,
  payload      TEXT NOT NULL,                    -- JSON, uniquement les champs changés
  base_revs    TEXT NOT NULL,                    -- {"title": 48102}
  client_ts    INTEGER,                          -- horloge murale, pour affichage
  enqueued_elapsed INTEGER NOT NULL,             -- ticks monotones, pour le backoff
  state        TEXT NOT NULL DEFAULT 'queued',   -- queued | inflight
  attempts     INTEGER NOT NULL DEFAULT 0,
  last_error   TEXT
);

-- Un curseur PAR FOYER. Pas une ligne unique CHECK (id = 1).
CREATE TABLE sync_cursors (
  household_id TEXT PRIMARY KEY,
  cursor_seq        INTEGER NOT NULL DEFAULT 0,
  purge_horizon_seq INTEGER NOT NULL DEFAULT 0,
  protocol     INTEGER NOT NULL,
  min_app_version TEXT, sunset_at INTEGER,
  server_wall_offset_ms INTEGER,                 -- survit à un redémarrage
  server_mono_anchor_ms INTEGER, boot_id TEXT,   -- plus précis, meurt au redémarrage
  last_sync_elapsed INTEGER, last_outcome TEXT
);

CREATE TABLE device_identity (id INTEGER PRIMARY KEY CHECK (id = 1),
  device_id TEXT, access_token TEXT, refresh_token TEXT);

-- Miroir local des notices, pour que le plateau « À revoir » s'affiche hors-ligne.
CREATE TABLE notices (/* miroir */, seen INTEGER NOT NULL DEFAULT 0);

-- Une ligne d'état qu'on n'a pas su appliquer. Elle ne bloque JAMAIS le curseur.
CREATE TABLE quarantine (household_id TEXT, entity TEXT, row_id TEXT,
  rev INTEGER, raw TEXT, error TEXT, at_elapsed INTEGER);
```

**Deux règles clientes non négociables**, chacune ferme une panne observée dans les
propositions examinées :

1. **Aucune contrainte `CHECK` et aucune clé étrangère sur une colonne synchronisée.** Les
   pages arrivent en ordre de `rev`, pas en ordre causal : une tâche peut référencer une
   catégorie pas encore livrée, et un serveur N peut livrer `status = 'skipped'` à une app
   N-1. L'écran affiche « Sans catégorie », pas une exception.
2. **L'application d'une page est ligne par ligne, et une ligne qui échoue va en
   `quarantine` — le curseur avance quand même.** Une seule ligne illisible ne doit jamais
   geler *toute* la synchronisation d'un téléphone, en silence : c'est précisément la panne
   que la contrainte « un téléphone met des semaines à se mettre à jour » interdit. Le
   compteur `sync_quarantine_total` est la seule alerte qui sert à ça.

---

## Les points de terminaison

Tout sous `/v1`. `Authorization: Bearer <access>` (`D-009`). Chaque requête porte
`protocol`, `device_id` et `app_version` **dans le corps** — plus facile à journaliser, à
`grep` et à falsifier dans un test qu'un en-tête.

### 1. `POST /v1/households/{hid}/sync` — pousser et tirer en un aller-retour

C'est le protocole. Un appui sur « coché » est un appel HTTP qui envoie le geste **et**
ramène les ajouts de l'autre téléphone.

```json
{ "protocol": 1, "device_id": "018f…", "app_version": "1.4.2",
  "cursor_seq": 48210, "limit": 500, "wait_ms": 20000,
  "outbox_age_s": 864000, "client_now": "2026-10-15T09:12:00Z",
  "ops": [
    { "op_id": "019a…01", "kind": "set_fields", "entity": "task", "row_id": "018c…",
      "base_revs": { "title": 48102 },
      "fields": { "title": "Appeler le plombier (devis)" },
      "client_ts": "2026-10-14T18:03:11Z" },

    { "op_id": "019a…02", "kind": "complete", "entity": "task", "row_id": "018c…",
      "completion_id": "019a…c3", "occurred_on": "2026-10-14",
      "client_ts": "2026-10-14T18:04:02Z" },

    { "op_id": "019a…03", "kind": "create", "entity": "list_item", "row_id": "018d…",
      "fields": { "list_id": "018e…", "label": "Lait", "qty": 2 } }
  ] }
```

Bornes : **200 opérations ou 1 MiB**, au premier atteint, sinon `413` avec les bornes dans le
corps. La borne existe pour plafonner la durée pendant laquelle un lot tient le verrou du
foyer (~60 ms à 200 opérations), pas pour économiser du réseau.

**Chaque opération est sa propre transaction, appliquée dans l'ordre du tableau.** Pas une
transaction par lot : une opération refusée ne doit pas annuler les onze bonnes qui la
précèdent. C'est ce qui compte le jour du retour après dix jours.

Réponse `200` — **un résultat par opération, toujours, dans l'ordre de la requête** :

```json
{ "server_now": "2026-10-15T09:12:04Z", "head_seq": 48318,
  "results": [
    { "op_id": "019a…01", "outcome": "receipted", "code": "field_superseded",
      "fields": { "title": "receipt" }, "notice_id": "019b…" },
    { "op_id": "019a…02", "outcome": "applied", "rev": 48314,
      "series": { "advanced": true, "open_occurrence_id": "018c…08" } },
    { "op_id": "019a…03", "outcome": "applied", "rev": 48315 }
  ],
  "changes": [
    { "entity": "task", "row_id": "018c…", "rev": 48314, "deleted": false,
      "updated_by": "018b…", "updated_at": "2026-10-15T09:12:04Z",
      "state": { "kind": "task", "title": "Appeler le plombier", "due_on": "2026-10-16",
                 "series_id": null, "series_index": null, "overrides": [],
                 "completions": [ { "member_id": "018b…", "completion_id": "019a…c3",
                                    "occurred_on": "2026-10-14", "clock_suspect": false } ],
                 "tags": ["maison"], "ext": { "colour": "teal" },
                 "field_revs": { "title": 48211, "due_on": 48102 } } }
  ],
  "cursor_seq": 48318, "has_more": false, "purge_horizon_seq": 41000,
  "protocol_min": 1, "protocol_max": 2,
  "min_app_version": "1.2.0", "sunset_at": "2027-03-01T00:00:00Z" }
```

Règles qui comptent :

- `changes` est **toujours** trié par `rev` croissante, et le client avance `cursor_seq` à la
  dernière ligne **effectivement engagée dans la transaction SQLite**, jamais avant. Un
  plantage au milieu d'une page reprend là, sans page partielle à l'écran.
- `changes` **inclut les lignes que cet appareil vient d'écrire.** Pas de filtrage sur
  `device_id` : le serveur a pu réécrire l'intention (date dérivée, assigné figé par la
  rotation, borne sur `occurred_on`), et l'appareil doit adopter l'état autoritatif. La
  réception de ses propres écritures est ce qui vide `pending_fields`.
- `wait_ms` transforme l'appel en long poll : le serveur se gare jusqu'à ce que
  `change_seq > cursor_seq`, puis répond. `204` avec `Choreo-Head-Seq` si rien n'arrive.
- **Le gestionnaire relâche sa connexion `pgx` et sa transaction avant de se garer.** Un
  appel garé est une goroutine (~8 Ko), jamais une connexion `idle in transaction`. Se
  tromper là tue une machine à 2 vCPU vers trente foyers et bloque l'autovacuum.
- `cfg.ShutdownTimeout` vaut 15 s et `wait_ms` plafonne à **20 s** : à l'arrêt, tous les
  appels garés sont réveillés immédiatement avec `204`. Sans cette règle, chaque déploiement
  coupe un aller-retour en vol dans chaque foyer.
- `net/http` n'a pas de délai par route : **pas de `WriteTimeout` global** sous les 20 s, et
  un délai par gestionnaire via `http.ResponseController` sur les routes courtes.

Réveil : une connexion `pgx` dédiée fait `LISTEN choreo_changes` pour tout le processus, la
transaction qui valide fait `NOTIFY choreo_changes, '<household_id>'`, et une
`map[uuid][]chan struct{}` en mémoire diffuse. Pas de WebSocket : pas de trames, pas de
*ping/pong*, pas de machine à états de reconnexion, `curl`-able, et dégradation naturelle en
interrogation périodique sur un réseau hostile. **Elle a sa propre sonde** : la perte
silencieuse de cette connexion est la seule panne qui laisse tous les long polls expirer sans
rien casser d'autre, donc `sync_notify_connection_up` est une métrique, pas un détail.

Codes d'erreur de lot — les seuls cas où renvoyer le lot tel quel est sans espoir :
`401` jeton · `404` pas membre de ce foyer (ou foyer inexistant : **même réponse**) ·
`410 {"code":"cursor_too_old","purge_horizon_seq":41000}` · `413` lot trop gros ·
`426 {"code":"protocol_unsupported","protocol_min":1}`.

### 2. `GET /v1/households/{hid}/snapshot` — le chemin de remise à zéro

`200`, `application/x-ndjson`, `gzip`, diffusé en flux. **Exécuté dans une seule transaction
`REPEATABLE READ`, et `cursor_seq` est lu dans cette même transaction.** Sans cela, une
écriture validée en cours de flux est absente du corps tout en portant une `rev` inférieure
au curseur rendu : elle ne sera **jamais** relivrée, et l'appareil a tort sur son foyer pour
toujours, en silence.

```
{"type":"header","cursor_seq":48318,"purge_horizon_seq":41000,"tz":"Europe/Paris","protocol":1}
{"type":"member","row_id":"018b…","rev":40112,"state":{…}}
{"type":"task","row_id":"018c…","rev":48314,"state":{…}}
{"type":"end","rows":412}
```

Un `SELECT` sur les lignes vivantes, pas un repli de journal. **Pas de pierres tombales** :
on n'a pas besoin d'apprendre la suppression d'une ligne dont on n'a jamais entendu parler.
Un foyer réel : quelques centaines de kilo-octets.

Le snapshot est **monde clos** : à l'application, le client supprime localement toute ligne
d'une entité synchronisée absente du snapshot — **sauf celles que sa propre boîte d'envoi
référence encore**, conservées et marquées. C'est comme ça qu'une ligne supprimée il y a
100 jours finit par disparaître d'un téléphone longtemps absent, et c'est la raison pour
laquelle on peut se permettre de purger pour de vrai.

### 3. Comptes, foyers, appareils

```
POST   /v1/auth/register  {login, password}        -> {account_id, recovery_code, tokens}
POST   /v1/auth/token     {login, password} | {refresh_token}
POST   /v1/devices        {platform, app_version, protocol} -> {device_id}
POST   /v1/households     {name, tz}               -> {household_id, member_id, invite_code}
POST   /v1/invites/{code}/accept  {display_name}   -> {household_id, member_id, cursor_seq: 0}
GET    /v1/households                              -> les foyers de ce compte, un curseur chacun
DELETE /v1/devices/{id}                            -- révocation
GET    /v1/capabilities  -> {protocol_min, protocol_max, ops[], max_ops, max_bytes, sunset_at}
```

`cursor_seq: 0` veut dire « prends un snapshot d'abord ». Pas de code d'erreur pour un
appareil neuf : c'est l'état normal de départ.

### 4. Le vocabulaire des opérations — douze types, c'est toute la surface d'écriture

| `kind` | Arbitrage | Note |
|---|---|---|
| `create` | aucun | `row_id` frappé par le client ; `ON CONFLICT DO NOTHING` → `absorbed` |
| `set_fields` | causal, par champ | **ne peut jamais créer une ligne** |
| `delete` | causal, ligne | pierre tombale, colonnes conservées |
| `restore` | arbitré | règle du successeur si c'est une occurrence |
| `complete` | additif | ensemble par membre ; peut faire avancer une série |
| `uncomplete` | arbitré | retrait par observation ; règle du successeur |
| `tag_add` / `tag_remove` | additif / par observation | |
| `series_skip` | arbitré | « pas cette fois » : tombe l'occurrence **et** avance |
| `series_set_paused` | arbitré | la seule façon de mettre `open_occurrence_id` à `NULL` |
| `notice_resolve` | idempotent | répondre à une notice est une opération ordinaire, donc ça marche hors-ligne |
| `member_set_role` | arbitré | rôles, Phase 3 |

**Ce que le client n'a jamais le droit d'envoyer — et ce n'est pas une validation, c'est une
absence :** il n'existe aucune opération qui crée une occurrence, ni qui écrive
`series.next_index`, `series.open_occurrence_id`, `rev`, `field_revs`, `closed_rev`,
`updated_by` ou `updated_at`. Retirer la capacité est strictement plus fort que la valider,
et c'est moins de code.

Un corps qui porte `member_id`, `completed_by` ou `updated_by` est **refusé `malformed`**,
jamais ignoré : l'identité vient du jeton, point. Faire confiance au téléphone sur *qui* a
agi est plus dangereux que de lui faire confiance sur l'heure — c'est la faille qui ouvrait à
la fois le vol de points et le contournement complet du système de rôles dans une des
propositions examinées.

### 5. Métriques (§2.4)

`sync_ops_total{kind,outcome}` · `sync_refused_total{reason}` · `sync_notices_total{kind}` ·
**`sync_outbox_age_seconds`** · `sync_push_txn_seconds` (la queue du verrou de foyer) ·
`sync_snapshot_total` (une hausse dit qu'une *constante de conception* est mal réglée, pas
qu'un serveur va mal) · `sync_quarantine_total` · `sync_notify_connection_up`.

`sync_outbox_age_seconds` vient du champ `outbox_age_s` rapporté par le client, **calculé en
ticks monotones** et pas en heure murale : sinon l'indicateur que le §2.4 nomme explicitement
est mesuré avec l'horloge que tout ce document s'applique à ne pas croire.

---

## Règles de résolution

Le tableau fait foi. Chaque ligne est un test.

| Situation | Règle | Pourquoi |
|---|---|---|
| Écriture de champ, `base_rev == field_rev` | appliquée | l'auteur avait la valeur actuelle sous les yeux. **Plus de 99 % du trafic : il n'y a pas de conflit** |
| Champ jamais écrit (`field_rev` absent) | appliquée | il n'y a rien avec quoi entrer en conflit |
| `base_rev < field_rev`, ou `base_rev` absente alors que `field_rev` existe | **la valeur du serveur tient**, `outcome: receipted`, notice adressée à l'auteur avec son texte intact | premier-écrivain-gagne : une valeur vue synchronisée ne change plus sous nos yeux, et un téléphone qui refait surface après dix jours ne peut pas écraser la matinée du foyer. Personne ne perd son texte : la notice porte « Remettre la mienne » |
| `base_rev > field_rev` | `malformed` | impossible. C'est notre bug |
| Deux validations du même élément | **les deux sont conservées** | ensemble indexé par membre. Ce n'est pas un conflit, donc on ne le résout pas : on ne le crée pas |
| Dévalidation (`uncomplete`) | retire **exactement** les membres que l'auteur avait observés | décocher dans la cuisine n'effface pas une validation enregistrée au garage dix secondes plus tôt |
| Édition contre suppression | la suppression tient l'affichage, l'édition **n'est pas jetée**, notice `decision` adressée à **l'éditeur** | personne n'a tort. Mais le suppresseur a déjà ce qu'il demandait, donc une seule voix décide : celle dont le travail est en jeu. Deux voix, c'est une course |
| Suppression périmée (l'éditeur avait gagné) | `receipted`, notice à l'auteur de la suppression | une suppression détruit : elle demande, elle n'impose pas |
| `set_fields` sur une ligne inconnue | `refused: target_gone`, contenu conservé | **une écriture de champ ne crée jamais une ligne.** Sans cette règle, une ligne purgée ressuscite par morceaux, et la purge détruit la preuve dont l'arbitre a besoin |
| Deux téléphones ouvrent la prochaine occurrence | identité dérivée : **c'est la même ligne** | voir *L'invariant des séries* |
| Dévalider ou restaurer une occurrence dépassée | règle du successeur : annuler le successeur **intact**, sinon refuser avec une phrase | ré-ouvrir détruit un « fait » visible ; valider n'ajoute jamais que du bien |
| Deux personnes ajoutent « lait » | deux lignes | une liste n'est pas un ensemble de chaînes. Fusionner cache l'intention de l'une des deux ; une pression supprime un doublon. Le doublon est signalé **à la saisie**, par l'interface, jamais par une règle de fusion |
| Réordonner un article | `order_key` fractionnaire, champ causal ordinaire, égalité tranchée par `row_id` | les déplacements s'entrelacent au lieu de se battre |
| Rôle insuffisant, membre retiré, appareil révoqué | `refused: forbidden`, **jamais réécrit** | |
| Type d'opération inconnu du serveur | `refused: unsupported_op`, **réessayable, jamais terminal** | serveur plus vieux que l'app : avec la distribution APK directe (`D-006`), c'est banal, pas pathologique. Une erreur d'ordre de déploiement ne doit pas empoisonner pour toujours la file d'un vrai utilisateur |
| Schéma violé | `refused: malformed`, journalisé, **jamais montré** | c'est notre bug, pas le sien. L'utilisateur n'est pas notre supervision |
| `op_id` déjà vu, hachage identique | `absorbed`, résultat stocké rendu | idempotence de bout en bout |
| `op_id` déjà vu, hachage différent | `malformed` | sans ça, une mutation disparaît sans trace |

**Ce que ce tableau ne contient pas, délibérément :** aucune comparaison d'horodatage, aucune
fusion de texte à trois voies, aucun vecteur de versions. Un titre de tâche n'est pas un
document partagé : si deux personnes écrivent par-dessus, une version part dans une notice
avec un bouton, et c'est tout.

---

## Les sept cas

### 1. Deux adultes cochent la même corvée, tous les deux hors-ligne

Les deux téléphones envoient `complete` avec un `completion_id` distinct. **Il n'y a pas de
conflit, parce qu'une validation n'est pas un registre : c'est un ensemble indexé par
membre.** Camille arrive d'abord → `completions = {Camille}` ; Jordan arrive ensuite →
`completions = {Camille, Jordan}`. Les deux sont `applied`, chacune incrémente la `rev` de la
tâche, et les deux téléphones convergent sur « Fait — Camille et Jordan ». Aucune boîte de
dialogue, aucune notice, rien à l'écran.

Les points (Phase 3) sont dédoublonnés sur `completion_id`, jamais sur l'appareil : la même
corvée cochée sur le téléphone **et** sur la tablette de la même personne est une seule
validation (même clé `(row_id, member_id)`), donc un seul point. Et un snapshot suivi d'un
rejeu ne peut pas doubler le score, parce que la clé de dédoublonnage voyage *dans* l'état de
la tâche.

Si l'un coche et l'autre « passe » (`series_skip`), c'est la validation qui tient : faire bat
sauter, et le saut reste visible dans l'historique de la ligne.

*Ce qui est perdu* : rien. C'est le cas que le cahier des charges nomme comme impardonnable
(principe 6), et il est traité en **n'ayant pas de conflit**, pas en en résolvant un.

### 2. L'un renomme la tâche, l'autre la supprime

Les deux opérations portent une base causale. L'ordre d'arrivée décide lequel des deux
scénarios se joue, et ils sont symétriques.

**2a — la suppression arrive d'abord.** `delete` s'applique : `deleted_rev` est posé, **les
colonnes de contenu sont conservées** (c'est porteur, voir plus bas). Le `set_fields` arrive
ensuite : la ligne existe mais est tombée, et l'auteur ne pouvait pas savoir
(`base_rev < deleted_rev`). Le champ **n'est pas écrit** — écrire sous une pierre tombale ne
sert personne — et le serveur crée une notice `decision` adressée à **Jordan, l'éditeur** :

> « Camille a supprimé « Appeler le plombier » pendant que tu en changeais le titre.
> Ton titre : « Appeler le plombier (devis) ». »
> **[ Restaurer avec mon titre ]**   **[ Laisser supprimée ]**

« Restaurer » est une opération ordinaire (`restore` puis `set_fields`) qui remet la ligne
avec le titre de Jordan. **C'est pour ça que `delete` conserve ses colonnes** : la
récupération est une pression, et l'utilisateur *voit* ce qu'il récupère.

**2b — l'édition arrive d'abord.** Le titre est écrit. La suppression arrive périmée :
`outcome: receipted`, et c'est Camille qui reçoit la notice — « Jordan a renommé cette tâche
pendant que tu la supprimais » / **[ Supprimer quand même ]** / **[ Garder ]**.

*Pourquoi une seule personne décide* : en 2a, le suppresseur a déjà obtenu ce qu'il
demandait. Adresser la décision à tout le foyer crée une course où deux réponses
contradictoires arrivent et l'une est jetée en silence. `notice_resolve` est idempotente :
une seconde réponse rend `absorbed` avec le choix qui a tenu, et l'interface dit « Camille a
déjà répondu : gardée ».

*Pourquoi pas « l'édition ressuscite la ligne »* : un téléphone absent dix jours
ressusciterait alors tout ce que le foyer a délibérément supprimé, à chaque synchronisation,
pour toujours. Suppression-gagne plus une restauration en une pression tient la
bienveillance sans zombies.

*L'arête, traitée* : la purge ne supprime **jamais** une ligne tombée qu'une notice non
résolue référence encore (`WHERE NOT EXISTS`, voir *Pierres tombales*). Sans cela, le bouton
« Restaurer » cible une ligne disparue et devient une boucle de notices.

### 3. Une série, deux téléphones hors-ligne, exactement une occurrence ouverte

Démonstration complète dans *L'invariant des séries*. Le déroulé :

Camille coche l'occurrence #7 dans une cave, Jordan coche la même #7 dans le métro. Les deux
téléphones prédisent localement #8, dont l'identifiant est
`uuidv5(ns, series_id || ':' || 8)` — **le même sur les deux appareils**, parce qu'il dérive
d'un entier et non d'une date.

- `complete(#7)` de Camille : validation enregistrée, puis l'avancée, gardée par
  `WHERE open_occurrence_id = #7` → 1 ligne → `next_index` passe à 8, #8 est créée, le
  registre pointe sur #8. Résultat : `applied`, `series.advanced: true`.
- `complete(#7)` de Jordan : validation enregistrée **aussi** (les deux gardent le crédit),
  puis l'avancée, gardée par `WHERE open_occurrence_id = #7` → **0 ligne** → pas d'avancée.
  Résultat : `applied`, `series.advanced: false`, `open_occurrence_id: #8`.

Zéro ligne mise à jour n'est pas une erreur : rien n'est annulé, et la validation de Jordan
n'est jamais détruite par un échec de contrainte. C'est exactement le mécanisme qui manquait
aux propositions examinées, où la seconde validation faisait avancer le compteur deux fois,
ou bien faisait échouer la transaction qui la portait.

Le #8 prédit par Jordan **est** le #8 autoritatif : même identifiant, donc l'état serveur le
remplace. Pas de ligne orpheline, pas de doublon, rien à ramasser. Si la date dérivée diffère
de celle qu'il avait affichée, elle est corrigée en silence : déplacer une date calculée par
la machine n'est pas perdre le travail d'un humain.

### 4. Liste de courses, les deux téléphones dans le rayon, « quasi temps réel »

Aucun mécanisme nouveau. Trois propriétés existantes font tout le travail.

**Ajouter** : `create` avec un `row_id` uuidv7 frappé par le client. Deux personnes qui
ajoutent « lait » obtiennent **deux lignes**, et c'est correct : une liste n'est pas un
ensemble de chaînes, et fusionner cacherait l'intention de l'une des deux. Le doublon est
signalé **à la saisie** par l'interface (« tu as déjà « Lait » dans la liste »), ce qui est
une affordance, pas une règle de fusion. Les identifiants adressés par le contenu, qui
rendaient cet ajout idempotent, ont été écartés : ils font réapparaître un article réajouté
**déjà coché**, sur l'écran le plus utilisé du produit.

**Cocher** : `complete` sur l'article, le même mécanisme que pour une tâche. Un article est
coché si et seulement si son ensemble de validations n'est pas vide. Donc une coche venue du
rayon voisin survit à une décoche qui ne l'avait jamais vue : **coche-gagne**, qui est le bon
défaut ici — au pire on décoche à nouveau, alors que le mauvais cas est de passer devant les
yaourts parce que la décoche périmée de quelqu'un a gagné. `uncomplete` ne retire que ce
qu'il a observé, ce qui ferme, pour les listes comme pour les tâches, le trou par lequel une
coche périmée recoche un article quatre jours plus tard.

**Réordonner** : `order_key` fractionnaire, champ causal ordinaire. Les déplacements
s'entrelacent.

**Latence** : l'écriture locale est validée dans Drift et repeinte **avant** tout réseau,
donc la latence perçue est nulle, en ligne comme hors-ligne. Tant que l'écran de liste est au
premier plan, l'app tient un `sync` avec `wait_ms = 20000`, réveillé par `NOTIFY` dès la
validation de l'autre téléphone : un aller-retour, de l'ordre de 200 à 400 ms. En
arrière-plan, pas de long poll (Android le tue, et `D-010` exclut un service de push) : une
tâche périodique toutes les 15 minutes.

*Dit franchement* : deux téléphones dans le même supermarché **sans réseau ne se
synchronisent pas**. Pas de LAN, pas de Bluetooth, pas de découverte locale. Hors-ligne veut
dire *utilisable*, pas *partagé* — et le §3.2 met ce scénario en vitrine, donc il faut le
dire plutôt que l'habiller.

### 5. Un téléphone revient après dix jours

Ordre fixe, non négociable : **tirer d'abord, valider la file localement, pousser ensuite.**
Se réconcilier avec le réel avant d'affirmer quoi que ce soit.

1. `sync` avec `cursor_seq` et `ops: []`. Au-dessus de `purge_horizon_seq` — le cas normal,
   la fenêtre est de 90 jours — le serveur renvoie **l'état actuel des lignes qui ont
   changé**, pas dix jours d'opérations. Dix jours d'un foyer actif : quelques centaines de
   lignes, deux ou trois pages. En dessous : `410`, snapshot, **la boîte d'envoi n'est pas
   touchée**.
2. Appliquer page par page, ligne par ligne, en avançant `cursor_seq` **à la validation de la
   transaction SQLite**. Une ligne illisible va en `quarantine` et n'empêche pas le curseur
   d'avancer.
3. **Pré-valider la file localement, contre l'état qui vient d'arriver.** C'est l'étape qui
   enlève le plus de douleur pour le moins de code : la tâche à renommer est supprimée,
   l'article à cocher a disparu, l'occurrence à ouvrir existe déjà. Ces opérations sont
   abandonnées **localement**, sans déranger le serveur : moins de refus, moins de cartes
   inquiétantes, moins de travail pour 2 vCPU.
   **Et la limite de l'étape, écrite noir sur blanc : elle ne réécrit JAMAIS une `base_rev`
   périmée vers la valeur actuelle.** Faire monter la base, c'est faire accepter au serveur
   une écriture fondée sur un écran que l'utilisateur n'a jamais vu. L'opération part telle
   qu'elle a été écrite, elle reçoit son reçu, et on demande à l'humain. C'est toute la
   philosophie en un `if`, et c'est la première chose qui sautera sous la pression du
   calendrier, parce que ça fait tomber le compteur de conflits à zéro.
4. **Compacter la file localement** (ce qui tourne aussi en continu hors-ligne) :
   - plusieurs `set_fields` sur le même champ de la même ligne fusionnent en **une** opération
     qui garde **la `base_rev` la plus ancienne** — celle que l'utilisateur a réellement vue —
     et **la dernière valeur**. Sans cette règle, la deuxième frappe de l'utilisateur perd
     contre sa propre première frappe et il reçoit un reçu de lui-même : la panne la plus
     absurde trouvée dans les propositions examinées ;
   - créer-puis-supprimer un identifiant jamais synchronisé s'annule et n'atteint jamais le
     serveur ;
   - les va-et-vient de coche s'effondrent.

   Dix jours de 3 000 gestes deviennent quelques centaines d'opérations.
5. Pousser par lots de ≤ 200, dans l'ordre de `lseq`, **un seul lot en vol à la fois**.
6. Appliquer les `changes` du dernier lot : c'est la vue du serveur sur nos propres
   écritures, et c'est ce qui vide `pending_fields`.

Ce que l'utilisateur voit : « Mise à jour… 312 modifications », puis son foyer tel qu'il est,
puis **au plus une ligne** : « 3 de tes modifications sont à revoir ». L'écran de la boîte
d'envoi affiche en permanence « 14 en attente, la plus ancienne depuis 10 jours » — c'est
l'indicateur « âge maximal de la file hors-ligne » du §2.4, montré à l'utilisateur et pas
seulement à l'exploitant, parce qu'une file silencieusement bloquée est la panne qui fait
partir les gens.

*Les deux arêtes, traitées.* Une erreur serveur ou un délai au milieu d'un lot n'est **pas**
un refus : l'opération reste `queued` — il existe une transition explicite
`inflight → queued` au démarrage de l'app, sinon une app tuée au mauvais moment gèle la file
— et elle repart en *backoff* exponentiel de 1 s à 5 min, mesuré en **ticks monotones** : un
changement d'horloge ne doit pas rendre le *backoff* long de quarante ans. Et
`SET LOCAL statement_timeout = '3s'` s'applique aussi à l'attente du verrou : un dépassement
est une erreur de transport, pas un refus, donc l'opération est conservée.

### 6. Une opération en file est refusée

Détail et libellés dans *Mutations refusées*. L'essentiel : **cinq issues, et deux seulement
atteignent l'utilisateur.** `applied`, `absorbed` et `superseded` sont silencieuses.
`receipted` est une ligne descartable. `refused` est la seule qui demande quelque chose, et
elle ne le demande que pour `forbidden`, `target_gone` et `successor_in_use` — tout le reste
est notre bug et se règle entre nous.

### 7. Le même utilisateur sur un téléphone et une tablette

Ça tombe sans aucun cas particulier, parce que **l'unité de synchronisation est l'appareil,
pas la personne.** Deux lignes `devices`, deux lignes `device_cursors`, deux boîtes d'envoi.
Les deux appareils sont des pairs ordinaires du foyer qui partagent un identifiant de membre.
Cinq conséquences qui méritent d'être construites :

- `changes` **n'est pas filtré** par appareil : la tablette applique l'opération du téléphone
  par le même chemin que celle de n'importe qui. Pas de raccourci « c'est la mienne, je
  passe » — c'est le raccourci qui finit par diverger.
- Une validation est indexée `(row_id, member_id)` : la même personne qui coche sur les deux
  appareils produit **une** validation, donc **un** point. Jamais « coché par Camille et
  Camille ».
- Les notices sont **adressées à un membre et stockées sur le serveur** : résolue sur la
  tablette, la carte disparaît du téléphone. Et le contenu refusé est sur le serveur, donc
  « Renvoyer » et « Copier » marchent sur la tablette même si le téléphone est perdu ou
  réinitialisé. C'est le trou que les quatre propositions avaient en commun : le contenu
  refusé vivait uniquement sur l'appareil qui peut disparaître.
- Le libellé doit tester `by_member == moi` et dire **« Modifié sur ton autre appareil »**,
  jamais « Camille a modifié… » quand Camille est vous. Se faire annoncer un conflit avec
  soi-même par son propre prénom est ce qui fait qu'une app a l'air bête.
- `device_cursors.last_seen_at` permet de dire « ta tablette n'a pas synchronisé depuis
  12 jours », la seule façon honnête d'expliquer pourquoi elle affiche autre chose.

La déconnexion garde la réplique locale et arrête la synchronisation ; seul un « Retirer cet
appareil » explicite l'effface. Un téléphone est une réplique complète, et l'argument de
réversibilité de `D-005` en dépend.

---

## Horloges

**Règle en une ligne : aucun horodatage d'appareil n'est jamais comparé à un autre
horodatage pour décider quoi que ce soit.** Il n'y a pas de dernière-écriture-gagne dans ce
protocole, donc il n'y a aucune dispute qu'une horloge fausse pourrait gagner.

**L'ordre vient de trois entiers, et aucun n'est une horloge.**

| Entier | Portée | Attribué par |
|---|---|---|
| `households.change_seq` | le foyer | PostgreSQL, sous verrou de ligne, dans la transaction qui écrit |
| `rev` / `field_revs[f]` | la ligne, le champ | copie de `change_seq`. C'est tout le détecteur de conflit |
| `outbox.lseq` | l'appareil | `INTEGER PRIMARY KEY AUTOINCREMENT` de SQLite |

**La détection de conflit est causale.** `base_rev` répond à « qu'avais-tu sous les yeux ? »,
qui est une position dans le journal du foyer, pas un instant. C'est strictement plus fort
qu'un horodatage ici, et c'est immunisé contre un combiné réglé sur 1997 comme sur 2031.

**Ce que le serveur fait de l'horloge du téléphone.** `updated_at`, `deleted_rev`,
`done_rev` et `closed_rev` sont écrits par le serveur ; le corps de la requête **n'a pas de
champ pour eux**, et en envoyer un est `malformed`, pas ignoré, pour qu'un bug client soit
bruyant. `client_ts` est stocké dans `ops` et dans la validation, affiché (« Jordan a coché
ça hier soir »), et **ne décide de rien**. Au-delà de 24 h d'écart avec l'heure serveur
observée, la validation est marquée `clock_suspect` et l'interface montre l'heure serveur,
sans accusation et sans erreur : l'horloge fausse d'un enfant ne doit jamais l'empêcher
d'enregistrer une corvée, et le principe 6 interdit de lui en faire porter la faute.

**Les dates voulues par un humain sont des dates, pas des instants.** `due_on date` +
`due_time time`, interprétés dans `households.tz` (IANA). « 16 octobre » veut dire 16
octobre, quoi que croie le téléphone. Ça retire l'heure d'été et les voyages de tout le
problème, ce qui vaut plus que ça n'en a l'air — et c'est pourquoi le fuseau est sur le
foyer, pas sur l'appareil.

**Le seul endroit où le serveur consulte son horloge pour écrire une donnée : la borne sur
`occurred_on`.** Le client **propose** la date à laquelle la corvée a été faite — un humain
est debout devant l'évier en train d'appuyer sur « Fait » — et le serveur la **borne** à
`[server_today - 14 jours, server_today]`, en marquant `clamped: true` si elle a bougé. Deux
raisons, et la seconde est la plus importante :

1. un combiné réglé sur 2031 ne peut pas pousser une corvée en 2031 ;
2. **l'arithmétique d'une série `relative` (« X jours après la dernière fois », §3.2) part de
   `occurred_on`, jamais de l'heure de réception.** Sinon une chaudière cochée vendredi dans
   une cave et synchronisée lundi décale la suivante de trois jours — **à chaque cycle**,
   cumulativement, causé par exactement l'usage hors-ligne dont le produit se réclame.

> Le sens de la borne est `min`, pas `max`. Écrite dans l'autre sens, la borne laisse passer
> 2031 au lieu de le ramener à aujourd'hui. L'une des propositions examinées avait cette
> ligne inversée **dans la section qui expliquait pourquoi elle protégeait**. Un test nommé
> en porte la charge : `TestOccurredOnFromAFutureHandsetIsClampedToToday`.

**Là où l'horloge compte vraiment : les rappels locaux.** `D-010` calcule J-60 / J-30 / J-7
sur l'appareil, donc une horloge fausse les fait sonner le mauvais jour. Chaque réponse porte
`server_now`, et le client conserve **deux** ancrages :

- `server_mono_anchor_ms` + `boot_id` : précis, mais mort au redémarrage ;
- `server_wall_offset_ms = server_now - wall_now` : moins précis, mais **il survit au
  redémarrage**.

`now()` prend le premier si `boot_id` est inchangé, sinon le second, sinon l'horloge brute, en
le disant à l'écran. Un téléphone qui n'a jamais synchronisé n'a rien de mieux que son
horloge, et l'écran des rappels le dit en une ligne. Couvrir « jamais synchronisé » et
oublier « synchronisé, puis redémarré » est le défaut qu'avaient les propositions examinées :
après un redémarrage, l'ancrage monotone est nul et tout retombe sur l'horloge que l'on
refuse de croire.

Au-delà de 24 h d'écart, une ligne discrète dans les réglages : « L'heure de cet appareil est
décalée de 3 jours. Tes rappels risquent de sonner au mauvais moment. » Le dire est
bienveillant ; corriger son horloge en silence ne l'est pas.

**Ce qu'on ne défend pas** : un membre malveillant *dans son propre foyer* qui mentirait sur
`client_ts` pour que l'interface dise qu'il a fait la vaisselle hier soir. La tromperie
d'affichage entre gens qui vivent ensemble est un problème social ; `ops` garde le vrai
`received_at` et le `device_id` à côté.

---

## L'invariant des séries

« Une seule occurrence ouverte par série » (glossaire §4) est une contrainte **entre
lignes**. Aucune règle de fusion par ligne — dernière-écriture-gagne, CRDT ou autre — ne sait
l'exprimer : la jointure de {A ouverte} et {B ouverte} est {A ouverte, B ouverte}. Donc
l'invariant ne vit pas dans la fusion. **Il vit dans une colonne.**

### L'état, en une phrase

`series.open_occurrence_id` désigne **toujours** exactement une ligne `tasks`, et vaut `NULL`
**si et seulement si** la série est `paused` ou `deleted_rev IS NOT NULL`. Ce n'est pas « au
plus une » : c'est « exactement une ». La différence est la raison pour laquelle une des
propositions examinées laissait une corvée récurrente mourir en silence, sans que personne ne
soit prévenu.

### Couche 0 — l'identité dérive d'un entier, donc le cas courant n'est pas un conflit

```
occurrence_id = uuidv5(NS_CHOREO_OCCURRENCE, series_id || ':' || series_index)
series_index  = series.next_index          -- un entier. Rien d'autre
```

Deux téléphones hors-ligne qui ouvrent la prochaine occurrence calculent **le même
identifiant**, donc l'insertion du second est un `ON CONFLICT DO NOTHING`. Il n'y a rien à
fusionner, rien à arbitrer, aucun vote.

**Et l'identité dépend d'un incrément d'entier, pas d'une fonction de créneau.** C'est la
correction la plus importante par rapport aux propositions examinées, qui dérivaient
l'identifiant d'une date calculée par du code applicatif : la moindre évolution de cette
arithmétique — ou un simple écart de base `tzdata` entre une app N-1 et le serveur Go —
faisait frapper deux identifiants différents pour la même occurrence, et l'invariant cassait
**pendant un déploiement progressif**, c'est-à-dire au pire moment et chez certains foyers
seulement. L'addition d'entiers n'a pas de versions. La **date** de l'occurrence est un champ
dérivé ordinaire, corrigible en silence ; l'**identité**, non.

### Couche 1 — chaque transition est un compare-et-échange sur une colonne

Toutes les transitions ont la même forme, sous le verrou du foyer :

```sql
UPDATE series
   SET next_index         = next_index + 1,
       open_occurrence_id = $derived_next,
       rev                = $seq
 WHERE household_id = $hh AND id = $sid
   AND open_occurrence_id = $expected          -- LE compare-et-échange
   AND paused = false AND deleted_rev IS NULL
RETURNING next_index, open_occurrence_id;
```

| Geste | `$expected` | 1 ligne mise à jour | 0 ligne mise à jour |
|---|---|---|---|
| `complete` d'une occurrence | l'occurrence cochée | avance, crée la suivante | **la validation est conservée**, `advanced: false`, rien d'autre |
| `series_skip` (« pas cette fois ») | l'occurrence ouverte | la tombe **et** avance | `advanced: false` |
| `delete` de l'occurrence ouverte | l'occurrence ouverte | identique à `series_skip` | — |

**Zéro ligne mise à jour n'est pas une erreur.** Rien n'est annulé, rien n'échoue, et surtout
la validation humaine qui a déclenché le geste n'est **jamais** détruite par un échec de
contrainte. Les propositions examinées plaçaient la création de l'occurrence et la validation
dans la même transaction sans garde : une violation d'unicité annulait la coche de l'humain
**et** son enregistrement, sous une issue déclarée « jamais montrée à l'utilisateur ». Une
corvée faite disparaissait sans un mot.

**Et supprimer l'occurrence ouverte fait avancer la série.** C'est ce qui rend l'invariant
exact plutôt que « au plus une » : « on saute cette semaine » ne peut pas laisser une série
sans occurrence ouverte. Il n'y a donc **aucun balayeur** qui régénère ce que l'utilisateur a
supprimé — le mécanisme qui, dans une des propositions examinées, ressuscitait toutes les 15
minutes, pour toujours, la corvée que l'utilisateur venait d'écarter.

### Couche 2 — la règle du successeur, pour les deux gestes qui détruisent un « fait »

`uncomplete` qui vide l'ensemble de validations d'une occurrence, et `restore` d'une
occurrence tombée, ré-ouvriraient une occurrence que la série a dépassée. Les deux passent par
la même règle, sous le verrou :

1. l'occurrence visée **est** `open_occurrence_id` → rien à arbitrer, le geste s'applique ;
2. sinon, si le successeur est **intact** — `rev == created_rev`, `overrides` vide, aucune
   validation — il est annulé : suppression dure du successeur, `next_index` revient en
   arrière, le registre repointe sur l'occurrence visée. C'est exactement ce que
   l'utilisateur veut dire par « je me suis trompé » ;
3. sinon → `refused: successor_in_use`, avec une phrase actionnable et sans jargon :
   « Impossible de revenir : la prochaine occurrence a déjà été modifiée. »

Refuser une **dé**validation est acceptable là où refuser une validation ne l'est jamais :
dévalider détruit un « fait » visible par tout le foyer, valider n'ajoute que du bien. C'est
la seule frontière de ce document où une opération liée à une validation peut être refusée, et
elle est tracée exprès à cet endroit.

### Couche 3 — l'index partiel, garde-fou contre mes propres bugs

```sql
CREATE UNIQUE INDEX tasks_one_open_per_series ON tasks (household_id, series_id)
  WHERE series_id IS NOT NULL AND deleted_rev IS NULL AND closed_rev IS NULL;
```

Il n'est **jamais** le mécanisme : le compare-et-échange de la couche 1 fait qu'il ne peut pas
déclencher en fonctionnement normal. Il est là pour le seul bug qui compte réellement pour un
développeur seul : le mien, à 2 h du matin. Une violation sort en
`refused: invariant_violation`, est comptée dans `sync_refused_total`, journalisée avec
l'`op_id` et le payload — **et non pas sous la forme d'un foyer qui acquiert discrètement deux
« sortir les poubelles »**. Index partiel plutôt que contrainte `EXCLUDE` : le texte d'erreur
est lisible dans un journal.

### La preuve

1. `open_occurrence_id` est écrit **uniquement** par la forme de requête de la couche 1 ou de
   la couche 2, **uniquement** sous le verrou de ligne du foyer, donc les écritures d'un foyer
   sont totalement ordonnées. Aucune opération cliente n'existe pour l'écrire (§ vocabulaire).
2. Chaque écriture est de la forme `WHERE open_occurrence_id = $expected`. Deux transactions
   concurrentes partant du même `$expected` sont sérialisées par le verrou ; la seconde voit la
   valeur déjà déplacée, met à jour 0 ligne, et n'écrit pas.
3. L'état initial a une occurrence ouverte (créer une série la crée). Chaque transition est
   ouverte → ouverte (couche 1 : la suivante ; couche 2 : la précédente), ou ouverte → `NULL`
   (`series_set_paused`, suppression de la série), ou `NULL` → ouverte (reprise). Aucune
   transition ne produit deux ouvertes, et aucune ne produit `NULL` sans que la série soit en
   pause ou supprimée.
4. Par récurrence sur la suite des transactions validées du foyer, le cardinal de l'ensemble
   des occurrences ouvertes d'une série vaut 1 si elle est active, 0 sinon. ∎

### Ce que ça coûte, et ça ne se négocie pas en silence

**L'occurrence suivante est dérivée par le serveur.** Le téléphone la **prédit** localement —
identifiant dérivé, date dérivée, assigné dérivé de la rotation — et l'affiche `provisional`
avec une ligne discrète : « Prochaine : à confirmer à la synchronisation ». L'accusé de
réception de `complete` porte `open_occurrence_id` : le client supprime toute occurrence
provisoire qui n'est pas celle-là. Dans le cas écrasant, la prédiction *est* la vraie ligne,
parce que les deux dérivent du même entier.

`catch_up` est à **`skip`** par défaut : un téléphone absent dix jours sur une série
quotidienne ne génère **pas** dix occurrences. La série reprend à la prochaine échéance et
l'app ne vous accueille pas avec neuf jours de poubelles manquées (principe 6).
`accumulate` existe pour les séries où les manques comptent (relevés de compteur) et n'est pas
le défaut.

Et une conséquence à assumer : **un foyer dont aucun téléphone n'atteint le serveur pendant un
mois ne génère aucune occurrence.** Les corvées n'ont simplement pas été suivies. C'est le bon
arbitrage pour la bienveillance et pour une machine à 4 Go, mais cela veut dire que le serveur
ne peut pas répondre seul à « qu'est-ce qui est dû aujourd'hui », ce qui ferme la porte à la
tablette murale de la V2 sans mécanique nouvelle.

---

## Pierres tombales

**Un seul réglage : `CHOREO_RETENTION_DAYS`, défaut 90.** Un seul nombre pour le journal, les
pierres tombales et le registre `ops`. Un seul, parce qu'avec trois quelqu'un finit par les
régler de façon incohérente et une tâche supprimée ressuscite.

**Pourquoi 90.** Le pire cas réel est des vacances scolaires plus une tablette oubliée dans un
tiroir : des semaines, pas des mois. 30 jours forceraient un snapshot à un téléphone qui a
passé août dans un sac. Le coût est négligeable : quelques milliers de lignes et un à deux
mégaoctets par foyer sur le NVMe de 60 à 80 Go du §0.2. Et le coût de se tromper vers le bas
est une corvée supprimée qui revient d'entre les morts dans une liste partagée, ce qui détruit
la confiance d'une façon qu'aucune facture de stockage n'égale.

**Une pierre tombale est une colonne, pas une table.** `deleted_rev` sur chaque table
synchronisée, les colonnes de contenu **conservées**. Rien à joindre à 2 h du matin, et
`SELECT * FROM tasks WHERE id = …` raconte toute l'histoire, y compris comment la chose
s'appelait. Les retraits dans un ensemble — une validation retirée, une étiquette enlevée —
n'ont besoin d'aucune pierre tombale : le retrait par observation nomme exactement les
éléments qu'il a vus.

**La purge, une tâche nocturne, une transaction, deux requêtes :**

```sql
-- 1. retirer les lignes mortes, SAUF celles qu'une notice non résolue référence encore
WITH purged AS (
  DELETE FROM tasks t
   WHERE t.household_id = $1
     AND t.deleted_rev IS NOT NULL
     AND t.updated_at < now() - ($2 || ' days')::interval
     AND NOT EXISTS (SELECT 1 FROM notices n
                      WHERE n.household_id = t.household_id AND n.row_id = t.id
                        AND n.resolved_rev IS NULL)
  RETURNING rev)
-- 2. publier le nouvel horizon dans la MÊME transaction
UPDATE households SET purge_horizon_seq =
         GREATEST(purge_horizon_seq, COALESCE((SELECT max(rev) FROM purged), 0))
 WHERE id = $1;

DELETE FROM ops WHERE household_id = $1
   AND received_at < now() - ($2 || ' days')::interval;
```

L'ordre compte : l'horizon monte dans la transaction qui supprime, donc aucun appareil ne peut
observer un curseur qui n'est ni servable ni déclaré trop vieux. La même tâche tourne pour
`series`, `lists`, `list_items`, `memos`, `members` et `notices` résolues.

**Deux arêtes que la purge aurait pu ouvrir, et pourquoi elles sont fermées.**

- *Clés étrangères.* La purge est une suppression dure, et une cascade emporterait des lignes
  synchronisées **sans incrémenter `change_seq`** : aucun téléphone n'en serait jamais
  informé, divergence permanente, et aucun refus nulle part. Donc : **aucune clé étrangère
  entre deux tables synchronisées.** `assignee_id`, `category_id`, `series_id`, `list_id` sont
  des UUID nus, pas des références. L'intégrité référentielle entre lignes synchronisées est
  tenue par l'arbitre et par l'écran (« Sans catégorie »), jamais par `ON DELETE`.
- *Résurrection par morceaux.* `set_fields` ne peut jamais créer une ligne (tableau des
  règles). Donc une écriture périmée visant une ligne purgée rend `target_gone` avec son
  contenu conservé, et non pas une tâche à demi reconstituée avec un seul champ rempli. C'est
  **cette** règle qui rend la purge sûre, pas la longueur de la fenêtre.

**La purge suit l'horloge de rétention, pas l'appareil le plus lent.** Un téléphone mort dans
un tiroir ne peut pas épingler le journal d'un foyer pour toujours ; il paye par un snapshot à
son réveil. Les curseurs des appareils sont rapportés et tracés (`last_seen_at`), mais ils **ne
commandent pas** la suppression — le choix inverse est la façon classique dont un disque se
remplit à cause d'un seul combiné perdu.

**Le téléphone qui revient après la fenêtre.**

1. `sync` → `410 {"code":"cursor_too_old","purge_horizon_seq":41000}`.
2. Le client **ne vide pas sa boîte d'envoi.** Jamais. « Ton curseur a expiré » ne peut pas
   vouloir dire « tes quatorze modifications hors-ligne sont perdues » — c'est précisément là
   que les conceptions naïves perdent le travail de l'utilisateur.
3. `GET /snapshot` dans un schéma d'attente, bascule, `cursor_seq` = celui de l'en-tête.
4. Application **monde clos** : toute ligne absente du snapshot est supprimée localement —
   **sauf celles que la boîte d'envoi référence encore**, conservées et marquées. C'est comme
   ça qu'une suppression vieille de 100 jours, dont la pierre tombale a elle-même été purgée,
   finit par disparaître.
5. Pré-valider, compacter, pousser (cas 5). Les opérations qui portent du contenu humain —
   une validation, un titre, une note, un nouvel article — **partent quand même** : le serveur
   les applique, ou les convertit en notices.
6. L'utilisateur voit une ligne : « Ce téléphone était hors-ligne depuis le 5 juillet. Tout a
   été remis à jour. » plus le badge « À revoir » si l'étape 5 a produit des cartes. Pas une
   déconnexion, pas une réinitialisation, pas une liste vide.

**Franchement, au cas extrême.** Un téléphone trois mois hors-ligne dans un foyer qui a
beaucoup bougé peut produire beaucoup de cartes. La parade est la compaction locale (3 000
gestes → quelques centaines d'opérations) et le regroupement des cartes par entité, pas une
règle de fusion plus intelligente. Un téléphone absent **plus longtemps** que la fenêtre dans
un foyer reconstruit autour de lui est une réinstallation, et l'app le dit plutôt que de faire
semblant.

**Suppression de compte (RGPD, Phase 4)** purge `ops` et les notices du membre en plus des
lignes du domaine, ce qui invalide tous les curseurs du foyer et force un snapshot sur chaque
appareil. À concevoir maintenant, pas à découvrir en Phase 4.

---

## Mutations refusées

**Premier principe : un refus d'une intention humaine est toujours montré, avec le contenu
intact ; un refus portant sur une ligne générée par la machine n'est jamais montré.** Cette
seule ligne décide de tous les cas ci-dessous, et c'est la forme opérationnelle du principe 6.

### Les cinq issues

| `outcome` | Quand | Ce que devient l'opération | Ce que l'utilisateur voit |
|---|---|---|---|
| `applied` | arbitrée et écrite | retirée de la file | son geste, déjà à l'écran |
| `absorbed` | `op_id` déjà vu avec le même hachage ; `create` d'un `row_id` existant | retirée, résultat stocké rendu | rien |
| `superseded` | une ligne **générée par la machine** a perdu une course (occurrence, date dérivée) | retirée, la ligne provisoire locale est supprimée | **rien.** Aucune excuse à faire pour une date recalculée |
| `receipted` | valeur de champ concurrente (premier-écrivain-gagne), suppression périmée | retirée, **notice créée** | une ligne descartable dans « À revoir » |
| `refused` | les cinq raisons ci-dessous | retirée de la file, **notice créée** sauf `malformed` | une carte, ou rien si c'est notre bug |

### Les cinq raisons de refus

| `reason` | Cause | Terminal ? | Montré ? |
|---|---|---|---|
| `target_gone` | la ligne visée a été supprimée, ou purgée, pendant l'absence | oui | **oui**, si l'opération portait du contenu humain |
| `forbidden` | rôle insuffisant, membre retiré du foyer, appareil révoqué | oui | **oui** |
| `successor_in_use` | règle du successeur (séries) | oui | **oui**, une phrase, zéro jargon |
| `unsupported_op` | serveur plus vieux que l'app | **non — reste en file, en *backoff*** | non |
| `malformed` | schéma violé, `op_id` réutilisé, champ réservé envoyé | oui | **non. C'est notre bug** |

`unsupported_op` n'est **pas** terminal, et c'est une correction délibérée. Avec la
distribution APK directe et le dépôt F-Droid (`D-006`), il n'y a pas de déploiement progressif
de magasin : un téléphone portant un APK plus récent que le serveur déployé est banal. Rendre
ce cas terminal — ce que faisait une des propositions examinées — veut dire qu'une erreur
d'ordre de déploiement empoisonne définitivement la file de vrais utilisateurs. Elle reste en
file, repart en *backoff*, et `sync_refused_total{reason="unsupported_op"}` prévient
l'exploitant.

`malformed` ne va jamais à l'écran. C'est notre bug, et **l'utilisateur n'est pas notre
supervision.** Il est journalisé avec `op_id`, `device_id` et `app_version`, et il compte dans
l'indicateur « taux de synchronisation en échec » du §2.4.

### Le plateau « À revoir »

Une surface, un badge, **jamais une boîte de dialogue modale**. Le mot est *revoir* : ni
« Erreurs », ni « Échecs de synchronisation », ni bannière rouge. C'est une décision produit
(principe 6), pas de la cosmétique.

Chaque carte est quatre choses :

1. **Ce que tu as fait, dans tes mots** : « Tu as renommé « Appeler le plombier » en
   « Appeler le plombier (devis) ». »
2. **Quand, en termes humains, depuis ton propre appareil** : « hier soir » — c'est pour ça
   que `client_ts` est conservé alors qu'il ne décide de rien.
3. **Ce qui s'est passé, sans blâme et sans jargon** : « Camille a supprimé cette tâche
   pendant ce temps. » Jamais « conflit », jamais « refusé », jamais un code.
4. **Un ou deux boutons qui sont eux-mêmes des opérations ordinaires** :
   **[ Restaurer ]**, **[ Laisser supprimée ]**, **[ Remettre la mienne ]**,
   **[ En faire une nouvelle tâche ]**, **[ Copier le texte ]**. Répondre est une opération
   (`notice_resolve`), donc ça marche hors-ligne et la carte disparaît aussi de la tablette.

Règles du plateau, non négociables :

- **Le texte de l'utilisateur lui est toujours rendu verbatim.** Si rien d'autre n'est
  sauvable, il peut au moins être copié.
- **[ En faire une nouvelle tâche ]** est proposé pour tout refus qui porte du contenu. Il
  transforme l'opération morte en une ligne neuve que l'utilisateur possède à coup sûr. C'est
  l'issue de secours qui rend « ton travail n'est jamais perdu » littéral plutôt
  qu'aspirationnel.
- **La formulation est une clé de localisation choisie par `code`**
  (`sync.refused.forbidden`), jamais une phrase venue du serveur, jamais un statut HTTP,
  jamais une trace. Le serveur ne doit jamais être une source de français visible par
  l'utilisateur. `fr` et `en` dès le premier écran (`L0.5`).
- **Rien ne disparaît du plateau avant que l'utilisateur ait répondu.** Les cartes
  vieillissent, elles n'expirent pas. Écarter la bannière arrête le rappel, jamais la
  conservation.
- Pour `forbidden` chez un enfant, la carte n'offre pas un bouton qui ne marcherait pas :
  « Seuls les adultes peuvent supprimer une tâche » plus **[ Copier le texte ]**, pour que sa
  frappe ne soit jamais piégée. « Demander à un adulte » arrive en Phase 3, avec les rôles.

### Les trois règles qui rendent « ne jamais perdre » réel

1. **Rien n'est supprimé de la base locale parce que le serveur a dit non.** La ligne
   optimiste reste à l'écran, marquée, jusqu'à ce que l'utilisateur réponde. Un utilisateur
   qui voit sa tâche disparaître pendant que l'app affiche « synchronisé » s'est fait mentir,
   et c'est la panne que le cahier des charges désigne nommément.
2. **Le contenu est conservé sur le serveur**, dans `ops.payload` et dans `notices.mine`.
   « Renvoyer » et « Copier » marchent une semaine plus tard, depuis un autre appareil, et
   après une réinstallation.
3. **Une carte terminale libère ses `pending_fields`.** Quand une opération quitte la file
   pour le plateau, les champs qu'elle épinglait sont retirés de `pending_fields` et la ligne
   locale adopte l'état du serveur, **le contenu de l'utilisateur vivant désormais dans la
   carte**. Sans cette règle, un champ reste épinglé pour toujours et l'appareil ne peut plus
   **jamais** recevoir la valeur du foyer pour ce champ : divergence silencieuse, permanente,
   et invisible dans les métriques puisque le curseur est parfaitement à jour.

**Politique de reprise.** `applied | absorbed | superseded | receipted` → retiré de la file.
`refused` terminal → **jamais réessayé automatiquement** : une boucle de reprise sur un
`forbidden` met un serveur à 2 vCPU à genoux et donne à l'utilisateur 400 cartes identiques.
Les échecs de transport — 5xx, délai, pas de réseau, dépassement d'attente de verrou — ne sont
**pas** des refus : l'opération reste en file, en *backoff* exponentiel de 1 s à 5 min, gigué,
mesuré en ticks monotones.

---

## Compatibilité N-2

L'exigence (§1.1 n°7, §15.4) est que le serveur serve les **deux dernières versions de l'app
en même temps**. Quatre mécanismes, tous ennuyeux exprès.

**1. Les états descendent — c'est ce mécanisme qui rend le reste gratuit.** Le `pull` ne livre
pas des opérations, il livre des **états de lignes**. Une app ancienne qui reçoit une ligne
contenant un champ qu'elle ne connaît pas applique les champs qu'elle connaît ; une app
ancienne qui recevrait une *opération* d'un type apparu après sa sortie ne pourrait rien en
faire. Le sens descendant ne demande donc **aucune négociation de version**. Seul le sens
montant a un vocabulaire fermé — douze types — et le serveur répond `unsupported_op`,
réessayable, à ce qu'il ne connaît pas.

**2. `protocol` est un entier séparé de la version de l'app**, et il bouge beaucoup plus
lentement : la plupart des sorties n'y touchent pas. Le serveur accepte `N` et `N-1`.
`app_version` pilote les métriques et le libellé « mets l'app à jour », **et rien d'autre,
jamais un comportement** — deux versions d'app sur un même protocole est donc le cas normal et
il ne coûte rien, ce qui est la forme la plus fréquente du §15.4 et celle qui doit être
gratuite.

**3. `ext jsonb` par ligne, fusionné par clé.** Un champ introduit en app 1.6 que le serveur
de l'époque ne connaît pas atterrit dans `ext.priority` et y est conservé tel quel. Les clés
`ext` sont arbitrées par **exactement la même règle causale** que les colonnes typées :
`field_revs` contient `"priority": 48211`. Conséquence qui compte : **une app N-1 qui modifie
un titre n'effface pas la `priority` posée par une app N**, parce qu'elle envoie
`{"title": …}` avec sa seule `base_rev` et que la fusion est par champ. **Il n'existe nulle
part un `PUT` de ligne entière.**

Quand un champ `ext` devient une colonne typée, une migration `goose` en avant recopie
`ext.<k>` dans la colonne — et **`field_revs` ne change pas**, puisque ses clés sont des noms
de champ logiques et non des emplacements de stockage. C'est ce qui évite l'alias de
transition pendant lequel une même valeur porte deux provenances que l'arbitre ne sait pas
comparer : le trou ouvert, dans une des propositions examinées, par le mécanisme même que la
règle causale existait pour fermer.

**4. L'app ne doit jamais s'étouffer sur une ligne.** Les contraintes `CHECK` et les clés
étrangères sont interdites sur les colonnes synchronisées **du côté client**, et une ligne
qu'on n'a pas su appliquer va en `quarantine` sans bloquer le curseur. Un serveur N qui
introduit `status = 'skipped'` — ce que les règles additives autorisent — ne doit pas geler la
synchronisation **entière** d'une app N-1. Les nouvelles valeurs d'énumération se dégradent
vers un défaut nommé (`kind` → `task`, et un `role` inconnu n'ajoute **aucun** droit) et la
valeur brute reste dans `ext`, intacte pour une app plus récente.

**Interdit dans toutes les versions :** renommer ou supprimer un champ, restreindre un type,
rendre obligatoire un champ optionnel, réutiliser un identifiant, et surtout **changer le sens
d'un type d'opération existant**. Un sens qui change corrompt en silence au lieu d'échouer :
c'est le seul point sur lequel être dogmatique. Pour changer un sens, on ajoute `kind/2`.

**La retraite, explicite et bienveillante.** Chaque réponse porte `min_app_version` et
`sunset_at`, qui pilotent une bannière **non bloquante**. En dessous de `protocol_min`, le
`pull` **continue de marcher en lecture seule** et seul le `push` est refusé, pour que
l'utilisateur voie encore son foyer et puisse exporter sa file au lieu de regarder une
brique : « Cette version ne peut plus envoyer de modifications. Mets l'app à jour pour
retrouver tes 12 actions en attente. » **La boîte d'envoi est conservée.**

**Les deux tests sans lesquels rien de tout ça n'est vrai.**

- *Rejeu de fichiers d'or*, en CI : `server/internal/sync/testdata/protocol-N/*.json` garde
  des paires requête/réponse enregistrées depuis le client N-1 effectivement publié. Le test
  rejoue chaque requête contre le gestionnaire courant et compare, en ignorant `server_now` et
  les clés que le fixture marque additives. **Un fixture publié n'est jamais réédité** :
  l'éditer pour faire passer une construction est la seule façon de casser N-2, donc cette
  règle va dans `CLAUDE.md`.
- *Matrice de tolérance descendante* : un client N-1 simulé consomme le corpus d'un serveur N,
  et on vérifie que (a) chaque ligne est appliquée ou mise en quarantaine, (b) le curseur
  atteint toujours la tête, (c) l'état final du client N-1 est une projection correcte de
  celui du client N, et (d) rien de ce que le client N-1 a poussé n'a été perdu. Le premier
  test prouve que le serveur n'a pas bougé ; **seul le second prouve qu'une app ancienne
  survit à une ligne nouvelle**, et c'est là qu'est la vraie rupture.

---

## Ce que nous abandonnons

La liste honnête. Chaque point est un choix, pas un oubli.

1. **Deux personnes qui modifient deux champs différents de la même ligne ne se gênent pas,
   mais la détection de concurrence reste grossière.** Un client à jour qui écrit un champ
   touché une révision plus tôt ne paraît pas concurrent et ne reçoit aucun reçu. Pas de
   vecteurs de versions, pas de causalité par champ. Accepté, et ça veut dire que certains
   vrais conflits passent en premier-écrivain-gagne sans que personne ne soit prévenu.

2. **Le texte ne fusionne jamais.** Deux personnes qui écrivent par-dessus le même titre ou le
   même mémo : une version part dans une notice. Une liste de 200 articles va bien parce que
   les articles sont des lignes ; un mémo de 500 mots écrit à deux, non. Si les mémos de la
   Phase 3 se révèlent réellement collaboratifs, il leur faudra un mécanisme propre, et le
   greffer sur celui-ci sera laid. Signalé maintenant pour que ce ne soit pas une surprise.

3. **Premier-écrivain-gagne est inhabituel et paraîtra parfois faux.** « J'ai tapé en dernier,
   pourquoi ça affiche l'ancien ? » Le reçu adoucit, il ne supprime pas la surprise, et **les
   utilisateurs ignoreront le plateau**. Nous avons choisi une perte **visible et réversible**
   plutôt qu'une perte silencieuse ou un texte fusionné. C'est une question produit : elle est
   dans les questions ouvertes.

4. **Un verrou de ligne par foyer sérialise toutes les écritures de ce foyer.** Correct,
   simple, parfaitement dimensionné pour huit personnes — et c'est un plafond **sans issue de
   secours**. Un client bogué qui pousse en boucle se refuse le service à lui-même et à son
   foyer. Le risque réel n'est pas le débit, c'est le **couplage de la latence de queue** :
   une requête lente dans une transaction bloque les écritures du foyer. D'où
   `statement_timeout = 3s`, une limite de débit par foyer — du code qui n'existe pas encore —
   et la métrique `sync_push_txn_seconds`.

5. **Aucune cohérence causale entre lignes, et pour toujours.** Les pages arrivent en ordre de
   `rev`, pas en ordre causal, et chaque opération est sa propre transaction. Créer une
   étiquette et l'attacher en deux opérations peut voir la seconde refusée alors que la
   première a réussi. Conséquence permanente : **aucune clé étrangère sur une colonne
   synchronisée, et chaque écran doit tolérer une référence pendante jusqu'à la fin du
   projet.** C'est le coût le plus diffus de cette conception et le plus facile à oublier en
   écrivant la quarantième fonctionnalité.

6. **Le serveur n'est plus un pur relais, et `D-005` s'appuie sur l'affirmation qu'il l'est.**
   Il arbitre cinq invariants et dérive les dates d'occurrence, l'assigné de rotation et le
   compteur de série. Un bug de l'arbitre produit un **effet décidé faux** qui se réplique
   fidèlement sur chaque téléphone et devient indiscernable de la vérité. `ops` permet de
   trouver l'opération exacte et d'ajouter une correction, mais il n'y a pas de réparation
   automatique, et pas de « les téléphones avaient raison, qu'on annule le serveur ».
   `docs/ARCHITECTURE.md` doit être amendé en même temps que ce document est validé (Q9).

7. **Le long poll n'est pas du push, et il n'existe aucun chemin local.** « Quasi temps réel »
   veut dire ~300 ms quand les deux apps sont ouvertes, et « à la prochaine ouverture » sinon
   (`D-010` repousse le vrai push en Phase 4). Deux téléphones dans le même supermarché **sans
   réseau ne se synchronisent pas du tout**. C'est un vrai trou dans le scénario que le §3.2
   met en vitrine.

8. **90 jours d'`ops` sont un journal en clair de qui a fait quoi dans le foyer**, et le
   serveur garde aussi le contenu refusé des utilisateurs dans `notices.mine`. C'est le
   meilleur atout de débogage de ce document et un butin plus intéressant pour un attaquant que
   les tables vivantes. C'est aussi une surface de vie privée **à l'intérieur** du foyer :
   « Camille a décoché les bières trois fois ». À inclure dans l'export RGPD, à purger à la
   suppression, et l'interface doit **résister** à la tentation de l'exposer — ce qui entre en
   tension avec la « transparence » que réclame la persona coloc. Tension non résolue, laissée
   au product owner exprès (Q5).

9. **La bande passante descendante est gaspillée.** Un changement de titre expédie l'état
   complet de la tâche. Sans importance à l'échelle d'un foyer (`D-005` : des kilo-octets par
   foyer et par jour), et c'est la première chose à changer à cent fois ces données.

10. **`series_index` est un entier, donc une série ne se rattrape pas rétroactivement.**
    `catch_up: skip` ne matérialise pas les créneaux manqués, et il n'y a aucune planification
    côté serveur. `accumulate` est prévu dans le schéma mais n'est pas spécifié ici.

11. **Appliquer une page est une transaction SQLite.** Les pages partielles restent invisibles,
    mais 500 lignes sur un téléphone d'entrée de gamme est un à-coup perceptible, et un
    snapshot est une transaction de plusieurs mégaoctets. À découper avant que la Phase 3
    n'ajoute les points et les mémos.

12. **Les pièces jointes sont hors périmètre et rien ici ne s'y étend.** Une photo de la
    chaudière demande un envoi adressé par le contenu plus une référence : un treizième
    mécanisme, un second protocole, et un coût réel le jour où quelqu'un le demande.

13. **Pas de RLS.** Décision assumée (§Isolation) : une seconde ceinture décorative est pire
    que pas de seconde ceinture, parce qu'on relâche la première en croyant être couvert. Ce
    qui la remplace est une classe de tests, pas une politique du moteur — donc l'isolation
    repose sur de la discipline **vérifiée mécaniquement**, et pas sur une garantie du SGBD.

---

## Questions ouvertes pour le product owner

Ces décisions ne sont pas les nôtres. Elles n'empêchent pas de commencer la Phase 1 — chacune
a un défaut écrit dans ce document — mais elles engagent le produit, et reculer après coup
coûte une migration de données **et** un déploiement progressif des règles de fusion.

**Q1 — Premier-écrivain-gagne, ou dernier-écrivain-gagne, sur deux éditions concurrentes du
même champ ?** Défaut ici : **premier**. On gagne qu'une valeur vue synchronisée ne change plus
sous les yeux, et qu'un téléphone absent dix jours ne peut pas écraser la matinée du foyer. On
perd que « j'ai tapé en dernier » n'est pas ce qui s'affiche. Les deux variantes produisent une
notice et **personne ne perd son texte** ; la question est seulement laquelle des deux valeurs
est à l'écran avant la réponse.

**Q2 — Deux adultes cochent la même corvée : qui marque les points ?** Les deux validations
sont conservées, c'est acquis. Reste à trancher pour la Phase 3 : « les deux marquent », « le
premier marque », ou « les points se partagent ». La donnée est là dans tous les cas
(`completions` est ordonné par `done_rev`) et rien n'est jeté : c'est un réglage de foyer ou
une constante, à vous de dire laquelle.

**Q3 — Le plateau « À revoir » doit-il pouvoir se vider tout seul ?** Défaut ici : **non**, une
carte reste jusqu'à ce qu'un humain réponde. C'est le plus bienveillant, et c'est aussi ce qui
peut accumuler quarante cartes sur un téléphone revenu de trois mois d'absence. Accepte-t-on un
« Tout écarter » groupé pour les reçus — jamais pour les décisions ?

**Q4 — 90 jours de rétention : d'accord ?** C'est le seul nombre du protocole. Il fixe
simultanément quand un téléphone absent paye un téléchargement complet sur une connexion
mauvaise par définition, et combien de temps le serveur garde un journal de l'activité du
foyer. Monter à 365 n'achète presque rien — un téléphone d'un an de retard a de toute façon une
app périmée ; descendre à 30 force un snapshot à un téléphone qui a passé août dans un sac.

**Q5 — L'historique « qui a fait quoi » est-il visible dans l'app ?** Le serveur le détient de
toute façon (`ops`, 90 jours). La persona coloc réclame de la transparence, le principe 6
interdit la culpabilisation, et les deux pointent dans des directions opposées. Notre défaut :
**non exposé en Phases 1 et 2**, seulement « Fait par Camille et Jordan » sur la ligne
concernée. Le dire explicitement évite qu'un écran d'historique apparaisse par glissement en
Phase 3.

**Q6 — Une décision « édition contre suppression » est-elle adressée à l'éditeur seul, ou à
tout le foyer ?** Défaut ici : **à l'éditeur seul**, parce que le suppresseur a déjà ce qu'il
demandait et que deux voix créent une course. L'alternative — tout adulte peut répondre — est
plus transparente dans une coloc et plus bruyante dans un couple.

**Q7 — `occurred_on` est borné à 14 jours en arrière.** Assez pour « j'ai oublié de cocher la
semaine dernière », trop peu pour « je rattrape le mois d'août ». C'est aussi la fenêtre dans
laquelle un combiné à l'heure fausse peut décaler une validation, et elle alimente directement
l'arithmétique des séries `relative`. Quatorze jours, sept, ou trente ?

**Q8 — Multi-foyers (Phase 3) est câblé dans le schéma dès maintenant** — un curseur et une
boîte d'envoi par foyer, `household_id` sur chaque table cliente — mais **pas dans
l'interface**. C'est volontaire : l'ajouter après coup veut dire reconstruire la frontière de
sécurité et le moteur de synchronisation après la mise en service, ce qui est précisément ce
qui a coulé les quatre propositions examinées sur ce point. Le coût : un membre de trois foyers
paye trois boucles de synchronisation, donc trois fois la batterie et trois long polls.
Acceptable, ou faut-il une boucle fusionnée dès la Phase 1 ?

**Q9 — Ce document amende `docs/ARCHITECTURE.md` et `CLAUDE.md`.** « Le serveur est un relais,
pas le cerveau » devient « le serveur est un relais **et l'arbitre de cinq invariants** ».
Cinq, et pas plus : la séquence du foyer, la provenance des champs, l'unicité de l'occurrence
ouverte, les rôles et l'appartenance, la borne sur `occurred_on`. Valider ce document, c'est
valider cet amendement — et c'est le seul endroit où ce protocole contredit un document déjà
écrit, ce qui méritait d'être dit en face plutôt qu'enterré dans une annexe.
