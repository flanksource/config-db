import { useState, useMemo } from 'preact/hooks';
import type { ScrapeResult, ConfigChange, UIRelationship, ConfigMeta, ExternalConfigAccess, ExternalConfigAccessLog, ExternalUser, ExternalGroup, ExternalRole } from '../types';
import { healthIcon, healthColor, type Lookups, resolve } from '../utils';
import { JsonView } from './JsonView';
import { AliasList } from './AliasList';

type EntityKind = 'users' | 'groups' | 'roles';

interface Props {
  item: ScrapeResult | null;
  changes?: ConfigChange[];
  relationships?: UIRelationship[];
  configMeta?: Record<string, ConfigMeta>;
  access?: ExternalConfigAccess[];
  accessLogs?: ExternalConfigAccessLog[];
  allUsers?: ExternalUser[];
  allGroups?: ExternalGroup[];
  allRoles?: ExternalRole[];
  lookups: Lookups;
  // Optional navigate callback. When provided, entity badges become clickable
  // links that navigate to /users/{id}, /groups/{id}, /roles/{id} via the
  // SPA router. When omitted, badges fall back to plain spans.
  onNavigate?: (kind: EntityKind, id: string) => void;
}

function LabelBadges({ labels, color }: { labels?: Record<string, string>; color: string }) {
  if (!labels) return null;
  const entries = Object.entries(labels);
  if (entries.length === 0) return null;
  return (
    <div class="flex flex-wrap gap-1">
      {entries.map(([k, v]) => (
        <span key={k} class={`text-xs px-1.5 py-0.5 rounded ${color}`}>{k}={v}</span>
      ))}
    </div>
  );
}

// matchesConfig decides whether a config_access (or access_log) row belongs
// to a given config item. Some scrapers populate the nested
// external_config_id struct (most ADO scrapers), others populate the
// sibling top-level config_id field directly (e.g. AAD enterprise apps).
// Some scrapers normalize IDs into a path form while others use a UUID
// form. We check every plausible identifier shape so the match is
// resilient to any of these patterns.
function matchesConfig(
  a: { external_config_id?: any; config_id?: string },
  item: { id: string; aliases?: string[] },
): boolean {
  const itemKeys = new Set<string>();
  itemKeys.add(item.id);
  for (const alias of item.aliases || []) itemKeys.add(alias);

  const ext = a.external_config_id;
  if (ext) {
    if (typeof ext === 'string') {
      if (itemKeys.has(ext)) return true;
    } else if (typeof ext === 'object') {
      if (ext.external_id && itemKeys.has(ext.external_id)) return true;
      if (ext.config_id && itemKeys.has(ext.config_id)) return true;
    }
  }
  if (a.config_id && itemKeys.has(a.config_id)) return true;
  return false;
}

function Expandable({ summary, data, color }: { summary: any; data: any; color: string }) {
  const [open, setOpen] = useState(false);
  return (
    <div class={`border rounded ${color}`}>
      <div class="flex items-center gap-1.5 px-2 py-1.5 cursor-pointer text-xs" onClick={() => setOpen(!open)}>
        <span class="text-gray-400">{open ? '▼' : '▶'}</span>
        <div class="flex-1">{summary}</div>
      </div>
      {open && (
        <div class="px-2 pb-2 border-t">
          <JsonView data={data} />
        </div>
      )}
    </div>
  );
}

// resolveEntityID maps an alias-or-id back to the canonical entity .id by
// scanning the entity list. The badges in the Access section receive an
// alias from the access row (which may differ from the entity's primary id),
// so we resolve it before building the navigation URL — otherwise the
// /users/{id} route wouldn't match anything in the entity tab.
function resolveEntityID<T extends { id: string; aliases?: string[] }>(
  entities: T[] | undefined,
  aliasOrId: string,
): string {
  if (!entities || !aliasOrId) return aliasOrId;
  for (const e of entities) {
    if (e.id === aliasOrId) return e.id;
    if (e.aliases?.includes(aliasOrId)) return e.id;
  }
  return aliasOrId;
}

interface EntityBadgeProps {
  kind: EntityKind;
  prefix: string;
  aliasOrId: string;
  display: string;
  colorClass: string;
  entities?: { id: string; aliases?: string[] }[];
  onNavigate?: (kind: EntityKind, id: string) => void;
}

function EntityBadge({ kind, prefix, aliasOrId, display, colorClass, entities, onNavigate }: EntityBadgeProps) {
  const canonicalId = resolveEntityID(entities, aliasOrId);
  const href = `/${kind}/${encodeURIComponent(canonicalId)}`;
  if (!onNavigate) {
    return <span class={`px-1.5 py-0.5 rounded ${colorClass}`}>{prefix}{display}</span>;
  }
  return (
    <a
      href={href}
      onClick={(e) => {
        e.preventDefault();
        e.stopPropagation();
        onNavigate(kind, canonicalId);
      }}
      class={`px-1.5 py-0.5 rounded ${colorClass} hover:brightness-95 no-underline cursor-pointer`}
    >{prefix}{display}</a>
  );
}

function Section({ title, count, children, defaultOpen = true }: { title: string; count?: number; children: any; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div>
      <h3
        class="text-sm font-semibold text-gray-700 mb-2 cursor-pointer select-none flex items-center gap-1 hover:text-gray-900"
        onClick={() => setOpen(!open)}
      >
        <span class="text-gray-400 text-xs">{open ? '▼' : '▶'}</span>
        {title}{count !== undefined && ` (${count})`}
      </h3>
      {open && children}
    </div>
  );
}

export function DetailPanel({ item, changes, relationships, configMeta, access, accessLogs, allUsers, allGroups, allRoles, lookups, onNavigate }: Props) {
  const itemChanges = useMemo(() => {
    if (!item || !changes) return [];
    return changes.filter(ch => ch.source?.includes(item.id));
  }, [item, changes]);

  const itemRelationships = useMemo(() => {
    if (!item || !relationships) return [];
    return relationships.filter(r => r.config_id === item.id || r.related_id === item.id);
  }, [item, relationships]);

  const itemAccess = useMemo(() => {
    if (!item || !access) return [];
    return access.filter(a => matchesConfig(a, item));
  }, [item, access]);

  const itemAccessLogs = useMemo(() => {
    if (!item || !accessLogs) return [];
    return accessLogs.filter(a => matchesConfig(a, item));
  }, [item, accessLogs]);

  if (!item) {
    return (
      <div class="flex items-center justify-center h-full text-gray-400 text-sm">
        Select a config item to view details
      </div>
    );
  }

  return (
    <div class="p-4 space-y-4">
      <div class="flex items-center gap-2">
        <iconify-icon
          icon={healthIcon(item.health)}
          class={`text-xl ${healthColor(item.health)}`}
        />
        <div class="flex-1">
          <div class="flex items-center gap-2">
            <h2 class="text-lg font-semibold text-gray-900">{item.name || item.id}</h2>
            <button
              class="text-gray-300 hover:text-blue-500 transition-colors"
              title="Copy link to this config"
              onClick={() => {
                const url = new URL(location.href);
                url.hash = `tab=configs&id=${encodeURIComponent(item.id)}`;
                navigator.clipboard.writeText(url.toString());
              }}
            >
              <iconify-icon icon="codicon:link" class="text-sm" />
            </button>
            <a
              class="text-gray-300 hover:text-blue-500 transition-colors"
              title="Download JSON for this config"
              href={`/api/config/${encodeURIComponent(item.id)}`}
              download
            >
              <iconify-icon icon="codicon:cloud-download" class="text-sm" />
            </a>
          </div>
          <div class="flex items-center gap-2 text-sm text-gray-500">
            <span>{item.config_type}</span>
            {item.config_class && <span>({item.config_class})</span>}
            {item.status && (
              <span class="px-1.5 py-0.5 rounded bg-gray-100 text-xs">{item.status}</span>
            )}
            {(item.Action === 'inserted' || (!item.Action && item.created_at)) && (
              <span class="px-1.5 py-0.5 rounded bg-green-100 text-green-700 text-xs">New</span>
            )}
            {item.Action === 'updated' && (
              <span class="px-1.5 py-0.5 rounded bg-yellow-100 text-yellow-700 text-xs">Updated</span>
            )}
            {item.deleted_at && (
              <span class="px-1.5 py-0.5 rounded bg-red-100 text-red-700 text-xs">
                Deleted{item.delete_reason ? `: ${item.delete_reason}` : ''}
              </span>
            )}
          </div>
        </div>
      </div>

      <div class="text-xs text-gray-400 font-mono break-all">ID: {item.id}</div>

      {/* Metadata: parents, location, timestamps */}
      <div class="text-xs text-gray-500 space-y-1">
        {configMeta?.[item.id]?.parents && configMeta[item.id].parents!.length > 0 && (
          <div class="flex items-center gap-1">
            <iconify-icon icon="codicon:type-hierarchy" class="text-gray-400" />
            <span>{configMeta[item.id].parents!.join(' → ')}</span>
          </div>
        )}
        {(configMeta?.[item.id]?.location || (item.locations && item.locations.length > 0)) && (
          <div class="flex items-center gap-1">
            <iconify-icon icon="codicon:location" class="text-gray-400" />
            <span>{configMeta?.[item.id]?.location || item.locations!.join(', ')}</span>
          </div>
        )}
        {(item.created_at || item.last_modified) && (
          <div class="flex items-center gap-2">
            {item.created_at && <span>Created: {item.created_at}</span>}
            {item.last_modified && item.last_modified !== '0001-01-01T00:00:00Z' && <span>Modified: {item.last_modified}</span>}
            {item.deleted_at && <span class="text-red-500">Deleted: {item.deleted_at}</span>}
          </div>
        )}
      </div>

      <LabelBadges labels={item.labels} color="bg-blue-50 text-blue-600" />
      <LabelBadges labels={item.tags} color="bg-gray-100 text-gray-600" />

      {item.aliases && item.aliases.length > 0 && (
        <Section title="Aliases" count={item.aliases.length}>
          <AliasList aliases={item.aliases} />
        </Section>
      )}

      {item.analysis && (
        <div class="p-3 bg-indigo-50 border border-indigo-200 rounded text-sm">
          <div class="font-medium text-indigo-800">Analysis</div>
          <JsonView data={item.analysis} />
        </div>
      )}

      {/* Relationships */}
      {itemRelationships.length > 0 && (
        <Section title="Relationships" count={itemRelationships.length}>
          <div class="space-y-1">
            {itemRelationships.map((rel, i) => {
              const isOutgoing = rel.config_id === item.id;
              const targetId = isOutgoing ? rel.related_id : rel.config_id;
              const targetName = isOutgoing
                ? (rel.related_name || lookups.configs.get(targetId) || targetId)
                : (rel.config_name || lookups.configs.get(targetId) || targetId);
              const resolvedLabel = lookups.configs.get(targetId);
              const targetType = resolvedLabel?.match(/\(([^)]+)\)$/)?.[1];
              return (
                <div key={i} class="flex items-center gap-2 px-2 py-1.5 text-xs bg-teal-50 border border-teal-200 rounded">
                  <iconify-icon
                    icon={isOutgoing ? 'codicon:arrow-right' : 'codicon:arrow-left'}
                    class="text-teal-500 shrink-0"
                  />
                  {(targetType || rel.relation) && (
                    <span class="text-teal-700 font-medium">{targetType || rel.relation}</span>
                  )}
                  <span class="text-gray-600 truncate">{targetName}</span>
                  <span class="text-gray-400 ml-auto shrink-0">{isOutgoing ? 'outgoing' : 'incoming'}</span>
                </div>
              );
            })}
          </div>
        </Section>
      )}

      {/* Changes */}
      {itemChanges.length > 0 && (
        <Section title="Changes" count={itemChanges.length}>
          <div class="space-y-1">
            {itemChanges.map((ch, i) => (
              <Expandable
                key={i}
                color="bg-purple-50 border-purple-200"
                data={ch}
                summary={
                  <div class="flex items-center gap-2">
                    <span class="font-medium text-purple-800">{ch.change_type}</span>
                    {(ch.resolved?.action || ch.action) && (
                      <span class="px-1 py-0.5 rounded bg-orange-100 text-orange-700">{ch.resolved?.action || ch.action}</span>
                    )}
                    {ch.severity && <span class="text-purple-500">{ch.severity}</span>}
                    {ch.summary && <span class="text-gray-600 truncate">{ch.summary}</span>}
                    {ch.created_at && <span class="text-gray-400 ml-auto shrink-0">{ch.created_at}</span>}
                  </div>
                }
              />
            ))}
          </div>
        </Section>
      )}

      {/* Config Access */}
      {itemAccess.length > 0 && (
        <Section title="Access" count={itemAccess.length}>
          <div class="space-y-1">
            {itemAccess.map((a, i) => (
              <Expandable
                key={i}
                color="bg-amber-50 border-amber-200"
                data={a}
                summary={
                  <div class="flex flex-wrap items-center gap-1.5">
                    {(a.external_user_aliases?.length ? a.external_user_aliases : a.external_user_id ? [a.external_user_id] : []).map((u, j) => (
                      <EntityBadge
                        key={`u-${j}`}
                        kind="users"
                        prefix="user: "
                        aliasOrId={u}
                        display={resolve(lookups.users, u)}
                        colorClass="bg-blue-100 text-blue-700"
                        entities={allUsers}
                        onNavigate={onNavigate}
                      />
                    ))}
                    {(a.external_role_aliases?.length ? a.external_role_aliases : a.external_role_id ? [a.external_role_id] : []).map((r, j) => (
                      <EntityBadge
                        key={`r-${j}`}
                        kind="roles"
                        prefix="role: "
                        aliasOrId={r}
                        display={resolve(lookups.roles, r)}
                        colorClass="bg-purple-100 text-purple-700"
                        entities={allRoles}
                        onNavigate={onNavigate}
                      />
                    ))}
                    {(a.external_group_aliases?.length ? a.external_group_aliases : a.external_group_id ? [a.external_group_id] : []).map((g, j) => (
                      <EntityBadge
                        key={`g-${j}`}
                        kind="groups"
                        prefix="group: "
                        aliasOrId={g}
                        display={resolve(lookups.groups, g)}
                        colorClass="bg-green-100 text-green-700"
                        entities={allGroups}
                        onNavigate={onNavigate}
                      />
                    ))}
                    {a.created_at && <span class="text-gray-400 ml-auto">{a.created_at}</span>}
                  </div>
                }
              />
            ))}
          </div>
        </Section>
      )}

      {/* Access Logs */}
      {itemAccessLogs.length > 0 && (
        <Section title="Access Logs" count={itemAccessLogs.length}>
          <div class="space-y-1">
            {itemAccessLogs.map((a, i) => (
              <Expandable
                key={i}
                color="bg-gray-50 border-gray-200"
                data={a}
                summary={
                  <div class="flex items-center gap-2">
                    {a.external_user_aliases?.map((u, j) => (
                      <EntityBadge
                        key={`u-${j}`}
                        kind="users"
                        prefix=""
                        aliasOrId={u}
                        display={resolve(lookups.users, u)}
                        colorClass="bg-blue-100 text-blue-700"
                        entities={allUsers}
                        onNavigate={onNavigate}
                      />
                    ))}
                    {a.mfa !== undefined && (
                      <span class={a.mfa ? 'text-green-600' : 'text-red-500'}>MFA: {a.mfa ? 'Yes' : 'No'}</span>
                    )}
                    {a.count != null && <span class="text-gray-500">x{a.count}</span>}
                    {a.created_at && <span class="text-gray-400 ml-auto">{a.created_at}</span>}
                  </div>
                }
              />
            ))}
          </div>
        </Section>
      )}

      {/* Config JSON */}
      {item.config && (
        <Section title="Configuration">
          <div class="bg-gray-50 p-3 rounded border overflow-x-auto max-h-96 overflow-y-auto">
            {typeof item.config === 'string' ? (
              <pre class="text-xs font-mono whitespace-pre-wrap">{item.config}</pre>
            ) : (
              <JsonView data={item.config} />
            )}
          </div>
        </Section>
      )}
    </div>
  );
}
