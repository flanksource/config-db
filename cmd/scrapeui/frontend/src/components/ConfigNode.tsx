import type { ScrapeResult } from '../types';
import { healthIcon, healthColor } from '../utils';

export interface ConfigItemCounts {
  changes: number;
  access: number;
  accessLogs: number;
  analysis: number;
}

interface Props {
  item: ScrapeResult;
  selected: ScrapeResult | null;
  onSelect: (item: ScrapeResult) => void;
  counts?: ConfigItemCounts;
}

function Badge({ count, color, label }: { count: number; color: string; label: string }) {
  if (count === 0) return null;
  return <span class={`text-xs px-1 py-0.5 rounded ${color}`} title={label}>{count}</span>;
}

export function ConfigNode({ item, selected, onSelect, counts }: Props) {
  const isSelected = selected?.id === item.id && selected?.config_type === item.config_type;

  return (
    <div
      class={`flex items-center gap-1.5 px-3 py-1.5 cursor-pointer text-sm border-l-2 transition-colors ${
        isSelected
          ? 'bg-blue-50 border-l-blue-500'
          : 'border-l-transparent hover:bg-gray-50'
      }`}
      onClick={() => onSelect(item)}
    >
      <iconify-icon
        icon={healthIcon(item.health)}
        class={`text-base shrink-0 ${healthColor(item.health)}`}
      />
      <span class="truncate flex-1" title={item.name}>{item.name || item.id}</span>
      {counts && (
        <div class="flex gap-0.5 shrink-0">
          <Badge count={counts.changes} color="bg-purple-100 text-purple-600" label="changes" />
          <Badge count={counts.access} color="bg-amber-100 text-amber-600" label="access" />
          <Badge count={counts.accessLogs} color="bg-cyan-100 text-cyan-600" label="access logs" />
          <Badge count={counts.analysis} color="bg-indigo-100 text-indigo-600" label="analysis" />
        </div>
      )}
    </div>
  );
}
