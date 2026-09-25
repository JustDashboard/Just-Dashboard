"use client"

import { Database } from "@/components/icons"
import { bytes, plural, relativeTime } from "@/lib/format"
import type { DbFleetEntry } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ChoiceCard } from "@/components/choice-card"
import { Metric } from "@/components/page"
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

/** Where a connection's server runs, in the words the card and the page use. */
export function fleetWhere(entry: DbFleetEntry) {
  return entry.source === "docker"
    ? entry.container
    : entry.source === "host"
      ? "on this server"
      : entry.source === "file"
        ? "a file on this server"
        : `${entry.host}${entry.port ? `:${entry.port}` : ""}`
}

/** The engine's version without the engine's name in front of it. */
export function fleetVersion(entry: DbFleetEntry) {
  return entry.version?.replace(/^(PostgreSQL|MySQL|MariaDB|Redis|MongoDB)\s*/i, "")
}

/**
 * One database on the control center: a card you open, drawn as its engine.
 *
 * The lit edge because it is a destination (§16 — a row you *take*), the
 * engine's own logo because nine connections are told apart by their marks
 * before their names are read (§14), and its three readings — what it holds,
 * how many tables or keys, how many sessions are open — as a strip of figures
 * rather than a sentence, because the five tiles that stood over the grid
 * said the fleet's totals and nobody arrives at Databases to read a total.
 * The card is the reading now, and the grid is three across so each has the
 * room for it. The verdict colours one word, never the card.
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
  const version = fleetVersion(entry)
  return (
    <ChoiceCard
      verb={`Open ${entry.name}`}
      href={href}
      index={index}
      className="p-4"
      logo={
        <ProductLogo
          id={entry.driver}
          fallback={Database}
          className="size-12 rounded-xl [&_img]:size-7"
        />
      }
      title={<span className="text-title">{entry.name}</span>}
      description={
        <>
          {engineLabel(entry.driver)}
          {version ? ` ${version}` : ""}
          {entry.database ? ` · ${entry.database}` : ""}
          {" · "}
          {fleetWhere(entry)}
        </>
      }
      trailing={
        <span className="mt-1.5 flex min-w-0 flex-col gap-2.5">
          {entry.ok ? (
            <span className="grid grid-cols-3 gap-x-3 [&>*+*]:border-l [&>*+*]:border-hairline [&>*+*]:pl-3">
              <Metric label="Stored" value={entry.sizesKnown ? bytes(entry.bytes) : "—"} />
              <Metric
                label={entry.objectWord[0].toUpperCase() + entry.objectWord.slice(1)}
                value={entry.objects.toLocaleString()}
              />
              <Metric label="Sessions" value={entry.sessions.toLocaleString()} />
            </span>
          ) : (
            <span className="line-clamp-2 font-mono text-hint break-words text-destructive">
              {entry.error || "the server did not answer"}
            </span>
          )}
          <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
            <Status
              verdict={entry.ok ? "ok" : "critical"}
              label={entry.ok ? "connected" : "unreachable"}
            />
            {entry.consumers > 0 && (
              <span className="numeric">feeds {plural(entry.consumers, "deployment")}</span>
            )}
            {entry.exposure === "public" && <Tag tone="warning">public</Tag>}
            {entry.source !== "file" &&
              (entry.lastBackup ? (
                <span className={cn(concern?.reason.startsWith("last backup") && "text-warning")}>
                  backed up {relativeTime(entry.lastBackup)}
                </span>
              ) : (
                <span className="text-warning">never backed up</span>
              ))}
          </span>
        </span>
      }
    />
  )
}
