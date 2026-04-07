import type { Counts, SaveSummary } from '../types';
import { formatDuration } from '../utils';

interface Props {
  counts: Counts;
  saveSummary?: SaveSummary;
  startedAt: number;
  done: boolean;
  elapsed: number;
}

function Badge({ label, count, color }: { label: string; count: number; color: string }) {
  if (count === 0) return null;
  return (
    <span class={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium ${color}`}>
      {count} {label}
    </span>
  );
}

export function Summary({ counts, saveSummary, done, elapsed }: Props) {
  return (
    <div class="flex items-center gap-2 text-sm">
      <Badge label="configs" count={counts.configs} color="bg-blue-100 text-blue-700" />
      <Badge label="changes" count={counts.changes} color="bg-purple-100 text-purple-700" />
      <Badge label="relationships" count={counts.relationships} color="bg-cyan-100 text-cyan-700" />
      <Badge label="analysis" count={counts.analysis} color="bg-indigo-100 text-indigo-700" />
      <Badge label="errors" count={counts.errors} color="bg-red-100 text-red-700" />

      {saveSummary && saveSummary.config_types && (() => {
        let added = 0, updated = 0, unchanged = 0;
        for (const v of Object.values(saveSummary.config_types)) {
          added += v.added;
          updated += v.updated;
          unchanged += v.unchanged;
        }
        return (
          <>
            {added > 0 && <Badge label="added" count={added} color="bg-green-100 text-green-700" />}
            {updated > 0 && <Badge label="updated" count={updated} color="bg-yellow-100 text-yellow-700" />}
            {unchanged > 0 && <Badge label="unchanged" count={unchanged} color="bg-gray-100 text-gray-600" />}
          </>
        );
      })()}

      <span class="text-gray-400 ml-2">
        {done ? 'done' : 'running'} {formatDuration(elapsed)}
      </span>
    </div>
  );
}
