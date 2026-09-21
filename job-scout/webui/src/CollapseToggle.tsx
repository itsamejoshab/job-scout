import { ChevronDown } from "lucide-react";
import type { ReactNode } from "react";

import { cn } from "./lib/utils";

export function CollapseToggle({
  open,
  controls,
  onToggle,
  children,
}: {
  open: boolean;
  controls: string;
  onToggle: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      className="flex min-w-0 flex-1 items-center gap-2 rounded-md py-0.5 text-left"
      aria-expanded={open}
      aria-controls={controls}
      onClick={onToggle}
    >
      <ChevronDown
        className={cn("h-4 w-4 shrink-0 transition-transform", open && "rotate-180")}
        aria-hidden="true"
      />
      {children}
    </button>
  );
}
