# Où en est le projet

Mis à jour le 15 octobre 2026. À lire en premier pour reprendre à froid.

**Phase en cours : 1 — Le noyau.** Plan : [PLAN.md](PLAN.md). Arbitrages :
[DECISIONS.md](DECISIONS.md). Forme du système : [ARCHITECTURE.md](ARCHITECTURE.md).
Protocole : [SYNC_PROTOCOL.md](SYNC_PROTOCOL.md).

## Phase 1 — avancement

| Lot | État |
|---|---|
| L1.1 schéma serveur | **fait**, et ses garanties prouvées en base |
| L1.2 authentification | **fait** — spécifié ([AUTH.md](AUTH.md)), implémenté, éprouvé de bout en bout |
| L1.3 `POST /sync` | **le vocabulaire est complet** — 22 règles, 22 tests ; restent les listes, le `order_key` fractionnaire et l'arithmétique de récurrence (Phase 2) |
| L1.4 notices et mutations refusées | **fait** — `notice_resolve` est une **opération**, pas un endpoint, donc répondre marche hors-ligne |
| L1.5 `GET /snapshot` et curseur périmé | **fait** |
| L1.6 long poll | **fait** |
| L1.7 schéma Drift | **fait** |
| L1.8 boucle de synchronisation | **faite** — reste la reconstruction par snapshot |
| L1.9 → L1.10 (écrans, recette) | pas commencés |

**La boucle tient ses trois règles, et chacune a son test.**

**Une synchronisation ne révoque jamais la phrase qu'on est en train de taper.** Le champ
reste dans `pending_fields` jusqu'à ce que le serveur ait répondu *à son sujet*, et un état
entrant écrit tout **sauf** ceux-là. Le test le vérifie dans les deux sens : le titre en cours
d'édition survit, **et** les autres champs du même état sont bien appliqués — sinon la règle
dégénérerait en « ligne gelée ». C'est la **réception de la réponse** qui libère le champ, pas
l'envoi du geste.

**Le curseur ne bouge que sur ce que SQLite a réellement engagé.** La page entière et le
curseur sont dans **une seule transaction**, donc un plantage en cours reprend où il s'est
arrêté au lieu de laisser une demi-page à l'écran et un curseur au-delà.

**Une ligne qu'on ne sait pas lire est mise de côté et le curseur avance quand même.** Le
test envoie une entité inventée au milieu d'une page : elle part en `quarantine`, la ligne
lisible est appliquée, et le curseur va jusqu'au bout. C'est aussi ce qui fait qu'une app
ancienne survit à un serveur plus récent.

Deux détails qui comptent :

- **`unsupported_op` reste dans la file.** Un serveur plus vieux que l'app se corrige par un
  déploiement ; jeter le geste empoisonnerait la file d'une vraie personne pour une erreur
  d'ordre de déploiement. Un refus terminal, lui, quitte la file.
- **Rejouer une page ne change rien.** Appliquer un état, c'est remplacer la ligne si la `rev`
  entrante est supérieure — donc aucun moteur de fusion sur le téléphone, et aucun ordre
  d'arrivée ne peut le casser.

**Le schéma client est en deux moitiés, et la séparation porte une règle.**
`lib/core/db/sync_tables.dart` miroir du serveur, `lib/core/db/local_tables.dart` ce qui ne
quitte jamais le téléphone.

**Aucun `CHECK`, aucune clé étrangère sur une colonne synchronisée.** Les pages arrivent en
ordre de `rev`, pas en ordre causal : une tâche peut référencer une catégorie pas encore
livrée, et un serveur plus récent peut envoyer une valeur que cette app n'a jamais vue.
L'écran doit afficher « Sans catégorie », pas une exception.

Cette règle a une conséquence concrète en Drift qu'il faut connaître : **`boolean()` génère
un `CHECK (col IN (0,1))` et `withLength()` un `CHECK` de longueur.** Les deux manières
évidentes d'écrire ces tables produisent donc exactement les contraintes qui les cassent.
D'où des `IntColumn` pour les drapeaux synchronisés, et **un test qui lit le DDL dans
`sqlite_master`** et échoue s'il contient `CHECK` ou `REFERENCES`. C'est le pendant des tests
d'architecture Go : la règle est mécanique, pas une convention à se rappeler.

Deux autres choix du protocole, respectés tels quels :

- **`outbox.lseq` est un entier autoincrémenté, jamais un horodatage.** Sur un téléphone,
  l'autoincrément est la seule chose monotone — l'horloge murale recule dès que le réseau la
  corrige. Un test insère des `client_ts` *décroissants* et vérifie que l'ordre de rejeu reste
  celui des gestes.
- **Une ligne qu'on n'a pas su appliquer va en `quarantine` et le curseur avance quand
  même.** Une seule ligne illisible ne doit jamais geler toute la synchronisation d'un
  téléphone, en silence.

Et `pending_fields` est sur chaque ligne : quand un état serveur arrive, le client écrira tous
les champs **sauf** ceux qui y figurent. C'est le seul mécanisme fiable contre la
synchronisation qui révoque la phrase qu'on est en train de taper.

**Le serveur de la Phase 1 est complet.** Le long poll se gare sur une goroutine et
**rien d'autre** : `ApplySync` a rendu sa connexion et sa transaction avant l'attente. Un
test le prouve en comptant les connexions `idle in transaction` pendant que six appels sont
garés — zéro. C'est l'affirmation sur laquelle repose le plafond de coût de `D-005`, donc
elle est mesurée et pas déclarée.

Deux détails que l'implémentation a imposés :

- **L'abonnement se fait avant la lecture.** Un changement validé entre la lecture et
  l'inscription serait perdu, et le client attendrait son timeout entier pour quelque chose
  qui avait déjà eu lieu.
- **Le `NOTIFY` part de l'intérieur de la transaction**, donc PostgreSQL ne le livre qu'à la
  validation. Envoyé plus tôt, il réveillerait un client qui relirait l'état d'avant.
- **La connexion `LISTEN` est hors du pool.** Une seule pour tout le processus, tenue à vie :
  la prendre dans le pool coûterait au pool une connexion pour toujours, et le pool existe
  pour servir les requêtes. Sa mort silencieuse est la seule panne qui laisse tous les longs
  appels expirer sans réveil — elle ne perd rien, mais elle est journalisée bruyamment avec
  sa conséquence écrite en clair.

**Le snapshot tient la propriété qui compte.** Tout tourne dans **une seule transaction
`REPEATABLE READ`, curseur lu dedans**. Sans cela, une écriture validée pendant le flux est
absente du corps tout en portant une `rev` inférieure au curseur rendu : elle ne serait
**jamais** relivrée, et l'appareil aurait tort sur son foyer pour toujours, en silence. Un
test vérifie qu'aucune ligne du flux ne porte une `rev` au-dessus du curseur.

Le flux se termine par une ligne `end` explicite, et c'est délibéré : dès le premier octet le
statut est déjà `200`, donc une panne en cours de route ne peut que tronquer. Lire l'`end`
est le seul moyen de distinguer un monde tronqué d'un monde clos.

**Et le curseur périmé répond `200`, pas `410`.** Un `410` jetterait les opérations que le
client vient de pousser — « ton curseur a expiré » ne doit jamais vouloir dire « tes quatorze
modifications hors-ligne sont perdues ». Le serveur applique la poussée, répond normalement,
et pose `snapshot_required`. Un test le prouve. Un curseur à zéro, lui, est un **appareil
neuf** : c'est son état normal de départ, pas une faute.

**L1.3, ce qui tient déjà.** Quinze lignes de la table des règles ont chacune leur test,
nommé d'après la règle. Le choix structurant : *classify* décide champ par champ **sans
toucher la base**, donc les règles se lisent en un seul endroit et se testent sans
transaction.

| Règle | État |
|---|---|
| `base_rev == field_rev` → appliquée | ✓ |
| champ jamais écrit → appliquée | ✓ |
| `base_rev < field_rev` → **le serveur tient**, notice à l'auteur avec son texte intact | ✓ |
| `base_rev > field_rev` → `malformed` (notre bug) | ✓ |
| `set_fields` sur ligne inconnue → `target_gone`, **ne crée jamais la ligne** | ✓ |
| édition contre suppression → notice `decision` à **l'éditeur** | ✓ |
| opération inconnue → `unsupported_op`, **réessayable** | ✓ |
| schéma violé → `malformed`, jamais montré | ✓ |
| `op_id` rejoué, même hachage → `absorbed`, résultat stocké | ✓ |
| `op_id` rejoué, hachage différent → `malformed` | ✓ |
| deux validations du même élément → **les deux conservées** | ✓ |
| dévalidation → retire **exactement** les membres observés | ✓ |
| suppression périmée → `receipted`, notice au suppresseur | ✓ |
| deux téléphones ouvrent la prochaine occurrence → même ligne | ✓ |
| dévalider/restaurer une occurrence dépassée → règle du successeur | ✓ |
| rôle insuffisant / membre retiré / appareil révoqué → `forbidden` | tenu par l'intergiciel, pas encore par un test de `/sync` |
| listes, `order_key` fractionnaire | Phase 2 |

**L'invariant des séries tient, et il est exact et non « au plus un ».** Trois choses le
portent :

1. **L'identité dérive d'un entier**, `uuidv5(series_id || ':' || index)`. Deux téléphones
   hors-ligne calculent le **même** identifiant, donc le second `INSERT` devient un
   `ON CONFLICT DO NOTHING` : il n'y a rien à fusionner. Un test fige la valeur dérivée —
   changer la dérivation casserait l'invariant pour tous les foyers existants.
2. **Supprimer l'occurrence ouverte fait avancer la série.** « On saute cette semaine » ne
   peut pas laisser une série sans rien d'ouvert, et c'est pour cela qu'il n'existe **aucun
   balayeur** qui régénère ce que l'utilisateur a écarté.
3. **Zéro ligne mise à jour n'est pas une erreur.** Un test vérifie qu'une série en pause
   laisse quand même la coche en place avec `series_advanced: false`. Mettre la création de
   l'occurrence et la validation dans une même transaction sans garde, c'est ainsi qu'une
   corvée faite disparaît sans un mot.

**Une instabilité de tests trouvée et corrigée** : `go test ./...` lance les *paquets* en
parallèle, et deux paquets tronquaient la même base `choreo_test` entre leurs propres tests.
L'échec n'apparaissait qu'une fois sur deux, ce qui est pire qu'un échec franc. `-p 1` est
désormais dans `make test-server` et dans la CI, avec la raison écrite à côté. Vérifié par
**trois passages sans cache**, le cache rendant un second passage sans valeur.

**Et la CI a maintenant un PostgreSQL.** Sans lui, tous les tests d'intégration
s'ignoraient : une CI verte ne disait rien de la moitié du serveur qui détient les données.

Deux détails qui auraient coûté cher plus tard :

- **La descente est un seul `UNION ALL` trié globalement par `rev`**, pas une requête par
  entité fusionnée en Go. Avec des requêtes séparées, l'entité qui dépasse sa propre limite
  voit ses lignes excédentaires passer sous le curseur — et elles sont perdues pour de bon.
- **La page ne coupe jamais au milieu d'une révision.** Une opération écrit une ligne *et*
  sa notice au même `rev` ; couper entre les deux avancerait le curseur au-delà de la
  moitié jetée. Une notice perdue en silence est précisément ce que la table `notices`
  côté serveur existe pour empêcher.

**L1.2 est terminé.** Les onze points de terminaison de `AUTH.md` répondent, et les règles
qui comptent sont vérifiées par un test chacune, à travers HTTP :

| Règle | Test |
|---|---|
| Un foyer dont on n'est pas membre → **`404`, jamais `403`** | et un foyer inexistant donne la **même** réponse |
| Identifiant inconnu et mot de passe faux | **réponses identiques**, `invalid_credentials` |
| Se déconnecter ≠ voir son appareil retiré | `unauthenticated` contre `device_revoked` — le second seul efface la réplique |
| Un code d'invitation ne peut pas accorder `admin` | refusé par l'API **et** par la base |
| Un membre non-admin ne peut pas inviter | `403 forbidden_role` — ici `403` est correct : il sait déjà qu'il est membre |
| Le QR porte l'adresse du serveur | sinon un foyer auto-hébergé ne pourrait inviter personne |
| Rejeu de *refresh* après réponse perdue | accordé dans la fenêtre de grâce |
| La récupération révoque les autres appareils | et remet un code de secours neuf |

Les 71 secondes de cette suite sont le coût réel d'argon2 à `m=19456` : c'est le signe que
les tests passent par le vrai chemin et non par un raccourci.

**Un réglage ajouté en route** : `CHOREO_PUBLIC_URL`. Le serveur ne peut pas déduire sa
propre adresse d'une requête — l'en-tête `Host` est ce que l'appelant veut bien y mettre — et
sans elle le lien derrière un QR code d'invitation est faux. En `prod`, le serveur refuse de
démarrer s'il vaut encore `localhost`.

**L1.1, vérifié contre un vrai PostgreSQL** — 14 tables, et quatre garanties structurelles
éprouvées puis annulées :

| | |
|---|---|
| Affectation inter-foyers | **refusée par la base** (clé étrangère composite) |
| Deuxième occurrence ouverte sur une série | **refusée** (index unique partiel) |
| Avance normale d'une série après clôture | **acceptée** — le garde-fou ne bloque pas le cas sain, qui est l'erreur classique de ce type d'index |
| Même créneau de série deux fois, même clôturé | **refusé** |

Plus deux tests d'architecture exigés par le protocole, qui échouent si une requête SQL
apparaît hors de `internal/store`, ou si une méthode du magasin ne prend pas `householdID`
juste après le contexte. Ils contraindront tout le code de L1.3 au moment où il s'écrira.

**L1.2, les primitives** — `internal/auth`, sans HTTP ni SQL : argon2id au format PHC,
jetons opaques, codes en base32 Crockford lisibles à voix haute, limitation de débit en
mémoire, et un **plafond de 4 calculs argon2id simultanés** sans lequel la promesse de coût
de `D-005` était une fiction. Spécifié dans [AUTH.md](AUTH.md), consigné en `D-021`.

**L1.2, la couche de stockage** — `internal/store` scindé en deux : `Store` pour ce qui est
lié à un foyer, `Identity` pour les comptes, appareils et sessions, qui ne le sont pas. Le
test d'architecture connaît cette exemption **par son nom** : y ajouter un type est un diff
visible, pas un contournement silencieux.

La primitive qui porte tout L1.3 est en place : `nextSeq` incrémente `households.change_seq`
**sous verrou de ligne, dans la transaction appelante**. Les écritures d'un foyer deviennent
sérielles — c'est le prix, et il est théorique à quelques dizaines d'écritures par jour —
mais l'ordre d'attribution devient l'ordre de validation, donc « tout ce qui suit 48 210 » ne
peut rien manquer.

**Un amendement à ma propre décision, trouvé en implémentant** : la règle « rejouer un
*refresh* consommé révoque l'appareil » se retourne contre l'utilisateur quand la *réponse*
du serveur est perdue en route. Le client retente avec le jeton qu'il détient encore et se
verrouille — exactement la reconnexion que `A1` voulait éviter. D'où une **fenêtre de grâce
de 60 secondes** : rejouer est accordé si le successeur n'a jamais servi, refusé sinon. La
détection de vol survit ; le faux positif disparaît. Voir `D-021`.

**Banc d'essai d'intégration** — les tests de `internal/store` tournent contre un vrai
PostgreSQL, parce que tout ce qui est intéressant ici vit dans la base : le verrou de ligne,
les clés étrangères composites, l'index unique partiel. Un faux testerait le faux. Sans
`CHOREO_TEST_DATABASE_URL` ils **s'ignorent** au lieu d'échouer, pour que `go test ./...`
marche sans Docker — d'où `make test-db` puis `make test-server`.

| Lot | État |
|---|---|
| L0.1 environnement | **fait** |
| L0.2 garde-fous du dépôt | **fait** |
| L0.3 squelette Go | **fait** |
| L0.4 PostgreSQL et migrations | **fait** |
| L0.5 squelette Flutter | **fait** |
| L0.6 Compose et Makefile | **fait** |
| L0.7 intégration continue | **écrite**, pas encore exercée — il manque le dépôt distant |
| L0.8 observabilité | **fait** |
| L0.9 protocole de synchronisation | **écrit**, 1 499 lignes, **ses 9 questions tranchées** (`D-020`) |
| L0.10 mémoire du projet | **fait** |

**La Phase 0 est terminée côté code**, et le protocole n'est plus bloquant : ses neuf
questions ouvertes ont été tranchées par délégation (`D-020`). Nuance à garder en tête — le
document n'a pas été relu par le product owner. La section qui mérite ses yeux est
**« Les sept cas »** : c'est le comportement que les utilisateurs ressentiront. Une section,
pas 1 499 lignes.

---

## Ce qui est vérifié, pas seulement écrit

La pile complète a été montée et éprouvée de bout en bout :

```
/healthz  → 200  {"status":"ok","version":"c973d73-dirty"}   + en-tête X-Request-Id
/readyz   → 200  {"status":"ready"}
/metrics  → choreo_http_requests_total{method,route,status}   étiqueté par motif de route
base      → goose_db_version + schema_bootstrap présentes
```

Et l'invariant inverse, celui qui compte, **postgres arrêté** :

```
/healthz  → 200          la vivacité ne dépend de rien
/readyz   → 503          {"reason":"database unreachable"}
journaux  → l'erreur de pilote, qui nomme l'hôte et le compte, reste DANS les journaux
```

| Vérification | Résultat |
|---|---|
| `go test -race ./...` | vert |
| `golangci-lint run` | **0 issue** |
| `flutter analyze --fatal-infos --fatal-warnings` | **0 issue** |
| `flutter test` | **8 tests verts** |
| `make dev` sur machine vierge | monte postgres + serveur, sans intervention |
| `make prod-config` | composition de production valide |
| `make apk` | **`app-debug.apk` produit** (153 Mo : débogage, toutes ABI) |
| `.github/workflows/ci.yml` | se parse — 3 postes, mais **jamais exécutée** faute de dépôt distant |
| crochet `pre-commit` | refuse un commit portant un jeton |

## Outils installés

Go 1.27.1 · Flutter 3.38.9 · Dart 3.10.8 · Android SDK 35 · adb 1.0.41 ·
Docker 28.1.1 · JDK 21.0.11 · gitleaks 8.30.1 · golangci-lint 2.13.2

## Suivant

1. **Lire la section « Les sept cas » de [SYNC_PROTOCOL.md](SYNC_PROTOCOL.md)** — et dire si
   le comportement décrit est celui qu'on veut. Le reste du document est de la plomberie ;
   cette section-là est le produit.
2. Pousser sur le dépôt distant pour que la CI tourne enfin (L0.7).
3. Installer l'APK sur un vrai téléphone.
4. Puis **Phase 1** : le noyau — foyer, membres, tâches, base locale, boîte d'envoi,
   synchronisation. Rien ne la bloque plus techniquement.

## En attente du product owner

- **Dépôt distant.** Pas encore créé, donc rien n'est poussé et la CI n'a jamais tourné.
  Marche à suivre exacte dans [SETUP_COMPTES.md](SETUP_COMPTES.md).
- **Adresse de commit.** Les 12 commits portent l'adresse personnelle. Le dépôt sera
  public : la réécriture est gratuite tant que rien n'est publié, coûteuse après.
- **Téléphone Android.** `adb devices` est vide. Non bloquant : la cible `linux` de Flutter
  suffit pour les essais, et la CI produit un APK en artefact.
- **Validation de `SYNC_PROTOCOL.md`** quand il arrive.

## Particularités de la machine, à ne pas redécouvrir

Les pièges d'outillage sont dans [CLAUDE.md](../CLAUDE.md) — `--force-jit`, le plafond
`drift`, `bash -lc` qui ne lit pas `.bashrc`. À retenir ici :

- **`ma-dev-box` n'a pas systemd** et lance un *runner* de CI au démarrage : la charge
  monte à 15+ sur 6 cœurs. Les commandes longues partent en tâche de fond.
- Le `PATH` de WSL hérite du `PATH` Windows, qui contient un SDK **Flutter pour Windows**
  incapable de s'exécuter sous Linux. `bootstrap-dev.sh` ne le supprime pas — il n'en a
  pas le droit — il place le nôtre devant.
- **Les profils Compose ne protègent pas l'interpolation** : une variable `${VAR:?}` d'un
  service en profil inactif casse quand même `docker compose up`. D'où deux fichiers,
  `compose.yaml` et `compose.prod.yaml`, et non un profil.
- `core.hooksPath` est une configuration **locale** : elle ne voyage pas avec un `push`.
  Qui clone doit lancer `scripts/bootstrap-dev.sh`.
- La pile de développement tourne encore en tâche de fond. `make down` pour l'arrêter.
