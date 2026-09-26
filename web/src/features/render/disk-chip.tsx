import { HardDrive } from "lucide-react";

import type { DiskStatus } from "../../api/gen/types.gen";
import { formatBytes } from "../../lib/format";

/** Disk chip: warning below the warn watermark, blocked below the minimum, hidden while space is fine. */
export function DiskChip({ disk }: { disk: DiskStatus }) {
  if (disk.level === "ok") return null;
  const tone = disk.level === "blocked" ? "border-destructive/40 text-destructive" : "border-warning/40 text-warning";
  const text = disk.level === "unknown" ? "Disk space unknown" : `${formatBytes(disk.freeBytes)} free`;
  return (
    <span className={`inline-flex h-7 w-fit items-center gap-1.5 rounded-md border px-2 text-xs ${tone}`} title={disk.message}>
      <HardDrive size={14} aria-hidden="true" />
      {text}
    </span>
  );
}
