import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, RotateCcw, Save, Trash2 } from "lucide-react";
import { useId, useState, type ReactNode } from "react";

import {
  getDashboardStats,
  getProviderSettings,
  replaceProviderSettings,
  resetProviderSettings,
  type IndeedProviderOptions,
  type ProviderSettings,
  type ProviderSettingsInput,
} from "./api";
import { CollapseToggle } from "./CollapseToggle";
import { Badge } from "./components/ui/badge";
import { Button } from "./components/ui/button";
import { cn } from "./lib/utils";
import { useDirtySection } from "./settingsDirty";

const linkedInWorkTypes = [
  { code: "1", label: "On-Site" },
  { code: "3", label: "Hybrid" },
  { code: "2", label: "Remote" },
] as const;

const indeedJobTypes = [
  "fulltime",
  "parttime",
  "contract",
  "internship",
  "temporary",
  "permanent",
  "seasonal",
  "freelance",
] as const;

const indeedFromDays = ["1", "3", "7", "14"] as const;
const indeedRadii = ["0", "5", "10", "15", "25", "35", "50", "100"] as const;

type LinkedInWorkType = (typeof linkedInWorkTypes)[number]["code"];

interface LinkedInLocation {
  location: string;
  workTypes: Record<LinkedInWorkType, boolean>;
}

interface LinkedInMatrix {
  queries: string[];
  locations: LinkedInLocation[];
}

interface DiceLocation {
  location: string;
  includeRemote: boolean;
}

interface DiceMatrix {
  queries: string[];
  locations: DiceLocation[];
}

interface IndeedLocation {
  location: string;
  radius: string;
  includeRemote: boolean;
  includeHybrid: boolean;
}

interface IndeedMatrix {
  queries: string[];
  locations: IndeedLocation[];
}

interface ProviderDraft extends ProviderSettingsInput {
  linkedInMatrix?: LinkedInMatrix;
  diceMatrix?: DiceMatrix;
  indeedMatrix?: IndeedMatrix;
}

const defaultIndeedRadius = "15";

function defaultIndeedOptions(): IndeedProviderOptions {
  return {
    country: "us",
    jobType: "fulltime",
    fromDays: "1",
    maxRows: 100,
    enableUniqueJobs: true,
    includeSimilarJobs: false,
  };
}

function toIndeedOptions(raw: ProviderSettings["provider_options"]): IndeedProviderOptions {
  const defaults = defaultIndeedOptions();
  if (!raw || typeof raw !== "object") {
    return defaults;
  }
  const options = raw as Partial<IndeedProviderOptions>;
  return {
    country: typeof options.country === "string" && options.country ? options.country : defaults.country,
    jobType: typeof options.jobType === "string" && options.jobType ? options.jobType : defaults.jobType,
    fromDays: typeof options.fromDays === "string" && options.fromDays ? options.fromDays : defaults.fromDays,
    maxRows: typeof options.maxRows === "number" && options.maxRows > 0 ? options.maxRows : defaults.maxRows,
    enableUniqueJobs: typeof options.enableUniqueJobs === "boolean"
      ? options.enableUniqueJobs
      : defaults.enableUniqueJobs,
    includeSimilarJobs: typeof options.includeSimilarJobs === "boolean"
      ? options.includeSimilarJobs
      : defaults.includeSimilarJobs,
  };
}

function legacyIndeedRadius(raw: ProviderSettings["provider_options"]): string {
  if (!raw || typeof raw !== "object") {
    return defaultIndeedRadius;
  }
  const radius = (raw as { radius?: unknown }).radius;
  return typeof radius === "string" && radius ? radius : defaultIndeedRadius;
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
    const codes = (query.f_WT ?? "")
      .split(/[,\s|]+/)
      .map((code) => code.trim())
      .filter(Boolean);
    if (codes.length === 0) {
      // No work-type selection means all three modes.
      location.workTypes = { "1": true, "2": true, "3": true };
      continue;
    }
    for (const code of codes) {
      if (code === "1" || code === "2" || code === "3") {
        location.workTypes[code] = true;
      }
    }
  }

  return { queries, locations: [...locations.values()] };
}

function fromLinkedInMatrix(matrix: LinkedInMatrix): ProviderSettings["search_queries"] {
  return matrix.queries.flatMap((keywords) =>
    matrix.locations.flatMap((location) => {
      const selected = linkedInWorkTypes
        .filter(({ code }) => location.workTypes[code])
        .map(({ code }) => code);
      if (selected.length === 0) {
        return [];
      }
      const f_WT = selected.length === linkedInWorkTypes.length ? "" : selected.join(",");
      return [{ keywords, location: location.location, f_WT }];
    }),
  );
}

function remoteFlag(value: ProviderSettings["search_queries"][number]["include_remote"]) {
  return value === true || value === "true";
}

function toDiceMatrix(searchQueries: ProviderSettings["search_queries"]): DiceMatrix {
  const queries: string[] = [];
  const locations: DiceLocation[] = [];
  const seen = new Map<string, number>();

  for (const query of searchQueries) {
    if (!queries.includes(query.keywords)) {
      queries.push(query.keywords);
    }
    const includeRemote = remoteFlag(query.include_remote);
    const key = `${query.location}\0${includeRemote ? "1" : "0"}`;
    const existing = seen.get(key);
    if (existing === undefined) {
      seen.set(key, locations.length);
      locations.push({ location: query.location, includeRemote });
      continue;
    }
    locations[existing] = { location: query.location, includeRemote };
  }

  return { queries, locations };
}

function fromDiceMatrix(matrix: DiceMatrix): ProviderSettingsInput["search_queries"] {
  const locations: DiceLocation[] = [];
  const seen = new Map<string, number>();
  for (const location of matrix.locations) {
    const city = location.location.trim();
    if (city === "") {
      locations.push(location);
      continue;
    }
    const key = `${city}\0${location.includeRemote ? "1" : "0"}`;
    const existing = seen.get(key);
    if (existing === undefined) {
      seen.set(key, locations.length);
      locations.push({ location: location.location, includeRemote: location.includeRemote });
      continue;
    }
    locations[existing] = { location: location.location, includeRemote: location.includeRemote };
  }
  return matrix.queries.flatMap((keywords) =>
    locations.map((location) => ({
      keywords,
      location: location.location,
      include_remote: location.includeRemote,
    })),
  );
}

function flagValue(value: ProviderSettings["search_queries"][number]["include_remote"]) {
  return value === true || value === "true";
}

function indeedLocationKey(location: string, includeRemote: boolean, includeHybrid: boolean) {
  return `${location}\0${includeRemote ? "1" : "0"}\0${includeHybrid ? "1" : "0"}`;
}

function toIndeedMatrix(
  searchQueries: ProviderSettings["search_queries"],
  fallbackRadius = defaultIndeedRadius,
): IndeedMatrix {
  const queries: string[] = [];
  const locations: IndeedLocation[] = [];
  const seen = new Map<string, number>();

  for (const query of searchQueries) {
    if (!queries.includes(query.keywords)) {
      queries.push(query.keywords);
    }
    const includeRemote = flagValue(query.include_remote);
    const includeHybrid = flagValue(query.include_hybrid);
    const radius = typeof query.radius === "string" && query.radius
      ? query.radius
      : fallbackRadius;
    const key = indeedLocationKey(query.location, includeRemote, includeHybrid);
    const existing = seen.get(key);
    if (existing === undefined) {
      seen.set(key, locations.length);
      locations.push({ location: query.location, radius, includeRemote, includeHybrid });
      continue;
    }
    locations[existing] = { location: query.location, radius, includeRemote, includeHybrid };
  }

  return { queries, locations };
}

function fromIndeedMatrix(matrix: IndeedMatrix): ProviderSettingsInput["search_queries"] {
  const locations: IndeedLocation[] = [];
  const seen = new Map<string, number>();
  for (const location of matrix.locations) {
    const city = location.location.trim();
    if (city === "") {
      locations.push(location);
      continue;
    }
    const key = indeedLocationKey(city, location.includeRemote, location.includeHybrid);
    const existing = seen.get(key);
    if (existing === undefined) {
      seen.set(key, locations.length);
      locations.push({
        location: location.location,
        radius: location.radius,
        includeRemote: location.includeRemote,
        includeHybrid: location.includeHybrid,
      });
      continue;
    }
    locations[existing] = {
      location: location.location,
      radius: location.radius,
      includeRemote: location.includeRemote,
      includeHybrid: location.includeHybrid,
    };
  }
  return matrix.queries.flatMap((keywords) =>
    locations.map((location) => ({
      keywords,
      location: location.location,
      radius: location.radius,
      include_remote: location.includeRemote,
      include_hybrid: location.includeHybrid,
    })),
  );
}

function toInput(settings: ProviderSettings): ProviderDraft {
  const diceMatrix = settings.job_source === "DICE"
    ? toDiceMatrix(settings.search_queries)
    : undefined;
  const indeedMatrix = settings.job_source === "INDEED"
    ? toIndeedMatrix(settings.search_queries, legacyIndeedRadius(settings.provider_options))
    : undefined;
  const indeedOptions = settings.job_source === "INDEED"
    ? toIndeedOptions(settings.provider_options)
    : undefined;
  return {
    enabled: settings.enabled,
    scrape_interval_seconds: settings.scrape_interval_seconds,
    timespan_code: settings.job_source === "INDEED" && indeedOptions
      ? indeedOptions.fromDays
      : settings.timespan_code,
    pages_to_scrape: settings.pages_to_scrape,
    rounds: settings.rounds,
    search_queries: settings.job_source === "DICE" && diceMatrix
      ? fromDiceMatrix(diceMatrix)
      : settings.job_source === "INDEED" && indeedMatrix
        ? fromIndeedMatrix(indeedMatrix)
        : settings.search_queries.map((query) => ({
            keywords: query.keywords,
            location: query.location,
            f_WT: query.f_WT ?? "",
          })),
    global_searches: settings.job_source === "DICE" || settings.job_source === "INDEED"
      ? []
      : [...settings.global_searches],
    provider_options: indeedOptions,
    linkedInMatrix: settings.job_source === "LINKEDIN"
      ? toLinkedInMatrix(settings.search_queries)
      : undefined,
    diceMatrix,
    indeedMatrix,
  };
}

function cloneInput(settings: ProviderDraft): ProviderSettingsInput {
  const body: ProviderSettingsInput = {
    enabled: settings.enabled,
    scrape_interval_seconds: settings.scrape_interval_seconds,
    timespan_code: settings.timespan_code,
    pages_to_scrape: settings.pages_to_scrape,
    rounds: settings.rounds,
    search_queries: settings.search_queries.map((query) => ({ ...query })),
    global_searches: [...settings.global_searches],
  };
  if (settings.provider_options) {
    body.provider_options = { ...settings.provider_options };
  }
  return body;
}

function inputsEqual(left: ProviderDraft, right: ProviderDraft) {
  return JSON.stringify(left) === JSON.stringify(right);
}

function SettingsGroup({
  title,
  hint,
  count,
  children,
}: {
  title: string;
  hint?: string;
  count?: string;
  children: ReactNode;
}) {
  return (
    <section className="border-b border-border/60 px-4 py-3 last:border-0">
      <div className="flex flex-wrap items-baseline gap-x-2">
        <h4 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          {title}
        </h4>
        {count && <span className="text-xs tabular-nums text-muted-foreground">{count}</span>}
      </div>
      {hint && <p className="mt-0.5 text-xs text-muted-foreground">{hint}</p>}
      {children}
    </section>
  );
}

function SettingRow({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <label className="settings-row">
      <span className="min-w-0">
        <span className="block text-sm font-medium">{label}</span>
        {hint && <span className="block text-xs text-muted-foreground">{hint}</span>}
      </span>
      {children}
    </label>
  );
}

function RemoveButton({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <Button variant="danger" size="icon" className="h-9 w-9 shrink-0" aria-label={label} onClick={onClick}>
      <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
    </Button>
  );
}

function LinkedInSearchEditor({
  matrix,
  onChange,
}: {
  matrix: LinkedInMatrix;
  onChange: (matrix: LinkedInMatrix) => void;
}) {
  return (
    <>
      <SettingsGroup
        title="Queries"
        hint="Combined with every location below."
        count={`${matrix.queries.length} queries`}
      >
        <div className="mt-2 max-w-md space-y-1.5">
          {matrix.queries.map((query, index) => (
            <div className="flex gap-2" key={index}>
              <input
                aria-label={`LinkedIn query ${index + 1}`}
                className="field-sm min-w-0 flex-1"
                value={query}
                onChange={(event) => {
                  const queries = matrix.queries.map((item, itemIndex) =>
                    itemIndex === index ? event.target.value : item
                  );
                  onChange({ ...matrix, queries });
                }}
              />
              <RemoveButton
                label={`Remove LinkedIn query ${index + 1}`}
                onClick={() => onChange({
                  ...matrix,
                  queries: matrix.queries.filter((_, itemIndex) => itemIndex !== index),
                })}
              />
            </div>
          ))}
        </div>
        <Button
          size="sm"
          variant="ghost"
          className="mt-2"
          onClick={() => onChange({ ...matrix, queries: [...matrix.queries, ""] })}
        >
          <Plus className="h-4 w-4" aria-hidden="true" />
          Add LinkedIn query
        </Button>
      </SettingsGroup>

      <SettingsGroup
        title="Locations and work types"
        hint="LinkedIn geo ID. Selected work types are added to each query as natural-language text."
        count={`${matrix.locations.length} locations`}
      >
        {matrix.locations.length === 0 ? (
          <p className="mt-2 text-sm text-muted-foreground">
            No locations yet. Add a LinkedIn geo ID to start.
          </p>
        ) : (
          <table className="mt-2 max-w-md text-left text-sm">
            <thead>
              <tr className="text-xs font-medium text-muted-foreground">
                <th className="py-1 pr-3 font-medium">Geo ID</th>
                {linkedInWorkTypes.map(({ code, label }) => (
                  <th className="px-2 py-1 text-center font-medium" key={code}>{label}</th>
                ))}
                <th className="sr-only">Action</th>
              </tr>
            </thead>
            <tbody>
              {matrix.locations.map((location, index) => (
                <tr key={index}>
                  <td className="py-1 pr-3">
                    <input
                      type="number"
                      min={1}
                      step={1}
                      aria-label={`LinkedIn location ${index + 1}`}
                      className="field-sm w-32 tabular-nums"
                      value={location.location}
                      onChange={(event) => {
                        const locations = matrix.locations.map((item, itemIndex) =>
                          itemIndex === index ? { ...item, location: event.target.value } : item
                        );
                        onChange({ ...matrix, locations });
                      }}
                    />
                  </td>
                  {linkedInWorkTypes.map(({ code, label }) => (
                    <td className="px-2 py-1 text-center" key={code}>
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
                    </td>
                  ))}
                  <td className="py-1 pl-2">
                    <RemoveButton
                      label={`Remove LinkedIn location ${index + 1}`}
                      onClick={() => onChange({
                        ...matrix,
                        locations: matrix.locations.filter((_, itemIndex) => itemIndex !== index),
                      })}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <Button
          size="sm"
          variant="ghost"
          className="mt-2"
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
          <Plus className="h-4 w-4" aria-hidden="true" />
          Add LinkedIn location
        </Button>
      </SettingsGroup>
    </>
  );
}

function DiceSearchEditor({
  matrix,
  onChange,
}: {
  matrix: DiceMatrix;
  onChange: (matrix: DiceMatrix) => void;
}) {
  return (
    <>
      <SettingsGroup
        title="Queries"
        hint="Combined with every location below."
        count={`${matrix.queries.length} queries`}
      >
        <div className="mt-2 max-w-md space-y-1.5">
          {matrix.queries.map((query, index) => (
            <div className="flex gap-2" key={index}>
              <input
                aria-label={`Dice query ${index + 1}`}
                className="field-sm min-w-0 flex-1"
                value={query}
                onChange={(event) => {
                  const queries = matrix.queries.map((item, itemIndex) =>
                    itemIndex === index ? event.target.value : item
                  );
                  onChange({ ...matrix, queries });
                }}
              />
              <RemoveButton
                label={`Remove Dice query ${index + 1}`}
                onClick={() => onChange({
                  ...matrix,
                  queries: matrix.queries.filter((_, itemIndex) => itemIndex !== index),
                })}
              />
            </div>
          ))}
        </div>
        <Button
          size="sm"
          variant="ghost"
          className="mt-2"
          onClick={() => onChange({ ...matrix, queries: [...matrix.queries, ""] })}
        >
          <Plus className="h-4 w-4" aria-hidden="true" />
          Add Dice query
        </Button>
      </SettingsGroup>

      <SettingsGroup
        title="Locations"
        hint="Free-text city and state. Include remote is stored per location. Add the same city twice when you want both onsite and remote searches."
        count={`${matrix.locations.length} locations`}
      >
        {matrix.locations.length === 0 ? (
          <p className="mt-2 text-sm text-muted-foreground">
            No locations yet. Add a city and state to start.
          </p>
        ) : (
          <table className="mt-2 max-w-md text-left text-sm">
            <thead>
              <tr className="text-xs font-medium text-muted-foreground">
                <th className="py-1 pr-3 font-medium">Location</th>
                <th className="px-2 py-1 text-center font-medium">Include remote</th>
                <th className="sr-only">Action</th>
              </tr>
            </thead>
            <tbody>
              {matrix.locations.map((location, index) => (
                <tr key={index}>
                  <td className="py-1 pr-3">
                    <input
                      aria-label={`Dice location ${index + 1}`}
                      className="field-sm min-w-0 w-56"
                      value={location.location}
                      onChange={(event) => {
                        const locations = matrix.locations.map((item, itemIndex) =>
                          itemIndex === index ? { ...item, location: event.target.value } : item
                        );
                        onChange({ ...matrix, locations });
                      }}
                    />
                  </td>
                  <td className="px-2 py-1 text-center">
                    <input
                      type="checkbox"
                      aria-label={`Dice location ${index + 1} include remote`}
                      checked={location.includeRemote}
                      onChange={(event) => {
                        const locations = matrix.locations.map((item, itemIndex) =>
                          itemIndex === index
                            ? { ...item, includeRemote: event.target.checked }
                            : item
                        );
                        onChange({ ...matrix, locations });
                      }}
                    />
                  </td>
                  <td className="py-1 pl-2">
                    <RemoveButton
                      label={`Remove Dice location ${index + 1}`}
                      onClick={() => onChange({
                        ...matrix,
                        locations: matrix.locations.filter((_, itemIndex) => itemIndex !== index),
                      })}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <Button
          size="sm"
          variant="ghost"
          className="mt-2"
          onClick={() => onChange({
            ...matrix,
            locations: [
              ...matrix.locations,
              { location: "", includeRemote: true },
            ],
          })}
        >
          <Plus className="h-4 w-4" aria-hidden="true" />
          Add Dice location
        </Button>
      </SettingsGroup>
    </>
  );
}

function IndeedSearchEditor({
  matrix,
  options,
  onMatrixChange,
  onOptionsChange,
}: {
  matrix: IndeedMatrix;
  options: IndeedProviderOptions;
  onMatrixChange: (matrix: IndeedMatrix) => void;
  onOptionsChange: (options: IndeedProviderOptions) => void;
}) {
  return (
    <>
      <SettingsGroup
        title="Indeed run options"
        hint="Shared actor filters for every query and location pair."
      >
        <SettingRow label="Country" hint="Indeed country code, such as us.">
          <input
            aria-label="Indeed country"
            className="field-sm w-20 shrink-0"
            value={options.country}
            onChange={(event) => onOptionsChange({ ...options, country: event.target.value })}
          />
        </SettingRow>
        <SettingRow label="Job type">
          <select
            aria-label="Indeed job type"
            className="field-sm w-36 shrink-0"
            value={options.jobType}
            onChange={(event) => onOptionsChange({ ...options, jobType: event.target.value })}
          >
            {indeedJobTypes.map((jobType) => (
              <option key={jobType} value={jobType}>{jobType}</option>
            ))}
          </select>
        </SettingRow>
        <SettingRow label="From days" hint="Only jobs posted within this many days.">
          <select
            aria-label="Indeed from days"
            className="field-sm w-24 shrink-0"
            value={options.fromDays}
            onChange={(event) => onOptionsChange({ ...options, fromDays: event.target.value })}
          >
            {indeedFromDays.map((fromDays) => (
              <option key={fromDays} value={fromDays}>{fromDays}</option>
            ))}
          </select>
        </SettingRow>
        <SettingRow label="Max rows" hint="Maximum jobs to scrape per actor run.">
          <input
            type="number"
            min={1}
            max={1000}
            aria-label="Indeed max rows"
            className="field-sm w-24 shrink-0 tabular-nums"
            value={options.maxRows}
            onChange={(event) => onOptionsChange({
              ...options,
              maxRows: Number(event.target.value),
            })}
          />
        </SettingRow>
        <SettingRow label="Unique jobs" hint="Ask the actor to skip duplicate jobs.">
          <input
            type="checkbox"
            aria-label="Indeed enable unique jobs"
            checked={options.enableUniqueJobs}
            onChange={(event) => onOptionsChange({
              ...options,
              enableUniqueJobs: event.target.checked,
            })}
          />
        </SettingRow>
        <SettingRow label="Similar jobs" hint="Include Indeed similar-job results.">
          <input
            type="checkbox"
            aria-label="Indeed include similar jobs"
            checked={options.includeSimilarJobs}
            onChange={(event) => onOptionsChange({
              ...options,
              includeSimilarJobs: event.target.checked,
            })}
          />
        </SettingRow>
      </SettingsGroup>

      <SettingsGroup
        title="Queries"
        hint="Combined with every location below."
        count={`${matrix.queries.length} queries`}
      >
        <div className="mt-2 max-w-md space-y-1.5">
          {matrix.queries.map((query, index) => (
            <div className="flex gap-2" key={index}>
              <input
                aria-label={`Indeed query ${index + 1}`}
                className="field-sm min-w-0 flex-1"
                value={query}
                onChange={(event) => {
                  const queries = matrix.queries.map((item, itemIndex) =>
                    itemIndex === index ? event.target.value : item
                  );
                  onMatrixChange({ ...matrix, queries });
                }}
              />
              <RemoveButton
                label={`Remove Indeed query ${index + 1}`}
                onClick={() => onMatrixChange({
                  ...matrix,
                  queries: matrix.queries.filter((_, itemIndex) => itemIndex !== index),
                })}
              />
            </div>
          ))}
        </div>
        <Button
          size="sm"
          variant="ghost"
          className="mt-2"
          onClick={() => onMatrixChange({ ...matrix, queries: [...matrix.queries, ""] })}
        >
          <Plus className="h-4 w-4" aria-hidden="true" />
          Add Indeed query
        </Button>
      </SettingsGroup>

      <SettingsGroup
        title="Locations"
        hint="Free-text city and state. Radius, remote, and hybrid are stored per location. Add the same city twice when you want both onsite and remote searches."
        count={`${matrix.locations.length} locations`}
      >
        {matrix.locations.length === 0 ? (
          <p className="mt-2 text-sm text-muted-foreground">
            No locations yet. Add a city and state to start.
          </p>
        ) : (
          <table className="mt-2 max-w-2xl text-left text-sm">
            <thead>
              <tr className="text-xs font-medium text-muted-foreground">
                <th className="py-1 pr-3 font-medium">Location</th>
                <th className="px-2 py-1 font-medium">Radius</th>
                <th className="px-2 py-1 text-center font-medium">Remote</th>
                <th className="px-2 py-1 text-center font-medium">Hybrid</th>
                <th className="sr-only">Action</th>
              </tr>
            </thead>
            <tbody>
              {matrix.locations.map((location, index) => (
                <tr key={index}>
                  <td className="py-1 pr-3">
                    <input
                      aria-label={`Indeed location ${index + 1}`}
                      className="field-sm min-w-0 w-56"
                      value={location.location}
                      onChange={(event) => {
                        const locations = matrix.locations.map((item, itemIndex) =>
                          itemIndex === index ? { ...item, location: event.target.value } : item
                        );
                        onMatrixChange({ ...matrix, locations });
                      }}
                    />
                  </td>
                  <td className="px-2 py-1">
                    <select
                      aria-label={`Indeed location ${index + 1} radius`}
                      className="field-sm w-20"
                      value={location.radius}
                      onChange={(event) => {
                        const locations = matrix.locations.map((item, itemIndex) =>
                          itemIndex === index ? { ...item, radius: event.target.value } : item
                        );
                        onMatrixChange({ ...matrix, locations });
                      }}
                    >
                      {indeedRadii.map((radius) => (
                        <option key={radius} value={radius}>{radius}</option>
                      ))}
                    </select>
                  </td>
                  <td className="px-2 py-1 text-center">
                    <input
                      type="checkbox"
                      aria-label={`Indeed location ${index + 1} remote`}
                      checked={location.includeRemote}
                      onChange={(event) => {
                        const locations = matrix.locations.map((item, itemIndex) =>
                          itemIndex === index
                            ? { ...item, includeRemote: event.target.checked }
                            : item
                        );
                        onMatrixChange({ ...matrix, locations });
                      }}
                    />
                  </td>
                  <td className="px-2 py-1 text-center">
                    <input
                      type="checkbox"
                      aria-label={`Indeed location ${index + 1} hybrid`}
                      checked={location.includeHybrid}
                      onChange={(event) => {
                        const locations = matrix.locations.map((item, itemIndex) =>
                          itemIndex === index
                            ? { ...item, includeHybrid: event.target.checked }
                            : item
                        );
                        onMatrixChange({ ...matrix, locations });
                      }}
                    />
                  </td>
                  <td className="py-1 pl-2">
                    <RemoveButton
                      label={`Remove Indeed location ${index + 1}`}
                      onClick={() => onMatrixChange({
                        ...matrix,
                        locations: matrix.locations.filter((_, itemIndex) => itemIndex !== index),
                      })}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <Button
          size="sm"
          variant="ghost"
          className="mt-2"
          onClick={() => onMatrixChange({
            ...matrix,
            locations: [
              ...matrix.locations,
              {
                location: "",
                radius: defaultIndeedRadius,
                includeRemote: false,
                includeHybrid: false,
              },
            ],
          })}
        >
          <Plus className="h-4 w-4" aria-hidden="true" />
          Add Indeed location
        </Button>
      </SettingsGroup>
    </>
  );
}

function GlobalSearchesEditor({
  source,
  searches,
  onChange,
}: {
  source: string;
  searches: string[];
  onChange: (searches: string[]) => void;
}) {
  return (
    <SettingsGroup
      title="Global searches"
      hint="Free-form search strings. Not combined with locations."
      count={`${searches.length} searches`}
    >
      {searches.length === 0 ? (
        <p className="mt-2 text-sm text-muted-foreground">No global searches.</p>
      ) : (
        <div className="mt-2 max-w-xl space-y-1.5">
          {searches.map((search, index) => (
            <div className="flex gap-2" key={index}>
              <input
                aria-label={`${source} global search ${index + 1}`}
                className="field-sm min-w-0 flex-1"
                value={search}
                onChange={(event) => {
                  const next = searches.map((item, itemIndex) =>
                    itemIndex === index ? event.target.value : item
                  );
                  onChange(next);
                }}
              />
              <RemoveButton
                label={`Remove ${source} global search ${index + 1}`}
                onClick={() => onChange(searches.filter((_, itemIndex) => itemIndex !== index))}
              />
            </div>
          ))}
        </div>
      )}
      <Button
        size="sm"
        variant="ghost"
        className="mt-2"
        onClick={() => onChange([...searches, ""])}
      >
        <Plus className="h-4 w-4" aria-hidden="true" />
        Add {source} global search
      </Button>
    </SettingsGroup>
  );
}

function ProviderSection({
  settings,
  implemented,
  configured,
  configurationMessage,
}: {
  settings: ProviderSettings;
  implemented: boolean;
  configured: boolean;
  configurationMessage?: string;
}) {
  const queryClient = useQueryClient();
  const initial = toInput(settings);
  const [base, setBase] = useState(initial);
  const [draft, setDraft] = useState(initial);
  const dirty = !inputsEqual(base, draft);
  const source = settings.job_source;
  const panelID = useId();
  const [open, setOpen] = useState(false);

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
  useDirtySection(
    source,
    dirty,
    source,
    () => save.mutate(cloneInput(draft)),
    pending,
  );

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
  const patchDice = (matrix: DiceMatrix) => {
    setDraft((current) => ({
      ...current,
      diceMatrix: matrix,
      search_queries: fromDiceMatrix(matrix),
      global_searches: [],
    }));
  };
  const patchIndeedMatrix = (matrix: IndeedMatrix) => {
    setDraft((current) => ({
      ...current,
      indeedMatrix: matrix,
      search_queries: fromIndeedMatrix(matrix),
      global_searches: [],
    }));
  };
  const patchIndeedOptions = (options: IndeedProviderOptions) => {
    setDraft((current) => ({
      ...current,
      provider_options: options,
      timespan_code: options.fromDays,
      global_searches: [],
    }));
  };

  return (
    <article className="surface mt-3 overflow-hidden">
      <header
        className={cn(
          "flex flex-wrap items-center gap-x-3 gap-y-2 bg-muted/40 px-4 py-3",
          open && "border-b border-border/60",
        )}
      >
        <h3 className="min-w-0 flex-1 text-sm font-semibold tracking-tight">
          <CollapseToggle open={open} controls={panelID} onToggle={() => setOpen((value) => !value)}>
            {source} provider
          </CollapseToggle>
        </h3>
        {!implemented && <Badge variant="secondary">Not implemented</Badge>}
        {implemented && !configured && <Badge variant="secondary">Setup required</Badge>}
        {!open && (
          <span className="text-xs tabular-nums text-muted-foreground">
            {draft.enabled ? "Enabled" : "Disabled"} · {draft.search_queries.length}{" "}
            {draft.search_queries.length === 1 ? "query" : "queries"}
          </span>
        )}
        <div className="ml-auto flex items-center gap-2">
          {dirty && (
            <span className="text-xs font-medium text-accent-foreground">Unsaved changes</span>
          )}
          <Button
            size="sm"
            variant="ghost"
            disabled={pending}
            onClick={() => {
              if (window.confirm(`Reset ${source} settings to seed defaults?`)) {
                reset.mutate();
              }
            }}
          >
            <RotateCcw className="h-4 w-4" aria-hidden="true" />
            Reset {source} settings
          </Button>
          <Button
            size="sm"
            variant="primary"
            disabled={!dirty || pending}
            onClick={() => save.mutate(cloneInput(draft))}
          >
            <Save className="h-4 w-4" aria-hidden="true" />
            Save {source} settings
          </Button>
        </div>
      </header>

      {open && (
        <div id={panelID}>
      {configurationMessage && (
        <p className="border-b border-border/60 bg-muted/20 px-4 py-3 text-sm text-muted-foreground">
          {configurationMessage}
        </p>
      )}

      <SettingsGroup title="Schedule">
        <SettingRow label="Enabled" hint="Include this provider in scheduled scrape runs.">
          <input
            type="checkbox"
            aria-label={`Enable ${source}`}
            checked={implemented && configured && draft.enabled}
            disabled={!implemented || !configured}
            onChange={(event) => patch({ enabled: event.target.checked })}
          />
        </SettingRow>
        <SettingRow label="Scrape interval" hint="Seconds to wait between scrape runs.">
          <input
            type="number"
            min={60}
            aria-label={`${source} scrape interval seconds`}
            className="field-sm w-24 shrink-0 tabular-nums"
            value={draft.scrape_interval_seconds}
            onChange={(event) => patch({ scrape_interval_seconds: Number(event.target.value) })}
          />
        </SettingRow>
        {source !== "INDEED" && (
          <SettingRow label="Timespan code" hint="Provider code for posting age, such as r86400 for one day.">
            {source === "DICE" ? (
              <select
                aria-label="DICE posted date"
                className="field-sm w-28 shrink-0"
                value={draft.timespan_code}
                onChange={(event) => patch({ timespan_code: event.target.value })}
              >
                {["all", "24h", "3d", "7d", "30d"].map((code) => (
                  <option key={code} value={code}>{code}</option>
                ))}
              </select>
            ) : (
              <input
                aria-label={`${source} timespan code`}
                className="field-sm w-28 shrink-0"
                value={draft.timespan_code}
                onChange={(event) => patch({ timespan_code: event.target.value })}
              />
            )}
          </SettingRow>
        )}
        {source !== "INDEED" && (
          <SettingRow label="Pages to scrape" hint="Max result pages per search. Next start follows cards already returned; stops when a later page is shorter.">
            <input
              type="number"
              min={1}
              aria-label={`${source} pages to scrape`}
              className="field-sm w-20 shrink-0 tabular-nums"
              value={draft.pages_to_scrape}
              onChange={(event) => patch({ pages_to_scrape: Number(event.target.value) })}
            />
          </SettingRow>
        )}
        <SettingRow label="Rounds" hint="Passes for each run, from 1 to 3.">
          <input
            type="number"
            min={1}
            max={3}
            aria-label={`${source} rounds`}
            className="field-sm w-20 shrink-0 tabular-nums"
            value={draft.rounds}
            onChange={(event) => patch({ rounds: Number(event.target.value) })}
          />
        </SettingRow>
      </SettingsGroup>

      {source === "LINKEDIN" && draft.linkedInMatrix ? (
        <LinkedInSearchEditor matrix={draft.linkedInMatrix} onChange={patchLinkedIn} />
      ) : source === "DICE" && draft.diceMatrix ? (
        <DiceSearchEditor matrix={draft.diceMatrix} onChange={patchDice} />
      ) : source === "INDEED" && draft.indeedMatrix ? (
        <IndeedSearchEditor
          matrix={draft.indeedMatrix}
          options={toIndeedOptions(draft.provider_options)}
          onMatrixChange={patchIndeedMatrix}
          onOptionsChange={patchIndeedOptions}
        />
      ) : (
        <SettingsGroup title="Search queries" count={`${draft.search_queries.length} queries`}>
          <table className="mt-2 max-w-xl text-left text-sm">
            <thead>
              <tr className="text-xs font-medium text-muted-foreground">
                <th className="py-1 pr-3 font-medium">Keywords</th>
                <th className="py-1 pr-3 font-medium">Location</th>
                <th className="sr-only">Action</th>
              </tr>
            </thead>
            <tbody>
              {draft.search_queries.map((query, index) => (
                <tr key={index}>
                  <td className="py-1 pr-3">
                    <input
                      aria-label={`${source} query keywords`}
                      className="field-sm w-40"
                      value={query.keywords}
                      onChange={(event) => {
                        const search_queries = draft.search_queries.map((item, itemIndex) =>
                          itemIndex === index ? { ...item, keywords: event.target.value } : item
                        );
                        patch({ search_queries });
                      }}
                    />
                  </td>
                  <td className="py-1 pr-3">
                    <input
                      aria-label={`${source} query location`}
                      className="field-sm w-40"
                      value={query.location}
                      onChange={(event) => {
                        const search_queries = draft.search_queries.map((item, itemIndex) =>
                          itemIndex === index ? { ...item, location: event.target.value } : item
                        );
                        patch({ search_queries });
                      }}
                    />
                  </td>
                  <td className="py-1">
                    <RemoveButton
                      label={`Remove ${source} query ${index + 1}`}
                      onClick={() => patch({
                        search_queries: draft.search_queries.filter(
                          (_, itemIndex) => itemIndex !== index,
                        ),
                      })}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <Button
            size="sm"
            variant="ghost"
            className="mt-2"
            onClick={() => patch({
              search_queries: [
                ...draft.search_queries,
                { keywords: "", location: "" },
              ],
            })}
          >
            <Plus className="h-4 w-4" aria-hidden="true" />
            Add {source} query
          </Button>
        </SettingsGroup>
      )}

      {source !== "DICE" && source !== "INDEED" && (
        <GlobalSearchesEditor
          source={source}
          searches={draft.global_searches}
          onChange={(global_searches) => patch({ global_searches })}
        />
      )}

      <SettingsGroup title="State">
        <div className="mt-1 space-y-0.5 text-xs text-muted-foreground">
          <p>Last scraped: {formatTimestamp(settings.last_scraped_at)}</p>
          <p>Next eligible: {formatTimestamp(settings.next_eligible_at)}</p>
          <p>Identifier: {settings.id}</p>
          <p>Updated: {formatTimestamp(settings.updated_at)}</p>
        </div>
      </SettingsGroup>

      {(save.isError || reset.isError) && (
        <p className="border-t border-border/60 px-4 py-3 text-sm text-destructive">
          {(save.error ?? reset.error)?.message}
        </p>
      )}
        </div>
      )}
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
  const configured = new Map(
    dashboard.data?.providers.map((provider) => [provider.job_source, provider.configured ?? true]) ?? [],
  );
  const configurationMessages = new Map(
    dashboard.data?.providers.map((provider) => [
      provider.job_source,
      provider.configuration_message,
    ]) ?? [],
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
            configured={configured.get(settings.job_source) ?? true}
            configurationMessage={configurationMessages.get(settings.job_source)}
          />
        ))}
    </>
  );
}
