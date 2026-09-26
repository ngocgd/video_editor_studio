import { useEffect, useState } from "react";

import type { AssetKind, LibraryAsset, LibrarySettings, LibraryUsage } from "../../api/gen/types.gen";
import { EmptyState } from "../../components/shared/empty-state";
import { InlineError } from "../../components/shared/inline-error";
import { VirtualTable, type VirtualTableColumn } from "../../components/shared/virtual-table";
import { Button } from "../../components/ui/button";
import { formatBytes } from "../../lib/format";
import { DiskChip } from "../render/disk-chip";
import { LibraryCleanup } from "./library-cleanup";
import { errorMessage } from "../render/render-model";
import { referenceLabel, usageShares } from "./library-model";
import { useLibraryAssets, useLibrarySettings, useLibraryUsage, useSaveLibrarySettings } from "./use-library";

const KINDS: { value: AssetKind | ""; label: string }[] = [
  { value: "", label: "All kinds" },
  { value: "video", label: "Video" },
  { value: "image", label: "Images" },
  { value: "audio", label: "Audio" },
  { value: "document", label: "Documents" },
];

const selectClass = "h-8 rounded-md border border-input bg-well px-2 text-sm";

const COLUMNS: VirtualTableColumn<LibraryAsset>[] = [
  { key: "kind", header: "Kind", render: (a) => <span title={a.mime}>{a.kind}</span>, className: "w-24 shrink-0 truncate px-3" },
  { key: "project", header: "Project", render: (a) => a.seriesTitle ?? <span className="text-text-2">—</span>, className: "flex-[2] truncate px-3" },
  { key: "size", header: "Size", render: (a) => <span className="font-mono tabular-nums">{formatBytes(a.bytes)}</span>, className: "w-24 shrink-0 px-3 text-right" },
  { key: "created", header: "Created", render: (a) => <span className="font-mono text-xs tabular-nums">{new Date(a.createdAt).toLocaleDateString()}</span>, className: "w-28 shrink-0 px-3" },
  { key: "refs", header: "Referenced by", render: (a) => <span className={a.referencedBy.length ? "" : "text-text-2"}>{referenceLabel(a.referencedBy)}</span>, className: "flex-[2] truncate px-3" },
];

function UsagePanel({ usage }: { usage: LibraryUsage }) {
  const rows = usageShares(usage.series, usage.totalBytes);
  return (
    <section aria-label="Storage usage" className="flex flex-col gap-2 rounded-md border border-border p-3">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-medium">Storage</h2>
        <span className="font-mono text-xs tabular-nums text-text-2">
          {formatBytes(usage.totalBytes)} · {usage.assets} assets
        </span>
      </div>
      <ul className="flex flex-col gap-1.5">
        {rows.map((r) => (
          <li key={r.seriesId ?? "none"} className="grid grid-cols-[1fr_auto] gap-x-2 gap-y-1 text-xs">
            <span className="truncate">{r.label}</span>
            <span className="font-mono tabular-nums text-text-2">{formatBytes(r.bytes)}</span>
            <span className="col-span-2 h-1 overflow-hidden rounded-full bg-muted">
              <span className="block h-full bg-primary" style={{ width: `${r.share * 100}%` }} />
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** Retention settings: how long unreferenced render cache and unselected takes are kept. */
export function RetentionForm({ settings, saving, error, onSave }: { settings: LibrarySettings; saving: boolean; error?: string; onSave: (s: LibrarySettings) => void }) {
  const [form, setForm] = useState(settings);
  useEffect(() => setForm(settings), [settings]);
  const dirty = form.segmentTtlDays !== settings.segmentTtlDays || form.takeTtlDays !== settings.takeTtlDays;
  const num = (key: "segmentTtlDays" | "takeTtlDays") => (e: React.ChangeEvent<HTMLInputElement>) => setForm((f) => ({ ...f, [key]: Number(e.target.value) }));
  return (
    <form
      aria-label="Retention"
      className="flex flex-col gap-2 rounded-md border border-border p-3 text-sm"
      onSubmit={(e) => {
        e.preventDefault();
        onSave({ segmentTtlDays: form.segmentTtlDays, takeTtlDays: form.takeTtlDays });
      }}
    >
      <h2 className="font-medium">Retention</h2>
      <label className="flex items-center justify-between gap-2 text-text-2">
        Keep unused render cache
        <span className="flex items-center gap-1">
          <input type="number" min={1} max={365} value={form.segmentTtlDays} onChange={num("segmentTtlDays")} className="h-7 w-16 rounded-md border border-input bg-well px-1 font-mono text-xs" /> days
        </span>
      </label>
      <label className="flex items-center justify-between gap-2 text-text-2">
        Keep unselected takes
        <span className="flex items-center gap-1">
          <input type="number" min={1} max={365} value={form.takeTtlDays} onChange={num("takeTtlDays")} className="h-7 w-16 rounded-md border border-input bg-well px-1 font-mono text-xs" /> days
        </span>
      </label>
      <p className="text-xs text-text-2">
        Expired items are removed daily{settings.lastCleanupAt ? `; last run ${new Date(settings.lastCleanupAt).toLocaleString()}` : ""}.
      </p>
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
      <Button type="submit" variant="secondary" size="sm" disabled={!dirty || saving}>
        Save retention
      </Button>
    </form>
  );
}

/**
 * The Library page: every asset of the workspace in a virtualized,
 * cursor-paged table (kind, project, size, created, referenced by),
 * storage use per project with the disk chip, retention settings and a
 * manual cleanup (dry run, confirm, audited job).
 */
export function LibraryView() {
  const [kind, setKind] = useState<AssetKind | "">("");
  const [seriesId, setSeriesId] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const assets = useLibraryAssets(kind || undefined, seriesId || undefined);
  const usage = useLibraryUsage();
  const settings = useLibrarySettings();
  const save = useSaveLibrarySettings();
  const rows = assets.data?.pages.flatMap((p) => p.items) ?? [];
  const projects = (usage.data?.series ?? []).filter((s) => s.seriesId);

  return (
    <div className="grid min-h-0 grid-cols-[minmax(0,1fr)_320px] gap-4">
      <section aria-label="Assets" className="flex min-h-0 flex-col gap-3">
        <header className="flex flex-wrap items-center gap-2">
          <span className="flex-1" />
          <label className="sr-only" htmlFor="library-kind">
            Kind
          </label>
          <select id="library-kind" className={selectClass} value={kind} onChange={(e) => setKind(e.target.value as AssetKind | "")}>
            {KINDS.map((k) => (
              <option key={k.value} value={k.value}>
                {k.label}
              </option>
            ))}
          </select>
          <label className="sr-only" htmlFor="library-project">
            Project
          </label>
          <select id="library-project" className={selectClass} value={seriesId} onChange={(e) => setSeriesId(e.target.value)}>
            <option value="">All projects</option>
            {projects.map((p) => (
              <option key={p.seriesId} value={p.seriesId}>
                {p.seriesTitle ?? "Untitled project"}
              </option>
            ))}
          </select>
        </header>
        {assets.isError ? (
          <InlineError cause="Could not load the library." onRetry={() => void assets.refetch()} />
        ) : rows.length === 0 && !assets.isLoading ? (
          <EmptyState message="No assets match these filters." />
        ) : (
          <VirtualTable rows={rows} columns={COLUMNS} getRowId={(a) => a.id} activeIndex={activeIndex} onActiveIndexChange={setActiveIndex} ariaLabel="Library assets" rowHeight={36} />
        )}
        {assets.hasNextPage && (
          <Button variant="secondary" size="sm" onClick={() => void assets.fetchNextPage()} disabled={assets.isFetchingNextPage}>
            {assets.isFetchingNextPage ? "Loading…" : "Load more"}
          </Button>
        )}
      </section>
      <aside aria-label="Storage and cleanup" className="flex flex-col gap-3">
        {usage.data && (
          <>
            <DiskChip disk={usage.data.disk} />
            <UsagePanel usage={usage.data} />
          </>
        )}
        {settings.data && <RetentionForm settings={settings.data} saving={save.isPending} error={errorMessage(save.error)} onSave={(body) => save.mutate({ body })} />}
        <LibraryCleanup />
      </aside>
    </div>
  );
}
