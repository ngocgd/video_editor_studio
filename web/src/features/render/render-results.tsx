import { CircleCheck, CircleX, Download } from "lucide-react";
import { useState } from "react";

import type { QcReport, Render } from "../../api/gen/types.gen";
import { assetUrl } from "../storyboard/use-scenes";
import { encoderLabel, formatClock } from "./render-model";

function QcSummary({ report }: { report: QcReport }) {
  const rows: [string, string][] = [
    ["Loudness", `${report.integratedLufs.toFixed(1)} LUFS${report.targetLufs != null ? ` (target ${report.targetLufs})` : ""}`],
    ["True peak", `${report.truePeakDbtp.toFixed(1)} dBTP`],
    ["Duration", `${formatClock(report.durationMs)} (expected ${formatClock(report.expectedDurationMs)})`],
    ["A/V drift", `${report.avDriftMs} ms`],
    ["Subtitle drift", `≤${report.maxSubtitleDriftMs} ms`],
    ["Streams", `${report.streams.video} video · ${report.streams.audio} audio · ${report.streams.subtitle} subtitle`],
    ["Keyframes", report.missingKeyframes.length === 0 ? "At every segment start" : `${report.missingKeyframes.length} segment starts missing`],
  ];
  return (
    <div className="flex flex-col gap-2">
      <p className={`flex items-center gap-1.5 text-sm font-medium ${report.passed ? "text-success" : "text-destructive"}`}>
        {report.passed ? <CircleCheck size={14} aria-hidden="true" /> : <CircleX size={14} aria-hidden="true" />}
        {report.passed ? "QC passed" : "QC failed"}
      </p>
      {report.failures.length > 0 && (
        <ul className="list-disc pl-5 text-xs text-destructive">
          {report.failures.map((f) => (
            <li key={f}>{f}</li>
          ))}
        </ul>
      )}
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
        {rows.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="text-text-2">{k}</dt>
            <dd className="font-mono tabular-nums">{v}</dd>
          </div>
        ))}
      </dl>
      <p className="break-all font-mono text-2xs text-text-2" title="SHA-256 of the final MP4">
        sha256 {report.sha256}
      </p>
    </div>
  );
}

const linkClass = "inline-flex h-7 items-center gap-1.5 rounded-md border border-border bg-secondary px-2.5 text-sm hover:bg-accent";

/**
 * Finished renders of the episode language, newest first: the 540p proxy
 * plays through the asset redirect (a short-lived presigned GET with range
 * requests), with the QC report and downloads of the MP4 and the SRT.
 */
export function RenderResults({ renders }: { renders: Render[] }) {
  const [selectedId, setSelectedId] = useState<string | undefined>();
  const selected = renders.find((r) => r.id === selectedId) ?? renders[0];
  if (!selected) return <p className="text-sm text-text-2">No finished renders yet.</p>;
  const playId = selected.previewAssetId ?? selected.assetId;

  return (
    <section aria-label="Finished renders" className="grid grid-cols-[minmax(0,1fr)_280px] gap-4">
      <div className="flex flex-col gap-2">
        <video key={playId} src={assetUrl(playId)} controls preload="metadata" className="aspect-video w-full rounded-md bg-black" aria-label="Render preview" />
        <div className="flex flex-wrap gap-2">
          <a className={linkClass} href={assetUrl(selected.assetId)} download>
            <Download size={14} aria-hidden="true" /> MP4
          </a>
          {selected.srtAssetId && (
            <a className={linkClass} href={assetUrl(selected.srtAssetId)} download>
              <Download size={14} aria-hidden="true" /> SRT
            </a>
          )}
          <span className="self-center text-xs text-text-2">
            {formatClock(selected.durationMs)} · {encoderLabel(selected.encoder)}
          </span>
        </div>
        <QcSummary report={selected.report} />
      </div>
      <ul aria-label="Render history" className="flex flex-col gap-1">
        {renders.map((r) => (
          <li key={r.id}>
            <button
              type="button"
              aria-pressed={r.id === selected.id}
              onClick={() => setSelectedId(r.id)}
              className={`flex w-full items-center justify-between rounded-md border px-2 py-1.5 text-left text-xs ${r.id === selected.id ? "border-primary bg-primary-muted" : "border-border hover:bg-accent"}`}
            >
              <span>{new Date(r.createdAt).toLocaleString()}</span>
              <span className={r.report.passed ? "text-success" : "text-destructive"}>{r.report.passed ? "QC ok" : "QC failed"}</span>
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}
