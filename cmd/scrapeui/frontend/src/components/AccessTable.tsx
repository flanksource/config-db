import { useMemo } from 'preact/hooks';
import type { ExternalConfigAccess } from '../types';
import { useSort, SortIcon } from '../hooks/useSort';
import { type Lookups, resolveConfigId, resolve, matchesSearch } from '../utils';

interface Props {
  entries: ExternalConfigAccess[];
  lookups: Lookups;
  search?: string;
}

const COLS: { key: string; label: string; cls: string }[] = [
  { key: 'external_config_id', label: 'Config', cls: 'px-3 py-2' },
  { key: 'external_user_aliases', label: 'User', cls: 'px-3 py-2' },
  { key: 'external_role_aliases', label: 'Role', cls: 'px-3 py-2' },
  { key: 'external_group_aliases', label: 'Group', cls: 'px-3 py-2' },
  { key: 'created_at', label: 'Created', cls: 'px-3 py-2' },
  { key: 'last_reviewed_at', label: 'Last Reviewed', cls: 'px-3 py-2' },
];

export function AccessTable({ entries, lookups, search }: Props) {
  const filtered = useMemo(() => {
    if (!search) return entries;
    return entries.filter(e =>
      matchesSearch(search,
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
          {sorted.map((e, i) => (
            <tr key={i} class="text-sm border-b border-gray-100 hover:bg-gray-50">
              <td class="px-3 py-2 text-xs whitespace-nowrap">{resolveConfigId(lookups, e.external_config_id)}</td>
              <td class="px-3 py-2 whitespace-nowrap">
                {(e.external_user_aliases?.length ? e.external_user_aliases : e.external_user_id ? [e.external_user_id] : []).map((a, j) => (
                  <span key={j} class="inline-block text-xs px-1.5 py-0.5 rounded bg-blue-50 text-blue-600 mr-1">{resolve(lookups.users, a)}</span>
                ))}
              </td>
              <td class="px-3 py-2 whitespace-nowrap">
                {(e.external_role_aliases?.length ? e.external_role_aliases : e.external_role_id ? [e.external_role_id] : []).map((a, j) => (
                  <span key={j} class="inline-block text-xs px-1.5 py-0.5 rounded bg-purple-50 text-purple-600 mr-1">{resolve(lookups.roles, a)}</span>
                ))}
              </td>
              <td class="px-3 py-2 whitespace-nowrap">
                {(e.external_group_aliases?.length ? e.external_group_aliases : e.external_group_id ? [e.external_group_id] : []).map((a, j) => (
                  <span key={j} class="inline-block text-xs px-1.5 py-0.5 rounded bg-green-50 text-green-600 mr-1">{resolve(lookups.groups, a)}</span>
                ))}
              </td>
              <td class="px-3 py-2 text-xs text-gray-500 whitespace-nowrap">{e.created_at || ''}</td>
              <td class="px-3 py-2 text-xs text-gray-500 whitespace-nowrap">{e.last_reviewed_at || ''}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
