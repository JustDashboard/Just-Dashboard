"use client"

import { Copy } from "@/components/icons"
import { get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import type { GameOverview } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { IconAction } from "@/components/icon-action"
import { Meter } from "@/components/meter"
import { FactDot, HostFact } from "@/components/metrics/host-identity"
import { Status } from "@/components/status-dot"
import { NumberTicker } from "@/components/ui/number-ticker"

/**
 * The game server as the game reports it — which game, where players join,
 * who is on — read every fifteen seconds. Its console, its players and its
 * settings file each open on it, so the three pages ask the same question the
 * same way.
 */
export function useGameOverview(projectId: number) {
  return usePoll(
    (signal) => get<GameOverview>(`/deploy/${projectId}/game`, undefined, signal),
    15000,
    [projectId],
  )
}

/**
 * What only the game can say about itself, as one line under the project's
 * header: the address a player types, one press from the clipboard, the
 * edition, and how close the server is to turning the next player away.
 *
 * The header above already draws the game as itself and names its template,
 * so this line does not do either again — a second tile and title under the
 * first said the same thing twice (§15).
 */
export function GameIdentity({ overview }: { overview: GameOverview }) {
  const players = overview.players
  const running = overview.status === "available"
  const share = players && players.maximum > 0 ? (players.online / players.maximum) * 100 : 0
  const aside = !running ? (
    <Status tone="warning" label="Not running" />
  ) : (
    players?.supported &&
    players.maximum > 0 && (
      <span className="flex items-center gap-3">
        <Meter
          value={share}
          size="thin"
          tone={share >= 100 ? "danger" : share >= 90 ? "warning" : "default"}
          label="Players online"
          className="w-24 sm:w-32"
        />
        <span className="numeric text-hint whitespace-nowrap text-muted-foreground">
          <span className="font-medium text-foreground">
            <NumberTicker value={players.online} />
          </span>{" "}
          of {players.maximum} online
        </span>
      </span>
    )
  )
  if (!overview.address && !overview.edition && !aside) return null

  return (
    // No rule of its own: it hangs from the header's, and a second hairline
    // here stacked three within a few lines of the block title's below it.
    <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-6 gap-y-2">
      <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
        {overview.address && (
          <span className="inline-flex min-w-0 items-center gap-0.5">
            <HostFact>
              <span className="font-mono text-foreground">{overview.address}</span>
            </HostFact>
            <IconAction
              label="Copy the join address"
              className="size-6"
              onClick={() => void copyText(overview.address!, "Join address copied")}
            >
              <Copy />
            </IconAction>
          </span>
        )}
        {overview.address && overview.edition && <FactDot />}
        {overview.edition && <HostFact>{titleCase(overview.edition)} edition</HostFact>}
      </p>
      {aside && <div className="shrink-0">{aside}</div>}
    </div>
  )
}

/** `java` as "Java", for an edition nothing else names. */
function titleCase(value: string) {
  return value
    .split(/[-_\s]+/)
    .filter(Boolean)
    .map((word) => word[0].toUpperCase() + word.slice(1))
    .join(" ")
}
