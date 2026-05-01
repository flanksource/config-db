import type { ScraperProgress } from '../types';

interface Props {
  scrapers: ScraperProgress[];
}

function statusIcon(status: ScraperProgress['status']): string {
  switch (status) {
    case 'pending': return 'codicon:circle-outline';
    case 'running': return 'svg-spinners:ring-resize';
    case 'complete': return 'codicon:pass-filled';
    case 'error': return 'codicon:error';
  }
}

function statusColor(status: ScraperProgress['status']): string {
  switch (status) {
    case 'pending': return 'text-gray-400';
    case 'running': return 'text-blue-500';
    case 'complete': return 'text-green-500';
    case 'error': return 'text-red-500';
  }
}

export function ScraperList({ scrapers }: Props) {
  if (!scrapers || scrapers.length === 0) return null;

  return (
    <div class="flex gap-3 items-center flex-wrap">
      {scrapers.map(s => (
        <div key={s.name} class="flex items-center gap-1 text-sm" title={s.error || ''}>
          <iconify-icon icon={statusIcon(s.status)} class={statusColor(s.status)} />
          <span class={s.status === 'error' ? 'text-red-600' : 'text-gray-700'}>{s.name}</span>
          {s.result_count > 0 && (
            <span class="text-xs text-gray-400">({s.result_count})</span>
          )}
          {(s.duration_secs ?? 0) > 0 && (
            <span class="text-xs text-gray-400">{(s.duration_secs as number).toFixed(1)}s</span>
          )}
        </div>
      ))}
    </div>
  );
}
