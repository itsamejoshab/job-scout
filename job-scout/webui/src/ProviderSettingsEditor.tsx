import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import {
  getDashboardStats,
  getProviderSettings,
  replaceProviderSettings,
  resetProviderSettings,
  type ProviderSettings,
  type ProviderSettingsInput,
} from "./api";
import { Badge } from "./components/ui/badge";

const linkedInWorkTypes = [
  { code: "1", label: "On-Site" },
  { code: "3", label: "Hybrid" },
  { code: "2", label: "Remote" },
] as const;

type LinkedInWorkType = (typeof linkedInWorkTypes)[number]["code"];

interface LinkedInLocation {
  location: string;
  workTypes: Record<LinkedInWorkType, boolean>;
}

interface LinkedInMatrix {
  queries: string[];
  locations: LinkedInLocation[];
}

interface ProviderDraft extends ProviderSettingsInput {
  linkedInMatrix?: LinkedInMatrix;
}

function toLinkedInMatrix(searchQueries: ProviderSettings["search_queries"]): LinkedInMatrix {
  const queries: string[] = [];
  const locations = new Map<string, LinkedInLocation>();

  for (const query of searchQueries) {
    if (!queries.includes(query.keywords)) {
      queries.push(query.keywords);
    }
    let location = locations.get(query.location);
    if (!location) {
      location = {
        location: query.location,
        workTypes: { "1": false, "2": false, "3": false },
      };
      locations.set(query.location, location);
    }
    if (query.f_WT === "1" || query.f_WT === "2" || query.f_WT === "3") {
      location.workTypes[query.f_WT] = true;
    } else if (!query.f_WT) {
      // No LinkedIn work-type filter includes all three modes.
      location.workTypes = { "1": true, "2": true, "3": true };
    }
  }

  return { queries, locations: [...locations.values()] };
}

function fromLinkedInMatrix(matrix: LinkedInMatrix): ProviderSettings["search_queries"] {
  return matrix.queries.flatMap((keywords) =>
    matrix.locations.flatMap((location) =>
      linkedInWorkTypes
        .filter(({ code }) => location.workTypes[code])
        .map(({ code }) => ({ keywords, location: location.location, f_WT: code })),
    ),
  );
}

function toInput(settings: ProviderSettings): ProviderDraft {
  return {
    enabled: settings.enabled,
    scrape_interval_seconds: settings.scrape_interval_seconds,
    timespan_code: settings.timespan_code,
    pages_to_scrape: settings.pages_to_scrape,
    rounds: settings.rounds,
    search_queries: settings.search_queries.map((query) => ({
      keywords: query.keywords,
      location: query.location,
      f_WT: query.f_WT ?? "",
    })),
    hardcoded_urls: settings.hardcoded_urls.map((entry) => ({ ...entry })),
    linkedInMatrix: settings.job_source === "LINKEDIN"
      ? toLinkedInMatrix(settings.search_queries)
      : undefined,
  };
}

function cloneInput(settings: ProviderDraft): ProviderSettingsInput {
  return {
    enabled: settings.enabled,
    scrape_interval_seconds: settings.scrape_interval_seconds,
    timespan_code: settings.timespan_code,
    pages_to_scrape: settings.pages_to_scrape,
    rounds: settings.rounds,
    search_queries: settings.search_queries.map((query) => ({ ...query })),
    hardcoded_urls: settings.hardcoded_urls.map((entry) => ({ ...entry })),
  };
}

function inputsEqual(left: ProviderDraft, right: ProviderDraft) {
  return JSON.stringify(left) === JSON.stringify(right);
}

function LinkedInSearchEditor({
  matrix,
  onChange,
}: {
  matrix: LinkedInMatrix;
  onChange: (matrix: LinkedInMatrix) => void;
}) {
  return (
    <div className="mt-5 grid gap-6 lg:grid-cols-2">
      <fieldset>
        <legend className="text-base font-semibold">Queries</legend>
        <div className="mt-2 space-y-2">
          {matrix.queries.map((query, index) => (
            <div className="flex gap-2" key={index}>
              <input
                aria-label={`LinkedIn query ${index + 1}`}
                className="min-w-0 flex-1 rounded-md border px-2 py-1"
                value={query}
                onChange={(event) => {
                  const queries = matrix.queries.map((item, itemIndex) =>
                    itemIndex === index ? event.target.value : item
                  );
                  onChange({ ...matrix, queries });
                }}
              />
              <button
                type="button"
                className="rounded-md border px-2 py-1 text-sm"
                aria-label={`Remove LinkedIn query ${index + 1}`}
                onClick={() => onChange({
                  ...matrix,
                  queries: matrix.queries.filter((_, itemIndex) => itemIndex !== index),
                })}
              >
                Remove
              </button>
            </div>
          ))}
        </div>
        <button
          type="button"
          className="mt-2 rounded-md border px-3 py-2 text-sm"
          onClick={() => onChange({ ...matrix, queries: [...matrix.queries, ""] })}
        >
          Add LinkedIn query
        </button>
      </fieldset>

      <fieldset>
        <legend className="text-base font-semibold">Locations and work types</legend>
        <div className="mt-2 space-y-3">
          {matrix.locations.map((location, index) => (
            <div className="rounded-md border p-3" key={index}>
              <div className="flex gap-2">
                <input
                  type="number"
                  min={1}
                  step={1}
                  aria-label={`LinkedIn location ${index + 1}`}
                  className="min-w-0 flex-1 rounded-md border px-2 py-1"
                  value={location.location}
                  onChange={(event) => {
                    const locations = matrix.locations.map((item, itemIndex) =>
                      itemIndex === index ? { ...item, location: event.target.value } : item
                    );
                    onChange({ ...matrix, locations });
                  }}
                />
                <button
                  type="button"
                  className="rounded-md border px-2 py-1 text-sm"
                  aria-label={`Remove LinkedIn location ${index + 1}`}
                  onClick={() => onChange({
                    ...matrix,
                    locations: matrix.locations.filter((_, itemIndex) => itemIndex !== index),
                  })}
                >
                  Remove
                </button>
              </div>
              <div className="mt-2 flex flex-wrap gap-4">
                {linkedInWorkTypes.map(({ code, label }) => (
                  <label className="flex items-center gap-2 text-sm" key={code}>
                    <input
                      type="checkbox"
                      aria-label={`LinkedIn location ${index + 1} ${label}`}
                      checked={location.workTypes[code]}
                      onChange={(event) => {
                        const locations = matrix.locations.map((item, itemIndex) =>
                          itemIndex === index
                            ? {
                                ...item,
                                workTypes: {
                                  ...item.workTypes,
                                  [code]: event.target.checked,
                                },
                              }
                            : item
                        );
                        onChange({ ...matrix, locations });
                      }}
                    />
                    {label}
                  </label>
                ))}
              </div>
            </div>
          ))}
        </div>
        <button
          type="button"
          className="mt-2 rounded-md border px-3 py-2 text-sm"
          onClick={() => onChange({
            ...matrix,
            locations: [
              ...matrix.locations,
              {
                location: "",
                workTypes: { "1": true, "2": false, "3": false },
              },
            ],
          })}
        >
          Add LinkedIn location
        </button>
      </fieldset>
    </div>
  );
}

function ProviderSection({
  settings,
  implemented,
}: {
  settings: ProviderSettings;
  implemented: boolean;
}) {
  const queryClient = useQueryClient();
  const initial = toInput(settings);
  const [base, setBase] = useState(initial);
  const [draft, setDraft] = useState(initial);
  const dirty = !inputsEqual(base, draft);
  const source = settings.job_source;

  const acceptStored = (stored: ProviderSettings) => {
    const next = toInput(stored);
    setBase(next);
    setDraft(next);
    queryClient.setQueryData<Record<string, ProviderSettings>>(
      ["provider-settings"],
      (current) => ({ ...current, [source]: stored }),
    );
  };

  const save = useMutation({
    mutationFn: (next: ProviderSettingsInput) => replaceProviderSettings(source, next),
    onSuccess: acceptStored,
  });
  const reset = useMutation({
    mutationFn: () => resetProviderSettings(source),
    onSuccess: acceptStored,
  });
  const pending = save.isPending || reset.isPending;

  const patch = (next: Partial<ProviderSettingsInput>) => {
    setDraft((current) => ({ ...current, ...next }));
  };
  const patchLinkedIn = (matrix: LinkedInMatrix) => {
    setDraft((current) => ({
      ...current,
      linkedInMatrix: matrix,
      search_queries: fromLinkedInMatrix(matrix),
    }));
  };

  return (
    <article className="mt-4 rounded-md border p-4">
      <div className="flex items-center gap-3">
        <h3 className="text-lg font-semibold">{source} provider</h3>
        {!implemented && <Badge variant="secondary">Not implemented</Badge>}
      </div>

      <div className="mt-4 grid gap-3 md:grid-cols-3">
        <label className="text-sm font-medium">
          <span className="flex items-center gap-2">
            <input
              type="checkbox"
              aria-label={`Enable ${source}`}
              checked={implemented && draft.enabled}
              disabled={!implemented}
              onChange={(event) => patch({ enabled: event.target.checked })}
            />
            Enabled
          </span>
        </label>
        <label className="text-sm font-medium">
          Scrape interval seconds
          <input
            type="number"
            min={60}
            className="mt-1 w-full rounded-md border bg-background px-3 py-2 font-normal"
            value={draft.scrape_interval_seconds}
            onChange={(event) => patch({ scrape_interval_seconds: Number(event.target.value) })}
          />
        </label>
        <label className="text-sm font-medium">
          Timespan code
          <input
            className="mt-1 w-full rounded-md border bg-background px-3 py-2 font-normal"
            value={draft.timespan_code}
            onChange={(event) => patch({ timespan_code: event.target.value })}
          />
        </label>
        <label className="text-sm font-medium">
          Pages to scrape
          <input
            type="number"
            min={1}
            className="mt-1 w-full rounded-md border bg-background px-3 py-2 font-normal"
            value={draft.pages_to_scrape}
            onChange={(event) => patch({ pages_to_scrape: Number(event.target.value) })}
          />
        </label>
        <label className="text-sm font-medium">
          Rounds
          <input
            type="number"
            min={1}
            max={3}
            className="mt-1 w-full rounded-md border bg-background px-3 py-2 font-normal"
            value={draft.rounds}
            onChange={(event) => patch({ rounds: Number(event.target.value) })}
          />
        </label>
        <div className="text-sm">
          <p>Last scraped: {formatTimestamp(settings.last_scraped_at)}</p>
          <p>Next eligible: {formatTimestamp(settings.next_eligible_at)}</p>
          <p>Identifier: {settings.id}</p>
          <p>Updated: {formatTimestamp(settings.updated_at)}</p>
        </div>
      </div>

      {source === "LINKEDIN" && draft.linkedInMatrix ? (
        <LinkedInSearchEditor matrix={draft.linkedInMatrix} onChange={patchLinkedIn} />
      ) : (
      <fieldset className="mt-5 overflow-x-auto">
        <legend className="text-base font-semibold">Search queries</legend>
        <table className="mt-2 w-full text-left text-sm">
          <thead>
            <tr>
              <th className="p-2">Keywords</th>
              <th className="p-2">Location</th>
              <th className="p-2">Remote-work parameter</th>
              <th className="p-2">Action</th>
            </tr>
          </thead>
          <tbody>
            {draft.search_queries.map((query, index) => (
              <tr key={index}>
                <td className="p-2">
                  <input
                    aria-label={`${source} query keywords`}
                    className="w-full rounded-md border px-2 py-1"
                    value={query.keywords}
                    onChange={(event) => {
                      const search_queries = draft.search_queries.map((item, itemIndex) =>
                        itemIndex === index ? { ...item, keywords: event.target.value } : item
                      );
                      patch({ search_queries });
                    }}
                  />
                </td>
                <td className="p-2">
                  <input
                    aria-label={`${source} query location`}
                    className="w-full rounded-md border px-2 py-1"
                    value={query.location}
                    onChange={(event) => {
                      const search_queries = draft.search_queries.map((item, itemIndex) =>
                        itemIndex === index ? { ...item, location: event.target.value } : item
                      );
                      patch({ search_queries });
                    }}
                  />
                </td>
                <td className="p-2">
                  <input
                    aria-label={`${source} query remote-work parameter`}
                    className="w-full rounded-md border px-2 py-1"
                    value={query.f_WT ?? ""}
                    onChange={(event) => {
                      const search_queries = draft.search_queries.map((item, itemIndex) =>
                        itemIndex === index ? { ...item, f_WT: event.target.value } : item
                      );
                      patch({ search_queries });
                    }}
                  />
                </td>
                <td className="p-2">
                  <button
                    type="button"
                    className="rounded-md border px-2 py-1"
                    aria-label={`Remove ${source} query ${index + 1}`}
                    onClick={() => patch({
                      search_queries: draft.search_queries.filter((_, itemIndex) => itemIndex !== index),
                    })}
                  >
                    Remove
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <button
          type="button"
          className="mt-2 rounded-md border px-3 py-2 text-sm"
          onClick={() => patch({
            search_queries: [
              ...draft.search_queries,
              { keywords: "", location: "", f_WT: "" },
            ],
          })}
        >
          Add {source} query
        </button>
      </fieldset>
      )}

      <fieldset className="mt-5 overflow-x-auto">
        <legend className="text-base font-semibold">Hardcoded URLs</legend>
        {draft.hardcoded_urls.length === 0 ? (
          <p className="mt-2 text-sm text-muted-foreground">No hardcoded URLs.</p>
        ) : (
          <table className="mt-2 w-full text-left text-sm">
            <thead>
              <tr>
                <th className="p-2">URL</th>
                <th className="p-2">Description</th>
                <th className="p-2">Remote</th>
                <th className="p-2">Action</th>
              </tr>
            </thead>
            <tbody>
              {draft.hardcoded_urls.map((entry, index) => (
                <tr key={index}>
                  <td className="p-2">
                    <input
                      aria-label={`${source} hardcoded URL`}
                      className="w-full rounded-md border px-2 py-1"
                      value={entry.url}
                      onChange={(event) => {
                        const hardcoded_urls = draft.hardcoded_urls.map((item, itemIndex) =>
                          itemIndex === index ? { ...item, url: event.target.value } : item
                        );
                        patch({ hardcoded_urls });
                      }}
                    />
                  </td>
                  <td className="p-2">
                    <input
                      aria-label={`${source} URL description`}
                      className="w-full rounded-md border px-2 py-1"
                      value={entry.description}
                      onChange={(event) => {
                        const hardcoded_urls = draft.hardcoded_urls.map((item, itemIndex) =>
                          itemIndex === index ? { ...item, description: event.target.value } : item
                        );
                        patch({ hardcoded_urls });
                      }}
                    />
                  </td>
                  <td className="p-2">
                    <input
                      type="checkbox"
                      aria-label={`${source} URL remote`}
                      checked={entry.is_remote}
                      onChange={(event) => {
                        const hardcoded_urls = draft.hardcoded_urls.map((item, itemIndex) =>
                          itemIndex === index ? { ...item, is_remote: event.target.checked } : item
                        );
                        patch({ hardcoded_urls });
                      }}
                    />
                  </td>
                  <td className="p-2">
                    <button
                      type="button"
                      className="rounded-md border px-2 py-1"
                      aria-label={`Remove ${source} URL ${index + 1}`}
                      onClick={() => patch({
                        hardcoded_urls: draft.hardcoded_urls.filter((_, itemIndex) => itemIndex !== index),
                      })}
                    >
                      Remove
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <button
          type="button"
          className="mt-2 rounded-md border px-3 py-2 text-sm"
          onClick={() => patch({
            hardcoded_urls: [
              ...draft.hardcoded_urls,
              { url: "", description: "", is_remote: false },
            ],
          })}
        >
          Add {source} URL
        </button>
      </fieldset>

      {(save.isError || reset.isError) && (
        <p className="mt-3 text-sm text-destructive">
          {(save.error ?? reset.error)?.message}
        </p>
      )}
      <div className="mt-4 flex gap-2">
        <button
          type="button"
          className="rounded-md border px-3 py-2 text-sm disabled:opacity-50"
          disabled={!dirty || pending}
          onClick={() => save.mutate(cloneInput(draft))}
        >
          Save {source} settings
        </button>
        <button
          type="button"
          className="rounded-md border px-3 py-2 text-sm disabled:opacity-50"
          disabled={pending}
          onClick={() => {
            if (window.confirm(`Reset ${source} settings to seed defaults?`)) {
              reset.mutate();
            }
          }}
        >
          Reset {source} settings
        </button>
      </div>
    </article>
  );
}

function formatTimestamp(value: string | null) {
  if (!value) {
    return "never";
  }
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString();
}

export function ProviderSettingsEditor() {
  const providers = useQuery({
    queryKey: ["provider-settings"],
    queryFn: getProviderSettings,
  });
  const dashboard = useQuery({
    queryKey: ["dashboard-stats"],
    queryFn: getDashboardStats,
  });
  const implemented = new Map(
    dashboard.data?.providers.map((provider) => [provider.job_source, provider.implemented]) ?? [],
  );

  if (providers.isPending) {
    return <p className="mt-2 text-sm text-muted-foreground">Loading provider settings.</p>;
  }
  if (providers.isError || !providers.data) {
    return <p className="mt-2 text-sm text-destructive">Provider settings are unavailable.</p>;
  }

  return (
    <>
      {Object.values(providers.data)
        .sort((left, right) => left.job_source.localeCompare(right.job_source))
        .map((settings) => (
          <ProviderSection
            key={settings.job_source}
            settings={settings}
            implemented={implemented.get(settings.job_source) ?? false}
          />
        ))}
    </>
  );
}
