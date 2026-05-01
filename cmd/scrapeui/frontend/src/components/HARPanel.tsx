import { useState, useMemo } from 'preact/hooks';
import type { HAREntry } from '../types';
import { statusColor, matchesSearch } from '../utils';
import { useSort, SortIcon } from '../hooks/useSort';
import { JsonView } from './JsonView';

interface Props {
  entries: HAREntry[];
  search?: string;
}

function tryParseJson(text: string): any | null {
  try { return JSON.parse(text); } catch { return null; }
}

function isJsonType(mime?: string): boolean {
  return !!mime && (mime.includes('json') || mime.includes('javascript'));
}

function BodyView({ text, mimeType }: { text: string; mimeType?: string }) {
  if (isJsonType(mimeType)) {
    const parsed = tryParseJson(text);
    if (parsed !== null) return <JsonView data={parsed} />;
  }
  return <pre class="whitespace-pre-wrap break-all">{text}</pre>;
}

function HARRow({ entry }: { entry: HAREntry }) {
  const [open, setOpen] = useState(false);

  return (
    <>
      <tr
        class="hover:bg-gray-50 cursor-pointer text-xs border-b border-gray-100"
        onClick={() => setOpen(!open)}
      >
        <td class="px-2 py-1.5 font-mono font-medium whitespace-nowrap">{entry.request.method}</td>
        <td class="px-2 py-1.5 font-mono truncate max-w-0" title={entry.request.url}>
          {entry.request.url}
        </td>
        <td class={`px-2 py-1.5 font-medium whitespace-nowrap ${statusColor(entry.response.status)}`}>
          {entry.response.status}
        </td>
        <td class="px-2 py-1.5 text-right text-gray-500 whitespace-nowrap">{entry.time.toFixed(0)}ms</td>
        <td class="px-2 py-1.5 text-right text-gray-500 whitespace-nowrap">{formatBytes(entry.response.bodySize)}</td>
        <td class="px-2 py-1.5 text-gray-400 whitespace-nowrap">{entry.response.content?.mimeType || ''}</td>
      </tr>
      {open && (
        <tr>
          <td colSpan={6} class="bg-gray-50 p-3 text-xs">
            <div class="grid grid-cols-2 gap-4">
              <div>
                <div class="font-semibold text-gray-700 mb-1">Request Headers</div>
                <div class="space-y-0.5">
                  {entry.request.headers?.map((h, i) => (
                    <div key={i} class="whitespace-nowrap"><span class="text-purple-600">{h.name}:</span> {h.value}</div>
                  ))}
                </div>
                {entry.request.postData?.text && (
                  <div class="mt-2">
                    <div class="font-semibold text-gray-700 mb-1">Request Body</div>
                    <div class="bg-white p-2 rounded border overflow-auto max-h-48">
                      <BodyView text={entry.request.postData.text} mimeType={entry.request.postData.mimeType} />
                    </div>
                  </div>
                )}
              </div>
              <div>
                <div class="font-semibold text-gray-700 mb-1">Response Headers</div>
                <div class="space-y-0.5">
                  {entry.response.headers?.map((h, i) => (
                    <div key={i} class="whitespace-nowrap"><span class="text-purple-600">{h.name}:</span> {h.value}</div>
                  ))}
                </div>
              </div>
            </div>
            {entry.response.content?.text && (
              <div class="mt-3">
                <div class="font-semibold text-gray-700 mb-1">Response Body</div>
                <div class="bg-white p-2 rounded border overflow-auto max-h-64">
                  <BodyView text={entry.response.content.text} mimeType={entry.response.content.mimeType} />
                </div>
              </div>
            )}
          </td>
        </tr>
      )}
    </>
  );
}

function formatBytes(bytes: number): string {
  if (bytes < 0) return '';
  if (bytes < 1024) return `${bytes}B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)}MB`;
}

const COLS: { key: string; label: string; cls: string }[] = [
  { key: 'request.method', label: 'Method', cls: 'px-2 py-2 w-16' },
  { key: 'request.url', label: 'URL', cls: 'px-2 py-2' },
  { key: 'response.status', label: 'Status', cls: 'px-2 py-2 w-20' },
  { key: 'time', label: 'Time', cls: 'px-2 py-2 w-16 text-right' },
  { key: 'response.bodySize', label: 'Size', cls: 'px-2 py-2 w-16 text-right' },
  { key: 'response.content.mimeType', label: 'Type', cls: 'px-2 py-2 w-40' },
];

export function HARPanel({ entries, search }: Props) {
  const filtered = useMemo(() => {
    if (!search) return entries;
    return entries.filter(e =>
      matchesSearch(search, e.request.url, e.request.method, e.request.postData?.text, e.response.content?.text)
    );
  }, [entries, search]);
  const { sorted, sort, toggle } = useSort(filtered, 'time');

  if (!entries || entries.length === 0) {
    return <div class="p-8 text-center text-gray-400 text-sm">No HTTP traffic captured</div>;
  }

  return (
    <div class="overflow-auto h-full">
      <table class="w-full text-left table-fixed">
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
          {sorted.map((e, i) => <HARRow key={i} entry={e} />)}
        </tbody>
      </table>
    </div>
  );
}
