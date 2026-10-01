"use client"

import { Fragment } from "react"
import Link from "next/link"
import { duration } from "@/lib/format"
import { cn } from "@/lib/utils"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Button } from "@/components/ui/button"
import { BorderBeam } from "@/components/ui/border-beam"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { EngineMark, EnvironmentTag, ProtectedTag } from "@/components/database/kit"
import { POWER } from "@/components/database/home/power"
import { usePowerChange } from "@/components/database/home/verbs"
import { ConnectPopover } from "@/components/database/shell/connect-popover"
import { ConnectionSwitcher } from "@/components/database/shell/connection-switcher"
import { useDatabase } from "@/components/database/shell/database-context"
import { DatabaseVerbs } from "@/components/database/shell/database-verbs"
import { connectionFacts } from "@/components/database/shell/facts"
import { DatabaseStatusMark } from "@/components/database/shell/status"

/**
 * The line a database's home opens on (`HostIdentity`): the engine as the
 * mark, the name — which is also the control that goes to another database —
 * and what it is, where it runs and how long it has been up. At the line's
 * end: whether it answers and how fast, and the page's commands, of which one
 * wears the brand — opening what the database holds, in the engine's own
 * word.
 *
 * While the server is being started, stopped or restarted the line says so
 * where the status was, and a beam runs round the engine's tile (§11 *in
 * flight*): the request can take minutes, and this is the surface that has to
 * show it is still going.
 */
export function HomeIdentity({
  uptimeSeconds,
}: {
  /** How long the server has been up, where its statistics say. */
  uptimeSeconds?: number
}) {
  const { conn, summary, engine, status, readOnly, href } = useDatabase()
  const change = usePowerChange(conn.id)
  const running = status.state === "running"
  const data = engine.section("data")
  const query = engine.section("query")
  const where = runsAs(summary)

  return (
    <HostIdentity
      logo={
        <span className="relative flex shrink-0 rounded-xl">
          <EngineMark engine={engine} size="lg" />
          {change && (
            <span aria-hidden className="pointer-events-none absolute -inset-px rounded-xl">
              <BorderBeam size={28} duration={3} />
            </span>
          )}
        </span>
      }
      // The title's box truncates, which clips a ring drawn outside it.
      title={<ConnectionSwitcher inset className="text-title font-semibold tracking-tight" />}
      facts={
        <>
          {connectionFacts(conn, engine, summary).map((fact, index) => (
            <Fragment key={fact.key}>
              {index > 0 && <FactDot />}
              <span title={fact.title} className={cn("truncate", fact.mono && "font-mono")}>
                {fact.text}
              </span>
              {fact.key === "engine" && where && (
                <>
                  <FactDot />
                  <HostFact product={where.product}>
                    <span title={where.title} className={cn(where.mono && "font-mono")}>
                      {where.text}
                    </span>
                  </HostFact>
                </>
              )}
            </Fragment>
          ))}
          {conn.user && (
            <>
              <FactDot />
              <span>as {conn.user}</span>
            </>
          )}
          {running && uptimeSeconds !== undefined && (
            <>
              <FactDot />
              <span className="numeric">up {duration(uptimeSeconds)}</span>
            </>
          )}
        </>
      }
      aside={
        // The line gives its end no room to shrink into, so the block states
        // its own ceiling — the window less the gutters, and less the rail
        // once there is one — and its two rows wrap inside that.
        <div className="grid max-w-[calc(100vw-2.5rem)] min-w-0 justify-items-start gap-2 sm:justify-items-end md:max-w-[calc(100vw-20rem)]">
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
            <EnvironmentTag environment={conn.environment} />
            {readOnly && <ProtectedTag />}
            {change ? (
              <span data-slot="database-changing" className="flex text-xs font-medium">
                <TextShimmer>{POWER[change.action].progressive}</TextShimmer>
              </span>
            ) : (
              <DatabaseStatusMark status={status} />
            )}
            {running && !change && Boolean(status.latencyMs) && (
              <span className="numeric text-hint text-muted-foreground">
                answers in {status.latencyMs} ms
              </span>
            )}
          </div>
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            {running && data && (
              <Button size="sm" asChild>
                <Link href={href("data")}>
                  <data.icon />
                  {engine.kind === "keyvalue" ? "Browse" : "Open"} {data.title.toLowerCase()}
                </Link>
              </Button>
            )}
            {running && query && (
              <Button size="sm" variant="outline" asChild>
                <Link href={href("query")} aria-label={query.title}>
                  <query.icon />
                  <span className="max-sm:sr-only">{query.title}</span>
                </Link>
              </Button>
            )}
            <ConnectPopover />
            <DatabaseVerbs />
          </div>
        </div>
      }
    />
  )
}

/** Where the server runs, as one fact after the engine's name. */
function runsAs(summary: ReturnType<typeof useDatabase>["summary"]) {
  if (!summary) return undefined
  if (summary.container) {
    return {
      product: "docker",
      text: summary.container.name,
      title: `Container ${summary.container.name}`,
      mono: true,
    }
  }
  if (summary.unit) {
    return {
      product: undefined,
      text: summary.unit.name,
      title: `systemd unit ${summary.unit.name}`,
      mono: true,
    }
  }
  if (summary.source === "host") return { product: undefined, text: "on this server" }
  if (summary.source === "remote") return { product: undefined, text: "on another machine" }
  // A file's path is the database fact already, and a row that cannot be
  // opened has no place to name.
  return undefined
}
