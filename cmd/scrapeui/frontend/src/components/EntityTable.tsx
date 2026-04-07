import { useState, useMemo } from 'preact/hooks';
import type { ExternalConfigAccess, ExternalConfigAccessLog } from '../types';
import { useSort, SortIcon } from '../hooks/useSort';
import { type Lookups, resolveConfigId, resolve, matchesSearch } from '../utils';

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
  lookups: Lookups;
  search?: string;
}

function entityAliases(e: Entity): string[] {
  return [e.name, ...(e.aliases || [])].filter(Boolean);
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

const COLS: { key: string; label: string; cls: string }[] = [
  { key: 'name', label: 'Name', cls: 'px-3 py-2' },
  { key: 'aliases', label: 'Aliases', cls: 'px-3 py-2' },
  { key: 'account_id', label: 'Account', cls: 'px-3 py-2' },
];

export function EntityTable({ title, kind, entities, access, accessLogs, lookups, search }: Props) {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const filtered = useMemo(() => {
    if (!search) return entities;
    return entities.filter(e => matchesSearch(search, e.name, ...(e.aliases || [])));
  }, [entities, search]);
  const { sorted, sort, toggle } = useSort(filtered, 'name');

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
              {COLS.map(c => (
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
                onClick={() => setSelectedId(selectedId === e.id ? null : e.id)}
              >
                <td class="px-3 py-2 font-medium">{e.name}</td>
                <td class="px-3 py-2">
                  {e.aliases?.map((a, i) => (
                    <span key={i} class="inline-block text-xs px-1.5 py-0.5 rounded bg-gray-100 mr-1 mb-0.5">{a}</span>
                  ))}
                </td>
                <td class="px-3 py-2 text-gray-500">{e.account_id || ''}</td>
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
              {selected.aliases && selected.aliases.length > 0 && (
                <div class="flex flex-wrap gap-1 mt-2">
                  {selected.aliases.map((a, i) => (
                    <span key={i} class="text-xs px-1.5 py-0.5 rounded bg-blue-50 text-blue-600">{a}</span>
                  ))}
                </div>
              )}
            </div>

            {relatedAccess.length > 0 && (
              <div>
                <h4 class="text-sm font-semibold text-gray-700 mb-2">Config Access ({relatedAccess.length})</h4>
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
              </div>
            )}

            {relatedLogs.length > 0 && (
              <div>
                <h4 class="text-sm font-semibold text-gray-700 mb-2">Access Logs ({relatedLogs.length})</h4>
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
              </div>
            )}

            {relatedAccess.length === 0 && relatedLogs.length === 0 && (
              <div class="text-sm text-gray-400">No access records for this {kind}</div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
