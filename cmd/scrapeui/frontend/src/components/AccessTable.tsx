import { useState, useMemo } from 'preact/hooks';
import type { ExternalConfigAccess } from '../types';
import { useSort, SortIcon } from '../hooks/useSort';
import { type Lookups, resolveConfigId, resolve, matchesSearch } from '../utils';
import { JsonView } from './JsonView';

interface Props {
  entries: ExternalConfigAccess[];
  lookups: Lookups;
  search?: string;
}

const COLS: { key: string; label: string; cls: string }[] = [
  { key: 'id', label: 'ID', cls: 'px-3 py-2' },
  { key: 'external_config_id', label: 'Config', cls: 'px-3 py-2' },
  { key: 'external_user_aliases', label: 'User', cls: 'px-3 py-2' },
  { key: 'external_role_aliases', label: 'Role', cls: 'px-3 py-2' },
  { key: 'external_group_aliases', label: 'Group', cls: 'px-3 py-2' },
  { key: 'created_at', label: 'Created', cls: 'px-3 py-2' },
];

const HIDDEN_KEYS = new Set([
  'id', 'config_id', 'external_config_id',
  'external_user_id', 'external_user_aliases',
  'external_role_id', 'external_role_aliases',
  'external_group_id', 'external_group_aliases',
  'created_at',
]);

function AccessRow({ entry, lookups }: { entry: ExternalConfigAccess; lookups: Lookups }) {
  const [open, setOpen] = useState(false);

  const extraProps = useMemo(() => {
    const out: Record<string, any> = {};
    for (const [k, v] of Object.entries(entry)) {
      if (HIDDEN_KEYS.has(k)) continue;
      if (v === null || v === undefined || v === '' || v === '00000000-0000-0000-0000-000000000000') continue;
      if (Array.isArray(v) && v.length === 0) continue;
      out[k] = v;
    }
    return out;
  }, [entry]);

  return (
    <>
      <tr
        class="text-sm border-b border-gray-100 hover:bg-gray-50 cursor-pointer"
        onClick={() => setOpen(!open)}
      >
        <td class="px-3 py-2 text-xs font-mono text-gray-500 whitespace-nowrap">{entry.id}</td>
        <td class="px-3 py-2 text-xs whitespace-nowrap">{resolveConfigId(lookups, entry.external_config_id)}</td>
        <td class="px-3 py-2 whitespace-nowrap">
          {(entry.external_user_aliases?.length ? entry.external_user_aliases : entry.external_user_id ? [entry.external_user_id] : []).map((a, j) => (
            <span key={j} class="inline-block text-xs px-1.5 py-0.5 rounded bg-blue-50 text-blue-600 mr-1">{resolve(lookups.users, a)}</span>
          ))}
        </td>
        <td class="px-3 py-2 whitespace-nowrap">
          {(entry.external_role_aliases?.length ? entry.external_role_aliases : entry.external_role_id ? [entry.external_role_id] : []).map((a, j) => (
            <span key={j} class="inline-block text-xs px-1.5 py-0.5 rounded bg-purple-50 text-purple-600 mr-1">{resolve(lookups.roles, a)}</span>
          ))}
        </td>
        <td class="px-3 py-2 whitespace-nowrap">
          {(entry.external_group_aliases?.length ? entry.external_group_aliases : entry.external_group_id ? [entry.external_group_id] : []).map((a, j) => (
            <span key={j} class="inline-block text-xs px-1.5 py-0.5 rounded bg-green-50 text-green-600 mr-1">{resolve(lookups.groups, a)}</span>
          ))}
        </td>
        <td class="px-3 py-2 text-xs text-gray-500 whitespace-nowrap">{entry.created_at || ''}</td>
      </tr>
      {open && Object.keys(extraProps).length > 0 && (
        <tr>
          <td colSpan={COLS.length} class="bg-gray-50 px-4 py-3">
            <JsonView data={extraProps} />
          </td>
        </tr>
      )}
    </>
  );
}

export function AccessTable({ entries, lookups, search }: Props) {
  const filtered = useMemo(() => {
    if (!search) return entries;
    return entries.filter(e =>
      matchesSearch(search,
        e.id,
        ...(e.external_user_aliases || []),
        ...(e.external_role_aliases || []),
        ...(e.external_group_aliases || []),
      )
    );
  }, [entries, search]);
  const { sorted, sort, toggle } = useSort(filtered);

  if (!entries || entries.length === 0) {
    return <div class="p-8 text-center text-gray-400 text-sm">No config access records</div>;
  }

  return (
    <div class="overflow-auto h-full">
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
          {sorted.map((e, i) => <AccessRow key={i} entry={e} lookups={lookups} />)}
        </tbody>
      </table>
    </div>
  );
}
