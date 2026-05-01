import { defineConfig, type Plugin } from 'vite';
import preact from '@preact/preset-vite';
import { execSync } from 'child_process';
import { readFileSync } from 'fs';
import { resolve } from 'path';

// Capture git commit + build time once at vite startup so the UI header can
// display the frontend build identity alongside the Go binary's. Failing to
// resolve git metadata (e.g. running from a tarball) falls back to safe
// defaults rather than breaking the build.
function uiBuildCommit(): string {
  try {
    return execSync('git rev-parse HEAD', { encoding: 'utf-8' }).trim();
  } catch {
    return 'unknown';
  }
}
const UI_BUILD_COMMIT = uiBuildCommit();
const UI_BUILD_DATE = new Date().toISOString();

function fileApiPlugin(): Plugin {
  return {
    name: 'file-api',
    configureServer(server) {
      const file = process.env.FILE;
      if (!file) return;

      const abs = resolve(file);
      let raw: any;
      try {
        raw = JSON.parse(readFileSync(abs, 'utf-8'));
      } catch (e) {
        console.error(`Failed to read ${abs}:`, e);
        return;
      }

      // Wrap raw FullScrapeResults into a Snapshot shape
      const snap = {
        scrapers: [],
        results: {
          configs: raw.Configs || raw.configs || [],
          changes: raw.Changes || raw.changes || [],
          analysis: raw.Analysis || raw.analysis || [],
          external_users: raw.ExternalUsers || raw.external_users || [],
          external_groups: raw.ExternalGroups || raw.external_groups || [],
          external_roles: raw.ExternalRoles || raw.external_roles || [],
          external_user_groups: raw.ExternalUserGroups || raw.external_user_groups || [],
          config_access: raw.ConfigAccess || raw.config_access || [],
          config_access_logs: raw.ConfigAccessLogs || raw.config_access_logs || [],
        },
        relationships: raw.Relationships || raw.relationships || [],
        counts: {
          configs: (raw.Configs || raw.configs || []).length,
          changes: (raw.Changes || raw.changes || []).length,
          analysis: (raw.Analysis || raw.analysis || []).length,
          relationships: (raw.Relationships || raw.relationships || []).length,
          external_users: (raw.ExternalUsers || raw.external_users || []).length,
          external_groups: (raw.ExternalGroups || raw.external_groups || []).length,
          external_roles: (raw.ExternalRoles || raw.external_roles || []).length,
          config_access: (raw.ConfigAccess || raw.config_access || []).length,
          access_logs: (raw.ConfigAccessLogs || raw.config_access_logs || []).length,
          errors: 0,
        },
        har: raw.har || raw.HAR || [],
        logs: '',
        done: true,
        started_at: Date.now(),
      };

      console.log(`Serving ${abs} — ${snap.counts.configs} configs, ${snap.counts.relationships} relationships`);

      server.middlewares.use('/api/scrape/stream', (_req, res) => {
        res.writeHead(200, {
          'Content-Type': 'text/event-stream',
          'Cache-Control': 'no-cache',
          Connection: 'keep-alive',
        });
        res.write(`data: ${JSON.stringify(snap)}\n\n`);
        res.write('event: done\ndata: {}\n\n');
        res.end();
      });

      server.middlewares.use('/api/scrape', (_req, res) => {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify(snap));
      });

      server.middlewares.use('/api/config/', (req, res) => {
        const id = decodeURIComponent((req.url || '').replace(/^\//, ''));
        const configs: any[] = snap.results.configs || [];
        const item = configs.find(c => c.id === id);
        if (!item) {
          res.writeHead(404);
          res.end('not found');
          return;
        }
        const rels = (snap.relationships || []).filter(
          (r: any) => r.config_id === id || r.related_id === id,
        );
        const changes = ((snap.results.changes as any[]) || []).filter(
          (c: any) => c.source && c.source.includes(id),
        );
        const detail = {
          ...item,
          _meta: (snap.config_meta || {})[id],
          _relationships: rels,
          _changes: changes,
        };
        const safe = id.replace(/[^a-zA-Z0-9._-]/g, '_');
        res.writeHead(200, {
          'Content-Type': 'application/json',
          'Content-Disposition': `attachment; filename="${safe}.json"`,
        });
        res.end(JSON.stringify(detail, null, 2));
      });
    },
  };
}

// Dev-only plugin: rewrite deep SPA routes (/configs/{id}, /groups/{id}, ...)
// back to '/' so the index HTML is served. Mirrors isSPARoute in server.go.
function spaHistoryFallback(): Plugin {
  const prefixes = [
    '/configs', '/logs', '/har', '/users', '/groups',
    '/roles', '/access', '/access_logs', '/issues', '/snapshot', '/last_summary', '/spec',
  ];
  return {
    name: 'spa-history-fallback',
    configureServer(server) {
      server.middlewares.use((req, _res, next) => {
        const url = req.url || '/';
        if (req.method !== 'GET' || url.startsWith('/api/') || url.startsWith('/@') || url.startsWith('/src/') || url.startsWith('/node_modules/')) {
          return next();
        }
        const pathOnly = url.split('?')[0];
        if (prefixes.some(p => pathOnly === p || pathOnly.startsWith(p + '/'))) {
          req.url = '/';
        }
        return next();
      });
    },
  };
}

export default defineConfig({
  plugins: [preact(), spaHistoryFallback(), fileApiPlugin()],
  define: {
    __UI_BUILD_COMMIT__: JSON.stringify(UI_BUILD_COMMIT),
    __UI_BUILD_DATE__: JSON.stringify(UI_BUILD_DATE),
  },
  build: {
    lib: {
      entry: 'src/index.tsx',
      name: 'ScrapeUI',
      formats: ['iife'],
      fileName: () => 'scrapeui.js',
    },
    outDir: 'dist',
    minify: true,
    rollupOptions: {
      output: { inlineDynamicImports: true },
    },
  },
});
