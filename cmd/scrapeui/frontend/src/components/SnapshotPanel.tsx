import { useMemo, useState } from 'preact/hooks';
import type { EntityWindowCounts, ScrapeSnapshot, ScrapeSnapshotDiff, ScrapeSnapshotPair } from '../types';

interface Props {
  pairs?: Record<string, ScrapeSnapshotPair>;
}

type View = 'diff' | 'after' | 'before';

const ZERO: EntityWindowCounts = {
  total: 0,
  updated_last: 0, updated_hour: 0, updated_day: 0, updated_week: 0,
  deleted_last: 0, deleted_hour: 0, deleted_day: 0, deleted_week: 0,
};

function isZero(c?: EntityWindowCounts): boolean {
  if (!c) return true;
  return c.total === 0 &&
    c.updated_last === 0 && c.updated_hour === 0 && c.updated_day === 0 && c.updated_week === 0 &&
    c.deleted_last === 0 && c.deleted_hour === 0 && c.deleted_day === 0 && c.deleted_week === 0;
}

function signed(n: number): string {
  if (n > 0) return `+${n}`;
  return `${n}`;
}

function cls(n: number, isDiff: boolean): string {
  if (!isDiff) return 'text-gray-700';
  if (n > 0) return 'text-green-600 font-medium';
  if (n < 0) return 'text-red-600 font-medium';
  return 'text-gray-400';
}

function fmt(n: number, isDiff: boolean): string {
  return isDiff ? signed(n) : String(n);
}

const ENTITY_ROWS: { key: keyof ScrapeSnapshot & keyof ScrapeSnapshotDiff; label: string }[] = [
  { key: 'external_users', label: 'External Users' },
  { key: 'external_groups', label: 'External Groups' },
  { key: 'external_roles', label: 'External Roles' },
  { key: 'external_user_groups', label: 'External User Groups' },
  { key: 'config_access', label: 'Config Access' },
  { key: 'config_access_logs', label: 'Access Logs' },
];

function CountsRow({ label, counts, isDiff }: { label: string; counts: EntityWindowCounts; isDiff: boolean }) {
  return (
    <tr class="border-b border-gray-100 text-sm">
      <td class="px-3 py-1.5 whitespace-nowrap">{label}</td>
      <td class={`px-3 py-1.5 text-right tabular-nums ${cls(counts.total, isDiff)}`}>{fmt(counts.total, isDiff)}</td>
      <td class={`px-3 py-1.5 text-right tabular-nums text-xs ${cls(counts.updated_last, isDiff)}`}>{fmt(counts.updated_last, isDiff)}</td>
      <td class={`px-3 py-1.5 text-right tabular-nums text-xs ${cls(counts.updated_hour, isDiff)}`}>{fmt(counts.updated_hour, isDiff)}</td>
      <td class={`px-3 py-1.5 text-right tabular-nums text-xs ${cls(counts.updated_day, isDiff)}`}>{fmt(counts.updated_day, isDiff)}</td>
      <td class={`px-3 py-1.5 text-right tabular-nums text-xs ${cls(counts.updated_week, isDiff)}`}>{fmt(counts.updated_week, isDiff)}</td>
      <td class={`px-3 py-1.5 text-right tabular-nums text-xs ${cls(counts.deleted_last, isDiff)}`}>{fmt(counts.deleted_last, isDiff)}</td>
      <td class={`px-3 py-1.5 text-right tabular-nums text-xs ${cls(counts.deleted_hour, isDiff)}`}>{fmt(counts.deleted_hour, isDiff)}</td>
      <td class={`px-3 py-1.5 text-right tabular-nums text-xs ${cls(counts.deleted_day, isDiff)}`}>{fmt(counts.deleted_day, isDiff)}</td>
      <td class={`px-3 py-1.5 text-right tabular-nums text-xs ${cls(counts.deleted_week, isDiff)}`}>{fmt(counts.deleted_week, isDiff)}</td>
    </tr>
  );
}

function CountsTable({ title, rows, isDiff }: { title: string; rows: { label: string; counts: EntityWindowCounts }[]; isDiff: boolean }) {
  if (rows.length === 0) {
    return null;
  }
  return (
    <div class="mb-6">
      <h4 class="text-xs font-semibold uppercase tracking-wide text-gray-500 mb-2">{title}</h4>
      <table class="w-full text-left border rounded">
        <thead class="bg-gray-50">
          <tr class="text-xs text-gray-500 border-b">
            <th class="px-3 py-2"></th>
            <th class="px-3 py-2 text-right">Total</th>
            <th class="px-3 py-2 text-right" colSpan={4}>Updated (L / H / D / W)</th>
            <th class="px-3 py-2 text-right" colSpan={4}>Deleted (L / H / D / W)</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <CountsRow key={i} label={r.label} counts={r.counts} isDiff={isDiff} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function renderSection(data: ScrapeSnapshot | ScrapeSnapshotDiff | undefined, isDiff: boolean) {
  if (!data) {
    return <div class="text-gray-400 text-sm">No data</div>;
  }

  const perScraper = data.per_scraper || {};
  const perType = data.per_config_type || {};

  const scraperRows = Object.keys(perScraper)
    .sort()
    .filter(k => !isDiff || !isZero(perScraper[k]))
    .map(k => ({ label: k, counts: perScraper[k] }));

  const typeRows = Object.keys(perType)
    .sort()
    .filter(k => !isDiff || !isZero(perType[k]))
    .map(k => ({ label: k, counts: perType[k] }));

  const entityRows = ENTITY_ROWS
    .map(({ key, label }) => ({ label, counts: (data as any)[key] as EntityWindowCounts || ZERO }))
    .filter(r => !isDiff || !isZero(r.counts));

  const empty = scraperRows.length === 0 && typeRows.length === 0 && entityRows.length === 0;
  if (empty && isDiff) {
    return <div class="text-sm text-gray-500 italic p-2">No changes between before and after snapshots.</div>;
  }

  return (
    <div>
      <CountsTable title="Per Scraper" rows={scraperRows} isDiff={isDiff} />
      <CountsTable title="Per Config Type" rows={typeRows} isDiff={isDiff} />
      <CountsTable title="External Entities" rows={entityRows} isDiff={isDiff} />
    </div>
  );
}

export function SnapshotPanel({ pairs }: Props) {
  const scraperNames = useMemo(() => pairs ? Object.keys(pairs).sort() : [], [pairs]);
  const [selectedScraper, setSelectedScraper] = useState<string | null>(null);
  const [userView, setUserView] = useState<View | null>(null);

  const activeScraper = selectedScraper || scraperNames[0] || null;
  const pair = activeScraper && pairs ? pairs[activeScraper] : undefined;

  // Default view picks the first side that actually has data: After for
  // successful runs, Before when the scrape failed before reaching the post-
  // save capture, Diff as a last resort.
  const defaultView: View = pair?.after ? 'after' : pair?.before ? 'before' : 'diff';
  const view = userView ?? defaultView;

  if (!pairs || scraperNames.length === 0) {
    return (
      <div class="p-8 text-center text-gray-400 text-sm">
        No scrape snapshot captured for this run. Snapshots are only captured when running with a database connection.
      </div>
    );
  }

  const data = pair && (view === 'diff' ? pair.diff : view === 'after' ? pair.after : pair.before);

  return (
    <div class="p-4 overflow-auto h-full">
      <div class="flex items-center gap-4 mb-4">
        {scraperNames.length > 1 && (
          <select
            class="text-sm border rounded px-2 py-1"
            value={activeScraper || ''}
            onChange={(e) => setSelectedScraper((e.target as HTMLSelectElement).value)}
          >
            {scraperNames.map(n => (
              <option key={n} value={n}>{n}</option>
            ))}
          </select>
        )}
        <div class="flex rounded border overflow-hidden text-sm">
          {(['after', 'diff', 'before'] as View[]).map(v => {
            const disabled =
              (v === 'after' && !pair?.after) ||
              (v === 'before' && !pair?.before);
            return (
              <button
                key={v}
                disabled={disabled}
                class={`px-3 py-1 capitalize ${view === v ? 'bg-blue-500 text-white' : disabled ? 'bg-white text-gray-300 cursor-not-allowed' : 'bg-white text-gray-600 hover:bg-gray-50'}`}
                onClick={() => !disabled && setUserView(v)}
              >
                {v}
              </button>
            );
          })}
        </div>
        {(pair?.after || pair?.before) && (
          <div class="text-xs text-gray-500">
            run started at {new Date((pair.after || pair.before)!.run_started_at).toLocaleString()}
            {!pair.after && <span class="ml-2 text-amber-600">(scrape failed — showing pre-scrape state)</span>}
          </div>
        )}
      </div>
      {renderSection(data, view === 'diff')}
    </div>
  );
}
