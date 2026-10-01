"use client"

import Link from "next/link"
import { duration } from "@/lib/format"
import { LANES, hueFor } from "@/lib/hue"
import { cn } from "@/lib/utils"
import { HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Button } from "@/components/ui/button"
import { BorderBeam } from "@/components/ui/border-beam"
import { EngineMark, EnvironmentTag, ProtectedTag } from "@/components/database/kit"
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
 * and what it is, where it runs, who it is dialled as and how long it has
 * been up. At the line's end: whether it answers and how fast, and the page's
 * commands, of which one wears the brand — opening what the database holds,
 * in the engine's own word.
 *
 * The line measures itself, not the window (§12): the rail takes a column of
 * its own, so a laptop's worth of window is a tablet's worth of line. Where
 * the line has room, the commands stand at its end under the status; where it
 * has not, they take a row of their own under the name. Left to share one row
 * at any width, the commands kept theirs and the name was cut to a letter
 * with its facts stacked a word to a line beside it.
 *
 * While the server is being started, stopped or restarted a beam runs round
 * the engine's tile (§11 *in flight*), the status gives way to the notice
 * under the line that says what is happening, and the two commands that open
 * the database's contents wait: there is nothing behind them to open.
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
  const facts = connectionFacts(conn, engine, summary)

  return (
    <div className="@container min-w-0">
      <HostIdentity
        // Under 56rem of line the end takes the whole of a second row.
        className="@max-4xl:[&>div:last-child]:basis-full"
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
          <Facts>
            {facts.map((fact) => [
              <Fact key={fact.key}>
                <span title={fact.title} className={cn("truncate", fact.mono && "font-mono")}>
                  {fact.text}
                </span>
              </Fact>,
              fact.key === "engine" && where && (
                <Fact key="where">
                  <HostFact product={where.product}>
                    <span title={where.title} className={cn(where.mono && "font-mono")}>
                      {where.text}
                    </span>
                  </HostFact>
                </Fact>
              ),
            ])}
            {conn.user && (
              <Fact>
                <span className="truncate">
                  as{" "}
                  {/* An account's name keeps one colour wherever the section
                      prints it (§14): here, among the server's databases'
                      owners, and in the session and access lists. */}
                  <span
                    className="font-medium"
                    style={{ color: hueFor(conn.user.toLowerCase(), LANES) }}
                  >
                    {conn.user}
                  </span>
                </span>
              </Fact>
            )}
            {running && uptimeSeconds !== undefined && (
              <Fact>
                <span className="numeric truncate">up {duration(uptimeSeconds)}</span>
              </Fact>
            )}
          </Facts>
        }
        aside={
          <div className="flex min-w-0 flex-1 flex-wrap items-center justify-between gap-x-4 gap-y-2 @4xl:flex-col @4xl:flex-nowrap @4xl:items-end @4xl:justify-center">
            {/* A row of its own height whether or not it holds a status, so
                the commands do not move when a change takes the status away. */}
            <div className="order-2 flex min-h-5 min-w-0 flex-wrap items-center gap-x-3 gap-y-1 @4xl:order-1">
              <EnvironmentTag environment={conn.environment} />
              {readOnly && <ProtectedTag />}
              {!change && <DatabaseStatusMark status={status} />}
              {running && !change && Boolean(status.latencyMs) && (
                <span className="numeric text-hint text-muted-foreground">
                  answers in {status.latencyMs} ms
                </span>
              )}
            </div>
            <div className="order-1 flex min-w-0 flex-wrap items-center gap-1.5 @4xl:order-2">
              {running &&
                data &&
                (change ? (
                  <Button size="sm" disabled>
                    <data.icon />
                    {engine.kind === "keyvalue" ? "Browse" : "Open"} {data.title.toLowerCase()}
                  </Button>
                ) : (
                  <Button size="sm" asChild>
                    <Link href={href("data")}>
                      <data.icon />
                      {engine.kind === "keyvalue" ? "Browse" : "Open"} {data.title.toLowerCase()}
                    </Link>
                  </Button>
                ))}
              {running &&
                query &&
                (change ? (
                  <Button size="sm" variant="outline" disabled aria-label={query.title}>
                    <query.icon />
                    <span className="max-sm:sr-only">{query.title}</span>
                  </Button>
                ) : (
                  <Button size="sm" variant="outline" asChild>
                    <Link href={href("query")} aria-label={query.title}>
                      <query.icon />
                      <span className="max-sm:sr-only">{query.title}</span>
                    </Link>
                  </Button>
                ))}
              <ConnectPopover />
              <DatabaseVerbs />
            </div>
          </div>
        }
      />
    </div>
  )
}

/**
 * The run of facts under the name, with a dot between two of them and none
 * at either end of a line.
 *
 * Every fact carries its dot before it, and the run is pulled left by a
 * dot's width inside a box that clips: the dot of whichever fact starts a
 * line falls outside the box. Dots laid between the facts as items of their
 * own were left hanging at a line's end wherever the run wrapped.
 */
function Facts({ children }: { children: React.ReactNode }) {
  return (
    <span className="flex min-w-0 flex-1 overflow-hidden">
      <span className="-ml-4 flex min-w-0 flex-1 flex-wrap items-center gap-y-1">{children}</span>
    </span>
  )
}

function Fact({ children }: { children: React.ReactNode }) {
  return (
    <span className="inline-flex max-w-full min-w-0 items-center">
      <span aria-hidden className="w-4 shrink-0 text-center text-muted-foreground/40">
        ·
      </span>
      {children}
    </span>
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
