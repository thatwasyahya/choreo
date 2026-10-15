# 0005 — Les notifications sont calculées sur l'appareil, sans service de push

- **Statut** : accepté
- **Date** : 20 septembre 2026
- **Décision** : `D-010`

## Contexte

Le §0 du cahier des charges exclut Firebase. Or Firebase Cloud Messaging est la voie
habituelle pour notifier un Android, ce qui ressemble à une contradiction : comment tenir
les rappels longs du §3.2 — J-60, J-30, J-7 sur les impôts, l'assurance, le contrôle
technique — et l'indicateur « taux d'activation des notifications » du §2.4, sans
passerelle de push ?

## Décision

**Aucun service de push en v1.** Les notifications sont de deux natures, et une seule
aurait eu besoin d'une passerelle.

### Les rappels datés : locaux, intégralement

C'est possible précisément parce que l'application est **hors-ligne d'abord**. Le téléphone
détient déjà les échéances — ce n'est pas un cache, c'est la base. Il n'a donc rien à
demander à personne pour savoir qu'il doit sonner mardi à 8 h.

`WorkManager` et les notifications locales suffisent. Zéro infrastructure, et ça fonctionne
en avion. C'est **l'écrasante majorité du volume** : rappels administratifs, routines du
matin, récurrences, entretien du logement.

### Les événements entre membres : à la synchronisation suivante

« Jordan t'a assigné une tâche » a besoin d'un réveil venu de l'extérieur. En v1, il est
livré à la synchronisation suivante — déclenchée par l'ouverture de l'application et par
une tâche de fond périodique.

## Conséquences

- Une pièce mobile de moins à exploiter, et aucun tiers qui apprenne quand une famille
  fait son ménage.
- Les rappels qui comptent le plus pour l'utilisateur sont aussi les plus fiables :
  ils ne peuvent pas échouer, puisqu'ils ne traversent rien.
- **Coût assumé** : une tâche assignée par quelqu'un d'autre peut mettre quelques dizaines
  de minutes à se signaler. Pour un foyer, c'est acceptable. Pour une messagerie
  instantanée, ça ne le serait pas — et le §2.3 range la messagerie dans les non-objectifs.
- L'horloge de l'appareil devient un sujet : un rappel local suit l'horloge locale. Le
  protocole de synchronisation doit en tenir compte (voir `SYNC_PROTOCOL.md`).

## Si cela ne suffit pas : UnifiedPush, pas Firebase

Le jour où le besoin est démontré, la suite est **UnifiedPush avec un relais `ntfy`
auto-hébergé** — cohérent avec la distribution F-Droid retenue en `D-006`, et derrière une
interface, comme l'exige la règle d'exploitation 8.

Firebase reste exclu : adopter une passerelle Google pour notifier des tâches ménagères
reviendrait à annuler le principe 7 pour un confort d'implémentation.
