import { defineConfig, type Plugin } from 'vite';
import preact from '@preact/preset-vite';
import { readFileSync } from 'fs';
import { resolve } from 'path';

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
    },
  };
}

export default defineConfig({
  plugins: [preact(), fileApiPlugin()],
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
