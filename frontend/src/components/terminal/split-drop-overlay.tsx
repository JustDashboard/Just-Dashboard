import { cn } from "@/lib/utils"
import type { SplitDirection } from "@/lib/terminal-layout"

const sides = {
  left: "inset-y-0 left-0 w-1/2",
  right: "inset-y-0 right-0 w-1/2",
  up: "inset-x-0 top-0 h-1/2",
  down: "inset-x-0 bottom-0 h-1/2",
}
const labels = { left: "left", right: "right", up: "above", down: "below" }

export function SplitDropOverlay({
  direction,
  blocked,
}: {
  direction: SplitDirection
  blocked: boolean
}) {
  return (
    <div
      data-terminal-drop={direction}
      data-drop-blocked={blocked}
      className={cn(
        "pointer-events-none absolute z-30 flex items-center justify-center rounded-md border-2 p-2",
        sides[direction],
        blocked ? "border-rule-danger bg-wash-danger" : "border-ring bg-wash-brand",
      )}
    >
      <span
        role="status"
        className="rounded-md border bg-popover px-3 py-2 text-center text-xs font-medium text-popover-foreground"
      >
        {blocked ? "Not enough space to split here" : `Drop to split ${labels[direction]}`}
      </span>
    </div>
  )
}
