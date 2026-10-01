import { cn } from "@/lib/utils"
import { Status, type DotTone } from "@/components/status-dot"
import type { DatabaseStatus } from "@/components/database/shell/database-context"

/**
 * The words and the dot for each thing a server can be found doing. A server
 * that refused or cannot be read is red: it is the one reading here somebody
 * has to act on. One that is stopped was stopped by someone and is grey.
 */
const READING: Record<DatabaseStatus["state"], { label: string; tone: DotTone }> = {
  checking: { label: "checking", tone: "unknown" },
  running: { label: "connected", tone: "running" },
  stopped: { label: "stopped", tone: "stopped" },
  paused: { label: "paused", tone: "warning" },
  unreachable: { label: "unreachable", tone: "danger" },
  broken: { label: "cannot be opened", tone: "danger" },
  unknown: { label: "not checked", tone: "unknown" },
}

/** The dot's tone for a state, for a list that draws the dot alone. */
export function statusTone(state: DatabaseStatus["state"]): DotTone {
  return READING[state].tone
}

/**
 * Whether the database answers, as a dot and a word (§4): the one reading
 * the strip, the home and the switcher share. What the server said when it
 * did not answer is the mark's title, for the pointer; a page that has to act
 * on it prints it in a `Notice`.
 */
export function DatabaseStatusMark({
  status,
  className,
}: {
  status: Pick<DatabaseStatus, "state" | "error">
  className?: string
}) {
  const reading = READING[status.state]
  return (
    // A flex box, not a bare span: a block wrapper keeps a 16px line box and
    // sets the 12px status below the centre line its neighbours sit on.
    <span title={status.error} className={cn("flex shrink-0", className)}>
      <Status tone={reading.tone} label={reading.label} />
    </span>
  )
}
