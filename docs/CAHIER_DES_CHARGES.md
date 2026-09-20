# Cahier des charges & prompt de lancement — Choreo (ex « Maisonnée »)

> **Produit** : application mobile d'organisation du foyer — tâches ménagères, administratif, enfants, sport, vie quotidienne — pour familles, colocations, couples et personnes seules.
> **Destinataire** : Claude Code. Tu es l'ingénieur principal et le DevOps du projet ; ce document est ta source de vérité.
> **Contraintes structurantes** : application **mobile native** (Flutter/Dart, ou Kotlin/Swift) · back-end **Go** (ou Rust) · **100 % auto-hébergé sur un VPS**, aucun service géré type Supabase, Firebase, Auth0 · **gratuit pour les utilisateurs finaux**, sans publicité ni offre payante.
> Version 2.0 — 20 septembre 2026 (remplace la v1.0 web/PWA/Supabase)

---

## 0. Paramètres et cadre économique

### 0.1 Paramètres du projet

| Clé | Valeur par défaut | Note |
|---|---|---|
| `APP_NAME` | Maisonnée | Provisoire, reste un simple paramètre (manifest, i18n, e-mails) |
| `LANGUES` | `fr` (défaut), `en` | Chaînes externalisées dès le premier écran |
| `MOBILE` | **Flutter + Dart** | Alternative : Kotlin Multiplatform + Compose Multiplatform (§8.3). Tranché au cadrage, jamais en cours de route |
| `BACKEND` | **Go** | Alternative : Rust (Axum + SQLx). Voir §8.2 pour l'arbitrage honnête |
| `BASE` | PostgreSQL 17 | Instance unique auto-hébergée |
| `ORCHESTRATION` | Docker Compose + systemd sur un VPS | k3s explicitement écarté (§13.2) |
| `PROVISIONNEMENT` | Ansible | Le VPS doit être reconstructible de zéro en une commande |
| `PROXY_TLS` | Caddy | TLS automatique via ACME |
| `CI_CD` | GitLab CI (ou GitHub Actions) | Registre d'images du même fournisseur |
| `PLATEFORME_CIBLE_1` | Android | Phase 1 et 2 |
| `PLATEFORME_CIBLE_2` | iOS | Phase 4, **si et seulement si** le compte développeur Apple est payé (§0.2) |
| `REGION` | UE (France ou Allemagne) | RGPD |
| `FUSEAU_PAR_DEFAUT` | `Europe/Paris` | Chaque foyer a son propre fuseau IANA |

> **Valeurs effectivement retenues au cadrage du 20/09/2026** : voir `docs/DECISIONS.md`.
> `APP_NAME` = **Choreo**, domaine `choreo.eu`, `MOBILE` = Flutter, `BACKEND` = Go,
> `CI_CD` = GitHub Actions, VPS = OVH (à partir de la Phase 2).

### 0.2 Le point à trancher d'abord : « gratuit » ne veut plus dire « sans coût »

En v1 (PWA + offres gratuites), le coût total était réellement de 0 €. Une app mobile auto-hébergée déplace le curseur : **gratuit pour les utilisateurs, à la charge de l'opérateur**. Chiffrage honnête :

| Poste | Coût | Statut |
|---|---|---|
| VPS (2 vCPU, 4 Go, 60-80 Go NVMe, UE) | 4 à 8 € / mois | **Incompressible.** C'est le prix de l'auto-hébergement |
| Nom de domaine | 5 à 12 € / an | **Quasi incompressible** : certificats TLS, Android App Links, iOS Universal Links, e-mails |
| Stockage de sauvegarde hors site (10 Go) | 0 € | Offres gratuites S3-compatibles, ou un second serveur |
| Compte développeur Google Play | 25 $ une fois | **Optionnel** : distribution hors Play possible (§16.1) |
| Vérification développeur Android | 25 $ une fois, ou 0 € | Obligatoire à terme même hors Play (§16.1). Gratuite en « distribution limitée » (20 appareils max) |
| **Compte développeur Apple** | **99 $ / an** | **Obligatoire pour toute distribution iOS**, y compris TestFlight. Aucune voie gratuite n'existe |
| Machine macOS pour compiler iOS | variable | Mac personnel, ou runners macOS gratuits d'un dépôt public |

**Conséquences à assumer :**
- Le principe « gratuit pour toujours » du §2.2 concerne **les utilisateurs**. Il reste intact : ni pub, ni premium, ni revente de données.
- **iOS est une décision économique, pas technique.** Il n'existe aucun moyen gratuit de distribuer une app iOS : App Store, TestFlight et distribution directe UE exigent tous l'adhésion à 99 $/an. Le code Flutter sera commun, mais la livraison iOS est conditionnée au paiement. D'où le plan : **Android d'abord, iOS en Phase 4**.
- Android reste distribuable gratuitement aujourd'hui (APK direct, dépôt F-Droid auto-hébergé), mais Google généralise la vérification d'identité des développeurs à tous les canaux d'installation, y compris le chargement manuel : première vague le 30 septembre 2026 dans quatre pays, extension mondiale annoncée pour 2027. Voir §16.1 : il faut anticiper, pas subir.

Si l'objectif « zéro euro absolu » prime sur « app native auto-hébergée », dis-le-moi : on revient à la v1. Sinon, on assume un coût opérateur d'environ 60 à 100 € par an, et on le rend visible dans l'app (page « Pourquoi c'est gratuit »).

---

## 1. Ton rôle et nos règles de collaboration

**Rôles.** Tu es à la fois l'architecte, le développeur back-end, le développeur mobile et le DevOps. Je suis le product owner et l'exploitant : je valide les plans, je paye et j'ouvre les comptes, je teste sur de vrais téléphones, je garde les clés.

**Source de vérité.** Ce document. S'il est ambigu : choisis l'option la plus simple qui respecte les principes produit (§2.2) et les principes d'exploitation (§1.1), consigne-la dans `docs/DECISIONS.md` et avance. Demande-moi avant si l'impact touche : le coût, la sécurité, les données personnelles, le protocole de synchronisation, ou le schéma de données.

### 1.1 Principes d'exploitation (culture DevOps du projet)

1. **Rien à la main sur le serveur.** Toute action sur le VPS existe sous forme de playbook Ansible, de fichier Compose ou de cible `make`, versionnée. Une connexion SSH sert à observer, pas à modifier. Si tu dois faire une action manuelle, elle devient d'abord du code.
2. **Reconstructible.** Objectif mesuré : **reprovisionner un VPS vierge et restaurer les données en moins de 60 minutes**, exercice réalisé pour de vrai avant la mise en production (§13.7).
3. **Une sauvegarde non testée n'existe pas.** La restauration est automatisée et vérifiée chaque semaine.
4. **Peu de pièces mobiles.** Chaque composant ajouté doit justifier sa RAM et sa maintenance. Le défaut est « non ». Un monolithe modulaire bien découpé bat six micro-services sur une machine à 4 Go.
5. **Observable avant d'être optimisé.** Journaux structurés, métriques et traces dès la Phase 0, pas après le premier incident.
6. **Sécurité par défaut.** Refus par défaut sur le réseau comme sur les données. Aucun port ouvert qui n'est pas justifié. Aucun secret en clair dans le dépôt.
7. **Migrations en avant uniquement**, compatibles avec la version précédente du client mobile. Un téléphone met des semaines à se mettre à jour : **le serveur doit toujours supporter les deux dernières versions de l'app** (§15.4).
8. **Pas de dépendance non remplaçable.** Tout service externe résiduel (relais SMTP, passerelle de push) est derrière une interface, avec une implémentation de rechange documentée.

**Rythme.** Phase par phase (§18). Avant : plan dans `docs/PLAN.md` (lots, ordre, risques, critères de sortie), puis mon GO. Pendant : petits incréments testés, uniquement la phase en cours. Après : checklist de recette, tests verts, documentation à jour, stop.

**Mémoire du projet** (créée en Phase 0, tenue à jour) :
- `CLAUDE.md` — 150 lignes max : commandes (dev, test, lint, migrations, build mobile, déploiement), conventions, invariants d'architecture, pointeurs vers les sections de ce document. Pas de copie du cahier des charges dedans.
- `docs/PROGRESS.md` — fait / en cours / suivant, pour reprendre à froid.
- `docs/ARCHITECTURE.md`, `docs/adr/NNNN-titre.md` (une décision structurante = un ADR d'une page), `docs/DECISIONS.md`, `docs/SYNC_PROTOCOL.md`, `docs/RUNBOOK.md` (§13.8), `docs/SETUP_COMPTES.md` (les actions manuelles à ma charge, avec les valeurs exactes à recopier), `README.md`.

**Git.** Commits atomiques (Conventional Commits), une branche par lot, `.env.example` à jour, jamais de secret dans l'historique, jamais de `--force` sur `main`. Un `pre-commit` qui détecte les secrets (gitleaks).

**Qualité non négociable.** Go : `golangci-lint`, erreurs enveloppées et jamais ignorées, contexte propagé partout, pas de variable globale mutable. Dart : `flutter analyze` sans avertissement, `very_good_analysis`. Tests des règles métier, tests d'isolation entre foyers, aucune chaîne d'interface en dur.

**Actions humaines.** Quand tu as besoin de moi (créer un compte, brancher un téléphone, payer quelque chose, valider une clé), dis-le explicitement, en une liste courte et actionnable.

---

## 2. Vision, principes, non-objectifs

### 2.1 Vision

« Le tableau de bord du foyer : tout ce qu'il y a à faire à la maison, qui s'en occupe et pour quand — aussi simple qu'un post-it sur le frigo, aussi fiable qu'un outil professionnel, et hébergé chez nous. »

Positionnement : l'esprit d'un Jira ou d'un kanban, sans le jargon. Pas de sprint, pas d'epic, pas de workflow configurable. Des tâches, des personnes, des dates, des rappels.

### 2.2 Principes produit (ils arbitrent tous les choix)

1. **Dix secondes pour créer une tâche.** Seul le titre est obligatoire.
2. **Zéro formation.** Compréhensible par un enfant de 8 ans et par un grand-parent.
3. **Mobile d'abord, à une main.** Actions principales dans la zone du pouce.
4. **Divulgation progressive.** Les options avancées existent mais ne se voient pas tant qu'on n'en a pas besoin.
5. **Utile même seul.** L'app sert dès le premier utilisateur, avant que les autres ne rejoignent (membres sans compte, §5 E02).
6. **Bienveillance.** Aucune culpabilisation. On dit « À rattraper », pas « En retard ». Aucun classement imposé.
7. **Gratuit pour les utilisateurs, pour toujours.** Ni publicité, ni offre premium, ni revente de données, ni traqueur tiers, ni compte obligatoire chez un tiers.
8. **Souveraineté des données.** Tout vit sur un serveur que nous contrôlons, dans l'UE. Export et suppression en libre-service. L'app doit pouvoir pointer vers un autre serveur (§8.6) : un foyer technicien peut s'auto-héberger.
9. **Hors-ligne d'abord, pas « hors-ligne toléré ».** L'app est une base locale qui se synchronise, pas un client d'API qui met en cache. Supermarché, cave, métro, avion : tout fonctionne.
10. **L'app s'adapte au foyer.** Solo : aucune notion d'équipe à l'écran. Coloc : rotation. Famille : mode enfant.

### 2.3 Non-objectifs

Gestion de projet professionnelle · messagerie instantanée · budget et banque · coffre-fort de documents sensibles · IA via API payante · SMS · haute disponibilité multi-région (un VPS, une sauvegarde solide, un plan de reprise : c'est proportionné à l'enjeu).

### 2.4 Indicateurs de succès

Calculés en base, agrégés, sans traqueur tiers : activation (foyer avec au moins 2 membres et 5 tâches créées en 7 jours) · délai jusqu'à la première tâche (moins de 60 s) · rétention à 4 semaines · tâches terminées par foyer et par semaine · taux d'activation des notifications · part de foyers multi-membres · **taux de synchronisation en échec** et **âge maximal de la file hors-ligne** (indicateurs techniques, mais ce sont eux qui font fuir les utilisateurs).

---

## 3. Personas et cas d'usage

### 3.1 Personas

| Persona | Contexte | Attentes prioritaires |
|---|---|---|
| Camille et Jordan, couple | Deux adultes actifs | Partager équitablement, ne plus « porter la liste dans sa tête », rappels fiables |
| Famille Martin | Deux parents, trois enfants (6, 10, 15 ans) | Routines des enfants, activités et rendez-vous, motivation par points, validation parentale, vue par enfant |
| Coloc des Lilas | Quatre colocataires, du turn-over | Rotation équitable des corvées, règles de la maison, transparence |
| Inès, solo | Vit seule | Routines, échéances administratives, entretien du logement, sans notion d'équipe |
| Famille recomposée, garde alternée | Enfants une semaine sur deux | Tâches actives seulement quand l'enfant est là, appartenance à deux foyers |
| Paul, aidant | Gère aussi le foyer de sa mère âgée | Plusieurs foyers, tâches pour un proche sans smartphone |
| Invités ponctuels | Baby-sitter, aide ménagère, grand-parent | Accès limité et temporaire à quelques tâches et consignes |

### 3.2 Cas d'usage par domaine de vie

- **Ménage et linge** : poubelles en rotation, ménage hebdomadaire par pièce, lessives, draps, grand ménage.
- **Courses et repas** : liste de courses partagée en temps réel, **utilisable hors-ligne au supermarché**. V2 : menus de la semaine qui alimentent la liste.
- **Enfants** : routines matin et soir avec pictogrammes, devoirs, cartable, activités extrascolaires (« qui emmène mercredi ? » en alternance), rendez-vous médicaux, fournitures, argent de poche lié aux points, vue « par enfant ».
- **Administratif** : impôts, assurances, contrôle technique, papiers à renouveler, contrats à renégocier, relevés de compteur, loyer, inscriptions. Rappels longs (J-60, J-30, J-7).
- **Sport et santé** : séances récurrentes, objectif hebdomadaire, rendez-vous, rappel de traitement (simple rappel, aucun conseil médical).
- **Animaux** : nourrir, promener, litière, vaccins, antiparasitaires.
- **Maison et véhicules** : chaudière, détecteur de fumée, filtres, jardin, pneus, révision. Souvent « X jours après la dernière fois ».
- **Moments de vie** : vacances, déménagement, rentrée, bébé, réception. Checklists prêtes à l'emploi.
- **Colocation** : rotation équitable et visible, règles de la maison, arrivées et départs. V2 : dépenses partagées.
- **Solo** : routines et échéances, interface sans assignation.
- **Proches et invités** : consignes baby-sitter, checklist aide ménagère, tâches pour un parent âgé sans smartphone.
- **Tablette murale** (V2) : l'écran du frigo affiche la journée de chacun.

---

## 4. Glossaire du domaine

| Terme | Définition |
|---|---|
| **Foyer** | L'espace partagé (le « namespace », le tenant). Un utilisateur peut appartenir à plusieurs foyers |
| **Membre** | Une personne du foyer, **avec ou sans compte**. Un membre sans compte peut recevoir des tâches et revendiquer son profil plus tard |
| **Rôle** | Admin, Adulte, Enfant, Invité (§6.7) |
| **Tâche** | Quelque chose à faire, avec un statut |
| **Série** | La définition d'une tâche récurrente (récurrence, rotation, valeurs par défaut) |
| **Occurrence** | Une tâche concrète générée par une série. **Une seule occurrence ouverte par série** |
| **Événement** (V1) | Un créneau daté (rendez-vous, entraînement). Même table, `kind = event`, sans cycle de statut |
| **Catégorie** | Classement unique par tâche (Ménage, Administratif, Enfants…) |
| **Étiquette** (V1) | Libellé libre, plusieurs par tâche. Sert aussi de « projet » : `#Lina`, `#voiture`, `#déménagement` |
| **Liste** (V1) | Liste à cocher partagée : courses, valise, achats |
| **Rappel** | Notification planifiée liée à l'échéance d'une tâche |
| **Points, récompenses** (V1) | Motivation optionnelle, activable par foyer |
| **Mémo** (V1) | Notes épinglées du foyer : wifi, numéros utiles, règles, consignes |
| **Boîte d'envoi** (*outbox*) | File locale des mutations faites hors-ligne, en attente d'envoi au serveur |
| **Curseur de synchronisation** | Position du client dans le journal de changements du foyer (§10.3) |
| **Pierre tombale** (*tombstone*) | Ligne supprimée conservée le temps que tous les clients voient la suppression |

---

## 5 à 20 — SECTIONS NON FOURNIES

Le document d'origine s'arrête au §4. Les sections §5 à §20 n'ont jamais été transmises et sont
réputées inexistantes (décision `D-015` du 20/09/2026). Elles sont remplacées par des documents
rédigés par l'équipe et validés par le product owner :

| Section attendue | Contenu attendu | Document de remplacement |
|---|---|---|
| §5 | Épiques, dont E02 « membres sans compte » | Backlog de phase dans `docs/PLAN.md` |
| §6.7 | Rôles Admin / Adulte / Enfant / Invité | `docs/ARCHITECTURE.md` (Phase 3) |
| §8.2, §8.3 | Arbitrages Go↔Rust, Flutter↔KMP | `docs/DECISIONS.md` D-002, D-003 |
| §8.6 | Serveur personnalisable par le foyer | `docs/ARCHITECTURE.md` |
| §10.3 | **Protocole de synchronisation** | `docs/SYNC_PROTOCOL.md` — lot L0.9, avant toute ligne de Phase 1 |
| §13.2, §13.7, §13.8 | Orchestration, exercice de restauration, runbook | `docs/RUNBOOK.md` (Phase 2) |
| §15.4 | Compatibilité serveur ↔ 2 dernières versions de l'app | `docs/SYNC_PROTOCOL.md` |
| §16.1 | Distribution Android et vérification développeur | `docs/DECISIONS.md` D-006 |
| §18 | Découpage en phases | `docs/PLAN.md` |
| §20 | Questions de cadrage | Traitées en séance le 20/09/2026 → `docs/DECISIONS.md` |

Si le document original complet réapparaît un jour, il écrase ce fichier, et les décisions
ci-dessus sont rejouées contre lui.
