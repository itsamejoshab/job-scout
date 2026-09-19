import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, ExternalLink } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
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
    <header className="border-b bg-background">
      <div className="flex w-full flex-wrap items-center gap-4 px-6 py-4">
        <div className="flex items-center gap-2 text-lg font-semibold">
          <Activity className="h-5 w-5 text-primary" aria-hidden="true" />
          <span>{config.data?.project_name ?? "JobScout"}</span>
        </div>
        <nav className="flex gap-1" aria-label="Main navigation">
          {pages.map((page) => (
            <NavLink
              key={page.path}
              to={page.path}
              className={({ isActive }) =>
                cn(
                  "rounded-md px-3 py-2 text-sm font-medium text-muted-foreground hover:bg-muted hover:text-foreground",
                  isActive && "bg-muted text-foreground",
                )
              }
            >
              {page.label}
            </NavLink>
          ))}
        </nav>
        <div className="ml-auto flex flex-wrap items-center justify-end gap-4">
          <Badge variant={down.length > 0 ? "destructive" : "secondary"}>
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
    <main className="max-w-6xl px-6 py-10">
      <h1 className="text-3xl font-semibold tracking-tight">Dashboard</h1>
      {dashboard.isPending && (
        <p className="mt-2 text-muted-foreground">Loading dashboard statistics.</p>
      )}
      {dashboard.isError && (
        <p className="mt-2 text-destructive">Dashboard statistics are unavailable.</p>
      )}
      {dashboard.data && (
        <>
          <p className="mt-2 text-muted-foreground">
            Generated at {formatTimestamp(dashboard.data.generated_at)}
          </p>

          <section className="mt-8 grid gap-4 md:grid-cols-2" aria-label="Provider cards">
            {dashboard.data.providers.map((provider) => (
              <article key={provider.job_source} className="rounded-lg border bg-card p-4">
                <div className="flex items-center justify-between">
                  <h2 className="text-lg font-semibold">{provider.job_source}</h2>
                  <Badge variant={provider.status === "due" ? "destructive" : "secondary"}>
                    {provider.status}
                  </Badge>
                </div>
                <p className="mt-2 text-sm">Status: {provider.status}</p>
                <p className="text-sm">Implemented: {provider.implemented ? "yes" : "no"}</p>
                <p className="text-sm">Enabled: {provider.enabled ? "yes" : "no"}</p>
                <p className="text-sm">Interval seconds: {provider.scrape_interval_seconds}</p>
                <p className="text-sm">Last scraped: {formatTimestamp(provider.last_scraped_at)}</p>
                <p className="text-sm">Failure backoff: {formatTimestamp(provider.next_eligible_at)}</p>
                <p className="text-sm">Total stored jobs: {provider.total_jobs}</p>

                <div className="mt-3 text-sm">
                  {stateOrder.map((state) => (
                    <p key={state}>
                      {state}: {provider.by_state[state] ?? 0}
                    </p>
                  ))}
                </div>

                <details className="mt-3 text-sm" open>
                  <summary className="cursor-pointer font-medium">Rejection reasons</summary>
                  <div className="mt-2">
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

          <section className="mt-8 rounded-lg border bg-card p-4">
            <h2 className="text-lg font-semibold">
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

          <section className="mt-8">
            <div className="flex gap-2">
              <button
                type="button"
                disabled={status.data?.temporal.ok === false || start.isPending}
                className="rounded-md border px-3 py-2 text-sm disabled:cursor-not-allowed disabled:opacity-50"
                onClick={() => start.mutate("scrape")}
              >
                Run scrape
              </button>
              <button
                type="button"
                disabled={status.data?.temporal.ok === false || start.isPending}
                className="rounded-md border px-3 py-2 text-sm disabled:cursor-not-allowed disabled:opacity-50"
                onClick={() => start.mutate("notify")}
              >
                Run notify
              </button>
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
    <main className="max-w-6xl px-6 py-10">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-semibold tracking-tight">Jobs</h1>
          <p className="mt-2 text-muted-foreground">
            {total} {total === 1 ? "job" : "jobs"}
          </p>
        </div>
        <button
          type="button"
          className="rounded-md border px-3 py-2 text-sm"
          onClick={() => {
            resetPage();
            void jobs.refetch();
          }}
        >
          Refresh
        </button>
      </div>

      <section className="mt-6 grid gap-3 rounded-lg border bg-card p-4 md:grid-cols-2 lg:grid-cols-4">
        <label className="text-sm font-medium">
          Search jobs
          <input
            className="mt-1 w-full rounded-md border bg-background px-3 py-2 font-normal"
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
            className="mt-1 w-full rounded-md border bg-background px-3 py-2 font-normal"
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
            className="mt-1 w-full rounded-md border bg-background px-3 py-2 font-normal"
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
            className="mt-1 w-full rounded-md border bg-background px-3 py-2 font-normal"
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
                className="mt-1 w-full rounded-md border bg-background px-3 py-2 font-normal"
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
                className="mt-1 w-full rounded-md border bg-background px-3 py-2 font-normal"
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
      <section className="mt-6 overflow-x-auto rounded-lg border">
        <table className="w-full text-left text-sm">
          <thead className="border-b bg-muted">
            <tr>
              <th className="px-4 py-3">Job</th>
              <th className="px-4 py-3">Source</th>
              <th className="px-4 py-3">State</th>
              <th className="px-4 py-3">Age</th>
            </tr>
          </thead>
          <tbody>
            {jobs.data?.items.map((job) => (
              <tr key={job.id} className="border-b last:border-0 align-top">
                <td className="px-4 py-3">
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
                <td className="px-4 py-3">{job.job_source}</td>
                <td className="px-4 py-3"><Badge>{job.state}</Badge></td>
                <td className="px-4 py-3">{formatAge(job.created_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {jobs.data?.items.length === 0 && (
          <p className="p-6 text-center text-muted-foreground">No jobs match these filters.</p>
        )}
        <div className="flex items-center justify-between border-t p-3">
          <button
            type="button"
            className="rounded-md border px-3 py-2 disabled:opacity-50"
            disabled={offset === 0}
            onClick={() => setOffset(Math.max(0, offset - pageSize))}
          >
            Previous page
          </button>
          <span>Page {pageNumber} of {pageCount}</span>
          <button
            type="button"
            className="rounded-md border px-3 py-2 disabled:opacity-50"
            disabled={offset + pageSize >= total}
            onClick={() => setOffset(offset + pageSize)}
          >
            Next page
          </button>
        </div>
      </section>
    </main>
  );
}

type FilterKey = keyof SearchSettingsInput;

const filterEditors: Array<{ key: FilterKey; heading: string; addLabel: string }> = [
  {
    key: "desc_include_words",
    heading: "Description include words",
    addLabel: "Add description include word",
  },
  {
    key: "desc_exclude_words",
    heading: "Description exclude words",
    addLabel: "Add description exclude word",
  },
  {
    key: "title_include",
    heading: "Title include words",
    addLabel: "Add title include word",
  },
  {
    key: "title_exclude",
    heading: "Title exclude words",
    addLabel: "Add title exclude word",
  },
  {
    key: "company_exclude",
    heading: "Company exclude words",
    addLabel: "Add company exclude word",
  },
  {
    key: "non_remote_phrases",
    heading: "Non-remote phrases",
    addLabel: "Add non-remote phrase",
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
  addLabel,
  values,
  onAdd,
  onRemove,
}: {
  heading: string;
  addLabel: string;
  values: string[];
  onAdd: (value: string) => void;
  onRemove: (index: number) => void;
}) {
  const [entry, setEntry] = useState("");

  const add = () => {
    const value = entry.trim();
    if (!value) {
      return;
    }
    onAdd(value);
    setEntry("");
  };

  return (
    <fieldset className="rounded-md border p-3">
      <legend className="px-1 text-sm font-semibold">{heading}</legend>
      {values.length === 0 ? (
        <p className="text-sm text-muted-foreground">No entries.</p>
      ) : (
        <ul className="flex flex-wrap gap-2">
          {values.map((value, index) => (
            <li key={`${value}-${index}`}>
              <button
                type="button"
                className="rounded-full border px-3 py-1 text-sm hover:bg-muted"
                onClick={() => onRemove(index)}
                aria-label={`Remove ${value}`}
              >
                {value} ×
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="mt-3 flex gap-2">
        <label className="sr-only" htmlFor={addLabel}>
          {addLabel}
        </label>
        <input
          id={addLabel}
          aria-label={addLabel}
          className="w-full rounded-md border bg-background px-3 py-2"
          value={entry}
          onChange={(event) => setEntry(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              add();
            }
          }}
        />
        <button
          type="button"
          className="rounded-md border px-3 py-2 text-sm"
          onClick={add}
        >
          {addLabel}
        </button>
      </div>
    </fieldset>
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
    <main className="max-w-6xl px-6 py-10">
      <h1 className="text-3xl font-semibold tracking-tight">Settings</h1>
      <p className="mt-2 text-muted-foreground">
        Edit universal filters and provider scrape settings.
      </p>

      <section className="mt-6 rounded-lg border bg-card p-4">
        <h2 className="text-xl font-semibold">Filter word lists</h2>
        {!draft && settings.isPending && (
          <p className="mt-2 text-muted-foreground">Loading filter settings.</p>
        )}
        {!draft && settings.isError && (
          <p className="mt-2 text-destructive">Filter settings are unavailable.</p>
        )}
        {draft && (
          <>
            <div className="mt-4 grid gap-3 md:grid-cols-2">
              {filterEditors.map((editor) => (
                <FilterWordEditor
                  key={editor.key}
                  heading={editor.heading}
                  addLabel={editor.addLabel}
                  values={draft[editor.key]}
                  onAdd={(value) => addEntry(editor.key, value)}
                  onRemove={(index) => removeEntry(editor.key, index)}
                />
              ))}
            </div>
            <div className="mt-4 flex gap-2">
              <button
                type="button"
                className="rounded-md border px-3 py-2 text-sm disabled:opacity-50"
                disabled={!dirty || save.isPending || reset.isPending}
                onClick={saveFilterSettings}
              >
                Save filter settings
              </button>
              <button
                type="button"
                className="rounded-md border px-3 py-2 text-sm disabled:opacity-50"
                disabled={save.isPending || reset.isPending}
                onClick={resetFilterSettings}
              >
                Reset filter settings
              </button>
            </div>
          </>
        )}
      </section>

      <section className="mt-6 rounded-lg border bg-card p-4">
        <h2 className="text-xl font-semibold">Rejected jobs</h2>
        <p className="mt-2 text-muted-foreground">
          Send rejected jobs back to pending so the next notify pass applies the current filters.
        </p>
        <button
          type="button"
          className="mt-4 rounded-md border px-3 py-2 text-sm disabled:opacity-50"
          disabled={reEvaluate.isPending}
          onClick={reEvaluateRejected}
        >
          Re-evaluate rejected jobs
        </button>
        {(reEvaluate.isPending || reEvaluate.isSuccess || reEvaluate.isError) && (
          <p role="status" className="mt-3 text-sm">
            {reEvaluate.isPending && "Re-evaluating rejected jobs."}
            {reEvaluate.isSuccess &&
              `${reEvaluate.data.updated} ${reEvaluate.data.updated === 1 ? "job" : "jobs"} ` +
                "moved back to pending."}
            {reEvaluate.isError && "Re-evaluation failed."}
          </p>
        )}
      </section>

      <section className="mt-6 rounded-lg border bg-card p-4">
        <h2 className="text-xl font-semibold">Provider settings</h2>
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
