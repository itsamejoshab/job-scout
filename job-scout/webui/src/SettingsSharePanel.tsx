import { useMutation, useQuery } from "@tanstack/react-query";
import { Check, Copy, Download, Upload } from "lucide-react";
import { useId, useState } from "react";

import { exportSettings, importSettings } from "./api";
import { CollapseToggle } from "./CollapseToggle";
import { Button } from "./components/ui/button";
import { cn } from "./lib/utils";

export function SettingsSharePanel({ onImported }: { onImported: () => Promise<void> | void }) {
  const shareID = useId();
  const jsonID = useId();
  const importID = useId();
  const panelID = useId();
  const [open, setOpen] = useState(false);
  const exported = useQuery({
    queryKey: ["settings-export"],
    queryFn: exportSettings,
  });
  const [importText, setImportText] = useState("");
  const [copied, setCopied] = useState<"share" | "json" | "">("");

  const jsonText = exported.data
    ? JSON.stringify(exported.data.settings, null, 2)
    : "";

  const copy = async (kind: "share" | "json", value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(kind);
      window.setTimeout(() => setCopied(""), 2000);
    } catch {
      setCopied("");
    }
  };

  const downloadJSON = () => {
    if (!jsonText) {
      return;
    }
    const blob = new Blob([jsonText], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = "jobscout-settings.json";
    link.click();
    URL.revokeObjectURL(url);
  };

  const apply = useMutation({
    mutationFn: (payload: string) => importSettings(payload),
    onSuccess: async () => {
      setImportText("");
      await onImported();
    },
  });

  const importSettingsPayload = () => {
    const payload = importText.trim();
    if (!payload) {
      return;
    }
    if (!window.confirm(
      "Import these settings? This replaces saved filter lists, the notification toggle, and any providers in the file.",
    )) {
      return;
    }
    apply.mutate(payload);
  };

  return (
    <section className="surface mt-6 overflow-hidden">
      <div
        className={cn(
          "flex flex-wrap items-center justify-between gap-x-3 gap-y-2 bg-muted/40 px-4 py-3",
          open && "border-b border-border/60",
        )}
      >
        <div className="min-w-0 flex-1">
          <h2 className="text-base font-semibold tracking-tight">
            <CollapseToggle open={open} controls={panelID} onToggle={() => setOpen((value) => !value)}>
              Share settings
            </CollapseToggle>
          </h2>
          <p className="text-xs text-muted-foreground">
            Copy a share code or JSON. The file has the settings on this page only. It does not include API tokens, webhooks, or scrape times.
          </p>
        </div>
      </div>

      {open && (
        <div id={panelID}>

      {exported.isPending && (
        <p className="px-4 py-3 text-sm text-muted-foreground">Loading share code.</p>
      )}
      {exported.isError && (
        <p className="px-4 py-3 text-sm text-destructive">Share export is unavailable.</p>
      )}
      {exported.data && (
        <div className="grid gap-4 px-4 py-3 lg:grid-cols-2">
          <div>
            <label htmlFor={shareID} className="text-sm font-medium">
              Share code
            </label>
            <textarea
              id={shareID}
              readOnly
              className="field mt-1 h-auto min-h-24 py-2 font-mono text-xs"
              value={exported.data.share_code}
            />
            <Button
              size="sm"
              className="mt-2"
              onClick={() => copy("share", exported.data.share_code)}
            >
              {copied === "share" ? <Check className="h-4 w-4" aria-hidden="true" /> : <Copy className="h-4 w-4" aria-hidden="true" />}
              {copied === "share" ? "Copied share code" : "Copy share code"}
            </Button>
          </div>
          <div>
            <label htmlFor={jsonID} className="text-sm font-medium">
              JSON
            </label>
            <textarea
              id={jsonID}
              readOnly
              className="field mt-1 h-auto min-h-24 py-2 font-mono text-xs"
              value={jsonText}
            />
            <div className="mt-2 flex flex-wrap gap-2">
              <Button size="sm" onClick={() => copy("json", jsonText)}>
                {copied === "json" ? <Check className="h-4 w-4" aria-hidden="true" /> : <Copy className="h-4 w-4" aria-hidden="true" />}
                {copied === "json" ? "Copied JSON" : "Copy JSON"}
              </Button>
              <Button size="sm" variant="ghost" onClick={downloadJSON}>
                <Download className="h-4 w-4" aria-hidden="true" />
                Download JSON
              </Button>
            </div>
          </div>
        </div>
      )}

      <div className="border-t border-border/60 px-4 py-3">
        <label htmlFor={importID} className="text-sm font-medium">
          Import
        </label>
        <p className="mt-0.5 text-xs text-muted-foreground">
          Paste a share code or JSON from another Job Scout.
        </p>
        <textarea
          id={importID}
          className="field mt-2 h-auto min-h-24 py-2 font-mono text-xs"
          placeholder="!JS:1!... or { &quot;format&quot;: &quot;jobscout-settings&quot;, ... }"
          value={importText}
          onChange={(event) => setImportText(event.target.value)}
        />
        {apply.isError && (
          <p className="mt-2 text-sm text-destructive" role="alert">
            {apply.error instanceof Error ? apply.error.message : "Import failed."}
          </p>
        )}
        {apply.isSuccess && (
          <p className="mt-2 text-sm text-muted-foreground" role="status">
            Settings imported.
          </p>
        )}
        <Button
          size="sm"
          variant="primary"
          className="mt-2"
          disabled={importText.trim() === "" || apply.isPending}
          onClick={importSettingsPayload}
        >
          <Upload className="h-4 w-4" aria-hidden="true" />
          Import settings
        </Button>
      </div>
        </div>
      )}
    </section>
  );
}
