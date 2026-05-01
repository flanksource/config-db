import { useState, useMemo } from 'preact/hooks';
import type { ExternalConfigAccess, ExternalConfigAccessLog, ExternalUserGroup, ExternalUser, ExternalGroup } from '../types';
import { useSort, SortIcon } from '../hooks/useSort';
import { type Lookups, resolveConfigId, resolve, matchesSearch } from '../utils';
import { AliasList } from './AliasList';

interface Entity {
  id: string;
  name: string;
  aliases?: string[];
  account_id?: string;
  user_type?: string;
}

interface Props {
  title: string;
  kind: 'user' | 'group' | 'role';
  entities: Entity[];
  access?: ExternalConfigAccess[];
  accessLogs?: ExternalConfigAccessLog[];
  userGroups?: ExternalUserGroup[];
  allUsers?: ExternalUser[];
  allGroups?: ExternalGroup[];
  lookups: Lookups;
  search?: string;
  selectedId?: string;
  onSelect?: (id: string | undefined) => void;
  onNavigate?: (kind: 'user' | 'group' | 'role', id: string) => void;
}

function entityAliases(e: Entity): string[] {
  return [e.name, ...(e.aliases || [])].filter(Boolean);
}

function Section({ title, count, children, defaultOpen = true }: { title: string; count?: number; children: any; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div>
      <h4
        class="text-sm font-semibold text-gray-700 mb-2 cursor-pointer select-none flex items-center gap-1 hover:text-gray-900"
        onClick={() => setOpen(!open)}
      >
        <span class="text-gray-400 text-xs">{open ? '▼' : '▶'}</span>
        {title}{count !== undefined && ` (${count})`}
      </h4>
      {open && children}
    </div>
  );
}

function matchesEntity(kind: string, aliases: string[], access: ExternalConfigAccess): boolean {
  const targets = kind === 'user' ? access.external_user_aliases
    : kind === 'group' ? access.external_group_aliases
    : access.external_role_aliases;
  if (targets?.some(t => aliases.includes(t))) return true;
  // Fall back to ID-based matching
  const id = kind === 'user' ? access.external_user_id
    : kind === 'group' ? access.external_group_id
    : access.external_role_id;
  return !!id && aliases.includes(id);
}

function matchesEntityLog(aliases: string[], log: ExternalConfigAccessLog): boolean {
  return log.external_user_aliases?.some(t => aliases.includes(t)) || false;
}

function columnsFor(kind: 'user' | 'group' | 'role'): { key: string; label: string; cls: string }[] {
  const base = [
    { key: 'name', label: 'Name', cls: 'px-3 py-2' },
    { key: 'account_id', label: 'Account', cls: 'px-3 py-2' },
  ];
  if (kind === 'role') base.splice(1, 0, { key: 'aliases', label: 'Aliases', cls: 'px-3 py-2' });
  if (kind === 'user') base.push({ key: 'groups', label: 'Groups', cls: 'px-3 py-2' });
  if (kind === 'group') base.push({ key: 'members', label: 'Members', cls: 'px-3 py-2' });
  return base;
}

export function EntityTable({ title, kind, entities, access, accessLogs, userGroups, allUsers, allGroups, lookups, search, selectedId, onSelect, onNavigate }: Props) {
  const filtered = useMemo(() => {
    if (!search) return entities;
    return entities.filter(e => matchesSearch(search, e.name, ...(e.aliases || [])));
  }, [entities, search]);
  const { sorted, sort, toggle } = useSort(filtered, 'name');
  const cols = columnsFor(kind);

  // Resolve a v1.ExternalUserGroup to a (userId, groupId) pair using direct
  // IDs when present, falling back to alias overlap against the entity lists
  // we already have. This handles Azure DevOps memberships, which describe
  // identities by descriptor alias rather than by Azure UUID.
  const resolveMembership = (ug: ExternalUserGroup): { userId?: string; groupId?: string } => {
    let userId = ug.external_user_id;
    if (!userId && ug.external_user_aliases?.length && allUsers) {
      const u = allUsers.find(x => ug.external_user_aliases!.some(a => a === x.id || x.aliases?.includes(a)));
      if (u) userId = u.id;
    }
    let groupId = ug.external_group_id;
    if (!groupId && ug.external_group_aliases?.length && allGroups) {
      const g = allGroups.find(x => ug.external_group_aliases!.some(a => a === x.id || x.aliases?.includes(a)));
      if (g) groupId = g.id;
    }
    return { userId, groupId };
  };

  const resolvedUserGroups = useMemo(() => {
    if (!userGroups) return [];
    return userGroups.map(resolveMembership).filter(r => r.userId && r.groupId) as { userId: string; groupId: string }[];
  }, [userGroups, allUsers, allGroups]);

  // Count memberships per entity for list display
  const membershipCounts = useMemo(() => {
    const m: Record<string, number> = {};
    for (const ug of resolvedUserGroups) {
      if (kind === 'user') m[ug.userId] = (m[ug.userId] || 0) + 1;
      if (kind === 'group') m[ug.groupId] = (m[ug.groupId] || 0) + 1;
    }
    return m;
  }, [resolvedUserGroups, kind]);

  const selected = useMemo(
    () => entities.find(e => e.id === selectedId) || null,
    [entities, selectedId],
  );

  const selectedAliases = useMemo(
    () => selected ? entityAliases(selected) : [],
    [selected],
  );

  const relatedAccess = useMemo(() => {
    if (!selected || !access) return [];
    return access.filter(a => matchesEntity(kind, selectedAliases, a));
  }, [selected, access, selectedAliases, kind]);

  const relatedLogs = useMemo(() => {
    if (!selected || !accessLogs || kind !== 'user') return [];
    return accessLogs.filter(a => matchesEntityLog(selectedAliases, a));
  }, [selected, accessLogs, selectedAliases, kind]);

  // For a user: find groups they belong to
  const userMemberships = useMemo<ExternalGroup[]>(() => {
    if (!selected || kind !== 'user' || !allGroups) return [];
    const groupIds = new Set(
      resolvedUserGroups
        .filter(ug => ug.userId === selected.id)
        .map(ug => ug.groupId),
    );
    return allGroups.filter(g => groupIds.has(g.id));
  }, [selected, kind, resolvedUserGroups, allGroups]);

  // For a group: find users that are members
  const groupMembers = useMemo<ExternalUser[]>(() => {
    if (!selected || kind !== 'group' || !allUsers) return [];
    const userIds = new Set(
      resolvedUserGroups
        .filter(ug => ug.groupId === selected.id)
        .map(ug => ug.userId),
    );
    return allUsers.filter(u => userIds.has(u.id));
  }, [selected, kind, resolvedUserGroups, allUsers]);

  if (!entities || entities.length === 0) {
    return <div class="p-8 text-center text-gray-400 text-sm">No {title.toLowerCase()} found</div>;
  }

  return (
    <div class="flex h-full">
      {/* Entity list */}
      <div class="w-1/2 overflow-auto border-r">
        <table class="w-full text-left">
          <thead class="bg-gray-50 sticky top-0">
            <tr class="text-xs text-gray-500 border-b">
              {cols.map(c => (
                <th key={c.key} class={`${c.cls} cursor-pointer hover:text-gray-700 select-none whitespace-nowrap`} onClick={() => toggle(c.key)}>
                  {c.label}<SortIcon active={sort?.key === c.key} dir={sort?.dir} />
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {sorted.map(e => (
              <tr
                key={e.id}
                class={`text-sm border-b border-gray-100 cursor-pointer transition-colors ${
                  selectedId === e.id ? 'bg-blue-50' : 'hover:bg-gray-50'
                }`}
                onClick={() => onSelect?.(selectedId === e.id ? undefined : e.id)}
              >
                <td class="px-3 py-2 font-medium">{e.name}</td>
                {kind === 'role' && (
                  <td class="px-3 py-2">
                    <AliasList aliases={e.aliases} />
                  </td>
                )}
                <td class="px-3 py-2 text-gray-500">{e.account_id || ''}</td>
                {(kind === 'user' || kind === 'group') && (
                  <td class="px-3 py-2">
                    {membershipCounts[e.id] ? (
                      <span class={`text-xs px-1.5 py-0.5 rounded ${kind === 'group' ? 'bg-blue-100 text-blue-700' : 'bg-green-100 text-green-700'}`}>
                        {membershipCounts[e.id]}
                      </span>
                    ) : (
                      <span class="text-gray-300 text-xs">0</span>
                    )}
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Detail pane */}
      <div class="w-1/2 overflow-auto p-4">
        {!selected ? (
          <div class="flex items-center justify-center h-full text-gray-400 text-sm">
            Select a {kind} to view access details
          </div>
        ) : (
          <div class="space-y-4">
            <div>
              <h3 class="text-sm font-semibold text-gray-900">{selected.name}</h3>
              <div class="text-xs text-gray-400 font-mono mt-1">{selected.id}</div>
            </div>

            {selected.aliases && selected.aliases.length > 0 && (
              <Section title="Aliases" count={selected.aliases.length}>
                <AliasList aliases={selected.aliases} />
              </Section>
            )}

            {kind === 'user' && userMemberships.length > 0 && (
              <Section title="Groups" count={userMemberships.length}>
                <div class="flex flex-wrap gap-1">
                  {userMemberships.map(g => (
                    <button
                      key={g.id}
                      type="button"
                      class="text-xs px-1.5 py-0.5 rounded bg-green-100 text-green-700 hover:bg-green-200 cursor-pointer"
                      onClick={() => onNavigate?.('group', g.id)}
                    >{g.name || g.id}</button>
                  ))}
                </div>
              </Section>
            )}

            {kind === 'group' && groupMembers.length > 0 && (
              <Section title="Members" count={groupMembers.length}>
                <div class="flex flex-wrap gap-1">
                  {groupMembers.map(u => (
                    <button
                      key={u.id}
                      type="button"
                      class="text-xs px-1.5 py-0.5 rounded bg-blue-100 text-blue-700 hover:bg-blue-200 cursor-pointer"
                      onClick={() => onNavigate?.('user', u.id)}
                    >{u.name || u.id}</button>
                  ))}
                </div>
              </Section>
            )}

            {relatedAccess.length > 0 && (
              <Section title="Config Access" count={relatedAccess.length}>
                <div class="space-y-1">
                  {relatedAccess.map((a, i) => (
                    <div key={i} class="px-2 py-1.5 bg-amber-50 border border-amber-200 rounded text-xs">
                      <div class="text-gray-800 font-medium">{resolveConfigId(lookups, a.external_config_id)}</div>
                      <div class="flex flex-wrap gap-1 mt-1">
                        {(a.external_role_aliases?.length ? a.external_role_aliases : a.external_role_id ? [a.external_role_id] : []).map((r, j) => (
                          <span key={j} class="px-1.5 py-0.5 rounded bg-purple-100 text-purple-700">{resolve(lookups.roles, r)}</span>
                        ))}
                      </div>
                      {a.created_at && <span class="text-gray-400">{a.created_at}</span>}
                    </div>
                  ))}
                </div>
              </Section>
            )}

            {relatedLogs.length > 0 && (
              <Section title="Access Logs" count={relatedLogs.length}>
                <div class="space-y-1">
                  {relatedLogs.map((a, i) => (
                    <div key={i} class="flex items-center gap-2 px-2 py-1.5 bg-gray-50 border border-gray-200 rounded text-xs">
                      <span class="text-gray-800 font-medium">{resolveConfigId(lookups, a.external_config_id)}</span>
                      {a.mfa !== undefined && (
                        <span class={a.mfa ? 'text-green-600' : 'text-red-500'}>MFA: {a.mfa ? 'Yes' : 'No'}</span>
                      )}
                      {a.count != null && <span class="text-gray-500">x{a.count}</span>}
                      {a.created_at && <span class="text-gray-400 ml-auto">{a.created_at}</span>}
                    </div>
                  ))}
                </div>
              </Section>
            )}

            {relatedAccess.length === 0 && relatedLogs.length === 0 && userMemberships.length === 0 && groupMembers.length === 0 && (
              <div class="text-sm text-gray-400">No access records for this {kind}</div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
