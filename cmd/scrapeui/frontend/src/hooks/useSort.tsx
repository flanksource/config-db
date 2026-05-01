import { useState, useMemo } from 'preact/hooks';

export type SortDir = 'asc' | 'desc';

export interface SortState {
  key: string;
  dir: SortDir;
}

export function useSort<T>(items: T[], defaultKey?: string) {
  const [sort, setSort] = useState<SortState | null>(
    defaultKey ? { key: defaultKey, dir: 'asc' } : null,
  );

  function toggle(key: string) {
    setSort(prev => {
      if (prev?.key === key) {
        return prev.dir === 'asc' ? { key, dir: 'desc' } : null;
      }
      return { key, dir: 'asc' };
    });
  }

  const sorted = useMemo(() => {
    if (!items) return [];
    if (!sort) return items;
    const { key, dir } = sort;
    return [...items].sort((a, b) => {
      const av = resolve(a, key);
      const bv = resolve(b, key);
      if (av == null && bv == null) return 0;
      if (av == null) return 1;
      if (bv == null) return -1;
      const cmp = typeof av === 'number' && typeof bv === 'number'
        ? av - bv
        : String(av).localeCompare(String(bv));
      return dir === 'asc' ? cmp : -cmp;
    });
  }, [items, sort]);

  return { sorted, sort, toggle };
}

function resolve(obj: any, path: string): any {
  return path.split('.').reduce((o, k) => o?.[k], obj);
}

export function SortIcon({ active, dir }: { active: boolean; dir?: SortDir }) {
  if (!active) return <span class="text-gray-300 ml-0.5">↕</span>;
  return <span class="text-blue-500 ml-0.5">{dir === 'asc' ? '↑' : '↓'}</span>;
}
