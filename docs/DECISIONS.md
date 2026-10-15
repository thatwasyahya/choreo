# Décisions

Journal des arbitrages. Une entrée = une décision, sa raison, son statut.
Les décisions structurantes ont en plus un ADR d'une page dans `docs/adr/`.

Statuts : **Ferme** (ne se rediscute pas sans motif neuf) · **Provisoire** (à revoir à une phase donnée) · **En attente** (bloque quelque chose).

---

## Séance de cadrage — 20 septembre 2026

### D-001 — Nom et domaine · Ferme

`APP_NAME` = **Choreo**. Domaine **`choreo.eu`**.

*Pourquoi* : de *chore* (corvée) et *choreography* — la chorégraphie du foyer, chacun sait ce qu'il
fait et quand, sans chef d'orchestre. Deux syllabes, prononçable sans effort en français comme en
anglais, s'écrit sans hésitation après l'avoir entendu.

*Vérifié* : 28 noms testés en `.app` par RDAP, tous pris ; idem en `.xyz`. `choreo.fr`, `choreo.eu`
et `choreo.co` libres au 20/09/2026, `choreo.com` pris. `.eu` retenu : le moins cher et cohérent
avec le principe 8 (souveraineté, hébergement UE).

*Réserve connue* : WSO2 exploite une plateforme d'intégration nommée Choreo sur `choreo.dev`.
Secteur sans rapport, risque de confusion faible mais non nul, même classe de dépôt de marque.
Accepté par le product owner.

### D-002 — Back-end : Go · Ferme

Alternative écartée : Rust. Sur un VPS 4 Go avec un monolithe modulaire, Rust n'apporte rien que Go
ne fasse déjà, et l'écosystème Go existant du product owner pèse plus lourd que l'écart de
performance.

### D-003 — Mobile : Flutter + Dart · Ferme

Alternative écartée : Kotlin Multiplatform + Compose Multiplatform. Cohérent avec D-004 : iOS est
reporté, pas annulé, donc l'interface ne sera pas à réécrire le jour où il arrive.
Conformément au §0.1, ce choix ne se rediscute pas en cours de route.

### D-004 — iOS reporté · Provisoire, à revoir quand le projet génère un revenu

Aucun code spécifique iOS, mais rien dans l'architecture qui l'interdise. Le compte développeur
Apple à 99 $/an est la seule voie de distribution iOS existante ; la dépense n'est pas engagée.

### D-005 — Hébergement : OVH, mais pas avant la Phase 2 · Ferme

| Quand | Dépense |
|---|---|
| Maintenant | domaine `choreo.eu`, ~5 €/an |
| Phases 0 et 1 | **0 €** — la pile `docker compose` tourne sur la machine de dev, exposée aux téléphones de test par le LAN ou un tunnel gratuit |
| Phase 2, premier foyer extérieur | VPS OVH, ~5 €/mois |

*Pourquoi pas « zéro euro absolu »* : les trois portes sont fermées. Les offres gratuites
d'hébergeur sont interdites par le §0 et structurellement fragiles (Heroku 2022, Railway 2023,
récupération d'instances inactives chez Oracle). L'auto-hébergement à domicile n'est ni gratuit ni
tenable pour des données de tiers. Et le pair-à-pair sans serveur tombe sur le besoin n°1 : deux
téléphones d'un même foyer ne sont presque jamais sur le même Wi-Fi au même moment — les CRDT
règlent la fusion des modifications, pas leur acheminement. Il faut un relais.

*Pourquoi c'est peu cher quand même* : voir D-010. Le serveur n'est qu'un relais de synchronisation,
pas le cerveau. Quelques kilo-octets par foyer et par jour.

*Ce qui rend ce oui réversible* : hors-ligne d'abord = chaque téléphone est une réplique complète,
donc la perte du serveur n'est pas une perte de données ; l'URL du serveur est un réglage (§8.6) ;
`docker compose up` permet à n'importe qui d'héberger la sienne.

*Sur OVH plutôt que Hetzner* : pas d'élément solide sur une fiabilité moindre des VPS d'entrée de
gamme Hetzner, mais leur détection de fraude est réputée sévère, ce qui est un risque réel pour un
opérateur solo. OVH est de toute façon un bon choix : français, UE, RGPD natif, domaine et VPS sur
la même facture. À retenir les concernant : l'incendie de Strasbourg de mars 2021 a détruit les
données des clients sans sauvegarde hors site — la sauvegarde ira chez un autre fournisseur, jamais
dans un second espace OVH.

### D-006 — Distribution Android : APK direct et F-Droid d'abord, Google Play ensuite · Ferme

Phase 2 : APK direct + dépôt F-Droid auto-hébergé, sous le régime de « distribution limitée »
(gratuit, 20 appareils maximum). Ce plafond de 20 appareils cadre exactement avec D-005 : le
périmètre qui n'a pas besoin de VPS. Phase 4 : compte Google Play à 25 $ une fois.

### D-007 — CI/CD : GitHub Actions · Ferme

### D-008 — Visibilité du dépôt : **public** · Ferme et irrévocable

Tranché par le product owner le 20/09/2026, contre la recommandation de l'équipe qui privilégiait
« privé d'abord » pour garder les deux options ouvertes. Décision assumée.

*Ce que ça apporte* : intégration continue sans plafond de minutes, runners macOS gratuits le jour
où iOS arrive (`D-004`), dépôt F-Droid possible (`D-006`), et surtout le principe 8 tenu pour de
vrai — un foyer technicien peut réellement s'auto-héberger.

*Ce que ça engage* : ce qui est publié l'est définitivement. Repasser le dépôt en privé plus tard ne
dépublierait rien — forks, clones, archives et moteurs de recherche subsistent.

*Conséquences à traiter en Phase 0* :

- **Fichier `LICENSE` obligatoire avant la première poussée.** Recommandation : **AGPL-3.0**, la
  seule famille de licences qui empêche un tiers d'exploiter une version fermée du service en
  gardant le code pour lui — cohérent avec « gratuit pour toujours » et avec le principe 8. Sans
  fichier de licence, le code est « tous droits réservés » : visible, mais personne n'a légalement le
  droit de l'auto-héberger, ce qui viderait le principe 8 de son sens.
- **Adresse de commit** : l'adresse personnelle apparaîtrait dans chaque commit public. Basculer sur
  l'adresse `users.noreply.github.com` (GitHub → Settings → Emails → *Keep my email private*), sans
  perdre l'attribution à `thatwasyahya`.
- **Aucun secret, aucune adresse IP privée, aucun nom d'hôte interne dans un fichier versionné.**
  Vaut en particulier pour `D-017` : les coordonnées du minipc restent dans le `~/.ssh/config` du
  product owner, jamais dans le dépôt.

### D-009 — Authentification : identifiant et mot de passe, pas d'OTP · Ferme

Inscription par **identifiant + mot de passe** (hachage argon2id), jetons d'accès courts et jeton de
rafraîchissement. **Aucune adresse e-mail obligatoire.**

- Rejoindre un foyer se fait par **code d'invitation ou QR code**, transmis de la main à la main ou
  par le canal que l'utilisateur veut. Aucun envoi côté serveur.
- La récupération passe par un **code de secours affiché à l'inscription** (modèle Signal, Bitwarden),
  et par la ré-invitation par un admin du foyer.
- L'e-mail devient **optionnel**, ajouté plus tard, derrière l'interface du §1.1 n°8.

*Pourquoi pas l'OTP* : un code à usage unique doit être acheminé. Par SMS, c'est un non-objectif
explicite du §2.3 et c'est payant. Par e-mail, il faut un relais SMTP — c'est-à-dire précisément le
service externe qu'on veut éviter, avec en prime le cauchemar de délivrabilité de l'auto-hébergement
de courrier, et une connexion qui devient impossible dès que le courrier tombe.

*Pourquoi c'est aussi le plus correct* : cohérent avec la notion de « membre sans compte » du
glossaire, et avec le principe 5 (utile même seul) — on crée un foyer et on s'en sert sans rien
donner à personne.

### D-010 — Notifications : locales sur l'appareil, aucun service de push en v1 · Ferme

Firebase Cloud Messaging **exclu** (§0 : aucun service géré).

- **Tous les rappels datés sont calculés et déclenchés sur le téléphone** : J-60 / J-30 / J-7 sur
  l'administratif, routines, récurrences. C'est possible précisément parce que l'app est
  hors-ligne d'abord — l'appareil détient déjà les échéances. Zéro infrastructure, et ça marche en
  avion. C'est l'écrasante majorité du volume, donc de l'indicateur « taux d'activation des
  notifications » du §2.4.
- Les événements entre membres (« Jordan t'a assigné une tâche ») sont livrés à la synchronisation
  suivante, déclenchée par l'ouverture de l'app et par une tâche de fond périodique.
- **Phase 4, si le besoin est démontré** : UnifiedPush avec un relais ntfy auto-hébergé — cohérent
  avec la distribution F-Droid de D-006. Derrière une interface, conformément au §1.1 n°8.

### D-011 — Environnement de développement : tout dans WSL2 · Ferme

Tout le travail se fait dans Ubuntu 24.04 (WSL2), jamais via le chemin `\\wsl.localhost` depuis un
outil Windows : Gradle et Flutter y sont d'une lenteur rédhibitoire et s'y heurtent à des problèmes
de permissions et de casse. Détail de la remise en état dans `docs/PLAN.md`, lot L0.1.

### D-012 — Routeur HTTP : `net/http` de la bibliothèque standard · Ferme

Le routage par motifs de méthode et de chemin disponible depuis Go 1.22 suffit à l'API prévue. Une
dépendance de moins à maintenir et à auditer (§1.1 n°4).

### D-013 — Migrations : goose, en avant uniquement · Ferme

Fichiers SQL embarqués dans le binaire du serveur, appliqués au démarrage. Conforme au §1.1 n°7.

### D-014 — Base locale mobile : Drift (SQLite) · Ferme

Structurant, donc tranché dès la Phase 0 : l'app *est* une base locale qui se synchronise (principe 9),
ce n'est pas un détail d'implémentation qu'on choisit en Phase 1.

### D-015 — Les §5 à §20 du cahier des charges n'existent pas · Ferme

Le document transmis s'arrête au §4. Les sections manquantes ont été réclamées deux fois sans suite.
Elles sont réputées inexistantes et remplacées par des documents rédigés par l'équipe puis validés :
table de correspondance en fin de `docs/CAHIER_DES_CHARGES.md`.

**Conséquence la plus lourde** : le §10.3, protocole de synchronisation, est à concevoir de zéro.
C'est le cœur technique du produit et le premier risque du projet. Il fait l'objet du lot L0.9 et
doit être validé **avant** la première ligne de Phase 1.

### D-016 — Identité et messages de commit · Ferme

Tous les commits sous `thatwasyahya` / identité personnelle, épinglée en configuration locale du
dépôt. **Aucune mention d'un outil d'assistance** dans les messages de commit, les `Co-Authored-By`
ou les descriptions de pull request.

### D-017 — Un serveur de test physique existe : le « minipc » · Ferme

Le product owner dispose d'un mini-PC sur son réseau local, déjà monté en serveur de test et
accessible par `ssh minipc` — coordonnées dans son `~/.ssh/config`, **jamais dans le dépôt**
(voir `D-008`).

*Ce que c'est réellement*, constaté le 20/09/2026 après connexion : **pas une machine Linux nue,
mais une instance WSL2 Ubuntu 24.04 hébergée dans un poste Windows**, jointe par redirection de port.

| | |
|---|---|
| RAM de l'instance | **2,4 Go au total, ~1,6 Go disponibles** — à comparer aux 4 Go du VPS cible du §0.1 |
| Disque | 1 To, occupé à 1 % (6,3 Go). L'espace n'est pas la contrainte, la mémoire l'est |
| Docker | 29.7.2, présent |
| `systemd` | actif en PID 1 — les services peuvent démarrer tout seuls |
| Déjà en service | **MinIO** (stockage objet compatible S3) et **cloudflared** (tunnel Cloudflare) |

*Rôle retenu — révisé par le product owner le 20/09/2026* : **la machine n'est pas utilisée avant
que l'application soit fonctionnelle.** Un projet de test du product owner y tourne déjà et se
réserve la mémoire. Tout le travail des Phases 0 et 1 reste sur le poste de développement.

- **Phase 2** : première cible de déploiement distante, avant le VPS. Répétition générale de la mise
  en ligne, sans rien payer.
- **Ensuite** : machine de répétition de l'exercice de restauration du §1.1 n°2 (« reprovisionner un
  serveur vierge et restaurer en moins de 60 minutes »). Rôle durable, pas un dépannage.

*Deux briques déjà présentes qui resserviront* : le tunnel `cloudflared` est exactement le mécanisme
prévu pour exposer un serveur de bêta sans IP publique ni domaine (`D-018`) ; et MinIO permettra de
construire et d'éprouver la chaîne de sauvegarde du §1.1 n°3 sans rien payer — étant entendu qu'une
sauvegarde sur la même machine n'est pas une sauvegarde hors site, et que la cible réelle sera
ailleurs.

*Ce qu'il ne sera jamais* : le serveur de production. Adresse résidentielle, aucun engagement de
service, 2,4 Go de mémoire partagée, et une instance WSL2 dont la survie dépend d'une session
Windows. Acceptable pour une bêta entre proches, pas pour des foyers tiers.

*Accès* : débloqué par le product owner le 20/09/2026 (`ssh-add`). La clé chargée dans l'agent est
la clé professionnelle ; sans incidence ici, GitHub restant épinglé sur `id_ed25519_personal` par le
`~/.ssh/config`.

### D-018 — Achat du domaine reporté après le premier APK fonctionnel · Provisoire

Tranché par le product owner. Sans incidence technique : rien en Phase 0 ni en Phase 1 n'a besoin
du domaine — le développement et la bêta locale passent par le réseau local et le minipc (`D-017`),
sans TLS public. Le domaine redevient nécessaire en Phase 2, pour les certificats et les Android
App Links.

*Seul risque* : `choreo.eu` était libre au 20/09/2026 et n'est pas réservé. Quelqu'un peut le
prendre entre-temps. Si cela arrive, `choreo.fr` était libre à la même date, et le nom `Choreo`
lui-même n'est pas engagé tant qu'aucun dépôt public ne le porte.

---

## 15 octobre 2026

### D-019 — Protocole de synchronisation : intentions montantes, états descendants · **En attente de validation**

`docs/SYNC_PROTOCOL.md` remplace le §10.3 jamais transmis (`D-015`). **Ce document bloque le
démarrage de la Phase 1** : il fixe le schéma de données, l'API et le comportement hors-ligne.

*Le mécanisme* : les **intentions** montent (« coche cette tâche », « renomme ce champ,
j'avais vu la révision 48 102 »), les **états de lignes** descendent déjà fusionnés, un
compteur par foyer (`households.change_seq`, incrémenté sous verrou de ligne) ordonne tout, et
**aucune horloge de téléphone n'arbitre jamais rien** — la détection de conflit est causale
(« cet auteur avait-il vu la valeur actuelle ? »), pas chronologique.

*La conséquence la plus structurante* : **il n'y a pas de moteur de fusion sur le téléphone.**
Appliquer un changement, c'est remplacer la ligne si la révision entrante est supérieure.
Idempotent, insensible à l'ordre d'arrivée. Une seule implémentation de la fusion existe, en
Go. Les quatre propositions examinées payaient toutes le même prix — deux réducteurs, en Go et
en Dart, devant rester d'accord pour toujours — et ce prix est supprimé, pas atténué.

*L'invariant « une seule occurrence ouverte par série »* est une contrainte **entre lignes**,
qu'aucune règle de fusion par ligne ne peut exprimer. Il vit donc dans une colonne
(`series.open_occurrence_id`), avec un compare-et-échange sous le verrou du foyer, et une
identité d'occurrence dérivée d'un **entier** (`uuidv5(series_id || ':' || series_index)`) et
non d'une date : deux téléphones hors-ligne calculent le même identifiant, l'insertion du
second devient un `ON CONFLICT DO NOTHING`, et rien ne casse pendant un déploiement progressif
à cause d'un écart de base `tzdata` entre une app N-1 et le serveur.

*Méthode* : quatre protocoles conçus indépendamment, chacun démonté par un relecteur adverse.
Scores des juges — `rowsync` 8/10 en simplicité et 5 défauts fatals, contre 3/10 et 10 défauts
pour le journal d'opérations, 2/10 et 9 pour les CRDT, 2/10 et 6 pour l'hétérogène. La synthèse
part de la base la plus simple et y greffe ce que les autres avaient de bon.

*Écarté, pour que la question ne revienne pas* : les **CRDT** convergent mais ne savent pas
exprimer l'invariant des séries, et personne seul ne les débogue à une heure du matin ; les
**horloges logiques hybrides** remplacent une horloge fausse par une horloge fausse plafonnée,
et le plafond récompense le tricheur ; le **journal d'opérations rejoué par le client** impose
les deux réducteurs ; la **dernière-écriture-gagne globale** est exactement le mécanisme par
lequel un téléphone qui refait surface écrase la matinée du foyer.

*Amendement à valider en même temps* (Q9 du document) : « le serveur est un relais, pas le
cerveau » devient « un relais **et l'arbitre de cinq invariants** » — séquence du foyer,
provenance des champs, unicité de l'occurrence ouverte, rôles et appartenance, borne sur
`occurred_on`. Cinq, et pas plus. `docs/ARCHITECTURE.md` et `CLAUDE.md` portent déjà la
correction, marquée comme conditionnelle.

*Huit autres questions ouvertes*, toutes du ressort du product owner, chacune avec un défaut
proposé et son arbitrage : premier ou dernier écrivain gagne, attribution des points sur une
corvée cochée deux fois, vidage du plateau « À revoir », rétention à 90 jours, exposition de
l'historique, destinataire d'une décision « édition contre suppression », borne de 14 jours sur
`occurred_on`, et boucle de synchronisation fusionnée ou non en multi-foyers.

→ Tranchées en `D-020`.

### D-020 — Les neuf questions ouvertes du protocole · Ferme

**Tranchées par délégation**, le product owner ayant demandé « les meilleurs choix, les plus
recommandés, mais ça doit rester gratuit ». À distinguer d'une validation : le document de
1 499 lignes n'a pas été relu par lui. Revenir sur l'une de ces réponses reste peu coûteux
**tant qu'aucune ligne de Phase 1 n'est écrite** ; après, c'est une migration de données.

Sept réponses sur neuf confirment le défaut proposé — non par facilité, mais parce que les
raisons écrites dans le protocole tiennent. Les deux écarts sont Q2 et Q8.

| | Question | Réponse | Pourquoi |
|---|---|---|---|
| **Q1** | Premier ou dernier écrivain gagne | **Premier** | Une valeur déjà vue ne change plus sous les yeux, et un téléphone absent dix jours ne peut pas écraser la matinée du foyer. Personne ne perd son texte dans aucune des deux variantes : seule change la valeur affichée avant réponse |
| **Q2** | Points sur une corvée cochée deux fois | **Les deux marquent** | *Écart.* « Le premier marque » crée une course au clic, et le partage enseigne l'arithmétique de la rancune : les deux contredisent le principe 6 (« aucun classement imposé »). Les points sont une fiction motivationnelle, pas une ressource rare — les dédoubler ne coûte rien |
| **Q3** | Le plateau « À revoir » peut-il se vider seul | **Non pour les décisions, « Tout écarter » groupé pour les reçus** | Une décision attend un humain, c'est le cœur de la bienveillance. Mais quarante *reçus* sur un téléphone revenu de trois mois est une punition, pas une information |
| **Q4** | Rétention | **90 jours**, défaut inchangé | Le coût est de 1 à 2 Mo par foyer sur un NVMe de 60 à 80 Go : le stockage n'est pas un argument. Descendre à 30 forcerait un téléchargement complet à un téléphone qui a passé août dans un sac ; monter n'achète rien et allonge la durée pendant laquelle le serveur détient un journal de la vie du foyer, ce qui coupe contre le principe 8 |
| **Q5** | Historique « qui a fait quoi » visible | **Non exposé en Phases 1 à 3** | Un écran d'historique est un tableau de scores avec des étapes en plus. « Fait par Camille et Jordan » sur la ligne donne l'attribution que réclame la coloc sans la culpabilisation qu'interdit le principe 6. Le serveur le détient de toute façon : c'est une décision d'interface, et elle est **explicitement étendue à la Phase 3** pour qu'un écran n'apparaisse pas par glissement |
| **Q6** | Décision « édition contre suppression » | **À l'éditeur seul** | Le suppresseur a déjà ce qu'il demandait, et deux voix créent une course |
| **Q7** | Borne de `occurred_on` | **14 jours** | Couvre « j'ai oublié de cocher la semaine dernière », qui est le cas réel. Trente jours élargiraient la fenêtre dans laquelle un combiné à l'heure fausse décale une validation, et compliqueraient l'arithmétique des séries `relative`, pour servir « je rattrape le mois d'août » — qui n'arrive pas |
| **Q8** | Boucle de synchronisation fusionnée en multi-foyers | **Non. Une requête par foyer, et en Phase 3 l'endpoint groupé sera une enveloppe** | *Écart, après lecture du protocole.* Le chemin `POST /v1/households/{hid}/sync` porte la frontière de sécurité **dans l'URL** : un contrôle d'appartenance, un verrou de ligne. Un endpoint groupé doit autoriser N foyers par requête, et c'est précisément là qu'un défaut d'isolation irait se loger. Le coût du report est nul : la facture de batterie ne concerne que les membres multi-foyers, qui n'existent pas avant que la Phase 3 livre l'interface. **Contrainte posée** : l'endpoint groupé de Phase 3 enveloppera le chemin par foyer, il ne le réimplémentera jamais |
| **Q9** | Amendement « relais et arbitre de cinq invariants » | **Accepté** | C'est une correction factuelle, pas un choix. `ARCHITECTURE.md` et `CLAUDE.md` la portent |

#### Sur la contrainte « ça doit rester gratuit »

Vérifié, et le protocole ne déplace pas le plafond de `D-005` :

- **Un long poll garé ne tient pas de connexion à la base.** Le gestionnaire relâche sa
  connexion `pgx` et sa transaction *avant* de se garer : un appel en attente est une
  goroutine de ~8 Ko, jamais une connexion `idle in transaction`. C'était le seul mécanisme
  du protocole capable d'imposer un VPS plus gros, et il est déjà écrit correctement.
- **Le réveil coûte une connexion pour tout le processus**, pas une par client : un `LISTEN
  choreo_changes` dédié, et un `NOTIFY` dans la transaction qui valide.
- **La rétention coûte 1 à 2 Mo par foyer.** Mille foyers tiennent dans 2 Go.

Le coût opérateur reste donc **0 € jusqu'à la Phase 2**, puis ~5 €/mois, inchangé. Et
« gratuit pour les utilisateurs, pour toujours » reste intact : aucune de ces réponses
n'introduit de service tiers, de publicité ni de palier payant.

### D-021 — Authentification : jetons opaques, `device_id` frappé par le serveur · Ferme

Spécifiée dans [`docs/AUTH.md`](AUTH.md), document séparé plutôt que section du protocole :
les deux sujets changent pour des raisons différentes, et celui-ci doit pouvoir être relu
seul. `D-009` avait tranché le mécanisme ; il manquait tout point de terminaison.

*Les choix qui engagent* :

- **Des jetons opaques de 32 octets, stockés hachés en sha256 — pas des JWT.** Le seul
  avantage du JWT est de vérifier sans toucher la base, et cet avantage vaut **zéro** ici : le
  protocole exige déjà une lecture à chaque requête, puisque l'appartenance au foyer se
  vérifie contre une ligne `members` vivante à chaque appel. On aurait payé une bibliothèque
  de crypto, une classe de failles connue (confusion d'algorithme, `alg: none`) et une
  cérémonie de rotation de clés pour rien. En échange la révocation est immédiate.
- **Le jeton de rafraîchissement est à usage unique et tourne.** Rejouer un jeton consommé
  révoque tout l'appareil : on ne peut pas savoir si c'est le voleur ou la victime qui
  rejoue, donc on ferme. Bruyant, et voulu — un faux positif coûte une saisie de mot de
  passe, un faux négatif coûte le foyer.
- **`device_id` est frappé par le serveur.** Il porte le curseur de synchronisation : un
  client qui pourrait revendiquer celui d'un autre appareil corromprait sa réplique.
- **argon2id m=19456, t=2, p=1**, paramètres embarqués au format PHC, donc relevables plus
  tard sans invalider un seul compte.
- **Un code d'invitation ne peut jamais accorder `admin`** — la contrainte `CHECK` l'interdit
  en base. Un code qui fuite ne livre pas les clés du foyer.
- **Codes en base32 Crockford**, sans `I`, `L`, `O` ni `U` : un code lu à voix haute n'est
  jamais ambigu, et la saisie pardonne la casse, les tirets et les trois substitutions que
  les gens font vraiment en recopiant.
- **Limitation de débit en mémoire, assumée** : elle se réinitialise au redémarrage et ne
  franchit pas une seconde instance. Écrit pour que ce soit un choix et non une surprise, à
  revoir le jour où il y a deux serveurs.

*Un plafond ajouté après coup, pour tenir la promesse de coût* : **au plus 4 calculs argon2id
simultanés**. Sans lui, le plafond de `D-005` était une fiction — chaque calcul retient
19 Mio, et les seaux sont par identifiant et par adresse, donc une tentative répartie sur
beaucoup d'identifiants distincts passe les deux et arrive quand même d'un coup. Quatre cents
tentatives simultanées réclameraient près de 8 Go sur une machine qui en a 4. Avec le
plafond, le serveur ralentit sous inondation ; il ne meurt pas. Le calcul à vide servi aux
identifiants inexistants prend un jeton lui aussi, sinon il suffirait d'envoyer des
identifiants faux pour contourner la file.

*Les deux questions ouvertes de `AUTH.md`, tranchées par la même délégation que `D-020`* :

| | Question | Réponse | Pourquoi |
|---|---|---|---|
| **A1** | Durée du rafraîchissement | **180 jours** glissants, et non 60 | Le modèle de menace est un téléphone perdu, et c'est la révocation d'appareil qui y répond, pas l'expiration. Une tablette murale qu'on ne touche qu'au passage ne doit pas se déconnecter — le principe 2 ne survit pas à un grand-parent devant un écran de connexion |
| **A2** | La récupération révoque-t-elle les autres appareils | **Oui**, défaut inchangé | Asymétrie décisive : se tromper en révoquant coûte une reconnexion, se tromper en ne révoquant pas laisse un voleur de code de secours cohabiter en silence. L'interface doit le dire avant de valider |
