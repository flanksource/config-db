---
name: external-id-aliases
description: Read when dealing with config external ids and aliases
---

External IDs are id of a config at the external source or an alias to uniquely identify a config.
Example: For a kubernetes pod, external_id = ['<uid>', 'kubernetes/cluster/pod/default/nginx-deployment-5f4b4d4f5-8r8b2']

Aliases are handpicked or crafted based on the type.
Aliases aren't globally unique but they are unique within a type.
So, external_id -> config lookup must always include the type.

## Caching

External ID lookups are cached using TempCache (@api/cache.go)

## Adding new scrapers / aliases (checklist)

- Always set `result.ID` to a stable provider-unique value (UID/ARN/resource ID).
- Treat aliases as type-scoped keys: unique within `result.Type`, not globally.
- Avoid name-only aliases (`name`, `displayName`, `hostname`) unless strongly scoped (account/region/cluster/org) and preferably include a unique token.
- Avoid nullable template segments that can generate malformed aliases (e.g. trailing `/`).
- Assume normalization: aliases are lowercased on persist; avoid case-only or whitespace-only distinctions.
- Remember persisted `config_items.external_id[]` is effectively `[result.ID] + aliases` (deduped/lowercased), including aliases from `transform.aliases` and scrape plugins.
- Do NOT add `result.ID` to `result.Aliases`; it is automatically prepended (see `db/config.go`).
- Do NOT hand-craft format strings for external IDs when a dedicated helper function exists (e.g., `releaseExternalID()`, `pipelineExternalID()`). Use the helper.
- Do NOT add "shorter forms" of the ID as aliases unless something explicitly looks them up. Extra aliases increase match surface and create backward-compat debt.
- Before shipping, run a duplicate check on active rows by `(type, lower(trim(ext_id)))`.

## External users / groups / roles

- External users, groups, and roles merge by exact ID or alias overlap across scrapers.
- Do not rely on `scraper_id`, `account_id`, `user_type`, or `group_type` to prevent entity merges; these fields affect filtering and stored metadata, not identity boundaries.
- Membership rows in `external_user_groups` are scraper-owned. A full scrape may delete stale memberships for its own scraper, but must not delete another scraper's memberships for the same user/group pair.
- For user/group aliases, prefer provider-scoped identifiers (ARNs, object IDs, descriptors, tenant/account-qualified names) over display names.
