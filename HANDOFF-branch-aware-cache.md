# Handoff : cache Nx conscient des branches

Statut : conception terminée, aucun code écrit. Ce document résume les décisions prises et ce qui reste à faire.

Explication visuelle interactive (privée, accès propriétaire) : https://claude.ai/artifact/JxCgJqLomcARKRyXp7WRYV

## Objectif

Isoler les repos entre eux, et dans chaque repo :

- la branche par défaut (`main`),
- des branches de version (patterns configurés, ex. `v1.0.x`, `v1.1.x`),
- des branches de dev et des PR (basées sur `main` ou sur une branche de version).

Chaque ref écrit dans son propre cache. La lecture peut croiser les caches de façon sécurisée. La conclusion de l'étude : c'est faisable, effort moyen.

## État actuel du serveur

- Go, stdlib `net/http`. Presque tout est dans `cmd/nx-caching-server/main.go`.
- Route unique : `/v1/cache/{hash}` (GET, HEAD, PUT) derrière un middleware bearer (`main.go:184-267`).
- Auth : `AUTH_TOKEN` (un token, namespace vide) ou `AUTH_TOKENS=nom:token,...`. Le namespace est la seule dimension de tenant.
- Stockage : `STORAGE_DIR/<namespace>/<hash>.cache` (`cacheFilePath`, `main.go:50-56`).
- PUT : 409 si le fichier existe déjà (first-write-wins), écriture via fichier temporaire puis `os.Link` (`main.go:58-119`).
- GET : met à jour le mtime du fichier à chaque lecture (`main.go:173`). Le nettoyage supprime les fichiers plus vieux que `CLEANUP_THRESHOLD` (`main.go:282-314`), seuil global.
- Aucune notion de branche, de PR ou de TTL par scope.

## Principe retenu

Nx n'envoie que `GET/PUT /v1/cache/<hash>` et un bearer token. Le hash est calculé à partir des entrées de la tâche : un résultat est donc valide pour toute branche ou tout commit qui produit le même hash. Le numéro de commit n'entre jamais dans la logique.

L'isolation vient du token, pas du hash. Le serveur déduit un scope d'écriture et une chaîne de lecture à partir des claims du token. Nx voit un cache plat et ignore les scopes.

## Décisions prises

1. **Identité** : OIDC du forge (GitHub Actions, GitLab CI). Le JWT est passé dans `NX_SELF_HOSTED_REMOTE_CACHE_ACCESS_TOKEN`. Le serveur vérifie la signature via JWKS, plus `iss`, `aud` (valeur choisie), `exp`, et `repository` dans une liste autorisée. Rien de ce que le client envoie hors token n'est cru.
2. **Écriture** : chaque appelant écrit uniquement dans son propre scope.
3. **Confiance** : une PR n'est jamais de confiance. Seul un `push` sur la branche par défaut ou sur un pattern de version écrit dans un scope de confiance.
4. **Lecture** : scope propre, puis la base, puis `main`. Jamais de scope voisin. `main` ne lit jamais les scopes de version ni de PR.
5. **Version branches** : toujours lisent `main`. `main` contient déjà les résultats d'avant la coupe de la branche, donc c'est une base naturelle (pas d'option, contrairement à une version intermédiaire de l'étude).
6. **Après merge** : rien n'est promu ni copié. La branche cible reconstruit dans son propre scope et le scope de PR expire par le nettoyage.

## Algorithme

Entrée : claims du token et config par repo (branche par défaut, patterns de version).

```
trusted = event_name == "push" ET ref correspond à (défaut | pattern de version)

si trusted : own = nom de la branche    chaîne = [own, défaut]   (défaut seulement si own != défaut)
si PR      : own = "pr-" + numéro       chaîne = [own, base_ref si trusted, défaut]
             (numéro extrait de ref = refs/pull/N/merge)
sinon      : own = "dev-" + encode(ref) chaîne = [own, défaut]

scope d'écriture = own
```

Chaînes obtenues :

| Appelant | Chaîne de lecture |
|---|---|
| push main | main |
| push v1.1.x | v1.1.x, main |
| PR sur main | pr-N, main |
| PR sur v1.1.x | pr-N, v1.1.x, main |
| branche dev sans PR | dev-x, main |
| fork PR | pas de token : lecture seule de main si autorisé, aucune écriture |

Lecture : pour chaque scope de la chaîne, tester `STORAGE_DIR/<repo>/<scope>/<hash>.cache`. Premier trouvé : servir et mettre à jour le mtime. Aucun : 404. PUT : écrire dans le scope d'écriture uniquement, en gardant le 409 par scope.

## Sécurité (CVE-2025-36852, "CREEP")

Source : https://nx.dev/blog/cve-2025-36852-critical-cache-poisoning-vulnerability-creep (seul l'article a été lu, pas l'analyse détaillée liée).

- L'attaque : une PR obtient le même hash que `main` (mêmes fichiers) et uploade avant lui. Le first-write-wins sert ensuite son artefact partout. Les checksums ne protègent pas, car le poison est fait avant le hash.
- Le serveur actuel est vulnérable : namespace plat plus 409 donne exactement ce first-write-wins partagé.
- Le design ci-dessus reproduit le modèle décrit pour Nx Cloud (branches protégées écrivent le cache partagé, les autres écrivent dans un cache isolé).
- Un poison dans `pr-N` n'affecte que cette PR. Le build du merge sur `main` doit lire uniquement `main` et ne copier depuis aucun scope de PR (merge queue comprise).
- Pas de token statique (`AUTH_TOKEN`) dans les jobs de PR : il donnerait l'écriture sur un scope de confiance.
- Limite assumée : un compromis d'un push sur `main` ou d'un runner de `main` passe toujours. Pas de signature des artefacts dans ce design.

## Points à vérifier avant ou pendant l'implémentation

- **`pull_request_target` (GitHub)** : à ma connaissance le claim `ref` y vaut la branche de base. Un job de PR pourrait alors ressembler à `main`. Le rejeter comme non fiable via `event_name`. Non testé sur un vrai forge.
- **Durée de vie du JWT** : courte, durée exacte non vérifiée. Un `nx run-many` long peut recevoir des 401 en cours de route. Piste : endpoint d'échange JWT contre un token de session émis par le serveur, lié au même scope et valable le temps du job.
- **Claims GitLab** : noms différents de GitHub (`ref`, `ref_protected`, champs de merge request). Prévoir un petit adaptateur par forge.
- **Branche dev sans PR** : aucune base déclarée, `main` par défaut.
- **Branche coupée depuis une autre version** (`v1.2.x` depuis `v1.1.x`) : seule `main` est en base. Un champ `inherits` en config règle ça si besoin.
- **Expiration** : le nettoyage actuel est global. `main` devrait avoir une durée plus longue, les scopes de PR et de dev plus courte. Sinon une vieille entrée de `main` (ex. `core@1` du commit 5) peut avoir disparu quand v1.0.x en a besoin, ce qui donne un simple miss, pas un résultat faux.
- **Tâches non hermétiques** : si une sortie dépend d'une entrée non déclarée à Nx (heure, variable d'env), une vieille entrée peut être réutilisée à tort. C'est un sujet de configuration Nx, pas du serveur.
- **Nom de scope** : à encoder pour éviter collisions et traversée de chemin (la validation actuelle ne couvre que le hash, regex `validHash`, `main.go:46`).

## Travail à faire

| Zone | Changement | Taille |
|---|---|---|
| Middleware auth, `main.go:184-267` | Vérifieur OIDC (JWKS en cache, `iss`/`aud`/`exp`/`repository`). Sortie : repo, scope d'écriture, chaîne de lecture, trusted. Garder les tokens statiques pour un usage de confiance séparé | la plus grosse |
| Config | Par repo : branche par défaut, patterns de version, audience, repos autorisés. Aujourd'hui env seulement, donc probablement un petit fichier de config | moyenne |
| `cacheFilePath`, `main.go:50-56` | Chemin `repo/scope/hash`, scope encodé | petite |
| GET/HEAD, `main.go:127-182` | Boucle sur la chaîne, premier hit gagne, mtime sur le fichier trouvé | petite |
| PUT, `main.go:58-114` | Écrire dans le scope d'écriture seulement, 409 par scope | petite |
| Nettoyage, `main.go:282-314` | Déjà un parcours de l'arbre, ajouter un TTL par type de scope | petite |
| Docs | `docs/configuration.md`, `README.md` (mentionne encore `go build ./main.go`, l'entrée est maintenant `cmd/nx-caching-server/`) | petite |

Suggestion de structure : sortir l'auth OIDC, le résolveur de scope et le stockage dans des paquets sous `internal/`, à côté de `internal/server/` qui existe déjà.

## Vérification proposée

- Tests unitaires du résolveur de chaîne : push main, push `v1.1.x`, PR sur main, PR sur `v1.1.x`, dev sans PR, `pull_request_target`, fork.
- Tests OIDC avec un JWKS local et des tokens signés de test : valide, mauvais `aud`, expiré, mauvais `repository`.
- Intégration : une PR écrit un hash X. `main` et une autre PR obtiennent 404 sur X. La PR lit un hash de `main` et obtient un hit.
- Tests de traversée de chemin sur les noms de scope.

## Ce qui n'a pas été fait

- Aucun code modifié dans le dépôt.
- Aucune vérification contre un vrai forge (GitHub ou GitLab).
- L'analyse détaillée de la CVE (nx.app/files/cve-2025-06) et la doc Nx Cloud sur les tokens à portée limitée n'ont pas été lues.
- Pas de commit ni de PR.
