import { useState, useMemo } from 'preact/hooks';
import type { ExternalConfigAccessLog } from '../types';
import { useSort, SortIcon } from '../hooks/useSort';
import { type Lookups, resolveConfigId, resolve, matchesSearch } from '../utils';
import { JsonView } from './JsonView';

interface Props {
  entries: ExternalConfigAccessLog[];
  lookups: Lookups;
  search?: string;
}

const COLS: { key: string; label: string; cls: string }[] = [
  { key: 'external_config_id', label: 'Config', cls: 'px-3 py-2' },
  { key: 'external_user_aliases', label: 'User', cls: 'px-3 py-2' },
  { key: 'mfa', label: 'MFA', cls: 'px-3 py-2 w-16' },
  { key: 'count', label: 'Count', cls: 'px-3 py-2 w-16 text-right' },
  { key: 'created_at', label: 'Timestamp', cls: 'px-3 py-2' },
];

const HIDDEN_KEYS = new Set([
  'config_id', 'external_config_id',
  'external_user_id', 'external_user_aliases',
  'mfa', 'count', 'created_at', 'scraper_id',
]);

function AccessLogRow({ entry, lookups }: { entry: ExternalConfigAccessLog; lookups: Lookups }) {
  const [open, setOpen] = useState(false);

  const extraProps = useMemo(() => {
    const out: Record<string, any> = {};
    for (const [k, v] of Object.entries(entry)) {
      if (HIDDEN_KEYS.has(k)) continue;
      if (v === null || v === undefined || v === '' || v === '00000000-0000-0000-0000-000000000000') continue;
      if (Array.isArray(v) && v.length === 0) continue;
      if (typeof v === 'object' && !Array.isArray(v) && Object.keys(v).length === 0) continue;
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
        <td class="px-3 py-2 text-xs whitespace-nowrap">{resolveConfigId(lookups, entry.external_config_id)}</td>
        <td class="px-3 py-2 whitespace-nowrap">
          {entry.external_user_aliases?.map((a, j) => (
            <span key={j} class="inline-block text-xs px-1.5 py-0.5 rounded bg-blue-50 text-blue-600 mr-1">{resolve(lookups.users, a)}</span>
          ))}
        </td>
        <td class="px-3 py-2 whitespace-nowrap">
          {entry.mfa !== undefined && (
            <span class={`text-xs font-medium ${entry.mfa ? 'text-green-600' : 'text-red-500'}`}>
              {entry.mfa ? 'Yes' : 'No'}
            </span>
          )}
        </td>
        <td class="px-3 py-2 text-right text-gray-600 whitespace-nowrap">{entry.count ?? ''}</td>
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

export function AccessLogTable({ entries, lookups, search }: Props) {
  const filtered = useMemo(() => {
    if (!search) return entries;
    return entries.filter(e => matchesSearch(search, ...(e.external_user_aliases || [])));
  }, [entries, search]);
  const { sorted, sort, toggle } = useSort(filtered);

  if (!entries || entries.length === 0) {
    return <div class="p-8 text-center text-gray-400 text-sm">No access log entries</div>;
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
          {sorted.map((e, i) => <AccessLogRow key={i} entry={e} lookups={lookups} />)}
        </tbody>
      </table>
    </div>
  );
}
