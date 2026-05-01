import { useState, useEffect, useRef } from 'preact/hooks';
import type { ScrapeResult, TypeGroup } from '../types';
import { typeIcon } from '../utils';
import { ConfigNode, type ConfigItemCounts } from './ConfigNode';

interface Props {
  groups: TypeGroup[];
  selected: ScrapeResult | null;
  onSelect: (item: ScrapeResult) => void;
  expandAll: boolean | null;
  configCounts?: Map<string, ConfigItemCounts>;
}

function TypeGroupNode({ group, selected, onSelect, expandAll, configCounts }: {
  group: TypeGroup;
  selected: ScrapeResult | null;
  onSelect: (item: ScrapeResult) => void;
  expandAll: boolean | null;
  configCounts?: Map<string, ConfigItemCounts>;
}) {
  const [open, setOpen] = useState(true);
  const prevExpandAll = useRef(expandAll);

  useEffect(() => {
    if (expandAll !== null && expandAll !== prevExpandAll.current) {
      setOpen(expandAll);
    }
    prevExpandAll.current = expandAll;
  }, [expandAll]);

  return (
    <div>
      <div
        class="flex items-center gap-2 px-3 py-2 cursor-pointer hover:bg-gray-50 border-b border-gray-100 select-none"
        onClick={() => setOpen(!open)}
      >
        <span class="text-gray-400 text-xs">{open ? '▼' : '▶'}</span>
        <iconify-icon icon={typeIcon(group.type)} class="text-base" />
        <span class="font-medium text-sm text-gray-800 truncate flex-1">{group.type}</span>
        <span class="text-xs text-gray-400">{group.items.length}</span>
      </div>
      {open && (
        <div class="ml-2">
          {group.items.map(item => (
            <ConfigNode
              key={`${item.config_type}-${item.id}`}
              item={item}
              selected={selected}
              onSelect={onSelect}
              counts={configCounts?.get(item.id)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

export function ConfigTree({ groups, selected, onSelect, expandAll, configCounts }: Props) {
  return (
    <div>
      {groups.map(group => (
        <TypeGroupNode
          key={group.type}
          group={group}
          selected={selected}
          onSelect={onSelect}
          expandAll={expandAll}
          configCounts={configCounts}
        />
      ))}
    </div>
  );
}
