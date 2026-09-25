"use client"

import { Database } from "@/components/icons"
import { bytes, plural, relativeTime } from "@/lib/format"
import type { DbFleetEntry } from "@/lib/types"
import { ChoiceCard } from "@/components/choice-card"
import { ProductLogo } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { fleetConcern } from "@/components/database/fleet"

const ENGINE: Record<string, string> = {
  postgres: "PostgreSQL",
  mysql: "MySQL",
  sqlite: "SQLite",
  sqlserver: "SQL Server",
  clickhouse: "ClickHouse",
  oracle: "Oracle",
  mongodb: "MongoDB",
  redis: "Redis",
}

export function engineLabel(driver: string) {
  return ENGINE[driver] ?? driver
}

/**
 * One database on the control center: a card you open, drawn as its engine.
 *
 * The lit edge because it is a destination (§16 — a row you *take*), the
 * engine's own logo because nine connections are told apart by their marks
 * before their names are read (§14), and readings rather than a caption: what
 * it is, where it runs, whether it answers, how big it is, who it feeds and
 * when it was last saved. The verdict colours one word, never the card.
 */
export function FleetCard({
  entry,
  href,
  index,
}: {
  entry: DbFleetEntry
  href: string
  index: number
}) {
  const concern = fleetConcern(entry)
  const where =
    entry.source === "docker"
      ? entry.container
      : entry.source === "host"
        ? "on this server"
        : entry.source === "file"
          ? "a file on this server"
          : `${entry.host}${entry.port ? `:${entry.port}` : ""}`
  const version = entry.version?.replace(/^(PostgreSQL|MySQL|MariaDB|Redis|MongoDB)\s*/i, "")
  return (
    <ChoiceCard
      verb={`Open ${entry.name}`}
      href={href}
      index={index}
      logo={<ProductLogo id={entry.driver} size="md" fallback={Database} />}
      title={entry.name}
      description={
        <>
          {engineLabel(entry.driver)}
          {version ? ` ${version}` : ""}
          {entry.database ? ` · ${entry.database}` : ""}
          {" · "}
          {where}
        </>
      }
      trailing={
        <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
          <Status
            verdict={entry.ok ? "ok" : "critical"}
            label={entry.ok ? "connected" : "unreachable"}
          />
          {entry.ok && (
            <span className="numeric">
              {entry.sizesKnown ? bytes(entry.bytes) : "—"}
              {" · "}
              {plural(entry.objects, entry.objectWord.replace(/s$/, ""))}
              {" · "}
              {plural(entry.sessions, "session")}
            </span>
          )}
          {entry.consumers > 0 && (
            <span className="numeric">feeds {plural(entry.consumers, "deployment")}</span>
          )}
          {entry.exposure === "public" && <Tag tone="warning">public</Tag>}
          {entry.source !== "file" &&
            (entry.lastBackup ? (
              <span
                className={concern?.reason.startsWith("last backup") ? "text-warning" : undefined}
              >
                backed up {relativeTime(entry.lastBackup)}
              </span>
            ) : (
              <span className="text-warning">never backed up</span>
            ))}
        </span>
      }
    />
  )
}
