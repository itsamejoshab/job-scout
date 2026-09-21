export interface OperatorConfig {
  project_name: string;
  temporal_ui_address: string;
  reporting_timezone: string;
}

interface DependencyStatus {
  ok: boolean;
  error?: string;
}

export interface SystemStatus {
  ok: boolean;
  database: DependencyStatus;
  temporal: DependencyStatus;
}

export interface NotificationSettings {
  enabled: boolean;
  configured: boolean;
  active: boolean;
  reason?: string;
}

export interface NotificationSettingsInput {
  enabled: boolean;
}

export type ApifyBudgetReason =
  | "ok"
  | "token_missing"
  | "budget_exhausted"
  | "usage_unknown"
  | "apify_unavailable";

export interface ApifyBudget {
  period_start: string;
  period_end: string;
  limit_usd: number;
  used_usd: number | null;
  remaining_usd: number | null;
  blocked: boolean;
  reason: ApifyBudgetReason;
}

export interface DashboardProviderStats {
  job_source: string;
  implemented: boolean;
  configured?: boolean;
  configuration_message?: string;
  enabled: boolean;
  scrape_interval_seconds: number;
  last_scraped_at: string | null;
  next_eligible_at: string | null;
  status: "disabled" | "setup_required" | "due" | "waiting";
  total_jobs: number;
  by_state: Record<string, number>;
  by_reject_reason: Record<string, number>;
  apify_budget?: ApifyBudget;
}

export interface DashboardDailyPoint {
  day: string;
  total: number;
  notified: number;
  applied: number;
  pending: number;
  skipped: number;
}

export interface DashboardStats {
  generated_at: string;
  timezone: string;
  notified: number;
  providers: DashboardProviderStats[];
  daily: DashboardDailyPoint[];
}

export interface WorkflowStart {
  status?: string;
  workflow_id?: string;
  reason?: string;
  enabled?: boolean;
  configured?: boolean;
  active?: boolean;
}

export interface WorkflowStatus {
  workflow_id: string;
  run_id: string;
  status: string;
}

export interface JobListItem {
  id: number;
  job_source: string;
  title: string;
  company: string;
  has_description: boolean;
  description_preview: string;
  location: string;
  job_url: string;
  created_at: string;
  updated_at: string;
  state: string;
  reject_reason: string | null;
  is_remote: boolean;
  notified_at: string | null;
}

export interface JobDetail extends JobListItem {
  description: string | null;
}

export interface JobsPage {
  items: JobListItem[];
  total: number;
  as_of: string;
}

export interface JobFilters {
  state: string;
  jobSource: string;
  query: string;
  dateFrom: string;
  dateTo: string;
  lastHours: number | null;
  limit: number;
  offset: number;
  asOf: string;
}

export interface SearchSettings {
  id: number;
  desc_include_words: string[];
  desc_exclude_words: string[];
  title_include: string[];
  title_exclude: string[];
  company_exclude: string[];
  created_at: string;
  updated_at: string;
}

export interface SearchSettingsInput {
  desc_include_words: string[];
  desc_exclude_words: string[];
  title_include: string[];
  title_exclude: string[];
  company_exclude: string[];
}

export interface ProviderSearchQuery {
  keywords: string;
  location: string;
  /** Work-type codes for LinkedIn settings UI (1 on-site, 2 remote, 3 hybrid). Not sent as a LinkedIn URL filter. */
  f_WT?: string;
  /** Dice/Indeed remote-inclusion flag. PUT sends a boolean; GET stores canonical "true"/"false" strings. */
  include_remote?: boolean | string;
  /** Indeed hybrid-inclusion flag. PUT sends a boolean; GET stores canonical "true"/"false" strings. */
  include_hybrid?: boolean | string;
  /** Indeed search radius in miles for this location. */
  radius?: string;
}

export interface IndeedProviderOptions {
  country: string;
  jobType: string;
  fromDays: string;
  maxRows: number;
  enableUniqueJobs: boolean;
  includeSimilarJobs: boolean;
}

export interface ProviderSettings {
  id: number;
  job_source: string;
  search_queries: ProviderSearchQuery[];
  global_searches: string[];
  timespan_code: string;
  pages_to_scrape: number;
  rounds: number;
  enabled: boolean;
  scrape_interval_seconds: number;
  provider_options?: IndeedProviderOptions | Record<string, unknown>;
  last_scraped_at: string | null;
  next_eligible_at: string | null;
  created_at: string;
  updated_at: string;
  apify_budget?: ApifyBudget;
}

export type ProviderSettingsInput = Pick<
  ProviderSettings,
  | "search_queries"
  | "global_searches"
  | "timespan_code"
  | "pages_to_scrape"
  | "rounds"
  | "enabled"
  | "scrape_interval_seconds"
  | "provider_options"
>;

async function requestJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const response = init ? await fetch(path, init) : await fetch(path);
  if (!response.ok) {
    let detail = "";
    try {
      const body = await response.json() as { detail?: string };
      detail = body.detail ?? "";
    } catch {
      detail = "";
    }
    throw new Error(detail || `Request failed with status ${response.status}`);
  }
  return response.json() as Promise<T>;
}

export function getConfig() {
  return requestJSON<OperatorConfig>("/api/v0/config");
}

export function getStatus() {
  return requestJSON<SystemStatus>("/api/v0/status");
}

export function getDashboardStats() {
  return requestJSON<DashboardStats>("/api/v0/dashboard/stats");
}

export function startScrape() {
  return requestJSON<WorkflowStart>("/api/v0/run?force=1", { method: "POST" });
}

export function startNotify() {
  return requestJSON<WorkflowStart>("/api/v0/notify", { method: "POST" });
}

export function getWorkflowStatus(workflowID: string) {
  return requestJSON<WorkflowStatus>(
    `/api/v0/workflow/${encodeURIComponent(workflowID)}`,
  );
}

export function getJobs(filters: JobFilters) {
  const params = new URLSearchParams({
    limit: String(filters.limit),
    offset: String(filters.offset),
  });
  if (filters.state) params.set("state", filters.state);
  if (filters.jobSource) params.set("job_source", filters.jobSource);
  if (filters.query) params.set("q", filters.query);
  if (filters.lastHours !== null) {
    params.set("last_hours", String(filters.lastHours));
  } else {
    if (filters.dateFrom) params.set("date_from", filters.dateFrom);
    if (filters.dateTo) params.set("date_to", filters.dateTo);
  }
  if (filters.asOf) params.set("as_of", filters.asOf);
  return requestJSON<JobsPage>(`/api/v0/jobs?${params.toString()}`);
}

export function getJob(id: number) {
  return requestJSON<JobDetail>(`/api/v0/jobs/${id}`);
}

export type ReviewAction = "applied" | "dismissed" | "ready";

export function reviewJob(id: number, action: ReviewAction) {
  return requestJSON<JobDetail>(`/api/v0/jobs/${id}/review`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ action }),
  });
}

export interface ReEvaluateResult {
  updated: number;
}

export function reEvaluateRejectedJobs() {
  return requestJSON<ReEvaluateResult>("/api/v0/jobs/re-evaluate", { method: "POST" });
}

export function getSearchSettings() {
  return requestJSON<SearchSettings>("/api/v0/search-settings");
}

export function getNotificationSettings() {
  return requestJSON<NotificationSettings>("/api/v0/notification-settings");
}

export function replaceNotificationSettings(settings: NotificationSettingsInput) {
  return requestJSON<NotificationSettings>("/api/v0/notification-settings", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(settings),
  });
}

export function replaceSearchSettings(settings: SearchSettingsInput) {
  return requestJSON<SearchSettings>("/api/v0/search-settings", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(settings),
  });
}

export function resetSearchSettings() {
  return requestJSON<SearchSettings>("/api/v0/search-settings/reset", {
    method: "POST",
  });
}

export function getProviderSettings() {
  return requestJSON<Record<string, ProviderSettings>>("/api/v0/scraper-settings/all");
}

export function replaceProviderSettings(source: string, settings: ProviderSettingsInput) {
  return requestJSON<ProviderSettings>(
    `/api/v0/scraper-settings/${encodeURIComponent(source)}`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(settings),
    },
  );
}

export function resetProviderSettings(source: string) {
  return requestJSON<ProviderSettings>(
    `/api/v0/scraper-settings/${encodeURIComponent(source)}/reset`,
    { method: "POST" },
  );
}
