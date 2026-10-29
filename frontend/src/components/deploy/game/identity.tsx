"use client"

import { useState } from "react"
import Link from "next/link"
import { Copy } from "@/components/icons"
import { get, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import type { GameOverview, GameReach } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { FormNote } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Meter } from "@/components/meter"
import { FactDot, HostFact } from "@/components/metrics/host-identity"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
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
export function GameIdentity({
  overview,
  projectId,
  onChanged,
}: {
  overview: GameOverview
  /** Where the reach note links to the Runtime settings. */
  projectId?: number
  /** Asks the overview again once the firewall admits the port. */
  onChanged?: () => void
}) {
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
  if (!overview.address && !overview.edition && !aside && !overview.reach) return null

  return (
    <div className="min-w-0 space-y-1.5">
      <GameIdentityLine overview={overview} aside={aside} />
      {overview.reach && (
        <ReachNote reach={overview.reach} projectId={projectId} onChanged={onChanged} />
      )}
    </div>
  )
}

function GameIdentityLine({ overview, aside }: { overview: GameOverview; aside: React.ReactNode }) {
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

/**
 * What stands between a player and the server, said only when something
 * does. A game starts on loopback, which is the operator's to change under
 * Runtime; once it is on every interface the one thing left is the
 * firewall, and an administrator can allow the port from here through the
 * firewall's own rule route, which reviews and audits it like any other rule.
 * A server players can already reach says nothing: the address above is the
 * whole answer.
 */
function ReachNote({
  reach,
  projectId,
  onChanged,
}: {
  reach: GameReach
  projectId?: number
  onChanged?: () => void
}) {
  const { can } = useAuth()
  const [opening, setOpening] = useState(false)
  const port = `${reach.port}/${reach.protocol}`
  if (!reach.public)
    return (
      <FormNote>
        Only this server can connect: {port} is published on{" "}
        <span className="font-mono">{reach.bind}</span>. To let players join, choose every interface
        under{" "}
        {projectId ? (
          <Link
            href={`/deploy/${projectId}/settings/runtime`}
            className="rounded-sm underline underline-offset-2 focus-ring hover:text-foreground"
          >
            Runtime
          </Link>
        ) : (
          "Runtime"
        )}{" "}
        and redeploy.
      </FormNote>
    )
  if (reach.firewall === "unknown")
    return (
      <FormNote>
        Published on every interface; whether the firewall admits {port} is unknown
        {reach.reason ? `: ${reach.reason}` : "."}
      </FormNote>
    )
  if (reach.firewall !== "drops") return null
  const allow = async () => {
    setOpening(true)
    try {
      // One rule per protocol, as the Firewall page writes them: a rule with
      // no protocol means something different to each firewall underneath.
      for (const protocol of reach.protocol.split("+")) {
        await post("/firewall/rules", {
          action: "allow",
          direction: "in",
          port: String(reach.port),
          protocol,
          comment: `just-dashboard game ${reach.port}/${protocol}`,
        })
      }
      notify.success(`The firewall now admits ${port}`)
      onChanged?.()
    } catch (error) {
      notify.error(`Could not allow ${port}`, error)
    } finally {
      setOpening(false)
    }
  }
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
      <FormNote tone="warning">The firewall drops {port}, so players cannot connect yet.</FormNote>
      {can("system.admin") && reach.editable ? (
        <Button size="xs" variant="outline" disabled={opening} onClick={() => void allow()}>
          {opening ? "Allowing…" : `Allow ${port}`}
        </Button>
      ) : (
        <Button size="xs" variant="ghost" asChild>
          <Link href="/network/firewall">Firewall</Link>
        </Button>
      )}
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
