"use client"

import { cn } from "@/lib/utils"
import type { ComposeStack, StackState } from "@/lib/types"
import { Status, type DotTone } from "@/components/status-dot"

/**
 * A stack's state as a word, because "0/0 up" is not a state.
 *
 * The fraction was counting containers on both sides of the slash, so a stack
 * that existed only as a compose file — never deployed, or brought down —
 * reported zero of zero and read as a bug. The six states below are the ones
 * that actually differ in what an operator should do about them, and the
 * sentence beside each comes from the server so every surface says the same
 * thing about the same stack.
 */

const STATE: Record<StackState, { label: string; tone: DotTone }> = {
  running: { label: "Running", tone: "running" },
  partial: { label: "Partially running", tone: "warning" },
  degraded: { label: "Degraded", tone: "warning" },
  stopped: { label: "Stopped", tone: "stopped" },
  "not-deployed": { label: "Not deployed", tone: "unknown" },
  unknown: { label: "Unknown", tone: "unknown" },
}

/** A stack's state as the one tone its dot and word are drawn in. */
export function stackTone(state: StackState): DotTone {
  return (STATE[state] ?? STATE.unknown).tone
}

export function StackStateBadge({
  stack,
  className,
}: {
  stack: Pick<ComposeStack, "state" | "running" | "total" | "summary">
  className?: string
}) {
  const meta = STATE[stack.state] ?? STATE.unknown
  return (
    <span className={cn("flex shrink-0 items-center gap-2", className)}>
      <Status tone={meta.tone} label={meta.label} />
      {stack.total > 0 && (
        <span className="numeric text-hint whitespace-nowrap text-muted-foreground">
          {stack.running}/{stack.total} services
        </span>
      )}
    </span>
  )
}

/** The full sentence, for a card header or a detail panel. */
export function StackSummary({ stack, className }: { stack: ComposeStack; className?: string }) {
  return <span className={cn("text-hint text-muted-foreground", className)}>{stack.summary}</span>
}

/**
 * Compose's own vocabulary, translated — and kept, because an operator who
 * knows compose should be able to see what is actually being run.
 *
 * `up`, `down` and `recreate` are precise and mean nothing to somebody who has
 * not read the compose reference; worse, `down` sounds like the opposite of
 * `up` and is not — it removes the containers and the project network. The
 * label is what the button says, the `command` is what runs, and `blastRadius`
 * is what it does to things that exist, which is the sentence that belongs in
 * the confirmation.
 */
export const COMPOSE_ACTIONS = {
  up: {
    label: "Deploy",
    command: "docker compose up -d --remove-orphans",
    blastRadius:
      "Creates or replaces whatever has changed and starts it. Services already running with an unchanged configuration are left alone. Named volumes are untouched.",
  },
  start: {
    label: "Start",
    command: "docker compose start",
    blastRadius:
      "Starts the containers that already exist. Nothing is created, replaced or reconfigured.",
  },
  pull: {
    label: "Pull images",
    command: "docker compose pull",
    blastRadius:
      "Downloads newer images. Nothing running changes until you deploy — this only puts the images on the server.",
  },
  build: {
    label: "Rebuild images",
    command: "docker compose build",
    blastRadius:
      "Rebuilds the images this stack builds from source. Nothing running changes until you deploy.",
  },
  restart: {
    label: "Restart",
    command: "docker compose restart",
    blastRadius:
      "Stops and starts each container. The service is interrupted for as long as it takes to come back; nothing is recreated and no configuration change is applied.",
  },
  update: {
    label: "Pull & redeploy",
    command: "docker compose pull && docker compose up -d",
    blastRadius:
      "Downloads newer images and replaces the containers using them. Data written inside a container rather than into a volume is lost; named volumes are untouched.",
  },
  recreate: {
    label: "Rebuild & redeploy",
    command: "docker compose up -d --force-recreate --build",
    blastRadius:
      "Rebuilds the images and replaces every container, whether or not anything changed. Data written inside a container is lost; named volumes are untouched.",
  },
  stop: {
    label: "Stop",
    command: "docker compose stop",
    blastRadius:
      "Stops every container and leaves them in place. Nothing is deleted and starting again is one click.",
  },
  down: {
    label: "Stop & remove stack",
    command: "docker compose down",
    blastRadius:
      "Stops and deletes the stack's containers and its project network. Named volumes are preserved — this command is not given -v — so the data survives and a deploy brings the stack back.",
  },
} as const

export type ComposeActionKey = keyof typeof COMPOSE_ACTIONS
