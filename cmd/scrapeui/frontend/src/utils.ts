import type { ScrapeResult, TypeGroup, FullScrapeResults } from './types';

export function groupByType(items: ScrapeResult[]): TypeGroup[] {
  const groups = new Map<string, ScrapeResult[]>();
  for (const item of items) {
    const key = item.config_type || 'Unknown';
    const list = groups.get(key) || [];
    list.push(item);
    groups.set(key, list);
  }

  return Array.from(groups.entries())
    .map(([type, items]) => ({
      type,
      items,
      counts: countHealth(items),
    }))
    .sort((a, b) => a.type.localeCompare(b.type));
}

export function countHealth(items: ScrapeResult[]) {
  const c = { healthy: 0, unhealthy: 0, warning: 0, unknown: 0, errors: 0 };
  for (const item of items) {
    switch (item.health) {
      case 'healthy': c.healthy++; break;
      case 'unhealthy': c.unhealthy++; break;
      case 'warning': c.warning++; break;
      default: c.unknown++; break;
    }
  }
  return c;
}

export function healthIcon(health?: string): string {
  switch (health) {
    case 'healthy': return 'codicon:pass-filled';
    case 'unhealthy': return 'codicon:error';
    case 'warning': return 'codicon:warning';
    default: return 'codicon:circle-outline';
  }
}

export function healthColor(health?: string): string {
  switch (health) {
    case 'healthy': return 'text-green-500';
    case 'unhealthy': return 'text-red-500';
    case 'warning': return 'text-yellow-500';
    default: return 'text-gray-400';
  }
}

const TYPE_ICONS: Record<string, string> = {
  'Kubernetes': 'logos:kubernetes',
  'AWS': 'logos:aws',
  'Azure': 'logos:microsoft-azure',
  'GCP': 'logos:google-cloud',
  'File': 'codicon:file',
  'SQL': 'codicon:database',
  'HTTP': 'codicon:globe',
  'Terraform': 'logos:terraform-icon',
  'GitHub': 'logos:github-icon',
  'Trivy': 'simple-icons:trivy',
};

export function typeIcon(configType: string): string {
  const prefix = configType.split('::')[0];
  return TYPE_ICONS[prefix] || 'codicon:symbol-misc';
}

export function filterItems(
  items: ScrapeResult[],
  healthFilter: Set<string>,
  typeFilter: Set<string>,
): ScrapeResult[] {
  return items.filter(item => {
    if (healthFilter.size > 0 && !healthFilter.has(item.health || 'unknown')) return false;
    if (typeFilter.size > 0 && !typeFilter.has(item.config_type)) return false;
    return true;
  });
}

export function formatDuration(ms: number): string {
  const secs = Math.floor(ms / 1000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  const remSecs = secs % 60;
  return `${mins}m ${remSecs}s`;
}

export function collectTypes(items: ScrapeResult[]): string[] {
  const types = new Set<string>();
  for (const item of items) {
    if (item.config_type) types.add(item.config_type);
  }
  return Array.from(types).sort();
}

export interface Lookups {
  users: Map<string, string>;   // alias/id -> name
  groups: Map<string, string>;  // alias/id -> name
  roles: Map<string, string>;   // alias/id -> name
  configs: Map<string, string>; // id -> name (type)
}

export function buildLookups(results?: FullScrapeResults): Lookups {
  const users = new Map<string, string>();
  const groups = new Map<string, string>();
  const roles = new Map<string, string>();
  const configs = new Map<string, string>();

  for (const u of results?.external_users || []) {
    users.set(u.id, u.name);
    if (u.name) users.set(u.name, u.name);
    for (const a of u.aliases || []) users.set(a, u.name);
  }
  for (const g of results?.external_groups || []) {
    groups.set(g.id, g.name);
    if (g.name) groups.set(g.name, g.name);
    for (const a of g.aliases || []) groups.set(a, g.name);
  }
  for (const r of results?.external_roles || []) {
    roles.set(r.id, r.name);
    if (r.name) roles.set(r.name, r.name);
    for (const a of r.aliases || []) roles.set(a, r.name);
  }
  for (const c of results?.configs || []) {
    const label = c.name ? `${c.name} (${c.config_type})` : c.id;
    configs.set(c.id, label);
  }
  return { users, groups, roles, configs };
}

export function resolve(lookup: Map<string, string>, key: string): string {
  return lookup.get(key) || key;
}

export function resolveConfigId(lookups: Lookups, extId: any): string {
  if (!extId) return '';
  if (typeof extId === 'string') return lookups.configs.get(extId) || extId;
  const eid = extId.external_id || extId.config_id || '';
  return lookups.configs.get(eid) || eid;
}

export function statusColor(status: number): string {
  if (status >= 200 && status < 300) return 'text-green-600';
  if (status >= 300 && status < 400) return 'text-blue-600';
  if (status >= 400 && status < 500) return 'text-yellow-600';
  if (status >= 500) return 'text-red-600';
  return 'text-gray-600';
}
