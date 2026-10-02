"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { plural, relativeTime } from "@/lib/format"
import type { DbFleetEntry } from "@/lib/types"
import { FindingList, type Finding } from "@/components/finding-list"
import { Section } from "@/components/page"
import { Button } from "@/components/ui/button"
import { engineOf, sectionHref } from "@/components/database/engine"
import type { WaitingServer } from "@/components/database/connect/inventory"
import type { Found } from "@/components/database/connect/use-found"
import {
  GROUP_ABOVE,
  concernGroups,
  whereWord,
  type ConcernGroup,
  type ConcernKind,
} from "@/components/database/fleet/fleet"
import type { FleetData } from "@/components/database/fleet/use-fleet"
import type { FleetControl } from "@/components/database/fleet/use-fleet-control"
import { EngineGlyph } from "@/components/database/kit"
import { useDatabases } from "@/components/database/shell/databases-context"

const RANK = { critical: 0, warning: 1, notice: 2 } as const

/**
 * How many findings are drawn before the rest fold behind a count — and how
 * many more than that it takes to fold any: a fold that hides one row costs
 * the reader a press to save a line.
 */
const SHOWN = 6
const FOLD_PAST = SHOWN + 2

type Fix = { label: string; onClick: () => void }

/** What a concern shared by several databases is called, and what it means. */
const SHARED: Record<
  ConcernKind,
  { title: (n: number) => string; detail: string; advice?: string }
> = {
  broken: {
    title: (n) => `${n} saved connections cannot be opened`,
    detail: "Their stored connection strings can no longer be read.",
    advice: "Give each a new connection string under its Settings, or forget it.",
  },
  unreachable: {
    title: (n) => `${n} databases cannot be reached`,
    detail: "The dashboard could not sign in to them; each says what its server answered.",
    advice: "Check that each server is running, then test its stored password under Settings.",
  },
  stopped: {
    title: (n) => `${n} databases are stopped while deployments use them`,
    detail: "They are not running, so everything bound to them has no database.",
  },
  paused: {
    title: (n) => `${n} databases are paused`,
    detail: "A paused container holds its memory and answers nothing.",
    advice: "Unpause each container, or stop it.",
  },
  public: {
    title: (n) => `${n} databases are reachable from the internet`,
    detail:
      "Their ports are published on every interface and the firewall lets them through, so a password is all that protects the data.",
    advice: "Restrict each to this server if nothing elsewhere needs it.",
  },
  "never-backed-up": {
    title: (n) => `${n} databases have never been backed up`,
    detail:
      "No dump is on this server. A database whose only copy is the live one is one disk failure from gone.",
    advice: "Take a dump now, or add them to a scheduled backup job under Backups.",
  },
  "stale-backup": {
    title: (n) => `${n} databases were last backed up over a week ago`,
    detail: "Their newest dumps on this server protect last week's data, not this week's.",
    advice: "Take a dump now, or add them to a scheduled backup job under Backups.",
  },
}

/**
 * What needs a hand, each with its fix as the action: a database that cannot
 * be reached, one that is stopped while a deployment depends on it, a port
 * open to the internet, a database nothing has dumped, a server found running
 * that waits for a password.
 *
 * Every concern a database has is listed — a public port does not hide a
 * missing backup behind it. What keeps the list short on a server with forty
 * databases is that it is a list of what is wrong, not of what it is wrong
 * with: more than two databases sharing a concern are one finding that names
 * them, each with its own fix inside it, and past six findings the rest wait
 * behind a count. The section is not drawn when there is nothing in it.
 */
export function Attention({
  data,
  control,
  found,
  waiting,
}: {
  data: FleetData
  control: FleetControl
  /** Connecting what discovery found; absent for a role that cannot. */
  found?: Found
  /** The servers found running here that wait for a password. */
  waiting: WaitingServer[]
}) {
  const router = useRouter()
  const { engineFor, drivers, newHref } = useDatabases()
  const [all, setAll] = useState(false)
  const findings: Finding[] = []

  const settings = (entry: DbFleetEntry): Fix => ({
    label: "Open settings",
    onClick: () => router.push(sectionHref(entry.id, "settings")),
  })
  const backUp = (entry: DbFleetEntry): Fix =>
    control.canBackup(entry)
      ? { label: "Back up now", onClick: () => void control.backup(entry) }
      : { label: "Open backups", onClick: () => router.push(sectionHref(entry.id, "backups")) }

  /** The one thing to do about a concern of one database. */
  const fixOf = (kind: ConcernKind, entry: DbFleetEntry): Fix => {
    switch (kind) {
      case "stopped":
        return control.canStart(entry)
          ? { label: "Start", onClick: () => control.power(entry, "start") }
          : settings(entry)
      case "paused":
        return entry.container
          ? {
              label: "Open container",
              onClick: () =>
                router.push(`/docker/containers/${encodeURIComponent(entry.container ?? "")}`),
            }
          : settings(entry)
      case "public":
        return control.canRestrict(entry)
          ? { label: "Restrict to this server", onClick: () => control.restrict(entry) }
          : settings(entry)
      case "never-backed-up":
      case "stale-backup":
        return backUp(entry)
      default:
        return settings(entry)
    }
  }

  /** What one database's line in a shared finding says beside its name. */
  const factOf = (kind: ConcernKind, entry: DbFleetEntry): string | undefined => {
    switch (kind) {
      case "broken":
        return entry.brokenReason ?? entry.error
      case "unreachable":
        return entry.error
      case "stopped":
        return `${plural(entry.consumers, "environment")} bound`
      case "stale-backup":
        return `backed up ${relativeTime(data.backups.get(entry.id)?.newest)}`
      case "never-backed-up":
        return undefined
      default:
        return whereWord(entry).text
    }
  }

  const meta = (entry: DbFleetEntry) => {
    const engine = engineFor(entry)
    return (
      <span className="flex items-center gap-1.5">
        <EngineGlyph engine={engine} />
        {engine.label}
        {entry.readOnly ? " · protected" : ""}
      </span>
    )
  }

  const single = (kind: ConcernKind, entry: DbFleetEntry): Finding => {
    const where = whereWord(entry).text
    const base = { id: `${kind}-${entry.id}`, meta: meta(entry), action: fixOf(kind, entry) }
    switch (kind) {
      case "broken":
        return {
          ...base,
          level: "critical",
          title: `${entry.name} cannot be opened`,
          detail:
            entry.brokenReason ?? entry.error ?? "The saved connection can no longer be read.",
          advice: "Give it a new connection string under Settings, or forget it.",
        }
      case "unreachable":
        return {
          ...base,
          level: "critical",
          title: `${entry.name} cannot be reached`,
          detail: `The dashboard could not sign in: ${entry.error ?? "the server did not answer"}.`,
          advice:
            "Check that the server is running, then test the stored password under Settings or set a new one.",
        }
      case "stopped":
        return {
          ...base,
          level: "warning",
          title: `${entry.name} is stopped and ${plural(entry.consumers, "deployment environment")} ${entry.consumers === 1 ? "uses" : "use"} it`,
          detail: `${where} is not running, so everything bound to it has no database.`,
        }
      case "paused":
        return {
          ...base,
          level: "warning",
          title: `${entry.name} is paused`,
          detail: `${where} is paused: it holds its memory and answers nothing.`,
          advice: "Unpause its container, or stop it.",
        }
      case "public":
        return {
          ...base,
          level: "warning",
          title: `${entry.name} is reachable from the internet`,
          detail:
            "Its port is published on every interface and the firewall lets it through, so the password is all that protects the data.",
          advice: control.canRestrict(entry)
            ? "Restrict it to this server if nothing elsewhere needs it."
            : "Change where it listens where it is declared; its Settings say how it is reached.",
        }
      case "never-backed-up":
        return {
          ...base,
          level: "warning",
          title: `${entry.name} has never been backed up`,
          detail:
            "No dump is on this server. A database whose only copy is the live one is one disk failure from gone.",
          advice: "Take a dump now, or add it to a scheduled backup job under Backups.",
        }
      case "stale-backup":
        return {
          ...base,
          level: "warning",
          title: `${entry.name} was last backed up ${relativeTime(data.backups.get(entry.id)?.newest)}`,
          detail: "Its newest dump on this server is over a week old.",
          advice: "Take a dump now, or add it to a scheduled backup job under Backups.",
        }
    }
  }

  const shared = (group: ConcernGroup): Finding => {
    const words = SHARED[group.kind]
    const backups = group.kind === "never-backed-up" || group.kind === "stale-backup"
    const able = backups ? group.entries.filter(control.canBackup) : []
    const startable = group.kind === "stopped" ? group.entries.filter(control.canStart) : []
    return {
      id: group.kind,
      level: group.level,
      title: words.title(group.entries.length),
      detail: words.detail,
      advice: words.advice,
      meta: group.entries.map((entry) => entry.name).join(", "),
      extra: (
        <ul className="divide-y divide-hairline" aria-label={words.title(group.entries.length)}>
          {group.entries.map((entry) => {
            const fix = fixOf(group.kind, entry)
            const fact = factOf(group.kind, entry)
            const busy = control.busyWord(entry)
            return (
              <li key={entry.id} className="flex min-w-0 items-center gap-2 py-1.5">
                <EngineGlyph engine={engineFor(entry)} />
                <span className="max-w-[45%] min-w-0 shrink-0 truncate pr-0.5 font-medium text-foreground">
                  {entry.name}
                </span>
                <span className="min-w-0 flex-1 truncate text-hint" title={fact}>
                  {fact}
                </span>
                {busy ? (
                  <span className="shrink-0 text-hint">{busy}…</span>
                ) : (
                  <Button
                    size="xs"
                    variant="ghost"
                    className="shrink-0 text-foreground"
                    aria-label={`${fix.label}: ${entry.name}`}
                    onClick={fix.onClick}
                  >
                    {fix.label}
                  </Button>
                )}
              </li>
            )
          })}
        </ul>
      ),
      // One press for all of them only where the fix asks nothing first: a
      // dump, a start. Restricting a port and stopping a server each confirm.
      action:
        able.length > 1
          ? {
              label: `Back up all ${able.length}`,
              onClick: () => able.forEach((entry) => void control.backup(entry)),
            }
          : startable.length > 1
            ? {
                label: `Start all ${startable.length}`,
                onClick: () => startable.forEach((entry) => control.power(entry, "start")),
              }
            : undefined,
    }
  }

  for (const group of concernGroups(data.entries, data.concernsOf)) {
    if (group.grouped) findings.push(shared(group))
    else for (const entry of group.entries) findings.push(single(group.kind, entry))
  }

  if (found && waiting.length > 0) {
    const connect = (server: WaitingServer): Fix => {
      switch (server.via.kind) {
        case "instance": {
          const instance = server.via.instance
          return { label: "Connect", onClick: () => found.ask(instance) }
        }
        case "host": {
          const host = server.via.server
          return { label: "Connect", onClick: () => found.askHost(host) }
        }
        case "list":
          return {
            label: "Open what was found",
            onClick: () => router.push(newHref({ mode: "found" })),
          }
      }
    }
    const mark = (server: WaitingServer) => {
      const engine = engineOf(server.engine, drivers)
      return (
        <span className="flex items-center gap-1.5">
          <EngineGlyph engine={engine} />
          {engine.label}
        </span>
      )
    }
    if (waiting.length > GROUP_ABOVE) {
      findings.push({
        id: "found",
        level: "notice",
        title: `${waiting.length} servers are running here and wait for a password to be connected`,
        detail:
          "They were found on this machine, and nothing on it states the password each one uses.",
        meta: waiting.map((server) => server.name).join(", "),
        extra: (
          <ul className="divide-y divide-hairline" aria-label="Servers waiting for a password">
            {waiting.map((server) => {
              const fix = connect(server)
              return (
                <li key={server.id} className="flex min-w-0 items-center gap-2 py-1.5">
                  <EngineGlyph engine={engineOf(server.engine, drivers)} />
                  <span className="max-w-[45%] min-w-0 shrink-0 truncate pr-0.5 font-medium text-foreground">
                    {server.name}
                  </span>
                  <span className="min-w-0 flex-1 truncate text-hint" title={server.reason}>
                    {server.reason}
                  </span>
                  <Button
                    size="xs"
                    variant="ghost"
                    className="shrink-0 text-foreground"
                    aria-label={`${fix.label}: ${server.name}`}
                    onClick={fix.onClick}
                  >
                    {fix.label}
                  </Button>
                </li>
              )
            })}
          </ul>
        ),
      })
    } else {
      for (const server of waiting) {
        findings.push({
          id: `found-${server.id}`,
          level: "notice",
          title: `${server.name} is running here and needs a password to be connected`,
          detail: server.reason,
          advice:
            server.via.kind === "instance" && server.via.instance.credentials === "peer"
              ? "Give it a password you know, or let the dashboard make an account for itself from this machine."
              : "Connect it with the password it uses.",
          meta: mark(server),
          action: connect(server),
        })
      }
    }
  }

  if (findings.length === 0) return null
  findings.sort((a, b) => RANK[a.level] - RANK[b.level])
  const folds = findings.length > FOLD_PAST
  const folded = folds && !all ? findings.length - SHOWN : 0
  return (
    <Section title="Needs attention" className="animate-rise">
      <div className="flex min-w-0 flex-col gap-1.5">
        <FindingList findings={folded > 0 ? findings.slice(0, SHOWN) : findings} />
        {folds && (
          <Button
            size="xs"
            variant="ghost"
            className="self-start text-muted-foreground"
            aria-expanded={all}
            onClick={() => setAll((open) => !open)}
          >
            {all ? "Show fewer" : `Show ${folded} more`}
          </Button>
        )}
      </div>
    </Section>
  )
}
