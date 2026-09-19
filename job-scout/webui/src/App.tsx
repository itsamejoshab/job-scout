import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  ExternalLink,
  Plus,
  RefreshCw,
  RotateCcw,
  Save,
  Undo2,
  X,
} from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { BrowserRouter, NavLink, Navigate, Route, Routes } from "react-router-dom";
import {
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import {
  getConfig,
  getDashboardStats,
  getJobs,
  getSearchSettings,
  getStatus,
  getWorkflowStatus,
  reEvaluateRejectedJobs,
  replaceSearchSettings,
  resetSearchSettings,
  startNotify,
  startScrape,
  type SearchSettingsInput,
  type WorkflowStart,
} from "./api";
import { Badge } from "./components/ui/badge";
import { Button } from "./components/ui/button";
import { cn } from "./lib/utils";
import { ProviderSettingsEditor } from "./ProviderSettingsEditor";

const pages = [
  { path: "/dashboard", label: "Dashboard" },
  { path: "/jobs", label: "Jobs" },
  { path: "/settings", label: "Settings" },
];

function Header() {
  const config = useQuery({ queryKey: ["config"], queryFn: getConfig });
  const status = useQuery({
    queryKey: ["status"],
    queryFn: getStatus,
    refetchInterval: 2_000,
  });

  const down = status.data
    ? [
        !status.data.database.ok && "Database",
        !status.data.temporal.ok && "Temporal",
      ].filter(Boolean)
    : [];
  const statusText = status.isPending
    ? "Checking health"
    : down.length > 0
      ? `${down.join(" and ")} down`
      : "All systems operational";

  return (
    <header className="sticky top-0 z-20 border-b border-border/70 bg-background/80 backdrop-blur">
      <div className="flex w-full flex-wrap items-center gap-4 px-6 py-3 lg:px-10">
        <div className="flex items-center gap-2.5 text-base font-semibold tracking-tight">
          <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/10 text-primary">
            <Activity className="h-4 w-4" aria-hidden="true" />
          </span>
          <span>{config.data?.project_name ?? "JobScout"}</span>
        </div>
        <nav
          className="flex gap-1 rounded-xl border border-border/70 bg-card/70 p-1 shadow-sm"
          aria-label="Main navigation"
        >
          {pages.map((page) => (
            <NavLink
              key={page.path}
              to={page.path}
              className={({ isActive }) =>
                cn(
                  "rounded-lg px-3 py-1.5 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground",
                  isActive && "bg-primary text-primary-foreground shadow-sm hover:text-primary-foreground",
                )
              }
            >
              {page.label}
            </NavLink>
          ))}
        </nav>
        <div className="ml-auto flex flex-wrap items-center justify-end gap-4">
          <Badge variant={down.length > 0 ? "destructive" : "secondary"}>
            <span
              className={cn(
                "mr-1.5 h-1.5 w-1.5 rounded-full",
                down.length > 0 ? "bg-destructive-foreground" : "bg-emerald-500",
              )}
              aria-hidden="true"
            />
            {statusText}
          </Badge>
          {config.data?.temporal_ui_address && (
            <a
              className="inline-flex items-center gap-1 text-sm font-medium text-primary hover:underline"
              href={config.data.temporal_ui_address}
              target="_blank"
              rel="noreferrer"
            >
              Temporal UI
              <ExternalLink className="h-4 w-4" aria-hidden="true" />
            </a>
          )}
        </div>
      </div>
    </header>
  );
}

const stateOrder = ["pending", "rejected", "eligible", "notifying", "notified"];
const rejectReasonOrder = ["duplicate", "title_company", "description", "remote_lie", "detail_failed"];
const chartColors = ["#2563eb", "#16a34a", "#ea580c", "#7c3aed", "#dc2626"];

function usePageVisible() {
  const [visible, setVisible] = useState(() => !document.hidden);

  useEffect(() => {
    const onVisibility = () => setVisible(!document.hidden);
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, []);

  return visible;
}

function formatTimestamp(value: string | null) {
  if (!value) {
    return "never";
  }
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) {
    return value;
  }
  return parsed.toLocaleString();
}

function workflowFinished(status: string | undefined) {
  if (!status) {
    return false;
  }
  const normalized = status
    .toUpperCase()
    .replace("WORKFLOW_EXECUTION_STATUS_", "");
  return [
    "COMPLETED",
    "FAILED",
    "CANCELED",
    "TERMINATED",
    "CONTINUED_AS_NEW",
    "TIMED_OUT",
  ].includes(normalized);
}

function temporalWorkflowURL(address: string, workflowID: string) {
  return `${address.replace(/\/+$/, "")}/namespaces/default/workflows/${encodeURIComponent(workflowID)}`;
}

function DashboardPage() {
  const visible = usePageVisible();
  const queryClient = useQueryClient();
  const config = useQuery({ queryKey: ["config"], queryFn: getConfig });
  const status = useQuery({
    queryKey: ["status"],
    queryFn: getStatus,
    refetchInterval: 2_000,
  });
  const dashboard = useQuery({
    queryKey: ["dashboard-stats"],
    queryFn: getDashboardStats,
    refetchInterval: visible ? 2_000 : false,
  });
  const [activeWorkflow, setActiveWorkflow] = useState<WorkflowStart | null>(null);
  const [runMessage, setRunMessage] = useState("");
  const completedWorkflow = useRef("");
  const start = useMutation({
    mutationFn: (kind: "scrape" | "notify") =>
      kind === "scrape" ? startScrape() : startNotify(),
    onSuccess: (workflow) => {
      completedWorkflow.current = "";
      setActiveWorkflow(workflow);
      setRunMessage("Workflow running.");
    },
    onError: () => {
      setRunMessage("Workflow could not start.");
    },
  });
  const workflow = useQuery({
    queryKey: ["workflow", activeWorkflow?.workflow_id],
    queryFn: () => getWorkflowStatus(activeWorkflow?.workflow_id ?? ""),
    enabled: activeWorkflow !== null,
    refetchInterval: (query) =>
      workflowFinished(query.state.data?.status) ? false : 1_000,
  });

  useEffect(() => {
    if (
      !activeWorkflow ||
      !workflowFinished(workflow.data?.status) ||
      completedWorkflow.current === activeWorkflow.workflow_id
    ) {
      return;
    }
    completedWorkflow.current = activeWorkflow.workflow_id;
    setRunMessage("Workflow finished.");
    void Promise.all([
      queryClient.refetchQueries({ queryKey: ["dashboard-stats"], type: "all" }),
      queryClient.refetchQueries({ queryKey: ["jobs"], type: "all" }),
    ]);
  }, [activeWorkflow, queryClient, workflow.data?.status]);

  const chartData = useMemo(() => {
    if (!dashboard.data) {
      return [];
    }
    const dayMap = new Map<string, Record<string, number | string>>();
    for (const point of dashboard.data.daily) {
      const row = dayMap.get(point.day) ?? { day: point.day };
      row[point.job_source] = point.count;
      dayMap.set(point.day, row);
    }
    for (const row of dayMap.values()) {
      for (const provider of dashboard.data.providers) {
        if (typeof row[provider.job_source] !== "number") {
          row[provider.job_source] = 0;
        }
      }
    }
    return [...dayMap.values()].sort((a, b) =>
      String(a.day).localeCompare(String(b.day)),
    );
  }, [dashboard.data]);

  return (
    <main className="w-full px-6 py-8 lg:px-10">
      <h1 className="text-3xl font-semibold tracking-tight">Dashboard</h1>
      {dashboard.isPending && (
        <p className="mt-2 text-muted-foreground">Loading dashboard statistics.</p>
      )}
      {dashboard.isError && (
        <p className="mt-2 text-destructive">Dashboard statistics are unavailable.</p>
      )}
      {dashboard.data && (
        <>
          <p className="mt-1.5 text-muted-foreground">
            Generated at {formatTimestamp(dashboard.data.generated_at)}
          </p>

          <section className="mt-8 grid gap-4 md:grid-cols-2" aria-label="Provider cards">
            {dashboard.data.providers.map((provider) => (
              <article key={provider.job_source} className="surface p-5">
                <div className="flex items-center justify-between">
                  <h2 className="text-lg font-semibold tracking-tight">{provider.job_source}</h2>
                  <Badge variant={provider.status === "due" ? "destructive" : "secondary"}>
                    {provider.status}
                  </Badge>
                </div>
                <div className="mt-4 grid gap-1 text-sm text-muted-foreground sm:grid-cols-2">
                  <p>Status: {provider.status}</p>
                  <p>Implemented: {provider.implemented ? "yes" : "no"}</p>
                  <p>Enabled: {provider.enabled ? "yes" : "no"}</p>
                  <p>Interval seconds: {provider.scrape_interval_seconds}</p>
                  <p>Last scraped: {formatTimestamp(provider.last_scraped_at)}</p>
                  <p>Failure backoff: {formatTimestamp(provider.next_eligible_at)}</p>
                  <p>Total stored jobs: {provider.total_jobs}</p>
                </div>

                <div className="mt-4 grid grid-cols-2 gap-2 sm:grid-cols-5">
                  {stateOrder.map((state) => (
                    <p
                      key={state}
                      className="rounded-lg bg-muted px-2.5 py-2 text-xs font-medium text-muted-foreground"
                    >
                      {state}: {provider.by_state[state] ?? 0}
                    </p>
                  ))}
                </div>

                <details className="mt-4 text-sm" open>
                  <summary className="cursor-pointer font-medium">Rejection reasons</summary>
                  <div className="mt-2 grid gap-1 text-muted-foreground sm:grid-cols-2">
                    {rejectReasonOrder.map((reason) => (
                      <p key={reason}>
                        {reason}: {provider.by_reject_reason[reason] ?? 0}
                      </p>
                    ))}
                  </div>
                </details>
              </article>
            ))}
          </section>

          <section className="surface mt-6 p-5">
            <h2 className="text-lg font-semibold tracking-tight">
              Daily jobs ({dashboard.data.timezone})
            </h2>
            <div className="mt-4 h-80">
              <ResponsiveContainer width="100%" height="100%" minWidth={320} minHeight={240}>
                <LineChart data={chartData}>
                  <CartesianGrid strokeDasharray="3 3" />
                  <XAxis dataKey="day" />
                  <YAxis allowDecimals={false} />
                  <Tooltip />
                  <Legend />
                  {dashboard.data.providers.map((provider, index) => (
                    <Line
                      key={provider.job_source}
                      type="monotone"
                      dataKey={provider.job_source}
                      stroke={chartColors[index % chartColors.length]}
                      strokeWidth={2}
                      dot={false}
                    />
                  ))}
                </LineChart>
              </ResponsiveContainer>
            </div>
          </section>

          <section className="mt-6">
            <div className="flex gap-2">
              <Button
                variant="primary"
                disabled={status.data?.temporal.ok === false || start.isPending}
                onClick={() => start.mutate("scrape")}
              >
                Run scrape
              </Button>
              <Button
                disabled={status.data?.temporal.ok === false || start.isPending}
                onClick={() => start.mutate("notify")}
              >
                Run notify
              </Button>
            </div>
            {activeWorkflow && config.data?.temporal_ui_address && (
              <p className="mt-3 text-sm">
                Workflow:{" "}
                <a
                  className="font-medium text-primary hover:underline"
                  href={temporalWorkflowURL(
                    config.data.temporal_ui_address,
                    activeWorkflow.workflow_id,
                  )}
                  target="_blank"
                  rel="noreferrer"
                >
                  {activeWorkflow.workflow_id}
                </a>
              </p>
            )}
            {runMessage && <p role="status" className="mt-2 text-sm">{runMessage}</p>}
          </section>
        </>
      )}
    </main>
  );
}

const jobStates = ["", "pending", "rejected", "eligible", "notifying", "notified"];
const pageSize = 50;

type CreatedWindow =
  | "24h"
  | "3d"
  | "7d"
  | "14d"
  | "30d"
  | "all"
  | "custom";

const createdWindowOptions: Array<{ value: CreatedWindow; label: string }> = [
  { value: "24h", label: "Last 24 hours" },
  { value: "3d", label: "Last 3 days" },
  { value: "7d", label: "Last 7 days" },
  { value: "14d", label: "Last 14 days" },
  { value: "30d", label: "Last 30 days" },
  { value: "all", label: "All time" },
  { value: "custom", label: "Custom range" },
];

function lastHoursForWindow(window: CreatedWindow): number | null {
  switch (window) {
    case "24h":
      return 24;
    case "3d":
      return 72;
    case "7d":
      return 168;
    case "14d":
      return 336;
    case "30d":
      return 720;
    default:
      return null;
  }
}

function formatAge(value: string) {
  const milliseconds = Date.now() - new Date(value).getTime();
  if (milliseconds < 60 * 60 * 1_000) {
    return `${Math.max(0, Math.floor(milliseconds / 60_000))}m`;
  }
  if (milliseconds < 24 * 60 * 60 * 1_000) {
    return `${Math.floor(milliseconds / (60 * 60 * 1_000))}h`;
  }
  return `${Math.floor(milliseconds / (24 * 60 * 60 * 1_000))}d`;
}

function JobsPage() {
  const visible = usePageVisible();
  const [state, setState] = useState("notified");
  const [jobSource, setJobSource] = useState("");
  const [query, setQuery] = useState("");
  const [createdWindow, setCreatedWindow] = useState<CreatedWindow>("24h");
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");
  const [offset, setOffset] = useState(0);
  const anchor = useRef("");

  const resetPage = () => {
    setOffset(0);
    anchor.current = "";
  };
  const lastHours = lastHoursForWindow(createdWindow);
  const filters = {
    state,
    jobSource,
    query,
    dateFrom: createdWindow === "custom" ? dateFrom : "",
    dateTo: createdWindow === "custom" ? dateTo : "",
    lastHours,
    limit: pageSize,
    offset,
    asOf: "",
  };
  const jobs = useQuery({
    queryKey: ["jobs", filters],
    queryFn: () => getJobs({ ...filters, asOf: anchor.current }),
    refetchInterval: visible ? 5_000 : false,
    placeholderData: keepPreviousData,
  });
  useEffect(() => {
    if (!anchor.current && jobs.data?.as_of) {
      anchor.current = jobs.data.as_of;
    }
  }, [jobs.data?.as_of]);

  const total = jobs.data?.total ?? 0;
  const pageNumber = Math.floor(offset / pageSize) + 1;
  const pageCount = Math.max(1, Math.ceil(total / pageSize));

  return (
    <main className="w-full px-6 py-8 lg:px-10">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-semibold tracking-tight">Jobs</h1>
          <p className="mt-1.5 text-muted-foreground">
            {total} {total === 1 ? "job" : "jobs"}
          </p>
        </div>
        <Button
          onClick={() => {
            resetPage();
            void jobs.refetch();
          }}
        >
          <RefreshCw className="h-4 w-4" aria-hidden="true" />
          Refresh
        </Button>
      </div>

      <section className="surface mt-6 grid gap-3 p-5 md:grid-cols-2 lg:grid-cols-4">
        <label className="text-sm font-medium">
          Search jobs
          <input
            className="field mt-1.5 font-normal"
            value={query}
            onChange={(event) => {
              setQuery(event.target.value);
              resetPage();
            }}
          />
        </label>
        <label className="text-sm font-medium">
          State
          <select
            className="field mt-1.5 font-normal"
            value={state}
            onChange={(event) => {
              setState(event.target.value);
              resetPage();
            }}
          >
            {jobStates.map((value) => (
              <option key={value} value={value}>{value || "All states"}</option>
            ))}
          </select>
        </label>
        <label className="text-sm font-medium">
          Job source
          <select
            className="field mt-1.5 font-normal"
            value={jobSource}
            onChange={(event) => {
              setJobSource(event.target.value);
              resetPage();
            }}
          >
            <option value="">All sources</option>
            <option value="LINKEDIN">LinkedIn</option>
            <option value="INDEED">Indeed</option>
          </select>
        </label>
        <label className="text-sm font-medium">
          Created
          <select
            className="field mt-1.5 font-normal"
            value={createdWindow}
            onChange={(event) => {
              setCreatedWindow(event.target.value as CreatedWindow);
              resetPage();
            }}
          >
            {createdWindowOptions.map((option) => (
              <option key={option.value} value={option.value}>{option.label}</option>
            ))}
          </select>
        </label>
        {createdWindow === "custom" && (
          <>
            <label className="text-sm font-medium">
              Created from
              <input
                type="date"
                className="field mt-1.5 font-normal"
                value={dateFrom}
                onChange={(event) => {
                  setDateFrom(event.target.value);
                  resetPage();
                }}
              />
            </label>
            <label className="text-sm font-medium">
              Created through
              <input
                type="date"
                className="field mt-1.5 font-normal"
                value={dateTo}
                onChange={(event) => {
                  setDateTo(event.target.value);
                  resetPage();
                }}
              />
            </label>
          </>
        )}
      </section>

      {jobs.isError && <p className="mt-4 text-destructive">Jobs are unavailable.</p>}
      <section className="surface mt-6 overflow-hidden">
        <table className="w-full text-left text-sm">
          <thead className="border-b bg-muted/60">
            <tr className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              <th className="px-5 py-3">Job</th>
              <th className="px-5 py-3">Source</th>
              <th className="px-5 py-3">State</th>
              <th className="px-5 py-3">Age</th>
            </tr>
          </thead>
          <tbody>
            {jobs.data?.items.map((job) => (
              <tr
                key={job.id}
                className="border-b border-border/70 align-top transition-colors last:border-0 hover:bg-muted/40"
              >
                <td className="px-5 py-4">
                  <a
                    className="font-medium text-primary hover:underline"
                    href={job.job_url}
                    target="_blank"
                    rel="noreferrer"
                    aria-label={`${job.title} at ${job.company}`}
                  >
                    {job.title}
                  </a>
                  <p className="text-muted-foreground">{job.company} · {job.location}</p>
                  {job.description_preview ? (
                    <p className="mt-2 line-clamp-2 text-muted-foreground">
                      {job.description_preview}
                    </p>
                  ) : (
                    <p className="mt-2 text-muted-foreground">No description is available.</p>
                  )}
                </td>
                <td className="px-5 py-4 text-muted-foreground">{job.job_source}</td>
                <td className="px-5 py-4"><Badge>{job.state}</Badge></td>
                <td className="px-5 py-4 tabular-nums text-muted-foreground">
                  {formatAge(job.created_at)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {jobs.data?.items.length === 0 && (
          <p className="p-6 text-center text-muted-foreground">No jobs match these filters.</p>
        )}
        <div className="flex items-center justify-between border-t border-border/70 bg-muted/30 p-3">
          <Button
            size="sm"
            disabled={offset === 0}
            onClick={() => setOffset(Math.max(0, offset - pageSize))}
          >
            Previous page
          </Button>
          <span className="text-sm text-muted-foreground">
            Page {pageNumber} of {pageCount}
          </span>
          <Button
            size="sm"
            disabled={offset + pageSize >= total}
            onClick={() => setOffset(offset + pageSize)}
          >
            Next page
          </Button>
        </div>
      </section>
    </main>
  );
}

type FilterKey = keyof SearchSettingsInput;

const filterEditors: Array<{
  key: FilterKey;
  heading: string;
  noun: string;
  hint: string;
  tone: "include" | "exclude";
}> = [
  {
    key: "desc_include_words",
    heading: "Description include words",
    noun: "description include word",
    hint: "A job description must contain one of these words.",
    tone: "include",
  },
  {
    key: "desc_exclude_words",
    heading: "Description exclude words",
    noun: "description exclude word",
    hint: "A job is rejected when its description contains one of these words.",
    tone: "exclude",
  },
  {
    key: "title_include",
    heading: "Title include words",
    noun: "title include word",
    hint: "A job title must contain one of these words.",
    tone: "include",
  },
  {
    key: "title_exclude",
    heading: "Title exclude words",
    noun: "title exclude word",
    hint: "A job is rejected when its title contains one of these words.",
    tone: "exclude",
  },
  {
    key: "company_exclude",
    heading: "Company exclude words",
    noun: "company exclude word",
    hint: "A job is rejected when its company name contains one of these words.",
    tone: "exclude",
  },
  {
    key: "non_remote_phrases",
    heading: "Non-remote phrases",
    noun: "non-remote phrase",
    hint: "These phrases show that a job which claims to be remote is not remote.",
    tone: "exclude",
  },
];

function listValuesEqual(left: string[], right: string[]) {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

function settingsEqual(left: SearchSettingsInput, right: SearchSettingsInput) {
  return filterEditors.every(({ key }) => listValuesEqual(left[key], right[key]));
}

function cloneSettings(input: SearchSettingsInput): SearchSettingsInput {
  return {
    desc_include_words: [...input.desc_include_words],
    desc_exclude_words: [...input.desc_exclude_words],
    title_include: [...input.title_include],
    title_exclude: [...input.title_exclude],
    company_exclude: [...input.company_exclude],
    non_remote_phrases: [...input.non_remote_phrases],
  };
}

function toFilterLists(input: SearchSettingsInput): SearchSettingsInput {
  return cloneSettings(input);
}

function FilterWordEditor({
  heading,
  noun,
  hint,
  tone,
  values,
  onAdd,
  onRemove,
}: {
  heading: string;
  noun: string;
  hint: string;
  tone: "include" | "exclude";
  values: string[];
  onAdd: (value: string) => void;
  onRemove: (index: number) => void;
}) {
  const [entry, setEntry] = useState("");
  const inputID = useId();

  const add = () => {
    const value = entry.trim();
    if (!value) {
      return;
    }
    onAdd(value);
    setEntry("");
  };

  return (
    <section className="w-full rounded-xl border border-border/70 bg-card p-5 shadow-soft">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <h3 className="text-sm font-semibold tracking-tight">{heading}</h3>
        <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-medium tabular-nums text-muted-foreground">
          {values.length} {values.length === 1 ? "entry" : "entries"}
        </span>
        <p className="w-full text-sm text-muted-foreground">{hint}</p>
      </div>

      {values.length === 0 ? (
        <p className="mt-4 rounded-lg border border-dashed border-border px-3 py-4 text-center text-sm text-muted-foreground">
          No entries.
        </p>
      ) : (
        <ul className="mt-4 flex flex-wrap gap-2">
          {values.map((value, index) => (
            <li key={`${value}-${index}`}>
              <button
                type="button"
                className={cn(
                  "group inline-flex items-center gap-1.5 rounded-full border py-1 pl-3 pr-2 text-sm transition-colors",
                  tone === "include"
                    ? "border-accent bg-accent text-accent-foreground hover:border-accent-foreground/30"
                    : "border-border bg-muted text-foreground hover:border-destructive/40 hover:text-destructive",
                )}
                onClick={() => onRemove(index)}
                aria-label={`Remove ${value}`}
              >
                {value}
                <X
                  className="h-3.5 w-3.5 opacity-50 transition-opacity group-hover:opacity-100"
                  aria-hidden="true"
                />
              </button>
            </li>
          ))}
        </ul>
      )}

      <div className="mt-4 flex gap-2">
        <input
          id={inputID}
          aria-label={`Add ${noun}`}
          placeholder={`Add a ${noun}`}
          className="field flex-1"
          value={entry}
          onChange={(event) => setEntry(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              add();
            }
          }}
        />
        <Button variant="primary" onClick={add} disabled={entry.trim() === ""}>
          <Plus className="h-4 w-4" aria-hidden="true" />
          Add
          <span className="sr-only"> {noun}</span>
        </Button>
      </div>
    </section>
  );
}

function SettingsPage() {
  const queryClient = useQueryClient();
  const settings = useQuery({
    queryKey: ["search-settings"],
    queryFn: getSearchSettings,
  });
  const dashboard = useQuery({
    queryKey: ["dashboard-stats"],
    queryFn: getDashboardStats,
  });

  const [base, setBase] = useState<SearchSettingsInput | null>(null);
  const [draft, setDraft] = useState<SearchSettingsInput | null>(null);

  useEffect(() => {
    if (!settings.data || draft !== null) {
      return;
    }
    const incoming = toFilterLists(settings.data);
    setBase(incoming);
    setDraft(incoming);
  }, [draft, settings.data]);

  const save = useMutation({
    mutationFn: (next: SearchSettingsInput) => replaceSearchSettings(next),
    onSuccess: (stored) => {
      const next = toFilterLists(stored);
      setBase(next);
      setDraft(next);
      queryClient.setQueryData(["search-settings"], stored);
    },
  });

  const reset = useMutation({
    mutationFn: () => resetSearchSettings(),
    onSuccess: (stored) => {
      const next = toFilterLists(stored);
      setBase(next);
      setDraft(next);
      queryClient.setQueryData(["search-settings"], stored);
    },
  });

  const reEvaluate = useMutation({
    mutationFn: () => reEvaluateRejectedJobs(),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["dashboard-stats"] });
      void queryClient.invalidateQueries({ queryKey: ["jobs"] });
    },
  });

  const rejectedCount = (dashboard.data?.providers ?? []).reduce(
    (total, provider) => total + (provider.by_state.rejected ?? 0),
    0,
  );

  const dirty = draft !== null && base !== null && !settingsEqual(draft, base);

  const addEntry = (key: FilterKey, value: string) => {
    setDraft((current) => {
      if (!current) {
        return current;
      }
      return {
        ...current,
        [key]: [...current[key], value],
      };
    });
  };

  const removeEntry = (key: FilterKey, index: number) => {
    setDraft((current) => {
      if (!current) {
        return current;
      }
      return {
        ...current,
        [key]: current[key].filter((_, i) => i !== index),
      };
    });
  };

  const saveFilterSettings = () => {
    if (!draft) {
      return;
    }
    save.mutate(cloneSettings(draft));
  };

  const resetFilterSettings = () => {
    if (!window.confirm("Reset filter settings to seed defaults?")) {
      return;
    }
    reset.mutate();
  };

  const reEvaluateRejected = () => {
    const confirmed = window.confirm(
      `Send ${rejectedCount} currently rejected ${rejectedCount === 1 ? "job" : "jobs"} ` +
        "back to pending for re-evaluation? The count can change before you confirm.",
    );
    if (!confirmed) {
      return;
    }
    reEvaluate.mutate();
  };

  return (
    <main className="w-full px-6 py-8 lg:px-10">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-semibold tracking-tight">Settings</h1>
          <p className="mt-1.5 text-muted-foreground">
            Edit universal filters and provider scrape settings.
          </p>
        </div>
        <div className="flex flex-col items-start gap-2 sm:items-end">
          <Button
            variant="secondary"
            disabled={reEvaluate.isPending}
            onClick={reEvaluateRejected}
            title="Send rejected jobs back to pending so the next notify pass applies the current filters."
          >
            <Undo2 className="h-4 w-4" aria-hidden="true" />
            Re-evaluate rejected jobs
            {rejectedCount > 0 && (
              <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-semibold tabular-nums text-muted-foreground">
                {rejectedCount}
              </span>
            )}
          </Button>
          {(reEvaluate.isPending || reEvaluate.isSuccess || reEvaluate.isError) && (
            <p
              role="status"
              className={cn(
                "text-sm",
                reEvaluate.isError ? "text-destructive" : "text-muted-foreground",
              )}
            >
              {reEvaluate.isPending && "Re-evaluating rejected jobs."}
              {reEvaluate.isSuccess &&
                `${reEvaluate.data.updated} ${reEvaluate.data.updated === 1 ? "job" : "jobs"} ` +
                  "moved back to pending."}
              {reEvaluate.isError && "Re-evaluation failed."}
            </p>
          )}
        </div>
      </div>

      <section className="surface mt-8 bg-muted/40 p-5 lg:p-6">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-lg font-semibold tracking-tight">Filter word lists</h2>
            <p className="mt-1 text-sm text-muted-foreground">
              Every list applies to all providers.
            </p>
          </div>
          {draft && (
            <div className="flex items-center gap-2">
              {dirty && (
                <span className="text-sm font-medium text-accent-foreground">
                  Unsaved changes
                </span>
              )}
              <Button
                variant="ghost"
                disabled={save.isPending || reset.isPending}
                onClick={resetFilterSettings}
              >
                <RotateCcw className="h-4 w-4" aria-hidden="true" />
                Reset filter settings
              </Button>
              <Button
                variant="primary"
                disabled={!dirty || save.isPending || reset.isPending}
                onClick={saveFilterSettings}
              >
                <Save className="h-4 w-4" aria-hidden="true" />
                Save filter settings
              </Button>
            </div>
          )}
        </div>

        {!draft && settings.isPending && (
          <p className="mt-4 text-muted-foreground">Loading filter settings.</p>
        )}
        {!draft && settings.isError && (
          <p className="mt-4 text-destructive">Filter settings are unavailable.</p>
        )}
        {draft && (
          <div className="mt-5 flex flex-col gap-4">
            {filterEditors.map((editor) => (
              <FilterWordEditor
                key={editor.key}
                heading={editor.heading}
                noun={editor.noun}
                hint={editor.hint}
                tone={editor.tone}
                values={draft[editor.key]}
                onAdd={(value) => addEntry(editor.key, value)}
                onRemove={(index) => removeEntry(editor.key, index)}
              />
            ))}
          </div>
        )}
      </section>

      <section className="surface mt-6 bg-muted/40 p-5 lg:p-6">
        <h2 className="text-lg font-semibold tracking-tight">Provider settings</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          Each provider keeps its own schedule and search queries.
        </p>
        <ProviderSettingsEditor />
      </section>
    </main>
  );
}

function OperatorRoutes() {
  return (
    <>
      <Header />
      <Routes>
        <Route path="/dashboard" element={<DashboardPage />} />
        <Route path="/jobs" element={<JobsPage />} />
        <Route path="/settings" element={<SettingsPage />} />
        <Route path="*" element={<Navigate to="/dashboard" replace />} />
      </Routes>
    </>
  );
}

export function App() {
  return (
    <BrowserRouter>
      <OperatorRoutes />
    </BrowserRouter>
  );
}
