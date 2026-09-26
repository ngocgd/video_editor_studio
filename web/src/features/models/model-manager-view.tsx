import { CircleCheck, CircleX } from "lucide-react";
import { useState } from "react";

import type { ModelInfo } from "../../api/gen/types.gen";
import { EmptyState } from "../../components/shared/empty-state";
import { InlineError } from "../../components/shared/inline-error";
import { StatusChip } from "../../components/shared/status-chip";
import { VirtualTable, type VirtualTableColumn } from "../../components/shared/virtual-table";
import { Button } from "../../components/ui/button";
import { formatBytes, formatPercent } from "../../lib/format";
import { type ModelRole, useModelAction, useModelRole, useModels } from "./use-models";

export function downloadPercent(model: ModelInfo): number {
  if (model.bytesTotal <= 0) return 0;
  return Math.min(100, Math.floor((model.bytesDone / model.bytesTotal) * 100));
}

function LicenceBadge({ model }: { model: ModelInfo }) {
  const Icon = model.licence.allowed ? CircleCheck : CircleX;
  return (
    <a
      href={model.licence.url}
      target="_blank"
      rel="noreferrer noopener"
      title={`Verified ${model.licence.verified}`}
      className={`inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 text-xs ${
        model.licence.allowed ? "bg-success-muted text-success" : "bg-destructive-muted text-destructive"
      }`}
    >
      <Icon size={14} strokeWidth={1.75} aria-hidden="true" />
      {model.licence.allowed ? model.licence.spdx : "Not commercial-allowlisted"}
    </a>
  );
}

export function ModelStatus({ model }: { model: ModelInfo }) {
  switch (model.status) {
    case "blocked":
      return <span className="text-xs text-text-2">Blocked: licence</span>;
    case "downloading": {
      const pct = downloadPercent(model);
      return (
        <span className="flex items-center gap-1.5" aria-label={`Downloading ${pct}%`}>
          <span className="h-1.5 w-20 overflow-hidden rounded-full bg-muted" aria-hidden="true">
            <span className="block h-full bg-primary" style={{ width: `${pct}%` }} />
          </span>
          <span className="font-mono text-xs tabular-nums">{formatPercent(pct)}</span>
        </span>
      );
    }
    case "paused":
      return <StatusChip state="queued" label="Paused" detail={formatPercent(downloadPercent(model))} />;
    case "failed":
      return (
        <span title={model.error}>
          <StatusChip state="failed" />
        </span>
      );
    case "installed":
      return model.loaded ? <StatusChip state="running" label="Loaded" /> : <StatusChip state="done" label="Installed" />;
    default:
      return <StatusChip state="none" label="Not installed" />;
  }
}

/** The one action a row offers, gated by role (the API enforces the same). */
export function rowAction(model: ModelInfo, role: ModelRole): { action: "install" | "pause" | "load" | "unload"; label: string } | null {
  const isOwner = role === "owner";
  const canRun = role === "owner" || role === "editor";
  switch (model.status) {
    case "not_installed":
      return isOwner ? { action: "install", label: "Install" } : null;
    case "paused":
      return isOwner ? { action: "install", label: "Resume" } : null;
    case "failed":
      return isOwner ? { action: "install", label: "Retry" } : null;
    case "downloading":
      return isOwner ? { action: "pause", label: "Pause" } : null;
    case "installed":
      if (!canRun) return null;
      return model.loaded ? { action: "unload", label: "Unload" } : { action: "load", label: "Load" };
    default:
      return null;
  }
}

/** Settings → Models & providers → Model manager (wireframe settings-models). */
export function ModelManagerView() {
  const query = useModels();
  const role = useModelRole();
  const act = useModelAction();
  const [activeIndex, setActiveIndex] = useState(0);
  const items = query.data?.items ?? [];

  const columns: VirtualTableColumn<ModelInfo>[] = [
    { key: "name", header: "Model", render: (m) => <span className="font-mono text-xs">{m.name}</span>, className: "flex-[2] truncate px-3" },
    { key: "task", header: "Task", render: (m) => m.title, className: "flex-[2] truncate px-3" },
    { key: "licence", header: "Licence", render: (m) => <LicenceBadge model={m} />, className: "flex-[2] px-3" },
    {
      key: "size",
      header: "Size",
      render: (m) => <span className="font-mono text-xs tabular-nums">{formatBytes(m.sizeBytes)}</span>,
      className: "flex-1 px-3",
    },
    {
      key: "vram",
      header: "VRAM",
      render: (m) => (
        <span className={`font-mono text-xs tabular-nums ${m.overBudget ? "text-warning" : ""}`} title={m.overBudget ? "Above the measured VRAM budget" : undefined}>
          ~{(m.vramMb / 1024).toFixed(1)} GB{m.overBudget ? " (over budget)" : ""}
        </span>
      ),
      className: "flex-[1.5] px-3",
    },
    { key: "status", header: "Status", render: (m) => <ModelStatus model={m} />, className: "flex-[1.5] px-3" },
    {
      key: "actions",
      header: "Actions",
      render: (m) => {
        const next = rowAction(m, role);
        if (!next) return null;
        return (
          <Button
            variant="ghost"
            size="sm"
            disabled={act.isPending}
            onClick={() => act.mutate({ action: next.action, name: m.name })}
            aria-label={`${next.label} ${m.name}`}
          >
            {next.label}
          </Button>
        );
      },
      className: "flex-1 px-3",
    },
  ];

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-baseline gap-3">
        <h2 className="text-sm font-semibold">Model manager</h2>
        <p className="text-xs text-text-2">Commercial-use licences only. One GPU model loaded at a time.</p>
        <span className="grow" />
        {query.data?.budgetMb != null && (
          <span className="font-mono text-xs tabular-nums text-text-2">VRAM budget {formatBytes(query.data.budgetMb * 1024 * 1024)}</span>
        )}
        {query.data?.workerOnline === false && <span className="text-xs text-warning">Worker offline</span>}
      </div>
      {query.isError ? (
        <InlineError cause="Could not load the model list." onRetry={() => void query.refetch()} />
      ) : items.length === 0 && !query.isLoading ? (
        <EmptyState message="The model manifest lists no models." />
      ) : (
        <VirtualTable
          rows={items}
          columns={columns}
          rowHeight={40}
          getRowId={(m) => `model-${m.name}`}
          activeIndex={activeIndex}
          onActiveIndexChange={setActiveIndex}
          ariaLabel="Model manager"
        />
      )}
      <p className="text-xs text-text-2">
        Licence data is recorded at install time and must be re-verified on each model update.
      </p>
    </div>
  );
}
