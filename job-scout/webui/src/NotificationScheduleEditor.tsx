import { Plus, Save, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import type { NotificationSchedule } from "./api";
import { Button } from "./components/ui/button";
import { useDirtySection } from "./settingsDirty";

const weekdays = [
  { value: 0, label: "Sun" },
  { value: 1, label: "Mon" },
  { value: 2, label: "Tue" },
  { value: 3, label: "Wed" },
  { value: 4, label: "Thu" },
  { value: 5, label: "Fri" },
  { value: 6, label: "Sat" },
];

function cloneSchedule(schedule: NotificationSchedule): NotificationSchedule {
  return {
    ...schedule,
    silent_periods: schedule.silent_periods.map((period) => ({
      ...period,
      days: [...period.days],
    })),
  };
}

function scheduleKey(schedule: NotificationSchedule | null) {
  return schedule ? JSON.stringify(schedule) : "";
}

type NotificationScheduleEditorProps = {
  schedule: NotificationSchedule;
  timezone: string;
  saving: boolean;
  saveError: boolean;
  onSave: (schedule: NotificationSchedule) => void;
};

export function NotificationScheduleEditor({
  schedule,
  timezone,
  saving,
  saveError,
  onSave,
}: NotificationScheduleEditorProps) {
  const [base, setBase] = useState<NotificationSchedule | null>(null);
  const [draft, setDraft] = useState<NotificationSchedule | null>(null);
  const incomingKey = scheduleKey(schedule);
  const draftKey = scheduleKey(draft);
  const dirty = base !== null && draft !== null && scheduleKey(base) !== draftKey;
  const incomingSchedule = useRef(schedule);
  const syncedIncomingKey = useRef("");
  incomingSchedule.current = schedule;

  useEffect(() => {
    if (syncedIncomingKey.current === incomingKey) {
      return;
    }
    syncedIncomingKey.current = incomingKey;
    const next = cloneSchedule(incomingSchedule.current);
    setBase(next);
    setDraft(cloneSchedule(next));
  }, [incomingKey]);

  const save = () => {
    if (draft) {
      onSave(cloneSchedule(draft));
    }
  };
  useDirtySection("notification-schedule", dirty, "Notification schedule", save, saving);

  if (!draft) {
    return null;
  }

  const patch = (next: Partial<NotificationSchedule>) => {
    setDraft((current) => current && { ...current, ...next });
  };

  const addSilentPeriod = () => {
    patch({
      silent_periods: [
        ...draft.silent_periods,
        { days: [0, 1, 2, 3, 4, 5, 6], start: "22:00", end: "07:00" },
      ],
    });
  };

  return (
    <div className="border-t border-border/60 px-4 py-4">
      <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)]">
        <div>
          <p className="text-sm font-semibold">Delivery cadence</p>
          <p className="mt-1 text-xs text-muted-foreground">
            Temporal evaluates this schedule in {timezone}.
          </p>

          <div className="mt-3 flex flex-wrap gap-4">
            <label className="flex items-center gap-2 text-sm">
              <input
                type="radio"
                name="notification-schedule-mode"
                value="interval"
                checked={draft.mode === "interval"}
                onChange={() => patch({ mode: "interval" })}
              />
              Every N minutes
            </label>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="radio"
                name="notification-schedule-mode"
                value="cron"
                checked={draft.mode === "cron"}
                onChange={() => patch({ mode: "cron" })}
              />
              Cron pattern
            </label>
          </div>

          {draft.mode === "interval" ? (
            <label className="mt-3 block text-sm font-medium">
              Minutes between notifications
              <input
                type="number"
                min={1}
                max={10080}
                required
                className="mt-1 block w-40 rounded-md border border-border bg-background px-3 py-2 text-sm"
                value={draft.interval_minutes}
                onChange={(event) => patch({ interval_minutes: Number(event.target.value) })}
              />
            </label>
          ) : (
            <label className="mt-3 block text-sm font-medium">
              Five-field cron pattern
              <input
                type="text"
                required
                spellCheck={false}
                className="mt-1 block w-full rounded-md border border-border bg-background px-3 py-2 font-mono text-sm"
                value={draft.cron_pattern}
                onChange={(event) => patch({ cron_pattern: event.target.value })}
                placeholder="*/5 * * * *"
              />
              <span className="mt-1 block text-xs font-normal text-muted-foreground">
                Example: */5 * * * * sends ready jobs every five minutes.
              </span>
            </label>
          )}
        </div>

        <div>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <p className="text-sm font-semibold">Silent periods</p>
              <p className="mt-1 text-xs text-muted-foreground">
                Matching local times are excluded from the Temporal schedule.
              </p>
            </div>
            <Button size="sm" variant="ghost" onClick={addSilentPeriod}>
              <Plus className="h-4 w-4" aria-hidden="true" />
              Add silent period
            </Button>
          </div>

          {draft.silent_periods.length === 0 && (
            <p className="mt-3 rounded-md border border-dashed border-border px-3 py-3 text-sm text-muted-foreground">
              No silent periods. Notifications can run at any scheduled time.
            </p>
          )}

          <div className="mt-3 space-y-3">
            {draft.silent_periods.map((period, index) => (
              <fieldset
                key={index}
                className="rounded-md border border-border bg-muted/20 p-3"
              >
                <legend className="px-1 text-xs font-semibold">
                  Silent period {index + 1}
                </legend>
                <div className="flex flex-wrap gap-2">
                  {weekdays.map((day) => (
                    <label
                      key={day.value}
                      className="flex items-center gap-1 rounded border border-border bg-background px-2 py-1 text-xs"
                    >
                      <input
                        type="checkbox"
                        aria-label={`${day.label}, silent period ${index + 1}`}
                        checked={period.days.includes(day.value)}
                        onChange={(event) => {
                          const days = event.target.checked
                            ? [...period.days, day.value].sort((a, b) => a - b)
                            : period.days.filter((value) => value !== day.value);
                          const silent_periods = draft.silent_periods.map((item, itemIndex) =>
                            itemIndex === index ? { ...item, days } : item,
                          );
                          patch({ silent_periods });
                        }}
                      />
                      {day.label}
                    </label>
                  ))}
                </div>
                <div className="mt-3 flex flex-wrap items-end gap-3">
                  <label className="text-xs font-medium">
                    Start
                    <input
                      type="time"
                      aria-label={`Silent period ${index + 1} start`}
                      className="mt-1 block rounded-md border border-border bg-background px-2 py-1.5 text-sm"
                      value={period.start}
                      onChange={(event) => {
                        const silent_periods = draft.silent_periods.map((item, itemIndex) =>
                          itemIndex === index ? { ...item, start: event.target.value } : item,
                        );
                        patch({ silent_periods });
                      }}
                    />
                  </label>
                  <label className="text-xs font-medium">
                    End
                    <input
                      type="time"
                      aria-label={`Silent period ${index + 1} end`}
                      className="mt-1 block rounded-md border border-border bg-background px-2 py-1.5 text-sm"
                      value={period.end}
                      onChange={(event) => {
                        const silent_periods = draft.silent_periods.map((item, itemIndex) =>
                          itemIndex === index ? { ...item, end: event.target.value } : item,
                        );
                        patch({ silent_periods });
                      }}
                    />
                  </label>
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={`Remove silent period ${index + 1}`}
                    onClick={() =>
                      patch({
                        silent_periods: draft.silent_periods.filter(
                          (_, itemIndex) => itemIndex !== index,
                        ),
                      })
                    }
                  >
                    <Trash2 className="h-4 w-4" aria-hidden="true" />
                    Remove
                  </Button>
                </div>
              </fieldset>
            ))}
          </div>
        </div>
      </div>

      <div className="mt-4 flex flex-wrap items-center justify-end gap-3">
        {saveError && (
          <p className="text-sm text-destructive" role="alert">
            The notification schedule could not be saved. Check the values and try again.
          </p>
        )}
        <Button variant="primary" disabled={!dirty || saving} onClick={save}>
          <Save className="h-4 w-4" aria-hidden="true" />
          Save notification schedule
        </Button>
      </div>
    </div>
  );
}
