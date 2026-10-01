"use client"

import { useRouter } from "next/navigation"
import { plural, relativeTime } from "@/lib/format"
import type { DbFleetEntry } from "@/lib/types"
import { FindingList, type Finding } from "@/components/finding-list"
import { Section } from "@/components/page"
import { Button } from "@/components/ui/button"
import { sectionHref } from "@/components/database/engine"
import { needsPassword } from "@/components/database/connect/inventory"
import type { Found } from "@/components/database/connect/use-found"
import { whereWord } from "@/components/database/fleet/fleet"
import type { DbInstance } from "@/components/database/fleet/types"
import type { FleetData } from "@/components/database/fleet/use-fleet"
import type { FleetControl } from "@/components/database/fleet/use-fleet-control"
import { EngineGlyph } from "@/components/database/kit"
import { useDatabases } from "@/components/database/shell/databases-context"

const RANK = { critical: 0, warning: 1, notice: 2 } as const

/**
 * What needs a hand, each with its fix as the action: a database that cannot
 * be reached, one that is stopped while a deployment depends on it, a port
 * open to the internet, a database nothing has ever dumped, a server found
 * running that waits for a password.
 *
 * Every concern a database has is listed — a public port does not hide a
 * missing backup behind it — and the databases that share the commonest one
 * (never backed up) are one finding that names them, not eight identical
 * rows. The section is not drawn when there is nothing in it.
 */
export function Attention({
  data,
  control,
  found,
  instances,
}: {
  data: FleetData
  control: FleetControl
  /** Connecting what discovery found; absent for a role that cannot. */
  found?: Found
  instances: DbInstance[]
}) {
  const router = useRouter()
  const { engineFor } = useDatabases()
  const findings: Finding[] = []
  const settings = (entry: DbFleetEntry) => ({
    label: "Open settings",
    onClick: () => router.push(sectionHref(entry.id, "settings")),
  })
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

  const never: DbFleetEntry[] = []
  for (const entry of data.entries) {
    for (const concern of data.concernsOf(entry)) {
      const id = `${concern.kind}-${entry.id}`
      const where = whereWord(entry).text
      switch (concern.kind) {
        case "broken":
          findings.push({
            id,
            level: "critical",
            title: `${entry.name} cannot be opened`,
            detail:
              entry.brokenReason ?? entry.error ?? "The saved connection can no longer be read.",
            advice: "Give it a new connection string under Settings, or forget it.",
            meta: meta(entry),
            action: settings(entry),
          })
          break
        case "unreachable":
          findings.push({
            id,
            level: "critical",
            title: `${entry.name} cannot be reached`,
            detail: `The dashboard could not sign in: ${entry.error ?? "the server did not answer"}.`,
            advice:
              "Check that the server is running, then test the stored password under Settings or set a new one.",
            meta: meta(entry),
            action: settings(entry),
          })
          break
        case "stopped":
          findings.push({
            id,
            level: "warning",
            title: `${entry.name} is stopped and ${plural(entry.consumers, "deployment environment")} ${entry.consumers === 1 ? "uses" : "use"} it`,
            detail: `${where} is not running, so everything bound to it has no database.`,
            meta: meta(entry),
            action: control.canStart(entry)
              ? { label: "Start", onClick: () => control.power(entry, "start") }
              : settings(entry),
          })
          break
        case "paused":
          findings.push({
            id,
            level: "warning",
            title: `${entry.name} is paused`,
            detail: `${where} is paused: it holds its memory and answers nothing.`,
            advice: "Unpause its container, or stop it.",
            meta: meta(entry),
            action: entry.container
              ? {
                  label: "Open container",
                  onClick: () =>
                    router.push(`/docker/containers/${encodeURIComponent(entry.container ?? "")}`),
                }
              : settings(entry),
          })
          break
        case "public":
          findings.push({
            id,
            level: "warning",
            title: `${entry.name} is reachable from the internet`,
            detail:
              "Its port is published on every interface and the firewall lets it through, so the password is all that protects the data.",
            advice: control.canRestrict(entry)
              ? "Restrict it to this server if nothing elsewhere needs it."
              : "Change where it listens where it is declared; its Settings say how it is reached.",
            meta: meta(entry),
            action: control.canRestrict(entry)
              ? { label: "Restrict to this server", onClick: () => control.restrict(entry) }
              : settings(entry),
          })
          break
        case "never-backed-up":
          never.push(entry)
          break
        case "stale-backup":
          findings.push({
            id,
            level: "notice",
            title: `${entry.name} was last backed up ${relativeTime(data.backups.get(entry.id)?.newest)}`,
            detail: "Its newest dump on this server is over a week old.",
            meta: meta(entry),
            action: control.canBackup(entry)
              ? { label: "Back up now", onClick: () => void control.backup(entry) }
              : {
                  label: "Open backups",
                  onClick: () => router.push(sectionHref(entry.id, "backups")),
                },
          })
          break
      }
    }
  }

  if (never.length > 0) {
    const able = never.filter(control.canBackup)
    const one = never.length === 1 ? never[0] : undefined
    findings.push({
      id: "never-backed-up",
      level: "warning",
      title: one
        ? `${one.name} has never been backed up`
        : `${never.length} databases have never been backed up`,
      detail:
        "No dump is on this server. A database whose only copy is the live one is one disk failure from gone.",
      advice: `Take a dump now, or add ${one ? "it" : "them"} to a scheduled backup job under Backups.`,
      meta: one ? meta(one) : never.map((entry) => entry.name).join(", "),
      extra: !one && (
        <ul className="divide-y divide-hairline">
          {never.map((entry) => (
            <li key={entry.id} className="flex min-w-0 items-center gap-2 py-1.5">
              <EngineGlyph engine={engineFor(entry)} />
              <span className="min-w-0 flex-1 truncate font-medium text-foreground">
                {entry.name}
              </span>
              {control.busyWord(entry) ? (
                <span className="text-hint">{control.busyWord(entry)}…</span>
              ) : (
                control.canBackup(entry) && (
                  <Button
                    size="xs"
                    variant="ghost"
                    aria-label={`Back up ${entry.name} now`}
                    onClick={() => void control.backup(entry)}
                  >
                    Back up now
                  </Button>
                )
              )}
            </li>
          ))}
        </ul>
      ),
      action:
        able.length > 0
          ? {
              label: able.length === 1 ? "Back up now" : `Back up all ${able.length}`,
              onClick: () => able.forEach((entry) => void control.backup(entry)),
            }
          : undefined,
    })
  }

  if (found) {
    for (const instance of instances.filter(needsPassword)) {
      const engine = found.engineOfInstance(instance)
      findings.push({
        id: `found-${instance.key}`,
        level: "notice",
        title: `${instance.name} is running here and needs a password to be connected`,
        detail:
          instance.credentials === "peer"
            ? "It is installed on this server, not in a container, so its passwords are kept in its own catalogue where the dashboard cannot read them."
            : "Nothing on this server states its password.",
        advice:
          instance.credentials === "peer"
            ? "Give it a password you know, or let the dashboard make an account for itself from this machine."
            : "Connect it with the password it uses.",
        meta: (
          <span className="flex items-center gap-1.5">
            <EngineGlyph engine={engine} />
            {engine.label}
          </span>
        ),
        action: { label: "Connect", onClick: () => found.ask(instance) },
      })
    }
  }

  if (findings.length === 0) return null
  findings.sort((a, b) => RANK[a.level] - RANK[b.level])
  return (
    <Section title="Needs attention" className="animate-rise">
      <FindingList findings={findings} />
    </Section>
  )
}
