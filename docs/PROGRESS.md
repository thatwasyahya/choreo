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
| L1.3 `POST /sync` | **en cours** — résolution causale, idempotence et descente des changements faites ; restent `delete`, `restore`, `complete`, `uncomplete` et les séries |
| L1.4 → L1.10 | pas commencés |

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
| validations, dévalidation, séries, réordonnancement | à faire |

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
