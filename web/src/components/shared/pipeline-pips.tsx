import * as TooltipPrimitive from "@radix-ui/react-tooltip";

import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { type EntityState, StatusChip } from "./status-chip";

export interface PipelinePip {
  key: string;
  label: string;
  state: EntityState;
  detail?: string;
  staleReason?: string;
}

/** `TXT IMG VOI SUB MOT` row from the storyboard scene tile (guidelines §7). */
export function PipelinePips({ pips }: { pips: PipelinePip[] }) {
  return (
    <TooltipPrimitive.Provider delayDuration={300}>
      <ul className="flex flex-wrap items-center gap-1 overflow-hidden" aria-label="Pipeline steps">
        {pips.map((pip) => (
          <li key={pip.key}>
            {pip.staleReason ? (
              <Tooltip>
                <TooltipTrigger asChild>
                  <span>
                    <StatusChip state={pip.state} label={pip.label} detail={pip.detail} />
                  </span>
                </TooltipTrigger>
                <TooltipContent>{pip.staleReason}</TooltipContent>
              </Tooltip>
            ) : (
              <StatusChip state={pip.state} label={pip.label} detail={pip.detail} />
            )}
          </li>
        ))}
      </ul>
    </TooltipPrimitive.Provider>
  );
}
