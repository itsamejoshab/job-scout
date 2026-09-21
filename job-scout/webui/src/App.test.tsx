import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { App } from "./App";
import type { ProviderSettings } from "./api";

const config = {
  project_name: "Job Scout Test",
  temporal_ui_address: "http://temporal.example:8233",
};

const dashboardStats = {
  generated_at: "2026-09-18T20:00:00Z",
  timezone: "America/New_York",
  notified: 1,
  providers: [
    {
      job_source: "LINKEDIN",
      implemented: true,
      enabled: true,
      scrape_interval_seconds: 900,
      last_scraped_at: "2026-09-18T19:55:00Z",
      next_eligible_at: null,
      status: "waiting",
      total_jobs: 2,
      by_state: {
        pending: 1,
        rejected: 1,
        needs_detail: 0,
        ready: 0,
        applied: 0,
        dismissed: 0,
      },
      by_reject_reason: {
        duplicate: 0,
        title_company: 1,
        description: 0,
        detail_failed: 0,
        unsupported_source: 0,
      },
    },
    {
      job_source: "INDEED",
      implemented: true,
      configured: false,
      configuration_message:
        "Create an Apify account and set APIFY_API_TOKEN in job-scout/.env to enable this provider.",
      enabled: false,
      scrape_interval_seconds: 43200,
      last_scraped_at: null,
      next_eligible_at: null,
      status: "setup_required",
      total_jobs: 0,
      by_state: {
        pending: 0,
        rejected: 0,
        needs_detail: 0,
        ready: 0,
        applied: 0,
        dismissed: 0,
      },
      by_reject_reason: {
        duplicate: 0,
        title_company: 0,
        description: 0,
        detail_failed: 0,
        unsupported_source: 0,
      },
      apify_budget: {
        period_start: "2026-09-21T00:00:00Z",
        period_end: "2026-10-21T00:00:00Z",
        limit_usd: 1,
        used_usd: null,
        remaining_usd: null,
        blocked: true,
        reason: "token_missing" as const,
      },
    },
    {
      job_source: "DICE",
      implemented: true,
      configured: false,
      configuration_message:
        "Create an Apify account and set APIFY_API_TOKEN in job-scout/.env to enable this provider.",
      enabled: false,
      scrape_interval_seconds: 43200,
      last_scraped_at: null,
      next_eligible_at: null,
      status: "setup_required",
      total_jobs: 0,
      by_state: {
        pending: 0,
        rejected: 0,
        needs_detail: 0,
        ready: 0,
        applied: 0,
        dismissed: 0,
      },
      by_reject_reason: {
        duplicate: 0,
        title_company: 0,
        description: 0,
        detail_failed: 0,
        unsupported_source: 0,
      },
      apify_budget: {
        period_start: "2026-09-21T00:00:00Z",
        period_end: "2026-10-21T00:00:00Z",
        limit_usd: 1,
        used_usd: null,
        remaining_usd: null,
        blocked: true,
        reason: "token_missing" as const,
      },
    },
  ],
  daily: [
    { day: "2026-09-17", total: 2, notified: 1, applied: 0, pending: 1, skipped: 1 },
    { day: "2026-09-18", total: 0, notified: 0, applied: 0, pending: 0, skipped: 0 },
  ],
};

const jobsPage = {
  items: [
    {
      id: 42,
      job_source: "LINKEDIN",
      title: "Platform Engineer",
      company: "Acme",
      location: "Remote",
      job_url: "https://example.test/jobs/42",
      created_at: "2026-09-18T20:00:00Z",
      updated_at: "2026-09-18T20:00:00Z",
      state: "ready",
      reject_reason: null,
      has_description: true,
      description_preview: "A short job description preview.",
      is_remote: true,
    },
  ],
  total: 51,
  as_of: "2026-09-18T21:00:00Z",
};

const filterSeed = {
  id: 1,
  desc_include_words: ["computer", "support"],
  desc_exclude_words: ["travel"],
  title_include: ["IT"],
  title_exclude: ["manager"],
  company_exclude: ["Bad Co"],
  created_at: "2026-09-18T20:00:00Z",
  updated_at: "2026-09-18T20:00:00Z",
};

const providerSeed: Record<"LINKEDIN" | "INDEED" | "DICE", ProviderSettings> = {
  LINKEDIN: {
    id: 1,
    job_source: "LINKEDIN",
    search_queries: [
      { keywords: "Support", location: "101076143", f_WT: "1,2" },
      { keywords: "Engineer", location: "101076143", f_WT: "1,2" },
    ],
    global_searches: [
      "Remote IT Help Desk near Port Orange FL",
    ],
    timespan_code: "r86400",
    pages_to_scrape: 1,
    rounds: 1,
    enabled: true,
    scrape_interval_seconds: 900,
    last_scraped_at: "2026-09-18T19:55:00Z",
    next_eligible_at: null,
    created_at: "2026-09-18T20:00:00Z",
    updated_at: "2026-09-18T20:00:00Z",
  },
  INDEED: {
    id: 2,
    job_source: "INDEED",
    search_queries: [
      {
        keywords: "Desktop or Endpoint or Application Support",
        location: "Port Orange, FL",
        include_remote: "false",
        include_hybrid: "false",
        radius: "15",
      } as ProviderSettings["search_queries"][number],
    ],
    global_searches: [],
    timespan_code: "1",
    pages_to_scrape: 1,
    rounds: 1,
    enabled: true,
    scrape_interval_seconds: 43200,
    provider_options: {
      country: "us",
      jobType: "fulltime",
      fromDays: "1",
      maxRows: 100,
      enableUniqueJobs: true,
      includeSimilarJobs: false,
    },
    last_scraped_at: null,
    next_eligible_at: null,
    created_at: "2026-09-18T20:00:00Z",
    updated_at: "2026-09-18T20:00:00Z",
  },
  DICE: {
    id: 3,
    job_source: "DICE",
    search_queries: [
      {
        keywords: "Desktop or Endpoint or Application Support",
        location: "Port Orange, FL",
        include_remote: "true",
      } as ProviderSettings["search_queries"][number],
    ],
    global_searches: [],
    timespan_code: "24h",
    pages_to_scrape: 1,
    rounds: 1,
    enabled: true,
    scrape_interval_seconds: 43200,
    last_scraped_at: null,
    next_eligible_at: null,
    created_at: "2026-09-18T20:00:00Z",
    updated_at: "2026-09-18T20:00:00Z",
  },
};

const notificationSeed = {
  enabled: true,
  configured: true,
  active: true,
  timezone: "America/New_York",
  schedule: {
    mode: "cron" as const,
    interval_minutes: 15,
    cron_pattern: "39 7,17,20 * * *",
    silent_periods: [],
  },
};

type RenderOptions = {
  status?: object;
  stats?: object;
  searchSettings?: typeof filterSeed;
  notificationSettings?: typeof notificationSeed;
  notifyResponse?: object;
  reEvaluatedCount?: number;
  workflowStatuses?: string[];
  jobs?: typeof jobsPage;
};

function jobsPageWithState(state: string) {
  const page = structuredClone(jobsPage);
  page.items[0].state = state;
  return page;
}

function statsWithRejected(linkedIn: number, indeed: number) {
  const stats = structuredClone(dashboardStats);
  stats.providers[0].by_state.rejected = linkedIn;
  stats.providers[1].by_state.rejected = indeed;
  return stats;
}

function normalizeWords(words: string[]) {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of words) {
    const trimmed = raw.trim();
    if (!trimmed) {
      continue;
    }
    const key = trimmed.toLowerCase();
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    out.push(trimmed);
  }
  return out;
}

function renderPath(path: string, options: RenderOptions = {}) {
  const status = options.status ?? {
    ok: true,
    database: { ok: true },
    temporal: { ok: true },
  };
  const stats = options.stats ?? dashboardStats;
  const seed = options.searchSettings ?? filterSeed;
  let currentSettings = structuredClone(seed);
  let currentNotifications = structuredClone(options.notificationSettings ?? notificationSeed);
  const currentProviders = structuredClone(providerSeed);
  let workflowStatusIndex = 0;

  window.history.replaceState({}, "", path);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: vi.fn().mockResolvedValue(undefined) },
  });
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method ?? "GET").toUpperCase();
    if (url === "/api/v0/run?force=1" && method === "POST") {
      return new Response(JSON.stringify({ workflow_id: "manual/scrape id" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    if (url === "/api/v0/notify" && method === "POST") {
      return new Response(JSON.stringify(options.notifyResponse ?? { workflow_id: "manual-notify-1" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    if (url === "/api/v0/workflow/manual%2Fscrape%20id") {
      const statuses = options.workflowStatuses ?? ["WORKFLOW_EXECUTION_STATUS_COMPLETED"];
      const workflowStatus = statuses[Math.min(workflowStatusIndex, statuses.length - 1)];
      workflowStatusIndex += 1;
      return new Response(JSON.stringify({
        workflow_id: "manual/scrape id",
        run_id: "run-1",
        status: workflowStatus,
      }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    if (url.startsWith("/api/v0/jobs?")) {
      return new Response(JSON.stringify(options.jobs ?? jobsPage), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    if (url === "/api/v0/jobs/42/review" && method === "POST") {
      const payload = JSON.parse(String(init?.body ?? "{}")) as { action: string };
      return new Response(JSON.stringify({ ...jobsPage.items[0], state: payload.action }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    if (url === "/api/v0/jobs/re-evaluate" && method === "POST") {
      return new Response(JSON.stringify({ updated: options.reEvaluatedCount ?? 7 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    if (url === "/api/v0/search-settings" && method === "GET") {
      return new Response(JSON.stringify(currentSettings), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    if (url === "/api/v0/notification-settings" && method === "GET") {
      return new Response(JSON.stringify(currentNotifications), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    if (url === "/api/v0/notification-settings" && method === "PUT") {
      const payload = JSON.parse(String(init?.body ?? "{}")) as {
        enabled: boolean;
        schedule?: typeof notificationSeed.schedule;
      };
      currentNotifications = {
        enabled: payload.enabled,
        configured: currentNotifications.configured,
        active: payload.enabled && currentNotifications.configured,
        timezone: currentNotifications.timezone,
        schedule: payload.schedule ?? currentNotifications.schedule,
        ...(payload.enabled && currentNotifications.configured
          ? {}
          : {
              reason: payload.enabled
                ? "Webhook delivery is not configured. Set WEBHOOK_BASE and WEBHOOK_ID."
                : "Notifications are turned off in Settings.",
            }),
      };
      return new Response(JSON.stringify(currentNotifications), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    if (url === "/api/v0/search-settings" && method === "PUT") {
      const payload = JSON.parse(String(init?.body ?? "{}")) as {
        desc_include_words: string[];
        desc_exclude_words: string[];
        title_include: string[];
        title_exclude: string[];
        company_exclude: string[];
      };
      currentSettings = {
        ...currentSettings,
        desc_include_words: normalizeWords(payload.desc_include_words ?? []),
        desc_exclude_words: normalizeWords(payload.desc_exclude_words ?? []),
        title_include: normalizeWords(payload.title_include ?? []),
        title_exclude: normalizeWords(payload.title_exclude ?? []),
        company_exclude: normalizeWords(payload.company_exclude ?? []),
        updated_at: "2026-09-18T20:05:00Z",
      };
      return new Response(JSON.stringify(currentSettings), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    if (url === "/api/v0/search-settings/reset" && method === "POST") {
      currentSettings = structuredClone(seed);
      return new Response(JSON.stringify(currentSettings), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    if (url === "/api/v0/scraper-settings/all" && method === "GET") {
      return new Response(JSON.stringify(currentProviders), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    const providerMatch = url.match(/^\/api\/v0\/scraper-settings\/(LINKEDIN|INDEED|DICE)(\/reset)?$/);
    if (providerMatch && method === "PUT") {
      const source = providerMatch[1] as keyof typeof currentProviders;
      const payload = JSON.parse(String(init?.body ?? "{}"));
      currentProviders[source] = {
        ...currentProviders[source],
        ...payload,
        timespan_code: String(payload.timespan_code).trim(),
        search_queries: payload.search_queries.map((query: Record<string, unknown>) => ({
          keywords: String(query.keywords).trim(),
          location: String(query.location).trim(),
          ...(query.f_WT !== undefined ? { f_WT: String(query.f_WT) } : {}),
          ...(query.include_remote !== undefined
            ? {
                include_remote:
                  query.include_remote === true || query.include_remote === "true"
                    ? "true"
                    : "false",
              }
            : {}),
          ...(query.include_hybrid !== undefined
            ? {
                include_hybrid:
                  query.include_hybrid === true || query.include_hybrid === "true"
                    ? "true"
                    : "false",
              }
            : {}),
        })),
        global_searches: (payload.global_searches as string[]).map((item) => item.trim()),
        ...(payload.provider_options !== undefined
          ? { provider_options: payload.provider_options }
          : {}),
        updated_at: "2026-09-18T20:05:00Z",
      };
      return new Response(JSON.stringify(currentProviders[source]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    if (providerMatch?.[2] === "/reset" && method === "POST") {
      const source = providerMatch[1] as keyof typeof currentProviders;
      currentProviders[source] = structuredClone(providerSeed[source]);
      return new Response(JSON.stringify(currentProviders[source]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    const sharedSettings = () => ({
      format: "jobscout-settings",
      version: 1,
      notifications: { enabled: currentNotifications.enabled },
      search: {
        desc_include_words: currentSettings.desc_include_words,
        desc_exclude_words: currentSettings.desc_exclude_words,
        title_include: currentSettings.title_include,
        title_exclude: currentSettings.title_exclude,
        company_exclude: currentSettings.company_exclude,
      },
      providers: Object.fromEntries(
        Object.entries(currentProviders).map(([source, provider]) => [
          source,
          {
            enabled: provider.enabled,
            scrape_interval_seconds: provider.scrape_interval_seconds,
            timespan_code: provider.timespan_code,
            pages_to_scrape: provider.pages_to_scrape,
            rounds: provider.rounds,
            search_queries: provider.search_queries,
            global_searches: provider.global_searches,
            ...(provider.provider_options ? { provider_options: provider.provider_options } : {}),
          },
        ]),
      ),
    });
    if (url === "/api/v0/settings/export" && method === "GET") {
      return new Response(JSON.stringify({
        share_code: "!JS:1!abcdefghijklmnopqrstuvwxyz012345",
        settings: sharedSettings(),
      }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    if (url === "/api/v0/settings/import" && method === "POST") {
      const body = JSON.parse(String(init?.body ?? "{}")) as { payload?: unknown };
      let bundle = body.payload;
      if (typeof bundle === "string") {
        const trimmed = bundle.trim();
        bundle = trimmed.startsWith("!JS:1!")
          ? sharedSettings()
          : JSON.parse(trimmed);
      }
      const incoming = bundle as {
        notifications?: { enabled?: boolean };
        search?: typeof filterSeed;
        providers?: Record<string, Partial<(typeof currentProviders)["LINKEDIN"]>>;
      };
      if (incoming.search) {
        currentSettings = {
          ...currentSettings,
          ...incoming.search,
          updated_at: "2026-09-18T20:10:00Z",
        };
      }
      if (incoming.notifications?.enabled !== undefined) {
        currentNotifications = {
          ...currentNotifications,
          enabled: incoming.notifications.enabled,
          active: incoming.notifications.enabled && currentNotifications.configured,
        };
      }
      if (incoming.providers) {
        for (const [source, provider] of Object.entries(incoming.providers)) {
          const key = source as keyof typeof currentProviders;
          if (!currentProviders[key]) {
            continue;
          }
          currentProviders[key] = {
            ...currentProviders[key],
            ...provider,
            updated_at: "2026-09-18T20:10:00Z",
          };
        }
      }
      return new Response(JSON.stringify({
        share_code: "!JS:1!abcdefghijklmnopqrstuvwxyz012345",
        settings: sharedSettings(),
      }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    if (
      url !== "/api/v0/status" &&
      url !== "/api/v0/config" &&
      url !== "/api/v0/dashboard/stats"
    ) {
      return new Response(null, { status: 404 });
    }
    const body = url === "/api/v0/status"
      ? status
      : url === "/api/v0/config"
        ? config
        : stats;
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }));

  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <App />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

async function expandProvider(
  user: ReturnType<typeof userEvent.setup>,
  source: "LINKEDIN" | "INDEED" | "DICE",
) {
  const toggle = await screen.findByRole("button", { name: `${source} provider` });
  if (toggle.getAttribute("aria-expanded") !== "true") {
    await user.click(toggle);
  }
}

async function expandShare(user: ReturnType<typeof userEvent.setup>) {
  const toggle = await screen.findByRole("button", { name: /share settings/i });
  if (toggle.getAttribute("aria-expanded") !== "true") {
    await user.click(toggle);
  }
}

describe("operator shell", () => {
  it("renders configured header chrome and names the down dependency", async () => {
    renderPath("/dashboard", {
      status: {
        ok: false,
        database: { ok: true },
        temporal: { ok: false, error: "connection refused" },
      },
    });

    expect(await screen.findByText("Job Scout Test")).toBeInTheDocument();
    expect(screen.getByText("Temporal down")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /temporal ui/i })).toHaveAttribute(
      "href",
      "http://temporal.example:8233",
    );
    expect(fetch).toHaveBeenCalledWith("/api/v0/config");
    expect(fetch).toHaveBeenCalledWith("/api/v0/status");
    expect(fetch).toHaveBeenCalledWith("/api/v0/dashboard/stats");
    expect(screen.queryByRole("button", { name: /re-evaluate rejected jobs/i })).not.toBeInTheDocument();
  });

  it.each([
    ["/dashboard", "Dashboard"],
    ["/jobs", "Jobs"],
    ["/settings", "Settings"],
    ["/pasted/deep-link", "Dashboard"],
  ])("renders %s as the %s shell", async (path, heading) => {
    renderPath(path);

    expect(
      await screen.findByRole("heading", { name: heading }),
    ).toBeInTheDocument();
    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith("/api/v0/config");
      expect(fetch).toHaveBeenCalledWith("/api/v0/status");
    });
  });

  it("renders provider cards and labels chart timezone", async () => {
    renderPath("/dashboard");

    expect(await screen.findByText("LINKEDIN")).toBeInTheDocument();
    expect(screen.getByText("INDEED")).toBeInTheDocument();
    expect(screen.getByText("DICE")).toBeInTheDocument();
    expect(screen.getByText("Status: on cooldown")).toBeInTheDocument();
    expect(screen.getAllByText("Status: setup required")).toHaveLength(2);
    const linkedInCard = screen.getByText("LINKEDIN").closest("article");
    expect(linkedInCard).not.toBeNull();
    expect(linkedInCard).toHaveTextContent("Pending");
    expect(linkedInCard).toHaveTextContent("Processing");
    expect(linkedInCard).toHaveTextContent("Title or company");
    expect(linkedInCard).not.toHaveTextContent("Apify token is missing");
    const diceCard = screen.getByText("DICE").closest("article");
    expect(diceCard).not.toBeNull();
    expect(diceCard).toHaveTextContent("Apify token is missing");
    expect(diceCard).toHaveTextContent("(blocked)");
    expect(diceCard).toHaveTextContent("Create an Apify account");
    expect(screen.getAllByText("Processing").length).toBeGreaterThanOrEqual(2);
    expect(screen.getByText("Jobs over time (America/New_York)")).toBeInTheDocument();
    expect(screen.queryByText(/Jobs emailed/i)).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Jobs by status" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Rejected jobs by reason" })).toBeInTheDocument();
    expect(screen.getByLabelText("Jobs by status legend")).toBeInTheDocument();
    expect(screen.getByLabelText("Rejected jobs by reason legend")).toBeInTheDocument();
    expect(screen.getByLabelText("Jobs by status legend")).toHaveTextContent("Pending");
    expect(screen.getByLabelText("Jobs by status legend")).toHaveTextContent("1");
    expect(screen.getByLabelText("Rejected jobs by reason legend")).toHaveTextContent(
      "Title or company",
    );
    expect(screen.getByLabelText("Rejected jobs by reason legend")).toHaveTextContent(
      "Dismissed by you",
    );
  });

  it("polls dashboard every two seconds and pauses while hidden", async () => {
    vi.useFakeTimers();
    let hidden = false;
    Object.defineProperty(document, "hidden", {
      configurable: true,
      get: () => hidden,
    });

    renderPath("/dashboard");

    const dashboardCalls = () =>
      ((fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls).filter(
        ([input]) => String(input) === "/api/v0/dashboard/stats",
      ).length;

    const first = dashboardCalls();
    expect(first).toBeGreaterThan(0);
    await vi.advanceTimersByTimeAsync(2_100);
    expect(dashboardCalls()).toBeGreaterThan(first);

    hidden = true;
    document.dispatchEvent(new Event("visibilitychange"));
    const pausedAt = dashboardCalls();
    await vi.advanceTimersByTimeAsync(6_000);
    expect(dashboardCalls()).toBe(pausedAt);

    hidden = false;
    document.dispatchEvent(new Event("visibilitychange"));
    await vi.advanceTimersByTimeAsync(2_100);
    expect(dashboardCalls()).toBeGreaterThan(pausedAt);
  }, 10_000);

  it("runs a forced scrape, links its workflow, and refreshes data after completion", async () => {
    const user = userEvent.setup();
    renderPath("/dashboard", {
      workflowStatuses: [
        "WORKFLOW_EXECUTION_STATUS_RUNNING",
        "WORKFLOW_EXECUTION_STATUS_COMPLETED",
      ],
    });

    const dashboardCalls = () =>
      ((fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls).filter(
        ([input]) => String(input) === "/api/v0/dashboard/stats",
      ).length;
    const jobsCalls = () =>
      ((fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls).filter(
        ([input]) => String(input).startsWith("/api/v0/jobs?"),
      ).length;

    await user.click(await screen.findByRole("link", { name: "Jobs" }));
    await screen.findByText("Platform Engineer");
    await user.click(screen.getByRole("link", { name: "Dashboard" }));
    await screen.findByText("LINKEDIN");
    const initialDashboardCalls = dashboardCalls();
    const initialJobsCalls = jobsCalls();
    await user.click(screen.getByRole("button", { name: "Scrape Jobs" }));

    expect(fetch).toHaveBeenCalledWith(
      "/api/v0/run?force=1",
      expect.objectContaining({ method: "POST" }),
    );
    const workflowLink = await screen.findByRole("link", { name: "manual/scrape id" });
    expect(workflowLink).toHaveAttribute(
      "href",
      "http://temporal.example:8233/namespaces/default/workflows/manual%2Fscrape%20id",
    );

    await waitFor(() => {
      expect(screen.getByRole("status")).toHaveTextContent("Workflow finished.");
      expect(screen.getByRole("status")).not.toHaveTextContent(/success/i);
      expect(dashboardCalls()).toBeGreaterThan(initialDashboardCalls);
      expect(jobsCalls()).toBeGreaterThan(initialJobsCalls);
    }, { timeout: 2_500 });
    const workflowCalls = (fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls
      .filter(([input]) => String(input) === "/api/v0/workflow/manual%2Fscrape%20id");
    expect(workflowCalls.length).toBeGreaterThanOrEqual(2);
    const callsAtCompletion = workflowCalls.length;
    await new Promise((resolve) => window.setTimeout(resolve, 1_500));
    expect(
      (fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls
        .filter(([input]) => String(input) === "/api/v0/workflow/manual%2Fscrape%20id"),
    ).toHaveLength(callsAtCompletion);
  });

  it("starts a notify workflow and shows its identifier", async () => {
    const user = userEvent.setup();
    renderPath("/dashboard");

    await user.click(await screen.findByRole("button", { name: "Send Email" }));

    expect(fetch).toHaveBeenCalledWith(
      "/api/v0/notify",
      expect.objectContaining({ method: "POST" }),
    );
    expect(await screen.findByRole("link", { name: "manual-notify-1" })).toHaveAttribute(
      "href",
      "http://temporal.example:8233/namespaces/default/workflows/manual-notify-1",
    );
  });

  it("shows the disabled notification reason without starting a workflow link", async () => {
    const user = userEvent.setup();
    renderPath("/dashboard", {
      notifyResponse: {
        status: "notifications_disabled",
        reason: "Webhook delivery is not configured. Set WEBHOOK_BASE and WEBHOOK_ID.",
        enabled: true,
        configured: false,
        active: false,
      },
    });

    await user.click(await screen.findByRole("button", { name: "Send Email" }));

    expect(await screen.findByRole("status")).toHaveTextContent(
      "Webhook delivery is not configured. Set WEBHOOK_BASE and WEBHOOK_ID.",
    );
    expect(screen.queryByRole("link", { name: "manual-notify-1" })).not.toBeInTheDocument();
  });

  it("disables manual runs while Temporal is down", async () => {
    renderPath("/dashboard", {
      status: {
        ok: false,
        database: { ok: true },
        temporal: { ok: false, error: "connection refused" },
      },
    });

    const scrape = await screen.findByRole("button", { name: "Scrape Jobs" });
    const notify = screen.getByRole("button", { name: "Send Email" });
    await waitFor(() => {
      expect(scrape).toBeDisabled();
      expect(notify).toBeDisabled();
    });
  });

  it("renders the ready review queue and submits final review actions", async () => {
    const user = userEvent.setup();
    renderPath("/jobs");

    expect(await screen.findByText("Platform Engineer")).toBeInTheDocument();
    expect(screen.getByText("In Review", { selector: "div" })).toBeInTheDocument();
    expect(screen.getByText("51 jobs")).toBeInTheDocument();
    expect(screen.getByText("A short job description preview.")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Job detail" })).not.toBeInTheDocument();
    expect(screen.queryByText(/posting date/i)).not.toBeInTheDocument();
    expect(screen.getByLabelText("State")).toHaveValue("ready");
    expect(screen.getByLabelText("Created")).toHaveValue("24h");
    expect(screen.getByLabelText("Job source")).toHaveValue("");
    expect(screen.getByRole("option", { name: "Dice" })).toHaveValue("DICE");
    expect(screen.queryByLabelText("Created from")).not.toBeInTheDocument();

    const titleLink = screen.getByRole("link", { name: /platform engineer/i });
    expect(titleLink).toHaveAttribute("href", "https://example.test/jobs/42");
    expect(titleLink).toHaveAttribute("target", "_blank");

    await waitFor(() => {
      const calls = (fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls
        .map(([input]) => String(input));
      expect(calls.some((url) =>
        url.includes("state=ready") && url.includes("last_hours=24"),
      )).toBe(true);
    });

    await user.click(screen.getByRole("button", { name: "Applied" }));
    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "/api/v0/jobs/42/review",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({ action: "applied" }),
        }),
      );
    });

    await user.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() => {
      const calls = (fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls
        .map(([input]) => String(input));
      expect(calls.some((url) =>
        url.includes("offset=50") &&
        url.includes("as_of=2026-09-18T21%3A00%3A00Z"),
      )).toBe(true);
    });

    await user.selectOptions(screen.getByLabelText("State"), "rejected");
    await user.type(screen.getByLabelText("Search jobs"), "platform");
    await waitFor(() => {
      const calls = (fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls
        .map(([input]) => String(input));
      expect(calls.some((url) =>
        url.includes("state=rejected") && url.includes("q=platform"),
      )).toBe(true);
    });

    await user.selectOptions(screen.getByLabelText("Job source"), "DICE");
    await waitFor(() => {
      const calls = (fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls
        .map(([input]) => String(input));
      expect(calls.some((url) => url.includes("job_source=DICE"))).toBe(true);
    });

    await user.selectOptions(screen.getByLabelText("Created"), "custom");
    expect(screen.getByLabelText("Created from")).toBeInTheDocument();
    expect(screen.getByLabelText("Created through")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Created from"), "2026-09-01");
    await waitFor(() => {
      const calls = (fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls
        .map(([input]) => String(input));
      expect(calls.some((url) =>
        url.includes("date_from=2026-09-01") && !url.includes("last_hours="),
      )).toBe(true);
    });
  });

  it.each([
    ["applied", "Undo"],
    ["rejected", "Review"],
    ["dismissed", "Undo"],
  ])("sends a %s job back to review with the %s action", async (state, label) => {
    const user = userEvent.setup();
    renderPath("/jobs", { jobs: jobsPageWithState(state) });

    await user.click(await screen.findByRole("button", { name: label }));
    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "/api/v0/jobs/42/review",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({ action: "ready" }),
        }),
      );
    });
  });

  it("polls jobs slower than dashboard and pauses while hidden", async () => {
    vi.useFakeTimers();
    let hidden = false;
    Object.defineProperty(document, "hidden", {
      configurable: true,
      get: () => hidden,
    });

    renderPath("/jobs");
    const jobCalls = () =>
      ((fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls).filter(
        ([input]) => String(input).startsWith("/api/v0/jobs?"),
      ).length;

    await vi.advanceTimersByTimeAsync(0);
    const first = jobCalls();
    expect(first).toBeGreaterThan(0);
    await vi.advanceTimersByTimeAsync(2_100);
    expect(jobCalls()).toBe(first);
    await vi.advanceTimersByTimeAsync(3_100);
    expect(jobCalls()).toBeGreaterThan(first);

    hidden = true;
    document.dispatchEvent(new Event("visibilitychange"));
    const pausedAt = jobCalls();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(jobCalls()).toBe(pausedAt);
  });

  it("renders settings as entry editors and tracks dirty save state", async () => {
    const user = userEvent.setup();
    renderPath("/settings");

    expect(await screen.findByRole("heading", { name: "Settings" })).toBeInTheDocument();
    await waitFor(() => {
      const calls = (fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls
        .map(([input]) => String(input));
      expect(calls).toContain("/api/v0/search-settings");
      expect(calls).toContain("/api/v0/notification-settings");
    });
    expect(await screen.findByRole("checkbox", { name: /enable notifications/i })).toBeChecked();
    expect(screen.getByText("Notifications are on. Ready jobs can be sent.")).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: /raw json/i })).not.toBeInTheDocument();

    const save = await screen.findByRole("button", { name: /save filter settings/i });
    expect(save).toBeDisabled();

    await user.type(screen.getByLabelText("Add title include word"), "  Desktop Support  ");
    await user.click(screen.getByRole("button", { name: "Add title include word" }));
    expect(screen.getByRole("button", { name: /remove Desktop Support/i })).toBeInTheDocument();
    expect(save).toBeEnabled();
    const banner = screen.getByRole("alert");
    expect(banner).toHaveTextContent(/unsaved settings/i);
    expect(banner).toHaveTextContent("Filter word lists");
    expect(screen.getByRole("button", { name: "Save Filter word lists" })).toBeInTheDocument();
  });

  it("toggles notification delivery from settings", async () => {
    const user = userEvent.setup();
    renderPath("/settings");

    const toggle = await screen.findByRole("checkbox", { name: /enable notifications/i });
    await user.click(toggle);

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "/api/v0/notification-settings",
        expect.objectContaining({
          method: "PUT",
          body: JSON.stringify({ enabled: false }),
        }),
      );
    });
    expect(await screen.findByText("Notifications are turned off in Settings.")).toBeInTheDocument();
  });

  it("saves an interval notification schedule with a silent period", async () => {
    const user = userEvent.setup();
    renderPath("/settings");

    await user.click(await screen.findByRole("radio", { name: "Every N minutes" }));
    const minutes = screen.getByRole("spinbutton", { name: "Minutes between notifications" });
    await user.clear(minutes);
    await user.type(minutes, "5");
    await user.click(screen.getByRole("button", { name: "Add silent period" }));

    const save = screen.getByRole("button", { name: "Save notification schedule" });
    expect(save).toBeEnabled();
    await user.click(save);

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "/api/v0/notification-settings",
        expect.objectContaining({
          method: "PUT",
          body: JSON.stringify({
            enabled: true,
            schedule: {
              mode: "interval",
              interval_minutes: 5,
              cron_pattern: "39 7,17,20 * * *",
              silent_periods: [{
                days: [0, 1, 2, 3, 4, 5, 6],
                start: "22:00",
                end: "07:00",
              }],
            },
          }),
        }),
      );
      expect(save).toBeDisabled();
    });
  });

  it("clears dirty state from echoed stored row after save", async () => {
    const user = userEvent.setup();
    renderPath("/settings");

    await screen.findByRole("heading", { name: "Settings" });
    const save = await screen.findByRole("button", { name: /save filter settings/i });
    await user.type(screen.getByLabelText("Add title include word"), "  Cloud Ops  ");
    await user.click(screen.getByRole("button", { name: "Add title include word" }));
    await user.type(screen.getByLabelText("Add title include word"), "cloud ops");
    await user.click(screen.getByRole("button", { name: "Add title include word" }));
    expect(save).toBeEnabled();

    await user.click(save);
    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "/api/v0/search-settings",
        expect.objectContaining({ method: "PUT" }),
      );
      expect(save).toBeDisabled();
      expect(screen.getAllByRole("button", { name: /remove Cloud Ops/i })).toHaveLength(1);
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    });
  });

  it("asks for confirmation before reset", async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, "confirm");
    renderPath("/settings");
    await screen.findByRole("heading", { name: "Settings" });
    await screen.findByRole("button", { name: /reset filter settings/i });

    confirm.mockReturnValueOnce(false);
    await user.click(screen.getByRole("button", { name: /reset filter settings/i }));
    await waitFor(() => {
      const calls = (fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls
        .filter(([input, init]) =>
          String(input) === "/api/v0/search-settings/reset" &&
          ((init as RequestInit | undefined)?.method ?? "").toString().toUpperCase() === "POST",
        );
      expect(calls).toHaveLength(0);
    });

    confirm.mockReturnValueOnce(true);
    await user.click(screen.getByRole("button", { name: /reset filter settings/i }));
    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "/api/v0/search-settings/reset",
        expect.objectContaining({ method: "POST" }),
      );
    });
  });

  it("renders provider editors and locks Apify providers without a token", async () => {
    const user = userEvent.setup();
    renderPath("/settings");

    expect(await screen.findByRole("heading", { name: "LINKEDIN provider" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "INDEED provider" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "DICE provider" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "LINKEDIN provider" })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(screen.queryByRole("checkbox", { name: "Enable INDEED" })).not.toBeInTheDocument();
    expect(screen.queryByText("Not implemented")).not.toBeInTheDocument();
    expect(screen.getAllByText(/setup required/i)).toHaveLength(2);
    expect(screen.getByRole("button", { name: "Save LINKEDIN settings" })).toBeDisabled();

    await expandProvider(user, "LINKEDIN");
    await expandProvider(user, "INDEED");
    await expandProvider(user, "DICE");
    expect(screen.getByRole("checkbox", { name: "Enable INDEED" })).toBeDisabled();
    expect(screen.getByRole("checkbox", { name: "Enable DICE" })).toBeDisabled();
    expect(screen.getAllByText(/set APIFY_API_TOKEN/).length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText(/^Last scraped: (?!never)/)).toBeInTheDocument();
    expect(screen.getAllByText("Next eligible: never")).toHaveLength(3);
  });

  it("edits Indeed queries, locations, run options, and hides globals", async () => {
    const user = userEvent.setup();
    renderPath("/settings");
    await expandProvider(user, "INDEED");

    const save = await screen.findByRole("button", { name: "Save INDEED settings" });
    expect(save).toBeDisabled();
    expect(screen.getByRole("checkbox", { name: "Enable INDEED" })).toBeDisabled();
    expect(screen.getByLabelText("Indeed query 1")).toHaveValue(
      "Desktop or Endpoint or Application Support",
    );
    expect(screen.getByLabelText("Indeed location 1")).toHaveValue("Port Orange, FL");
    expect(screen.getByLabelText("Indeed location 1 radius")).toHaveValue("15");
    expect(screen.getByLabelText("Indeed location 1 remote")).not.toBeChecked();
    expect(screen.getByLabelText("Indeed location 1 hybrid")).not.toBeChecked();
    expect(screen.getByLabelText("Indeed country")).toHaveValue("us");
    expect(screen.getByLabelText("Indeed job type")).toHaveValue("fulltime");
    expect(screen.getByLabelText("Indeed from days")).toHaveValue("1");
    expect(screen.getByLabelText("Indeed max rows")).toHaveValue(100);
    expect(screen.getByLabelText("Indeed enable unique jobs")).toBeChecked();
    expect(screen.getByLabelText("Indeed include similar jobs")).not.toBeChecked();
    expect(screen.queryByLabelText("INDEED global search 1")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add INDEED global search" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Add Indeed query" }));
    await user.type(screen.getByLabelText("Indeed query 2"), "Endpoint");
    await user.click(screen.getByRole("button", { name: "Add Indeed location" }));
    await user.type(screen.getByLabelText("Indeed location 2"), "Daytona Beach, FL");
    await user.click(screen.getByLabelText("Indeed location 2 remote"));
    await user.click(screen.getByLabelText("Indeed location 1 hybrid"));
    await user.selectOptions(screen.getByLabelText("Indeed job type"), "contract");
    await user.selectOptions(screen.getByLabelText("Indeed from days"), "3");
    await user.selectOptions(screen.getByLabelText("Indeed location 1 radius"), "25");
    expect(save).toBeEnabled();

    await user.click(save);
    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "/api/v0/scraper-settings/INDEED",
        expect.objectContaining({ method: "PUT" }),
      );
      expect(save).toBeDisabled();
    });
    const putCall = (fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls.find(
      ([input, init]) =>
        String(input) === "/api/v0/scraper-settings/INDEED" &&
        (init as RequestInit | undefined)?.method === "PUT",
    );
    const payload = JSON.parse(String((putCall?.[1] as RequestInit | undefined)?.body));
    expect(payload.enabled).toBe(true);
    expect(payload.timespan_code).toBe("3");
    expect(payload.global_searches).toEqual([]);
    expect(payload.provider_options).toEqual({
      country: "us",
      jobType: "contract",
      fromDays: "3",
      maxRows: 100,
      enableUniqueJobs: true,
      includeSimilarJobs: false,
    });
    expect(payload.search_queries).toEqual([
      {
        keywords: "Desktop or Endpoint or Application Support",
        location: "Port Orange, FL",
        radius: "25",
        include_remote: false,
        include_hybrid: true,
      },
      {
        keywords: "Desktop or Endpoint or Application Support",
        location: "Daytona Beach, FL",
        radius: "15",
        include_remote: true,
        include_hybrid: false,
      },
      {
        keywords: "Endpoint",
        location: "Port Orange, FL",
        radius: "25",
        include_remote: false,
        include_hybrid: true,
      },
      {
        keywords: "Endpoint",
        location: "Daytona Beach, FL",
        radius: "15",
        include_remote: true,
        include_hybrid: false,
      },
    ]);
    for (const query of payload.search_queries as Array<Record<string, unknown>>) {
      expect(query).not.toHaveProperty("f_WT");
      expect(typeof query.include_remote).toBe("boolean");
      expect(typeof query.include_hybrid).toBe("boolean");
      expect(typeof query.radius).toBe("string");
    }
  });

  it("edits LinkedIn queries, location work types, and global searches", async () => {
    const user = userEvent.setup();
    renderPath("/settings");
    await expandProvider(user, "LINKEDIN");

    const save = await screen.findByRole("button", { name: "Save LINKEDIN settings" });
    expect(save).toBeDisabled();
    expect(screen.getByLabelText("LinkedIn query 1")).toHaveValue("Support");
    expect(screen.getByLabelText("LinkedIn query 2")).toHaveValue("Engineer");
    expect(screen.getByLabelText("LinkedIn location 1")).toHaveValue(101076143);
    expect(screen.getByLabelText("LinkedIn location 1 On-Site")).toBeChecked();
    expect(screen.getByLabelText("LinkedIn location 1 Hybrid")).not.toBeChecked();
    expect(screen.getByLabelText("LinkedIn location 1 Remote")).toBeChecked();
    const linkedInCard = screen.getByRole("heading", { name: "LINKEDIN provider" }).closest("article");
    expect(linkedInCard).toHaveTextContent("1 locations");
    expect(screen.getByLabelText("LINKEDIN global search 1")).toHaveValue(
      "Remote IT Help Desk near Port Orange FL",
    );

    await user.click(screen.getByRole("button", { name: "Add LinkedIn query" }));
    await user.type(screen.getByLabelText("LinkedIn query 3"), "Analyst");
    await user.click(screen.getByRole("button", { name: "Add LinkedIn location" }));
    await user.type(screen.getByLabelText("LinkedIn location 2"), "105135351");
    await user.click(screen.getByLabelText("LinkedIn location 2 On-Site"));
    await user.click(screen.getByLabelText("LinkedIn location 2 Hybrid"));
    await user.click(screen.getByRole("button", { name: "Add LINKEDIN global search" }));
    await user.type(
      screen.getByLabelText("LINKEDIN global search 2"),
      "Hybrid help desk within 10 miles of Daytona Beach",
    );
    expect(save).toBeEnabled();
    expect(linkedInCard).toHaveTextContent("2 locations");
    expect(linkedInCard).toHaveTextContent("2 searches");

    await user.click(save);
    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "/api/v0/scraper-settings/LINKEDIN",
        expect.objectContaining({ method: "PUT" }),
      );
      expect(save).toBeDisabled();
    });
    const putCall = (fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls.find(
      ([input, init]) =>
        String(input) === "/api/v0/scraper-settings/LINKEDIN" &&
        (init as RequestInit | undefined)?.method === "PUT",
    );
    const payload = JSON.parse(String((putCall?.[1] as RequestInit | undefined)?.body));
    expect(payload.search_queries).toEqual([
      { keywords: "Support", location: "101076143", f_WT: "1,2" },
      { keywords: "Support", location: "105135351", f_WT: "3" },
      { keywords: "Engineer", location: "101076143", f_WT: "1,2" },
      { keywords: "Engineer", location: "105135351", f_WT: "3" },
      { keywords: "Analyst", location: "101076143", f_WT: "1,2" },
      { keywords: "Analyst", location: "105135351", f_WT: "3" },
    ]);
    expect(payload.global_searches).toEqual([
      "Remote IT Help Desk near Port Orange FL",
      "Hybrid help desk within 10 miles of Daytona Beach",
    ]);
    expect(payload).not.toHaveProperty("hardcoded_urls");
    expect(save).toBeDisabled();
  });

  it("edits Dice queries, locations, posted date, and hides globals", async () => {
    const user = userEvent.setup();
    renderPath("/settings");
    await expandProvider(user, "DICE");

    const save = await screen.findByRole("button", { name: "Save DICE settings" });
    expect(save).toBeDisabled();
    expect(screen.getByRole("checkbox", { name: "Enable DICE" })).toBeDisabled();
    expect(screen.getByLabelText("Dice query 1")).toHaveValue(
      "Desktop or Endpoint or Application Support",
    );
    expect(screen.getByLabelText("Dice location 1")).toHaveValue("Port Orange, FL");
    expect(screen.getByLabelText("Dice location 1 include remote")).toBeChecked();
    const postedDate = screen.getByLabelText("DICE posted date");
    expect(postedDate.tagName).toBe("SELECT");
    expect(postedDate).toHaveValue("24h");
    expect(screen.getByRole("option", { name: "all" })).toHaveValue("all");
    expect(screen.getByRole("option", { name: "24h" })).toHaveValue("24h");
    expect(screen.getByRole("option", { name: "3d" })).toHaveValue("3d");
    expect(screen.getByRole("option", { name: "7d" })).toHaveValue("7d");
    expect(screen.getByRole("option", { name: "30d" })).toHaveValue("30d");
    expect(screen.queryByLabelText("DICE timespan code")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("DICE global search 1")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add DICE global search" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Add Dice query" }));
    await user.type(screen.getByLabelText("Dice query 2"), "Endpoint");
    await user.click(screen.getByRole("button", { name: "Add Dice location" }));
    await user.type(screen.getByLabelText("Dice location 2"), "Daytona Beach, FL");
    expect(screen.getByLabelText("Dice location 2 include remote")).toBeChecked();
    await user.click(screen.getByLabelText("Dice location 2 include remote"));
    await user.selectOptions(postedDate, "7d");
    expect(save).toBeEnabled();

    await user.click(save);
    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "/api/v0/scraper-settings/DICE",
        expect.objectContaining({ method: "PUT" }),
      );
      expect(save).toBeDisabled();
    });
    const putCall = (fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls.find(
      ([input, init]) =>
        String(input) === "/api/v0/scraper-settings/DICE" &&
        (init as RequestInit | undefined)?.method === "PUT",
    );
    const payload = JSON.parse(String((putCall?.[1] as RequestInit | undefined)?.body));
    expect(payload.enabled).toBe(true);
    expect(payload.timespan_code).toBe("7d");
    expect(payload.global_searches).toEqual([]);
    expect(payload.search_queries).toEqual([
      {
        keywords: "Desktop or Endpoint or Application Support",
        location: "Port Orange, FL",
        include_remote: true,
      },
      {
        keywords: "Desktop or Endpoint or Application Support",
        location: "Daytona Beach, FL",
        include_remote: false,
      },
      { keywords: "Endpoint", location: "Port Orange, FL", include_remote: true },
      { keywords: "Endpoint", location: "Daytona Beach, FL", include_remote: false },
    ]);
    for (const query of payload.search_queries as Array<Record<string, unknown>>) {
      expect(query).not.toHaveProperty("f_WT");
      expect(typeof query.include_remote).toBe("boolean");
    }
    expect(screen.getByLabelText("Dice location 1 include remote")).toBeChecked();
    expect(screen.getByLabelText("Dice location 2 include remote")).not.toBeChecked();
  });

  it("collapses duplicate Dice locations to the last remote checkbox", async () => {
    const user = userEvent.setup();
    renderPath("/settings");
    await expandProvider(user, "DICE");

    const save = await screen.findByRole("button", { name: "Save DICE settings" });
    await user.click(screen.getByRole("button", { name: "Add Dice location" }));
    await user.clear(screen.getByLabelText("Dice location 2"));
    await user.type(screen.getByLabelText("Dice location 2"), "Port Orange, FL");
    await user.click(screen.getByLabelText("Dice location 2 include remote"));
    await user.click(save);
    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "/api/v0/scraper-settings/DICE",
        expect.objectContaining({ method: "PUT" }),
      );
    });
    const putCall = (fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls.find(
      ([input, init]) =>
        String(input) === "/api/v0/scraper-settings/DICE" &&
        (init as RequestInit | undefined)?.method === "PUT",
    );
    const payload = JSON.parse(String((putCall?.[1] as RequestInit | undefined)?.body));
    expect(payload.search_queries).toEqual([
      {
        keywords: "Desktop or Endpoint or Application Support",
        location: "Port Orange, FL",
        include_remote: false,
      },
    ]);
  });

  it.each([
    [3, 2, 5],
    [0, 4, 4],
  ])(
    "confirms re-evaluation with %i + %i rejected jobs from aggregate stats",
    async (linkedIn, indeed, total) => {
      const user = userEvent.setup();
      const confirm = vi.spyOn(window, "confirm");
      confirm.mockClear();
      renderPath("/settings", { stats: statsWithRejected(linkedIn, indeed) });
      const reEvaluate = await screen.findByRole("button", {
        name: /re-evaluate rejected jobs/i,
      });
      await waitFor(() => {
        expect(reEvaluate).toHaveTextContent(String(total));
      });

      confirm.mockReturnValueOnce(false);
      await user.click(reEvaluate);
      await waitFor(() => {
        expect(confirm).toHaveBeenCalledTimes(1);
      });
      const message = String(confirm.mock.calls[0][0]);
      expect(message).toMatch(new RegExp(`\\b${total}\\b`));
      expect(message).toMatch(/rejected/i);

      const posts = (fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls
        .filter(([input]) => String(input) === "/api/v0/jobs/re-evaluate");
      expect(posts).toHaveLength(0);
    },
  );

  it("posts an empty re-evaluation request and reports the updated row count", async () => {
    const user = userEvent.setup();
    vi.spyOn(window, "confirm").mockReturnValueOnce(true);
    renderPath("/settings", { stats: statsWithRejected(3, 2), reEvaluatedCount: 7 });

    const reEvaluate = await screen.findByRole("button", { name: /re-evaluate rejected jobs/i });
    await waitFor(() => {
      expect(reEvaluate).toHaveTextContent("5");
    });
    await user.click(reEvaluate);

    const result = await screen.findByRole("status", { name: /re-evaluation status/i });
    expect(result).toHaveTextContent(/\b7\b/);
    expect(result).not.toHaveTextContent(/\b5\b/);
    const call = (fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls.find(
      ([input]) => String(input) === "/api/v0/jobs/re-evaluate",
    );
    expect(call).toBeDefined();
    const init = call?.[1] as RequestInit | undefined;
    expect(String(init?.method).toUpperCase()).toBe("POST");
    expect(init?.body ?? null).toBeNull();
  });

  it("asks for confirmation before resetting one provider", async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, "confirm");
    renderPath("/settings");
    const reset = await screen.findByRole("button", { name: "Reset LINKEDIN settings" });

    confirm.mockReturnValueOnce(false);
    await user.click(reset);
    expect(fetch).not.toHaveBeenCalledWith(
      "/api/v0/scraper-settings/LINKEDIN/reset",
      expect.anything(),
    );

    confirm.mockReturnValueOnce(true);
    await user.click(reset);
    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "/api/v0/scraper-settings/LINKEDIN/reset",
        expect.objectContaining({ method: "POST" }),
      );
    });
  });

  it("copies a settings share code and imports JSON", async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, "confirm");
    renderPath("/settings");

    expect(await screen.findByRole("heading", { name: "Share settings" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /share settings/i })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(screen.queryByLabelText("Share code")).not.toBeInTheDocument();
    const reEvaluate = screen.getByRole("heading", { name: "Re-evaluate rejected jobs" });
    const notifications = screen.getByRole("heading", { name: "Notifications" });
    const providers = screen.getByRole("heading", { name: "Provider settings" });
    const shareHeading = screen.getByRole("heading", { name: "Share settings" });
    expect(reEvaluate.compareDocumentPosition(notifications) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(providers.compareDocumentPosition(shareHeading) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    await expandShare(user);
    const share = await screen.findByLabelText("Share code");
    expect(share).toHaveValue("!JS:1!abcdefghijklmnopqrstuvwxyz012345");
    const json = screen.getByLabelText("JSON") as HTMLTextAreaElement;
    expect(json.value).toContain('"format": "jobscout-settings"');
    expect(json.value).not.toContain("last_scraped_at");

    await user.click(screen.getByRole("button", { name: "Copy share code" }));
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith("!JS:1!abcdefghijklmnopqrstuvwxyz012345");

    confirm.mockReturnValueOnce(true);
    const importBox = screen.getByLabelText("Import");
    fireEvent.change(importBox, {
      target: {
        value: JSON.stringify({
          format: "jobscout-settings",
          version: 1,
          notifications: { enabled: true },
          search: {
            desc_include_words: ["computer"],
            desc_exclude_words: ["travel"],
            title_include: ["Imported Role"],
            title_exclude: ["manager"],
            company_exclude: ["Bad Co"],
          },
          providers: {},
        }),
      },
    });
    await user.click(screen.getByRole("button", { name: "Import settings" }));
    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "/api/v0/settings/import",
        expect.objectContaining({ method: "POST" }),
      );
    });
    expect(await screen.findByText("Settings imported.")).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: /remove Imported Role/i })).toBeInTheDocument();
  });
});
