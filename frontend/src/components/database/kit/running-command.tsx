"use client"

import { StopCircle } from "@/components/icons"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"

/**
 * The one way to stop a read that is out, on every query surface of a
 * database: SQL's editor and search, MongoDB's query bar, pipeline and schema
 * analysis. It takes the command's own place while the work is in flight, so
 * the reader's eye is already on it.
 *
 * The act had two verbs — "Stop" on MongoDB, "Cancel" with a shimmer on SQL —
 * and a verb is one word (D14): it is "Stop", and "Cancel" is left to the
 * dialogs, where it means closing without doing anything. `seconds` counts up
 * inside the button where nothing else on the surface says how long it has
 * been; SQL's status line says it beside the button instead.
 */
export function RunningCommand({
  seconds,
  onStop,
  stopping,
  size = "sm",
  className,
}: {
  seconds?: number
  onStop: () => void
  /** The stop was asked for and the server has not answered yet. */
  stopping?: boolean
  size?: "xs" | "sm"
  className?: string
}) {
  return (
    <Button
      type="button"
      size={size}
      variant="outline"
      className={cn(className)}
      disabled={stopping}
      onClick={onStop}
    >
      <StopCircle />
      Stop
      {seconds !== undefined && (
        <span className="numeric text-muted-foreground" aria-live="off">
          {seconds} s
        </span>
      )}
    </Button>
  )
}
