import { useState, useMemo } from 'preact/hooks';
import type { ScrapeResult, ConfigChange, ExternalConfigAccess, ExternalConfigAccessLog } from '../types';
import { healthIcon, healthColor, type Lookups, resolve } from '../utils';
import { JsonView } from './JsonView';

interface Props {
  item: ScrapeResult | null;
  changes?: ConfigChange[];
  access?: ExternalConfigAccess[];
  accessLogs?: ExternalConfigAccessLog[];
  lookups: Lookups;
}

function LabelBadges({ labels, color }: { labels?: Record<string, string>; color: string }) {
  if (!labels) return null;
  const entries = Object.entries(labels);
  if (entries.length === 0) return null;
  return (
    <div class="flex flex-wrap gap-1">
      {entries.map(([k, v]) => (
        <span key={k} class={`text-xs px-1.5 py-0.5 rounded ${color}`}>{k}={v}</span>
      ))}
    </div>
  );
}

function matchesConfig(extId: any, itemId: string): boolean {
  if (!extId) return false;
  if (typeof extId === 'string') return extId === itemId;
  return extId.external_id === itemId || extId.config_id === itemId;
}

function Expandable({ summary, data, color }: { summary: any; data: any; color: string }) {
  const [open, setOpen] = useState(false);
  return (
    <div class={`border rounded ${color}`}>
      <div class="flex items-center gap-1.5 px-2 py-1.5 cursor-pointer text-xs" onClick={() => setOpen(!open)}>
        <span class="text-gray-400">{open ? '▼' : '▶'}</span>
        <div class="flex-1">{summary}</div>
      </div>
      {open && (
        <div class="px-2 pb-2 border-t">
          <JsonView data={data} />
        </div>
      )}
    </div>
  );
}

export function DetailPanel({ item, changes, access, accessLogs, lookups }: Props) {
  const itemChanges = useMemo(() => {
    if (!item || !changes) return [];
    return changes.filter(ch => ch.source?.includes(item.id));
  }, [item, changes]);

  const itemAccess = useMemo(() => {
    if (!item || !access) return [];
    return access.filter(a => matchesConfig(a.external_config_id, item.id));
  }, [item, access]);

  const itemAccessLogs = useMemo(() => {
    if (!item || !accessLogs) return [];
    return accessLogs.filter(a => matchesConfig(a.external_config_id, item.id));
  }, [item, accessLogs]);

  if (!item) {
    return (
      <div class="flex items-center justify-center h-full text-gray-400 text-sm">
        Select a config item to view details
      </div>
    );
  }

  return (
    <div class="p-4 space-y-4">
      <div class="flex items-center gap-2">
        <iconify-icon
          icon={healthIcon(item.health)}
          class={`text-xl ${healthColor(item.health)}`}
        />
        <div>
          <h2 class="text-lg font-semibold text-gray-900">{item.name || item.id}</h2>
          <div class="flex items-center gap-2 text-sm text-gray-500">
            <span>{item.config_type}</span>
            {item.config_class && <span>({item.config_class})</span>}
            {item.status && (
              <span class="px-1.5 py-0.5 rounded bg-gray-100 text-xs">{item.status}</span>
            )}
          </div>
        </div>
      </div>

      <div class="text-xs text-gray-400 font-mono break-all">ID: {item.id}</div>

      <LabelBadges labels={item.labels} color="bg-blue-50 text-blue-600" />
      <LabelBadges labels={item.tags} color="bg-gray-100 text-gray-600" />

      {item.analysis && (
        <div class="p-3 bg-indigo-50 border border-indigo-200 rounded text-sm">
          <div class="font-medium text-indigo-800">Analysis</div>
          <JsonView data={item.analysis} />
        </div>
      )}

      {/* Changes */}
      {itemChanges.length > 0 && (
        <div>
          <h3 class="text-sm font-semibold text-gray-700 mb-2">Changes ({itemChanges.length})</h3>
          <div class="space-y-1">
            {itemChanges.map((ch, i) => (
              <Expandable
                key={i}
                color="bg-purple-50 border-purple-200"
                data={ch}
                summary={
                  <div class="flex items-center gap-2">
                    <span class="font-medium text-purple-800">{ch.change_type}</span>
                    {ch.severity && <span class="text-purple-500">{ch.severity}</span>}
                    {ch.summary && <span class="text-gray-600 truncate">{ch.summary}</span>}
                    {ch.created_at && <span class="text-gray-400 ml-auto shrink-0">{ch.created_at}</span>}
                  </div>
                }
              />
            ))}
          </div>
        </div>
      )}

      {/* Config Access */}
      {itemAccess.length > 0 && (
        <div>
          <h3 class="text-sm font-semibold text-gray-700 mb-2">Access ({itemAccess.length})</h3>
          <div class="space-y-1">
            {itemAccess.map((a, i) => (
              <Expandable
                key={i}
                color="bg-amber-50 border-amber-200"
                data={a}
                summary={
                  <div class="flex flex-wrap items-center gap-1.5">
                    {a.external_user_aliases?.map((u, j) => (
                      <span key={j} class="px-1.5 py-0.5 rounded bg-blue-100 text-blue-700">user: {resolve(lookups.users, u)}</span>
                    ))}
                    {a.external_role_aliases?.map((r, j) => (
                      <span key={j} class="px-1.5 py-0.5 rounded bg-purple-100 text-purple-700">role: {resolve(lookups.roles, r)}</span>
                    ))}
                    {a.external_group_aliases?.map((g, j) => (
                      <span key={j} class="px-1.5 py-0.5 rounded bg-green-100 text-green-700">group: {resolve(lookups.groups, g)}</span>
                    ))}
                    {a.created_at && <span class="text-gray-400 ml-auto">{a.created_at}</span>}
                  </div>
                }
              />
            ))}
          </div>
        </div>
      )}

      {/* Access Logs */}
      {itemAccessLogs.length > 0 && (
        <div>
          <h3 class="text-sm font-semibold text-gray-700 mb-2">Access Logs ({itemAccessLogs.length})</h3>
          <div class="space-y-1">
            {itemAccessLogs.map((a, i) => (
              <Expandable
                key={i}
                color="bg-gray-50 border-gray-200"
                data={a}
                summary={
                  <div class="flex items-center gap-2">
                    {a.external_user_aliases?.map((u, j) => (
                      <span key={j} class="px-1.5 py-0.5 rounded bg-blue-100 text-blue-700">{resolve(lookups.users, u)}</span>
                    ))}
                    {a.mfa !== undefined && (
                      <span class={a.mfa ? 'text-green-600' : 'text-red-500'}>MFA: {a.mfa ? 'Yes' : 'No'}</span>
                    )}
                    {a.count != null && <span class="text-gray-500">x{a.count}</span>}
                    {a.created_at && <span class="text-gray-400 ml-auto">{a.created_at}</span>}
                  </div>
                }
              />
            ))}
          </div>
        </div>
      )}

      {/* Config JSON */}
      {item.config && (
        <div>
          <h3 class="text-sm font-semibold text-gray-700 mb-2">Configuration</h3>
          <div class="bg-gray-50 p-3 rounded border overflow-x-auto max-h-96 overflow-y-auto">
            {typeof item.config === 'string' ? (
              <pre class="text-xs font-mono whitespace-pre-wrap">{item.config}</pre>
            ) : (
              <JsonView data={item.config} />
            )}
          </div>
        </div>
      )}
    </div>
  );
}
