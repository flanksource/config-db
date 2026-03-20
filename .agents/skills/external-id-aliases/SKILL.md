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
