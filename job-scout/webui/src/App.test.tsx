import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
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
      implemented: false,
      enabled: false,
      scrape_interval_seconds: 900,
      last_scraped_at: null,
      next_eligible_at: null,
      status: "disabled",
      total_jobs: 0,
      by_state: {
        pending: 0,
        rejected: 0,
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
    },
  ],
  daily: [
    { day: "2026-09-17", total: 2, notified: 1 },
    { day: "2026-09-18", total: 0, notified: 0 },
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

const providerSeed: Record<"LINKEDIN" | "INDEED", ProviderSettings> = {
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
      { keywords: "Support", location: "United States" },
    ],
    global_searches: [],
    timespan_code: "r86400",
    pages_to_scrape: 1,
    rounds: 1,
    enabled: false,
    scrape_interval_seconds: 900,
    last_scraped_at: null,
    next_eligible_at: null,
    created_at: "2026-09-18T20:00:00Z",
    updated_at: "2026-09-18T20:00:00Z",
  },
};

type RenderOptions = {
  status?: object;
  stats?: object;
  searchSettings?: typeof filterSeed;
  reEvaluatedCount?: number;
  workflowStatuses?: string[];
};

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
  const currentProviders = structuredClone(providerSeed);
  let workflowStatusIndex = 0;

  window.history.replaceState({}, "", path);
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
      return new Response(JSON.stringify({ workflow_id: "manual-notify-1" }), {
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
      return new Response(JSON.stringify(jobsPage), {
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
    const providerMatch = url.match(/^\/api\/v0\/scraper-settings\/(LINKEDIN|INDEED)(\/reset)?$/);
    if (providerMatch && method === "PUT") {
      const source = providerMatch[1] as keyof typeof currentProviders;
      const payload = JSON.parse(String(init?.body ?? "{}"));
      currentProviders[source] = {
        ...currentProviders[source],
        ...payload,
        timespan_code: String(payload.timespan_code).trim(),
        search_queries: payload.search_queries.map((query: Record<string, string>) => ({
          keywords: query.keywords.trim(),
          location: query.location.trim(),
          f_WT: query.f_WT ?? "",
        })),
        global_searches: (payload.global_searches as string[]).map((item) => item.trim()),
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
    expect(screen.getByText("Status: waiting")).toBeInTheDocument();
    expect(screen.getByText("Status: disabled")).toBeInTheDocument();
    expect(screen.getByText("pending: 1")).toBeInTheDocument();
    expect(screen.getByText("title_company: 1")).toBeInTheDocument();
    expect(screen.getByText("Jobs over time (America/New_York)")).toBeInTheDocument();
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

    await user.click(await screen.findByRole("button", { name: "Send Notification" }));

    expect(fetch).toHaveBeenCalledWith(
      "/api/v0/notify",
      expect.objectContaining({ method: "POST" }),
    );
    expect(await screen.findByRole("link", { name: "manual-notify-1" })).toHaveAttribute(
      "href",
      "http://temporal.example:8233/namespaces/default/workflows/manual-notify-1",
    );
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
    const notify = screen.getByRole("button", { name: "Send Notification" });
    await waitFor(() => {
      expect(scrape).toBeDisabled();
      expect(notify).toBeDisabled();
    });
  });

  it("renders the ready review queue and submits final review actions", async () => {
    const user = userEvent.setup();
    renderPath("/jobs");

    expect(await screen.findByText("Platform Engineer")).toBeInTheDocument();
    expect(screen.getByText("Ready for review", { selector: "div" })).toBeInTheDocument();
    expect(screen.getByText("51 jobs")).toBeInTheDocument();
    expect(screen.getByText("A short job description preview.")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Job detail" })).not.toBeInTheDocument();
    expect(screen.queryByText(/posting date/i)).not.toBeInTheDocument();
    expect(screen.getByLabelText("State")).toHaveValue("ready");
    expect(screen.getByLabelText("Created")).toHaveValue("24h");
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
    });
    expect(screen.queryByRole("textbox", { name: /raw json/i })).not.toBeInTheDocument();

    const save = await screen.findByRole("button", { name: /save filter settings/i });
    expect(save).toBeDisabled();

    await user.type(screen.getByLabelText("Add title include word"), "  Desktop Support  ");
    await user.click(screen.getByRole("button", { name: "Add title include word" }));
    expect(screen.getByRole("button", { name: /remove Desktop Support/i })).toBeInTheDocument();
    expect(save).toBeEnabled();
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

  it("renders provider editors and locks a provider without a scraper", async () => {
    renderPath("/settings");

    expect(await screen.findByRole("heading", { name: "LINKEDIN provider" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "INDEED provider" })).toBeInTheDocument();
    expect(screen.getByText("Not implemented")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "Enable INDEED" })).toBeDisabled();
    expect(screen.getByText(/^Last scraped: (?!never)/)).toBeInTheDocument();
    expect(screen.getAllByText("Next eligible: never")).toHaveLength(2);
    expect(screen.getByRole("button", { name: "Save LINKEDIN settings" })).toBeDisabled();
  });

  it("edits LinkedIn queries, location work types, and global searches", async () => {
    const user = userEvent.setup();
    renderPath("/settings");

    const save = await screen.findByRole("button", { name: "Save LINKEDIN settings" });
    expect(save).toBeDisabled();
    expect(screen.getByLabelText("LinkedIn query 1")).toHaveValue("Support");
    expect(screen.getByLabelText("LinkedIn query 2")).toHaveValue("Engineer");
    expect(screen.getByLabelText("LinkedIn location 1")).toHaveValue(101076143);
    expect(screen.getByLabelText("LinkedIn location 1 On-Site")).toBeChecked();
    expect(screen.getByLabelText("LinkedIn location 1 Hybrid")).not.toBeChecked();
    expect(screen.getByLabelText("LinkedIn location 1 Remote")).toBeChecked();
    expect(screen.getByText("1 locations")).toBeInTheDocument();
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
    expect(screen.getByText("2 locations")).toBeInTheDocument();
    expect(screen.getByText("2 searches")).toBeInTheDocument();

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

  it.each([
    [3, 2, 5],
    [0, 4, 4],
  ])(
    "confirms re-evaluation with %i + %i rejected jobs from aggregate stats",
    async (linkedIn, indeed, total) => {
      const user = userEvent.setup();
      const confirm = vi.spyOn(window, "confirm");
      confirm.mockClear();
      renderPath("/dashboard", { stats: statsWithRejected(linkedIn, indeed) });
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
    renderPath("/jobs", { stats: statsWithRejected(3, 2), reEvaluatedCount: 7 });

    const reEvaluate = await screen.findByRole("button", { name: /re-evaluate rejected jobs/i });
    await waitFor(() => {
      expect(reEvaluate).toHaveTextContent("5");
    });
    await user.click(reEvaluate);

    const result = await screen.findByRole("status");
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
});
