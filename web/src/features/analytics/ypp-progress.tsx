import type { YppProgress } from "../../api/gen/types.gen";
import { formatCount, formatDay, formatHours, NOT_AVAILABLE } from "./analytics-format";

function ProgressBar({ label, value, target, text, note }: { label: string; value: number; target: number; text: string; note?: string }) {
  const percent = Math.min(100, Math.max(0, (value / target) * 100));
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-baseline justify-between text-sm">
        <span className="text-text-2">{label}</span>
        <span className="font-mono tabular-nums text-foreground">{text}</span>
      </div>
      <div
        role="progressbar"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={target}
        aria-valuenow={Math.min(value, target)}
        className="h-1.5 w-full overflow-hidden rounded-full bg-muted"
      >
        <div className={`h-full rounded-full ${percent >= 100 ? "bg-success" : "bg-primary"}`} style={{ width: `${percent}%` }} />
      </div>
      {note && <span className="text-2xs text-text-2">{note}</span>}
    </div>
  );
}

/** YouTube Partner Program progress: public watch hours over the last 365 days and subscribers. */
export function YppProgressBars({ ypp }: { ypp: YppProgress }) {
  const hoursNote =
    ypp.daysMissing > 0
      ? `${ypp.daysMissing} of the 365 days (${formatDay(ypp.windowFrom)} to ${formatDay(ypp.windowTo)}) have no synced data yet.`
      : `${formatDay(ypp.windowFrom)} to ${formatDay(ypp.windowTo)}`;
  return (
    <div className="flex flex-col gap-3">
      <ProgressBar
        label="Watch hours (365 days)"
        value={ypp.watchHours}
        target={ypp.watchHoursTarget}
        text={`${formatHours(ypp.watchHours)} / ${formatCount(ypp.watchHoursTarget)} h`}
        note={hoursNote}
      />
      {ypp.subscribers === undefined ? (
        <div className="flex items-baseline justify-between text-sm">
          <span className="text-text-2">Subscribers</span>
          <span className="text-text-2">{NOT_AVAILABLE}</span>
        </div>
      ) : (
        <ProgressBar
          label="Subscribers"
          value={ypp.subscribers}
          target={ypp.subscribersTarget}
          text={`${formatCount(ypp.subscribers)} / ${formatCount(ypp.subscribersTarget)}`}
        />
      )}
    </div>
  );
}
