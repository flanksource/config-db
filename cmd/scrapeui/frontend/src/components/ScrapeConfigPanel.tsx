import { useState, useMemo } from 'preact/hooks';
import { JsonView } from './JsonView';
import type { PropertyInfo, LogLevelInfo } from '../types';

interface Props {
  spec: any;
  properties?: Record<string, PropertyInfo>;
  logLevel?: LogLevelInfo;
}

function formatValue(val: any, type?: string): string {
  if (val === null || val === undefined) return '';
  if (type === 'duration' && typeof val === 'number') {
    // Go's time.Duration serializes as nanoseconds
    const ms = val / 1e6;
    if (ms < 1000) return `${ms}ms`;
    const secs = ms / 1000;
    if (secs < 60) return `${secs}s`;
    const mins = secs / 60;
    if (mins < 60) return `${mins}m`;
    return `${mins / 60}h`;
  }
  if (type === 'bool') return val ? 'on' : 'off';
  return String(val);
}

function isOverridden(prop: PropertyInfo): boolean {
  if (prop.value === null || prop.value === undefined) return false;
  return String(prop.value) !== String(prop.default);
}

const typeBadgeColors: Record<string, string> = {
  bool: 'bg-purple-100 text-purple-700',
  int: 'bg-blue-100 text-blue-700',
  duration: 'bg-teal-100 text-teal-700',
  string: 'bg-gray-100 text-gray-600',
};

export function ScrapeConfigPanel({ spec, properties, logLevel }: Props) {
  const [propFilter, setPropFilter] = useState('');

  const sortedProps = useMemo(() => {
    if (!properties) return [];
    return Object.entries(properties)
      .map(([key, info]) => ({ key, ...info }))
      .sort((a, b) => a.key.localeCompare(b.key));
  }, [properties]);

  const filteredProps = useMemo(() => {
    if (!propFilter) return sortedProps;
    const q = propFilter.toLowerCase();
    return sortedProps.filter(p =>
      p.key.toLowerCase().includes(q) ||
      formatValue(p.value, p.type).toLowerCase().includes(q)
    );
  }, [sortedProps, propFilter]);

  const hasContent = spec || (sortedProps.length > 0) || logLevel;
  if (!hasContent) {
    return <div class="p-8 text-center text-gray-400 text-sm">No scrape configuration available</div>;
  }

  return (
    <div class="p-4 overflow-auto h-full space-y-4">
      {/* Log Levels */}
      {logLevel && (logLevel.scraper || logLevel.global) && (
        <div>
          <h3 class="text-sm font-semibold text-gray-700 mb-2">Log Level</h3>
          <div class="flex items-center gap-3">
            {logLevel.scraper && (
              <span class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-sm bg-amber-50 border border-amber-200 text-amber-800">
                <iconify-icon icon="codicon:file-code" class="text-xs" />
                Scraper: <span class="font-medium">{logLevel.scraper}</span>
              </span>
            )}
            {logLevel.global && (
              <span class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-sm bg-blue-50 border border-blue-200 text-blue-800">
                <iconify-icon icon="codicon:globe" class="text-xs" />
                Global: <span class="font-medium">{logLevel.global}</span>
              </span>
            )}
          </div>
        </div>
      )}

      {/* Properties Table */}
      {sortedProps.length > 0 && (
        <div>
          <div class="flex items-center justify-between mb-2">
            <h3 class="text-sm font-semibold text-gray-700">
              Properties
              <span class="ml-1.5 text-xs font-normal text-gray-400">({sortedProps.length})</span>
            </h3>
            <div class="relative">
              <iconify-icon icon="codicon:search" class="absolute left-2 top-1/2 -translate-y-1/2 text-gray-400 text-xs" />
              <input
                type="text"
                placeholder="Filter properties..."
                value={propFilter}
                onInput={(e) => setPropFilter((e.target as HTMLInputElement).value)}
                class="pl-6 pr-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-blue-500 w-48"
              />
            </div>
          </div>
          <div class="border rounded overflow-hidden">
            <table class="w-full text-xs">
              <thead>
                <tr class="bg-gray-50 border-b text-left text-gray-500">
                  <th class="px-3 py-1.5 font-medium">Key</th>
                  <th class="px-3 py-1.5 font-medium">Value</th>
                  <th class="px-3 py-1.5 font-medium">Default</th>
                  <th class="px-3 py-1.5 font-medium w-16">Type</th>
                </tr>
              </thead>
              <tbody>
                {filteredProps.map(prop => {
                  const overridden = isOverridden(prop);
                  return (
                    <tr
                      key={prop.key}
                      class={`border-b last:border-0 ${overridden ? 'bg-green-50' : ''}`}
                    >
                      <td class="px-3 py-1.5 font-mono text-gray-800">{prop.key}</td>
                      <td class={`px-3 py-1.5 font-mono ${overridden ? 'text-green-700 font-medium' : 'text-gray-500'}`}>
                        {formatValue(prop.value, prop.type) || <span class="text-gray-300 italic">—</span>}
                      </td>
                      <td class="px-3 py-1.5 font-mono text-gray-400">
                        {formatValue(prop.default, prop.type)}
                      </td>
                      <td class="px-3 py-1.5">
                        {prop.type && (
                          <span class={`px-1.5 py-0.5 rounded text-[10px] font-medium ${typeBadgeColors[prop.type] || 'bg-gray-100 text-gray-600'}`}>
                            {prop.type}
                          </span>
                        )}
                      </td>
                    </tr>
                  );
                })}
                {filteredProps.length === 0 && (
                  <tr><td colSpan={4} class="px-3 py-4 text-center text-gray-400">No matching properties</td></tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Scrape Configuration */}
      {spec && (
        <div>
          <h3 class="text-sm font-semibold text-gray-700 mb-2">Scrape Configuration</h3>
          <div class="bg-gray-50 p-3 rounded border overflow-x-auto">
            <JsonView data={spec} />
          </div>
        </div>
      )}
    </div>
  );
}
