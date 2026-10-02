import type { DbMaintenanceAction } from "@/components/database/schema/types"

/**
 * The ways one maintenance action can be asked for.
 *
 * The server's list says which options an action takes (`options`), by name,
 * from a closed set of four. Each option is a different run of the same
 * action — a reindex that blocks writes and one that does not are not the
 * same request to make of a busy table — so each is offered as its own
 * choice, named for what it changes, with a sentence the confirmation can
 * say. An action with no option is its own one way.
 */
export type MaintenanceVariant = {
  /** Stable among an action's variants. */
  key: string
  /** The choice in the menu and on the confirmation: "Reindex concurrently". */
  label: string
  /** What goes in the request's `options`; absent for the plain run. */
  options?: Record<string, boolean | string>
  /** What this way of running it costs or spares, in a sentence. */
  note?: string
}

const CHECKPOINT_MODES = ["passive", "full", "restart", "truncate"] as const

const CHECKPOINT_NOTES: Record<(typeof CHECKPOINT_MODES)[number], string> = {
  passive: "Writes back what it can without waiting for readers or writers.",
  full: "Waits for writers, then writes the whole log back.",
  restart: "As full, and waits for readers so the next write starts the log again.",
  truncate: "As restart, and empties the log file afterwards.",
}

export function maintenanceVariants(
  action: Pick<DbMaintenanceAction, "id" | "label" | "options">,
): MaintenanceVariant[] {
  const options = action.options ?? []
  if (options.includes("mode")) {
    return CHECKPOINT_MODES.map((mode) => ({
      key: `mode:${mode}`,
      label: `${action.label}: ${mode}`,
      options: { mode },
      note: CHECKPOINT_NOTES[mode],
    }))
  }
  const variants: MaintenanceVariant[] = [{ key: "plain", label: action.label }]
  if (options.includes("concurrently")) {
    variants.push({
      key: "concurrently",
      label: `${action.label} concurrently`,
      options: { concurrently: true },
      note: "Each index is built again beside the old one: writes go on meanwhile, it takes longer, and it needs room for both.",
    })
  }
  if (options.includes("final")) {
    variants.push({
      key: "final",
      label: `${action.label}, final`,
      options: { final: true },
      note: "Every part is merged into one, however long that takes.",
    })
  }
  if (options.includes("online")) {
    variants.push({
      key: "online",
      label: `${action.label} online`,
      options: { online: true },
      note: "The table stays readable and writable while it is rebuilt. Enterprise and Developer editions only: another edition refuses it.",
    })
  }
  return variants
}

/** How long a run has been going, for a line that ticks: "0:07", "12:40", "1:02:03". */
export function elapsed(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const seconds = String(total % 60).padStart(2, "0")
  return hours > 0
    ? `${hours}:${String(minutes).padStart(2, "0")}:${seconds}`
    : `${minutes}:${seconds}`
}
