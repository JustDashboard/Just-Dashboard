"use client"

import Link from "next/link"
import { LANES, hueFor } from "@/lib/hue"
import { cn } from "@/lib/utils"
import { Fact } from "@/components/fact-line"
import { HostIdentity } from "@/components/identity-header"
import { Button } from "@/components/ui/button"
import { BorderBeam } from "@/components/ui/border-beam"
import { EngineMark } from "@/components/database/kit/engine-mark"
import { EnvironmentTag, ProtectedTag } from "@/components/database/kit/environment-tag"
import { usePowerChange } from "@/components/database/home/verbs"
import { ConnectPopover } from "@/components/database/shell/connect-popover"
import { ConnectionSwitcher } from "@/components/database/shell/connection-switcher"
import { useDatabase } from "@/components/database/shell/database-context"
import { DatabaseVerbs } from "@/components/database/shell/database-verbs"
import { connectionFacts } from "@/components/database/shell/facts"
import { DatabaseStatusMark } from "@/components/database/shell/status"
import { supportFact } from "@/components/database/support"

/**
 * The header a database's pages open on, laid out as a project's is
 * (`HostIdentity` with a state, a command and a menu): the engine as the mark,
 * the name — which is also the control that goes to another database — with
 * whether it answers after it, then one line of what it is: the engine and
 * until when its release is maintained, where it runs, the address it is
 * dialled at, the database on that server and the account it is dialled as.
 * At the far end stands the one command, opening what the database holds in
 * the engine's own word, with Connect as the one outline verb beside it and
 * every other verb in the menu.
 *
 * Query stood there as a third button. It is a page of the database, in the
 * rail under Work, and a word in the menu now (`useOpenVerbs`): a header of
 * three equal commands had no first one.
 *
 * How long the server has been up is not here. It is read from the server's
 * statistics, which only the home asks for, and it arrived a moment after
 * the line was drawn: on a narrow line it wrapped the facts and pushed the
 * page down. It is a fact of what the database runs as, on that line of the
 * home.
 *
 * While the server is being started, stopped or restarted a beam runs round
 * the engine's tile (§11 *in flight*), the status gives way — the notice on
 * the home says what is happening — and the command that opens the
 * database's contents waits: there is nothing behind it to open.
 */
export function DatabaseHeader({ className }: { className?: string }) {
  const { conn, summary, engine, status, readOnly } = useDatabase()
  const change = usePowerChange(conn.id)
  const where = runsAs(summary)
  const facts = connectionFacts(conn, engine, summary)
  const eol = summary?.endOfLife
  const support = eol && (
    <Fact key="support" className={cn(eol.past && "text-warning")}>
      {supportFact(eol)}
    </Fact>
  )

  return (
    <HostIdentity
      label={`About ${conn.name}`}
      className={className}
      // The name is read whole and the end holds three controls, so on a
      // narrow line they stand under the facts.
      foot
      logo={
        <span className="relative flex shrink-0 rounded-lg">
          <EngineMark engine={engine} />
          {change && (
            <span aria-hidden className="pointer-events-none absolute -inset-px rounded-lg">
              <BorderBeam size={24} duration={3} />
            </span>
          )}
        </span>
      }
      // The title's box truncates, which clips a ring drawn outside it.
      title={<ConnectionSwitcher inset className="text-title font-semibold tracking-tight" />}
      state={
        <>
          {change ? (
            // The change is said once, in the notice under the line. What is
            // kept here is the status's room, so the row it stands on does
            // not close for as long as the change lasts.
            <span aria-hidden className="h-4 w-px shrink-0" />
          ) : (
            <DatabaseStatusMark status={status} />
          )}
          {status.state === "running" && !change && Boolean(status.latencyMs) && (
            <span className="numeric text-hint text-muted-foreground">
              answers in {status.latencyMs} ms
            </span>
          )}
          <EnvironmentTag environment={conn.environment} />
          {readOnly && <ProtectedTag />}
        </>
      }
      facts={
        <>
          {/* A release past its end of life is the one fact here that asks
              for something, so it leads the line, in the warning ink: on a
              phone the line is one row and what does not fit is cut. */}
          {eol?.past && support}
          {facts.map((fact) => [
            <Fact
              key={fact.key}
              title={fact.title}
              mono={fact.mono}
              className={cn(fact.key === "engine" && "text-foreground")}
            >
              {fact.text}
            </Fact>,
            // Said of the version, beside it, as a fact: no colour, no
            // badge, nothing to press.
            fact.key === "engine" && !eol?.past && support,
            fact.key === "engine" && where && (
              <Fact key="where" product={where.product} mono={where.mono} title={where.title}>
                {where.text}
              </Fact>
            ),
          ])}
          {conn.user && (
            <Fact key="user">
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
            </Fact>
          )}
        </>
      }
      command={
        <>
          <OpenContents />
          <ConnectPopover />
        </>
      }
      menu={<DatabaseVerbs />}
    />
  )
}

/**
 * The header's one command: what the database holds, opened, in the engine's
 * own word ("Open data", "Browse keys"). Drawn while the server runs, and held
 * while a start or a stop is under way: there is nothing behind it to open.
 * `compact` is the item pages' strip: an outline button, because the item's
 * own line under it holds the page's one brand-faced command, and the glyph
 * alone on a phone.
 */
export function OpenContents({ compact }: { compact?: boolean }) {
  const { conn, engine, status, href } = useDatabase()
  const change = usePowerChange(conn.id)
  const data = engine.section("data")
  if (status.state !== "running" || !data) return null
  const verb = engine.kind === "keyvalue" ? "Browse" : "Open"
  const label = `${verb} ${data.title.toLowerCase()}`
  const face = (
    <>
      <data.icon />
      <span className={cn(compact && "max-sm:sr-only")}>{label}</span>
    </>
  )
  const variant = compact ? "outline" : "default"
  return change ? (
    <Button size="sm" variant={variant} disabled aria-label={label}>
      {face}
    </Button>
  ) : (
    <Button size="sm" variant={variant} asChild>
      <Link href={href("data")} aria-label={label}>
        {face}
      </Link>
    </Button>
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
