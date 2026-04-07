export interface ScraperProgress {
  name: string;
  status: 'pending' | 'running' | 'complete' | 'error';
  started_at?: string;
  duration_secs?: number;
  error?: string;
  result_count: number;
}

export interface ScrapeResult {
  id: string;
  name: string;
  config_type: string;
  config_class?: string;
  status?: string;
  health?: string;
  icon?: string;
  labels?: Record<string, string>;
  tags?: Record<string, string>;
  config?: any;
  analysis?: any;
  properties?: any[];
  description?: string;
  source?: string;
  aliases?: string[];
}

export interface ConfigChange {
  change_type: string;
  severity?: string;
  source?: string;
  summary?: string;
  external_id?: string;
  config_type?: string;
  diff?: string;
  patches?: string;
  created_at?: string;
  external_created_by?: string;
}

export interface ConfigRelationship {
  config_id: string;
  related_id: string;
  relation: string;
}

export interface ConfigAnalysis {
  analyzer: string;
  message: string;
  severity: string;
  analysis_type: string;
  summary?: string;
  status?: string;
}

export interface ExternalUser {
  id: string;
  name: string;
  aliases?: string[];
  account_id?: string;
  user_type?: string;
}

export interface ExternalGroup {
  id: string;
  name: string;
  aliases?: string[];
  account_id?: string;
}

export interface ExternalRole {
  id: string;
  name: string;
  aliases?: string[];
}

export interface ExternalUserGroup {
  external_user_id: string;
  external_group_id: string;
}

export interface ExternalConfigAccess {
  id: string;
  external_config_id?: any;
  external_user_aliases?: string[];
  external_role_aliases?: string[];
  external_group_aliases?: string[];
  created_at?: string;
  last_reviewed_at?: string;
}

export interface ExternalConfigAccessLog {
  config_id?: string;
  external_config_id?: any;
  external_user_aliases?: string[];
  mfa?: boolean;
  count?: number;
  created_at?: string;
}

export interface FullScrapeResults {
  configs?: ScrapeResult[];
  changes?: ConfigChange[];
  analysis?: ConfigAnalysis[];
  relationships?: ConfigRelationship[];
  external_users?: ExternalUser[];
  external_groups?: ExternalGroup[];
  external_roles?: ExternalRole[];
  external_user_groups?: ExternalUserGroup[];
  config_access?: ExternalConfigAccess[];
  config_access_logs?: ExternalConfigAccessLog[];
}

// HAR types matching github.com/flanksource/commons/har
export interface HAREntry {
  startedDateTime: string;
  time: number;
  request: HARRequest;
  response: HARResponse;
  cache: any;
  timings: { send: number; wait: number; receive: number };
}

export interface HARRequest {
  method: string;
  url: string;
  httpVersion: string;
  headers: { name: string; value: string }[];
  queryString: { name: string; value: string }[];
  postData?: { mimeType: string; text: string };
  headersSize: number;
  bodySize: number;
}

export interface HARResponse {
  status: number;
  statusText: string;
  httpVersion: string;
  headers: { name: string; value: string }[];
  content: { size: number; mimeType?: string; text?: string; truncated?: boolean };
  redirectURL: string;
  headersSize: number;
  bodySize: number;
}

export interface Counts {
  configs: number;
  changes: number;
  analysis: number;
  relationships: number;
  external_users: number;
  external_groups: number;
  external_roles: number;
  config_access: number;
  access_logs: number;
  errors: number;
}

export interface SaveSummary {
  config_types?: Record<string, { added: number; updated: number; unchanged: number; changes: number }>;
}

export interface Snapshot {
  scrapers: ScraperProgress[];
  results: FullScrapeResults;
  counts: Counts;
  save_summary?: SaveSummary;
  scrape_spec?: any;
  har?: HAREntry[];
  logs: string;
  done: boolean;
  started_at: number;
}

export interface TypeGroup {
  type: string;
  items: ScrapeResult[];
  counts: { healthy: number; unhealthy: number; warning: number; unknown: number; errors: number };
}

export type Tab = 'configs' | 'logs' | 'har' | 'users' | 'groups' | 'roles' | 'access' | 'access_logs' | 'spec';
