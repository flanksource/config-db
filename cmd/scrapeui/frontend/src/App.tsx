import { useState, useEffect, useRef, useMemo } from 'preact/hooks';
import type { Snapshot, ScrapeResult, Tab } from './types';
import { groupByType, filterItems, collectTypes, formatDuration, buildLookups, globalSearch } from './utils';
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

const TAB_DEFS: { key: Tab; label: string; icon: string; countKey?: string }[] = [
  { key: 'configs', label: 'Configs', icon: 'codicon:server-process', countKey: 'configs' },
  { key: 'logs', label: 'Logs', icon: 'codicon:terminal' },
  { key: 'har', label: 'HTTP', icon: 'codicon:globe' },
  { key: 'users', label: 'Users', icon: 'codicon:person', countKey: 'external_users' },
  { key: 'groups', label: 'Groups', icon: 'codicon:organization', countKey: 'external_groups' },
  { key: 'roles', label: 'Roles', icon: 'codicon:shield', countKey: 'external_roles' },
  { key: 'access', label: 'Access', icon: 'codicon:lock', countKey: 'config_access' },
  { key: 'access_logs', label: 'Access Logs', icon: 'codicon:history', countKey: 'access_logs' },
  { key: 'spec', label: 'Spec', icon: 'codicon:file-code' },
];

function parseHash(): { tab?: Tab; id?: string; q?: string } {
  const params = new URLSearchParams(location.hash.slice(1));
  return {
    tab: (params.get('tab') as Tab) || undefined,
    id: params.get('id') || undefined,
    q: params.get('q') || undefined,
  };
}

function writeHash(tab: Tab, id?: string, q?: string) {
  const params = new URLSearchParams();
  params.set('tab', tab);
  if (id) params.set('id', id);
  if (q) params.set('q', q);
  const next = '#' + params.toString();
  if (location.hash !== next) history.replaceState(null, '', next);
}

const INITIAL_HASH = parseHash();

export function App() {
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [done, setDone] = useState(false);
  const [status, setStatus] = useState('Loading...');
  const [selected, setSelected] = useState<ScrapeResult | null>(null);
  const [expandAll, setExpandAll] = useState<boolean | null>(null);
  const [filters, setFilters] = useState<Filters>({ health: new Set(), type: new Set() });
  const [tab, setTab] = useState<Tab>(INITIAL_HASH.tab || 'spec');
  const [elapsed, setElapsed] = useState(0);
  const [search, setSearch] = useState(INITIAL_HASH.q || '');
  const pendingId = useRef<string | undefined>(INITIAL_HASH.id);
  const doneRef = useRef(false);
  const startRef = useRef(0);
  const logsRef = useRef<HTMLDivElement>(null);

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
    setSnapshot(snap);
    if (snap.done) {
      doneRef.current = true;
      setDone(true);
      setStatus('Scrape complete');
      setElapsed(Date.now() - snap.started_at);
    } else {
      setStatus('Scraping...');
    }
    if ((snap.results?.configs?.length ?? 0) > 0 && tabRef.current === 'spec' && !INITIAL_HASH.tab) {
      setTab('configs');
    }
  }

  // Auto-scroll logs
  useEffect(() => {
    if (tab === 'logs' && logsRef.current) {
      logsRef.current.scrollTop = logsRef.current.scrollHeight;
    }
  }, [snapshot?.logs, tab]);

  const configs = snapshot?.results?.configs || [];

  // Sync URL hash
  useEffect(() => {
    writeHash(tab, selected?.id, search || undefined);
  }, [tab, selected?.id, search]);

  // Restore selection from URL when configs load
  useEffect(() => {
    if (!pendingId.current || !configs.length) return;
    const match = configs.find(c => c.id === pendingId.current);
    if (match) {
      setSelected(match);
      pendingId.current = undefined;
    }
  }, [configs]);

  // Handle browser back/forward
  useEffect(() => {
    const onHashChange = () => {
      const h = parseHash();
      if (h.tab && h.tab !== tabRef.current) setTab(h.tab);
      if (h.q !== undefined) setSearch(h.q);
      if (h.id) {
        const match = configs.find(c => c.id === h.id);
        if (match) setSelected(match);
      } else {
        setSelected(null);
      }
    };
    window.addEventListener('hashchange', onHashChange);
    return () => window.removeEventListener('hashchange', onHashChange);
  }, [configs]);
  const filtered = useMemo(() => {
    let items = filterItems(configs, filters.health, filters.type);
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
  }, [configs, filters, search]);
  const groups = useMemo(() => groupByType(filtered), [filtered]);
  const types = useMemo(() => collectTypes(configs), [configs]);
  const healthValues = useMemo(() => {
    const vals = new Set<string>();
    for (const item of configs) vals.add(item.health || 'unknown');
    return Array.from(vals).sort();
  }, [configs]);

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

      {/* Tab bar */}
      <div class="border-b bg-white px-6 flex items-center gap-1 overflow-x-auto">
        {TAB_DEFS.map(t => {
          const count = t.countKey ? counts[t.countKey] || 0 : (
            t.key === 'har' ? (snapshot?.har?.length || 0) :
            t.key === 'logs' ? (snapshot?.logs ? 1 : 0) : 0
          );
          const isActive = tab === t.key;
          const searchHits = search ? (searchCounts[t.key] || 0) : 0;

          // Hide tabs with no data (except configs, logs, spec)
          if (!count && !isActive && !searchHits && !['configs', 'logs', 'spec'].includes(t.key)) return null;

          return (
            <button
              key={t.key}
              class={`flex items-center gap-1.5 px-3 py-2 text-sm border-b-2 transition-colors ${
                isActive
                  ? 'border-blue-500 text-blue-600 font-medium'
                  : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300'
              }`}
              onClick={() => setTab(t.key)}
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
                    <ConfigTree key={g.type} groups={[g]} selected={selected} onSelect={setSelected} expandAll={expandAll} configCounts={configCounts} />
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
              right={<DetailPanel item={selected} changes={snapshot?.results?.changes} relationships={snapshot?.relationships} configMeta={snapshot?.config_meta} access={snapshot?.results?.config_access} accessLogs={snapshot?.results?.config_access_logs} lookups={lookups} />}
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

        {tab === 'users' && <EntityTable title="Users" kind="user" entities={snapshot?.results?.external_users || []} access={snapshot?.results?.config_access} accessLogs={snapshot?.results?.config_access_logs} lookups={lookups} search={search} />}
        {tab === 'groups' && <EntityTable title="Groups" kind="group" entities={snapshot?.results?.external_groups || []} access={snapshot?.results?.config_access} accessLogs={snapshot?.results?.config_access_logs} lookups={lookups} search={search} />}
        {tab === 'roles' && <EntityTable title="Roles" kind="role" entities={snapshot?.results?.external_roles || []} access={snapshot?.results?.config_access} accessLogs={snapshot?.results?.config_access_logs} lookups={lookups} search={search} />}
        {tab === 'access' && <AccessTable entries={snapshot?.results?.config_access || []} lookups={lookups} search={search} />}
        {tab === 'access_logs' && <AccessLogTable entries={snapshot?.results?.config_access_logs || []} lookups={lookups} search={search} />}
        {tab === 'spec' && <ScrapeConfigPanel spec={snapshot?.scrape_spec} />}
      </div>
    </div>
  );
}
