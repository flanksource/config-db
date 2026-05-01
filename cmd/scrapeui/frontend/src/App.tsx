import { useState, useEffect, useRef, useMemo } from 'preact/hooks';
import type { Snapshot, ScrapeResult, Tab } from './types';
import { groupByType, filterItems, collectTypes, formatDuration, buildLookups, globalSearch, normalizeEntityIDs } from './utils';
import { useRoute } from './hooks/useRoute';
import { SplitPane } from './components/SplitPane';
import { ScraperList } from './components/ScraperList';
import { Summary } from './components/Summary';
import { FilterBar, type Filters } from './components/FilterBar';
import { ConfigTree } from './components/ConfigTree';
import { DetailPanel } from './components/DetailPanel';
import { AnsiHtml } from './components/AnsiHtml';
import { HARPanel } from './components/HARPanel';
import { EntityTable } from './components/EntityTable';
import { AccessTable } from './components/AccessTable';
import { AccessLogTable } from './components/AccessLogTable';
import { ScrapeConfigPanel } from './components/ScrapeConfigPanel';
import { SnapshotPanel } from './components/SnapshotPanel';
import { JsonView } from './components/JsonView';

const TAB_DEFS: { key: Tab; label: string; icon: string; countKey?: string }[] = [
  { key: 'configs', label: 'Configs', icon: 'codicon:server-process', countKey: 'configs' },
  { key: 'logs', label: 'Logs', icon: 'codicon:terminal' },
  { key: 'har', label: 'HTTP', icon: 'codicon:globe' },
  { key: 'users', label: 'Users', icon: 'codicon:person', countKey: 'external_users' },
  { key: 'groups', label: 'Groups', icon: 'codicon:organization', countKey: 'external_groups' },
  { key: 'roles', label: 'Roles', icon: 'codicon:shield', countKey: 'external_roles' },
  { key: 'access', label: 'Access', icon: 'codicon:lock', countKey: 'config_access' },
  { key: 'access_logs', label: 'Access Logs', icon: 'codicon:history', countKey: 'access_logs' },
  { key: 'issues', label: 'Issues', icon: 'codicon:warning' },
  { key: 'snapshot', label: 'Snapshot', icon: 'codicon:database' },
  { key: 'last_summary', label: 'Last Summary', icon: 'codicon:pulse' },
  { key: 'spec', label: 'Spec', icon: 'codicon:file-code' },
];

export function App() {
  const [route, navigate] = useRoute();
  const { tab, id: routeId, q: routeQ } = route;
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [done, setDone] = useState(false);
  const [status, setStatus] = useState('Loading...');
  const [selected, setSelected] = useState<ScrapeResult | null>(null);
  const [expandAll, setExpandAll] = useState<boolean | null>(null);
  const [filters, setFilters] = useState<Filters>({ health: new Set(), type: new Set() });
  const [elapsed, setElapsed] = useState(0);
  const search = routeQ || '';
  const setSearch = (value: string) => navigate({ q: value || undefined });
  const doneRef = useRef(false);
  const startRef = useRef(0);
  const logsRef = useRef<HTMLDivElement>(null);
  const initialTabRef = useRef(tab);

  useEffect(() => {
    fetch('/api/scrape')
      .then(r => r.json())
      .then((snap: Snapshot) => applySnap(snap))
      .catch(() => {});

    const es = new EventSource('/api/scrape/stream');
    es.addEventListener('message', (e: MessageEvent) => {
      const snap: Snapshot = JSON.parse(e.data);
      applySnap(snap);
      if (snap.done) es.close();
    });
    es.addEventListener('done', () => {
      doneRef.current = true;
      setDone(true);
      setStatus('Scrape complete');
      es.close();
    });
    es.onerror = () => {
      if (!doneRef.current) setStatus('Connection lost — retrying...');
    };

    const timer = setInterval(() => {
      if (startRef.current && !doneRef.current) setElapsed(Date.now() - startRef.current);
    }, 1000);

    return () => { es.close(); clearInterval(timer); };
  }, []);

  const tabRef = useRef(tab);
  tabRef.current = tab;

  function applySnap(snap: Snapshot) {
    startRef.current = snap.started_at;
    setSnapshot(normalizeEntityIDs(snap));
    if (snap.done) {
      doneRef.current = true;
      setDone(true);
      setStatus('Scrape complete');
      setElapsed(Date.now() - snap.started_at);
    } else {
      setStatus('Scraping...');
    }
    if ((snap.results?.configs?.length ?? 0) > 0 && tabRef.current === 'spec' && initialTabRef.current === 'spec' && location.pathname === '/') {
      navigate({ tab: 'configs' });
    }
  }

  // Auto-scroll logs
  useEffect(() => {
    if (tab === 'logs' && logsRef.current) {
      logsRef.current.scrollTop = logsRef.current.scrollHeight;
    }
  }, [snapshot?.logs, tab]);

  const configs = snapshot?.results?.configs || [];

  // Sync selected config with URL route id (when on configs tab)
  useEffect(() => {
    if (tab !== 'configs') return;
    if (!routeId) {
      setSelected(null);
      return;
    }
    if (selected?.id === routeId) return;
    const match = configs.find(c => c.id === routeId);
    if (match) setSelected(match);
  }, [routeId, configs, tab]);
  const orphanedConfigs = useMemo(() => {
    return (snapshot?.issues || [])
      .filter(issue => issue.type === 'orphaned' && issue.change)
      .map((issue, i): ScrapeResult => ({
        id: `orphaned-${i}`,
        name: issue.change!.summary || issue.change!.change_type || `Orphaned #${i + 1}`,
        config_type: 'Orphaned Changes',
        health: 'warning',
        config: issue.change,
      }));
  }, [snapshot?.issues]);

  const allConfigs = useMemo(() => [...configs, ...orphanedConfigs], [configs, orphanedConfigs]);

  const filtered = useMemo(() => {
    let items = filterItems(allConfigs, filters.health, filters.type);
    if (search) {
      const lq = search.toLowerCase();
      items = items.filter(c =>
        c.name?.toLowerCase().includes(lq) ||
        c.config_type?.toLowerCase().includes(lq) ||
        c.aliases?.some(a => a.toLowerCase().includes(lq)) ||
        Object.entries(c.labels || {}).some(([k, v]) => k.toLowerCase().includes(lq) || v.toLowerCase().includes(lq)) ||
        Object.entries(c.tags || {}).some(([k, v]) => k.toLowerCase().includes(lq) || v.toLowerCase().includes(lq)) ||
        JSON.stringify(c.config)?.toLowerCase().includes(lq)
      );
    }
    return items;
  }, [allConfigs, filters, search]);
  const groups = useMemo(() => groupByType(filtered), [filtered]);
  const types = useMemo(() => collectTypes(allConfigs), [allConfigs]);
  const healthValues = useMemo(() => {
    const vals = new Set<string>();
    for (const item of allConfigs) vals.add(item.health || 'unknown');
    return Array.from(vals).sort();
  }, [allConfigs]);

  const counts: Record<string, number> = snapshot?.counts as any || {};

  const zero = () => ({ changes: 0, access: 0, accessLogs: 0, analysis: 0, relationships: 0 });

  const configCounts = useMemo(() => {
    const m = new Map<string, ReturnType<typeof zero>>();
    const changes = snapshot?.results?.changes || [];
    const access = snapshot?.results?.config_access || [];
    const logs = snapshot?.results?.config_access_logs || [];
    const relationships = snapshot?.relationships || [];

    for (const ch of changes) {
      if (!ch.source) continue;
      for (const cfg of configs) {
        if (ch.source.includes(cfg.id)) {
          const c = m.get(cfg.id) || zero();
          c.changes++;
          m.set(cfg.id, c);
        }
      }
    }
    for (const a of access) {
      const extId = (a.external_config_id as any)?.external_id || a.external_config_id;
      if (!extId) continue;
      for (const cfg of configs) {
        if (cfg.id === extId) {
          const c = m.get(cfg.id) || zero();
          c.access++;
          m.set(cfg.id, c);
        }
      }
    }
    for (const l of logs) {
      const extId = (l.external_config_id as any)?.external_id || l.external_config_id;
      if (!extId) continue;
      for (const cfg of configs) {
        if (cfg.id === extId) {
          const c = m.get(cfg.id) || zero();
          c.accessLogs++;
          m.set(cfg.id, c);
        }
      }
    }
    for (const rel of relationships) {
      if (rel.config_id) {
        const c = m.get(rel.config_id) || zero();
        c.relationships++;
        m.set(rel.config_id, c);
      }
      if (rel.related_id && rel.related_id !== rel.config_id) {
        const c = m.get(rel.related_id) || zero();
        c.relationships++;
        m.set(rel.related_id, c);
      }
    }
    return m;
  }, [snapshot?.results, configs]);

  const lookups = useMemo(() => buildLookups(snapshot?.results), [snapshot?.results]);

  const searchCounts = useMemo(
    () => globalSearch(search, snapshot?.results, snapshot?.har, snapshot?.logs),
    [search, snapshot?.results, snapshot?.har, snapshot?.logs],
  );

  const scraperErrors = useMemo(
    () => (snapshot?.scrapers || []).filter(s => s.status === 'error' && s.error),
    [snapshot?.scrapers],
  );

  return (
    <div class="bg-gray-100 h-screen flex flex-col">
      {/* Header */}
      <div class="border-b bg-white px-6 py-3">
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-3">
            <h1 class="text-xl font-bold text-gray-900">
              <iconify-icon icon="codicon:server-process" class="mr-1.5 text-blue-600" />
              Scrape Results
            </h1>
            <span class="text-sm text-gray-400">{status}</span>
            {snapshot?.build_info && (
              <span
                class="text-xs text-gray-400 font-mono"
                title={`commit ${snapshot.build_info.commit}\nbuilt ${snapshot.build_info.date}\nui ${__UI_BUILD_COMMIT__} (${__UI_BUILD_DATE__})`}
              >
                {snapshot.build_info.version}
                {snapshot.build_info.commit && snapshot.build_info.commit !== 'none' && (
                  <> · {snapshot.build_info.commit.substring(0, 8)}</>
                )}
                {snapshot.build_info.date && snapshot.build_info.date !== 'unknown' && (
                  <> · {snapshot.build_info.date}</>
                )}
              </span>
            )}
          </div>
          {snapshot && (
            <Summary
              counts={snapshot.counts}
              saveSummary={snapshot.save_summary}
              startedAt={snapshot.started_at}
              done={done}
              elapsed={elapsed}
            />
          )}
        </div>
        {snapshot && <div class="mt-2"><ScraperList scrapers={snapshot.scrapers} /></div>}
      </div>

      {/* Scrape error banner — surfaces errors from failed scrapers so they
          aren't just a small red chip in the scraper list. */}
      {scraperErrors.length > 0 && (
        <div class="border-b border-red-200 bg-red-50 px-6 py-3">
          {scraperErrors.map(s => (
            <div key={s.name} class="flex items-start gap-2 text-sm">
              <iconify-icon icon="codicon:error" class="text-red-500 mt-0.5 flex-shrink-0" />
              <div class="min-w-0 flex-1">
                <div class="font-semibold text-red-700">{s.name} failed</div>
                <div class="text-red-600 font-mono text-xs whitespace-pre-wrap break-all">{s.error}</div>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Tab bar */}
      <div class="border-b bg-white px-6 flex items-center gap-1 overflow-x-auto">
        {TAB_DEFS.map(t => {
          const count = t.countKey ? counts[t.countKey] || 0 : (
            t.key === 'har' ? (snapshot?.har?.length || 0) :
            t.key === 'logs' ? (snapshot?.logs ? 1 : 0) :
            t.key === 'issues' ? (snapshot?.issues?.length || 0) : 0
          );
          const isActive = tab === t.key;
          const searchHits = search ? (searchCounts[t.key] || 0) : 0;

          // Hide tabs with no data (except configs, logs, spec, snapshot, last_summary)
          if (!count && !isActive && !searchHits && !['configs', 'logs', 'spec', 'snapshot', 'last_summary'].includes(t.key)) return null;

          return (
            <button
              key={t.key}
              class={`flex items-center gap-1.5 px-3 py-2 text-sm border-b-2 transition-colors ${
                isActive
                  ? 'border-blue-500 text-blue-600 font-medium'
                  : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300'
              }`}
              onClick={() => navigate({ tab: t.key, id: undefined })}
            >
              <iconify-icon icon={t.icon} />
              {t.label}
              {count > 0 && !search && (
                <span class="text-xs px-1.5 py-0.5 rounded-full bg-gray-100 text-gray-600">{count}</span>
              )}
              {search && searchHits > 0 && (
                <span class="text-xs px-1.5 py-0.5 rounded-full bg-yellow-100 text-yellow-700">{searchHits}</span>
              )}
            </button>
          );
        })}
        <div class="ml-auto flex items-center gap-2">
          <div class="relative">
            <iconify-icon icon="codicon:search" class="absolute left-2 top-1/2 -translate-y-1/2 text-gray-400 text-sm" />
            <input
              type="text"
              placeholder="Search across all tabs..."
              value={search}
              onInput={(e) => setSearch((e.target as HTMLInputElement).value)}
              class="pl-7 pr-7 py-1 text-sm border border-gray-300 rounded-md focus:outline-none focus:ring-1 focus:ring-blue-500 focus:border-blue-500 w-64"
            />
            {search && (
              <button
                class="absolute right-1.5 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600"
                onClick={() => setSearch('')}
              >
                <iconify-icon icon="codicon:close" class="text-sm" />
              </button>
            )}
          </div>
          <button
            class="text-gray-400 hover:text-blue-600 p-1 rounded transition-colors"
            title="Copy link to current view"
            onClick={() => {
              navigator.clipboard.writeText(location.href);
              const btn = document.activeElement as HTMLElement;
              btn?.blur();
            }}
          >
            <iconify-icon icon="codicon:link" class="text-base" />
          </button>
        </div>
      </div>

      {/* Content */}
      <div class="flex-1 overflow-hidden">
        {tab === 'configs' && (
          <div class="flex flex-col h-full">
            <div class="bg-white border-b px-6 py-2 shrink-0">
              {configs.length > 0 && (
                <div class="flex items-center gap-2">
                  <button
                    class="text-xs px-2 py-1 rounded border border-gray-300 text-gray-600 hover:bg-gray-200"
                    onClick={() => setExpandAll(true)}
                  >Expand</button>
                  <button
                    class="text-xs px-2 py-1 rounded border border-gray-300 text-gray-600 hover:bg-gray-200"
                    onClick={() => setExpandAll(false)}
                  >Collapse</button>
                  <FilterBar filters={filters} onChange={setFilters} healthValues={healthValues} typeValues={types} />
                </div>
              )}
            </div>
            <SplitPane
              defaultSplit={40}
              left={
                <>
                  {groups.map(g => (
                    <ConfigTree key={g.type} groups={[g]} selected={selected} onSelect={(item) => navigate({ tab: 'configs', id: item.id })} expandAll={expandAll} configCounts={configCounts} />
                  ))}
                  {configs.length === 0 && !done && (
                    <div class="p-8 text-center text-gray-400">
                      <iconify-icon icon="svg-spinners:ring-resize" class="text-3xl text-blue-500" />
                      <p class="mt-2">Waiting for scrape results...</p>
                    </div>
                  )}
                  {filtered.length === 0 && configs.length > 0 && (
                    <div class="p-8 text-center text-gray-400 text-sm">No items match the current filters</div>
                  )}
                </>
              }
              right={<DetailPanel
                item={selected}
                changes={snapshot?.results?.changes}
                relationships={snapshot?.relationships}
                configMeta={snapshot?.config_meta}
                access={snapshot?.results?.config_access}
                accessLogs={snapshot?.results?.config_access_logs}
                allUsers={snapshot?.results?.external_users}
                allGroups={snapshot?.results?.external_groups}
                allRoles={snapshot?.results?.external_roles}
                lookups={lookups}
                onNavigate={(kind, id) => navigate({ tab: kind, id })}
              />}
            />
          </div>
        )}

        {tab === 'logs' && (
          <div ref={logsRef} class="h-full overflow-auto bg-gray-900">
            {snapshot?.logs ? (
              <AnsiHtml text={snapshot.logs} class="p-4 text-xs text-gray-200 leading-relaxed" />
            ) : (
              <div class="p-8 text-center text-gray-500">
                {done ? 'No logs captured' : 'Waiting for logs...'}
              </div>
            )}
          </div>
        )}

        {tab === 'har' && <HARPanel entries={snapshot?.har || []} search={search} />}

        {tab === 'users' && <EntityTable title="Users" kind="user" entities={snapshot?.results?.external_users || []} access={snapshot?.results?.config_access} accessLogs={snapshot?.results?.config_access_logs} userGroups={snapshot?.results?.external_user_groups} allUsers={snapshot?.results?.external_users} allGroups={snapshot?.results?.external_groups} lookups={lookups} search={search} selectedId={routeId} onSelect={(id) => navigate({ tab: 'users', id })} onNavigate={(kind, id) => navigate({ tab: kind === 'user' ? 'users' : kind === 'group' ? 'groups' : 'roles', id })} />}
        {tab === 'groups' && <EntityTable title="Groups" kind="group" entities={snapshot?.results?.external_groups || []} access={snapshot?.results?.config_access} accessLogs={snapshot?.results?.config_access_logs} userGroups={snapshot?.results?.external_user_groups} allUsers={snapshot?.results?.external_users} allGroups={snapshot?.results?.external_groups} lookups={lookups} search={search} selectedId={routeId} onSelect={(id) => navigate({ tab: 'groups', id })} onNavigate={(kind, id) => navigate({ tab: kind === 'user' ? 'users' : kind === 'group' ? 'groups' : 'roles', id })} />}
        {tab === 'roles' && <EntityTable title="Roles" kind="role" entities={snapshot?.results?.external_roles || []} access={snapshot?.results?.config_access} accessLogs={snapshot?.results?.config_access_logs} lookups={lookups} search={search} selectedId={routeId} onSelect={(id) => navigate({ tab: 'roles', id })} />}
        {tab === 'access' && <AccessTable entries={snapshot?.results?.config_access || []} lookups={lookups} search={search} />}
        {tab === 'access_logs' && <AccessLogTable entries={snapshot?.results?.config_access_logs || []} lookups={lookups} search={search} />}

        {tab === 'issues' && (
          <div class="overflow-auto h-full p-4">
            {(!snapshot?.issues || snapshot.issues.length === 0) ? (
              <div class="p-8 text-center text-gray-400 text-sm">No issues found</div>
            ) : (
              <div class="space-y-2">
                {snapshot.issues.map((issue, i) => (
                  <div key={i} class={`border rounded p-3 text-sm ${
                    issue.type === 'fk_error' ? 'bg-red-50 border-red-200' :
                    issue.type === 'warning' ? 'bg-yellow-50 border-yellow-200' :
                    'bg-amber-50 border-amber-200'
                  }`}>
                    <div class="flex items-center gap-2 mb-1">
                      <span class={`px-1.5 py-0.5 rounded text-xs font-medium ${
                        issue.type === 'fk_error' ? 'bg-red-100 text-red-700' :
                        issue.type === 'warning' ? 'bg-yellow-100 text-yellow-700' :
                        'bg-amber-100 text-amber-700'
                      }`}>{issue.type}</span>
                      {issue.message && <span class="text-gray-600">{issue.message}</span>}
                      {issue.warning?.count && issue.warning.count > 1 && (
                        <span class="text-xs text-gray-400 ml-1">&times;{issue.warning.count}</span>
                      )}
                    </div>
                    {issue.change && (
                      <div class="mt-1 text-xs space-y-0.5">
                        <div><span class="text-gray-500">change_type:</span> <span class="font-medium">{issue.change.change_type}</span></div>
                        {issue.change.config_type && <div><span class="text-gray-500">config_type:</span> {issue.change.config_type}</div>}
                        {issue.change.external_id && <div><span class="text-gray-500">external_id:</span> <span class="font-mono">{issue.change.external_id}</span></div>}
                        {issue.change.summary && <div><span class="text-gray-500">summary:</span> {issue.change.summary}</div>}
                        {issue.change.source && <div><span class="text-gray-500">source:</span> {issue.change.source}</div>}
                        {issue.change.severity && <div><span class="text-gray-500">severity:</span> {issue.change.severity}</div>}
                        {issue.change.created_at && <div><span class="text-gray-500">created_at:</span> {issue.change.created_at}</div>}
                      </div>
                    )}
                    {issue.warning && (
                      <div class="mt-1 text-xs space-y-1">
                        {issue.warning.expr && <div><span class="text-gray-500">expr:</span> <code class="bg-gray-100 px-1 rounded font-mono">{issue.warning.expr}</code></div>}
                        {issue.warning.input && (
                          <details class="mt-1">
                            <summary class="text-gray-500 cursor-pointer hover:text-gray-700">input</summary>
                            <div class="mt-1 p-2 bg-gray-100 rounded overflow-auto max-h-48">
                              {typeof issue.warning.input === 'object' ? <JsonView data={issue.warning.input} /> : <pre class="whitespace-pre-wrap break-all">{String(issue.warning.input)}</pre>}
                            </div>
                          </details>
                        )}
                        {issue.warning.output && (
                          <details class="mt-1">
                            <summary class="text-gray-500 cursor-pointer hover:text-gray-700">output</summary>
                            <div class="mt-1 p-2 bg-gray-100 rounded overflow-auto max-h-48">
                              {typeof issue.warning.output === 'object' ? <JsonView data={issue.warning.output} /> : <pre class="whitespace-pre-wrap break-all">{String(issue.warning.output)}</pre>}
                            </div>
                          </details>
                        )}
                        {issue.warning.result && (
                          <details class="mt-1">
                            <summary class="text-gray-500 cursor-pointer hover:text-gray-700">result</summary>
                            <div class="mt-1 p-2 bg-gray-100 rounded overflow-auto max-h-48">
                              {typeof issue.warning.result === 'object' ? <JsonView data={issue.warning.result} /> : <pre class="whitespace-pre-wrap break-all">{String(issue.warning.result)}</pre>}
                            </div>
                          </details>
                        )}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
        )}

        {tab === 'snapshot' && <SnapshotPanel pairs={snapshot?.snapshots} />}
        {tab === 'last_summary' && (
          <div class="overflow-auto h-full p-4">
            {snapshot?.last_scrape_summary ? (
              <JsonView data={snapshot.last_scrape_summary} />
            ) : (
              <div class="p-8 text-center text-gray-400 text-sm">No previous scrape summary available (first run or no database connection)</div>
            )}
          </div>
        )}
        {tab === 'spec' && <ScrapeConfigPanel spec={snapshot?.scrape_spec} properties={snapshot?.properties} logLevel={snapshot?.log_level} />}
      </div>
    </div>
  );
}
