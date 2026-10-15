# Actions manuelles — à la charge du product owner

Tout ce qui ne peut pas être fait par un script : créer un compte, payer, brancher un
appareil, détenir une clé. Chaque entrée donne les **valeurs exactes à recopier**.

Ce fichier est versionné dans un dépôt public : il ne contient aucun secret, aucune
adresse privée, aucun nom d'hôte interne.

Mise à jour : 15 octobre 2026.

---

## Maintenant — phase 0

### 1. Créer le dépôt distant

GitHub → New repository.

| Champ | Valeur |
|---|---|
| Owner | `thatwasyahya` |
| Repository name | `choreo` |
| Visibility | **Public** (`D-008`) |
| Add a README | **non** — il existe déjà |
| Add .gitignore | **non** — il existe déjà |
| Choose a license | **non** — `LICENSE` (AGPL-3.0) est déjà dans le dépôt |

Puis, dans le dossier du projet :

```sh
git remote add origin git@github.com:thatwasyahya/choreo.git
git push -u origin main
```

La clé SSH est déjà en place : la configuration SSH locale épingle une clé dédiée pour
`github.com`.

### 2. Masquer l'adresse e-mail — à faire **avant** le premier `push`

Le dépôt est public, et un commit poussé est poussé pour de bon.

1. GitHub → Settings → Emails → cocher **Keep my email address private**.
2. Relever l'adresse affichée, de la forme `12345678+thatwasyahya@users.noreply.github.com`.
3. L'appliquer :

```sh
git config user.email '<ID>+thatwasyahya@users.noreply.github.com'
git rebase -r --exec 'git commit --amend --no-edit --reset-author' --root
```

La réécriture ne coûte rien tant que rien n'est publié. Elle coûtera cher après.

### 3. Brancher un téléphone Android

Le dépôt est prêt à produire un APK, mais `adb devices` est vide.

1. Sur le téléphone : Paramètres → À propos → taper 7 fois sur « Numéro de build ».
2. Options pour les développeurs → **Débogage sans fil** → activer.
3. « Associer l'appareil avec un code d'association » : relever l'adresse et le code à six
   chiffres, puis depuis WSL :

```sh
adb pair    <adresse-affichee-dans-la-fenetre-d-association>
adb connect <adresse-affichee-sur-l-ecran-principal>
flutter devices    # le téléphone doit apparaître
```

Deux ports différents : celui de l'association et celui de la connexion. C'est normal.

Le débogage **sans fil** est retenu pour éviter `usbipd-win`, qui demande de rattacher le
port USB à WSL à chaque branchement.

---

## Phase 2 — première mise en ligne

### Nom de domaine

Reporté par décision (`D-018`) jusqu'au premier APK fonctionnel. Au 20/09/2026,
**`choreo.eu` était libre**, ainsi que `choreo.fr`. `choreo.com` est pris.

| Champ | Valeur |
|---|---|
| Domaine | `choreo.eu` (~5 €/an) |
| Registraire | OVH, pour n'avoir qu'une facture avec le VPS |
| Puis | renseigner `CHOREO_DOMAIN=choreo.eu` dans `.env` |

### VPS

| Champ | Valeur |
|---|---|
| Fournisseur | OVH (`D-005`) |
| Gabarit | 2 vCPU, 4 Go, 60-80 Go NVMe, ~5 €/mois |
| Région | France ou Allemagne — **UE obligatoire** (RGPD) |
| Système | Ubuntu LTS |

À l'ouverture : utilisateur non root, connexion par mot de passe désactivée, et seulement
22, 80 et 443 ouverts. Tout cela passera par le playbook Ansible — ne rien faire à la main
(règle d'exploitation 1).

### Sauvegarde hors site

**Chez un autre fournisseur que celui du VPS.** L'incendie de Strasbourg en mars 2021 a
détruit les données des clients qui n'avaient pas de copie ailleurs. 10 Go suffisent, et
les offres gratuites S3-compatibles couvrent le besoin.

---

## Phase 4 — ouverture

| Poste | Coût | Quand |
|---|---|---|
| Compte développeur Google Play | 25 $ une fois | avant la première publication sur le Play Store |
| Vérification développeur Android | incluse avec le compte Play | obligatoire à terme sur tous les canaux |

Avant cela, la distribution se fait par APK direct et dépôt F-Droid auto-hébergé, sous le
régime de distribution limitée : **gratuit, 20 appareils maximum** (`D-006`).

---

## Phase 5 — iOS, conditionnelle

| Poste | Coût |
|---|---|
| Apple Developer Program | **99 $/an**, seule voie existante, TestFlight inclus |
| Machine macOS | runners macOS gratuits de GitHub Actions, le dépôt étant public |

Reporté jusqu'à ce que le projet dégage un revenu (`D-004`). Rien à engager.

---

## Rien à payer aujourd'hui

Phase 0 et phase 1 : **0 €**. Le développement et la bêta locale passent par le réseau
local, et une machine de test déjà disponible prend le relais en phase 2 (`D-017`).
