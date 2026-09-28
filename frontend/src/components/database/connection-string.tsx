"use client"

import { useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { Check, Copy, Eye, EyeOff } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get } from "@/lib/api"
import { buildDsn, DEFAULT_PORT } from "@/lib/db-dsn"
import type { DbAccess, DbConnection, DbDriver } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useCopy } from "@/hooks/use-copy"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { IconAction } from "@/components/icon-action"

/**
 * The connection string, as the thing an operator comes to a database for
 * after the first day: to paste it into an application.
 *
 * It is one block with two switches, the way a hosted database's own page
 * draws it — where it is read from (this server, or anywhere, when the port is
 * published), and the shape it is wanted in: the URL itself, the line an
 * `.env` file takes, or the command that opens a shell on it. The string is
 * shown masked, revealed or copied on purpose (the server audits the reveal),
 * and the masked one is built from the facts the page already has, so
 * drawing it reveals nothing and audits nothing.
 *
 * Where the server is a container on this host, the second target carries the
 * machine's public address, because a database published to loopback is
 * unreachable from the laptop the operator is sitting at — and when it is not
 * published, the block says so and names the switch that changes it, which
 * lives on the Connection page.
 */
export type SnippetFormat = "url" | "env" | "cli"

const CLI: Record<DbDriver, string> = {
  postgres: "psql",
  mysql: "mysql",
  sqlite: "sqlite3",
  sqlserver: "sqlcmd",
  clickhouse: "clickhouse-client",
  oracle: "sqlplus",
  mongodb: "mongosh",
  redis: "redis-cli",
}

const ENV: Record<DbDriver, string> = {
  postgres: "DATABASE_URL",
  mysql: "DATABASE_URL",
  sqlite: "DATABASE_URL",
  sqlserver: "DATABASE_URL",
  clickhouse: "CLICKHOUSE_URL",
  oracle: "DATABASE_URL",
  mongodb: "MONGODB_URI",
  redis: "REDIS_URL",
}

/**
 * The string as the engine would spell it, with the password hidden — built
 * from the facts the page already has. The real one is fetched when it is
 * copied or shown.
 */
export function previewDsn(conn: DbConnection, host: string) {
  return buildDsn(conn.driver, {
    host,
    port: conn.port || DEFAULT_PORT[conn.driver],
    user: conn.user,
    password: "••••••",
    database: conn.database,
    option: "",
  }).replace(encodeURIComponent("••••••"), "••••••")
}

/**
 * The MySQL driver's own `user:pass@tcp(host)/db` as the URL every
 * application expects. The other engines' strings already are URLs.
 */
function asUrl(driver: DbDriver, dsn: string) {
  if (driver !== "mysql") return dsn
  const m = /^(.*?):(.*)@tcp\((.*)\)\/(.*)$/.exec(dsn)
  if (!m) return dsn
  const [, user, password, at, db] = m
  return `mysql://${user}${password ? `:${password}` : ""}@${at}/${db}`
}

/** The string in the shape asked for. `host` is the address the string names. */
export function snippet(
  conn: DbConnection,
  dsn: string,
  host: string,
  format: SnippetFormat,
): string {
  const url = asUrl(conn.driver, dsn)
  if (format === "url") return url
  if (format === "env") return `${ENV[conn.driver]}=${url}`
  const port = conn.port || DEFAULT_PORT[conn.driver]
  const user = conn.user || "root"
  switch (conn.driver) {
    case "postgres":
      return `psql "${url}"`
    case "mongodb":
      return `mongosh "${url}"`
    case "redis":
      return `redis-cli -u "${url}"`
    case "sqlite":
      return `sqlite3 "${conn.database}"`
    // The shells that take fields rather than a URL ask for the password
    // themselves, so nothing secret lands in a shell history.
    case "mysql":
      return `mysql --host=${host} --port=${port} --user=${user} --password ${conn.database}`.trim()
    case "sqlserver":
      return `sqlcmd -S ${host},${port} -U ${user}${conn.database ? ` -d ${conn.database}` : ""}`
    case "clickhouse":
      return `clickhouse-client --host ${host} --port ${port} --user ${user} --database ${conn.database || "default"} --ask-password`
    case "oracle":
      return `sqlplus ${user}@//${host}:${port}/${conn.database}`
  }
}

type Target = "host" | "public"

export function ConnectionStrings({
  conn,
  access,
  className,
}: {
  conn: DbConnection
  /** Where the server is reachable from; absent while it loads or for a reader who may not know. */
  access?: DbAccess
  className?: string
}) {
  const { copy, copied } = useCopy()
  const [target, setTarget] = useState<Target>("host")
  const [format, setFormat] = useSessionState<SnippetFormat>("databases.connect.format", "url")
  // The real string per target, once revealed; hidden again on the next press.
  const [shown, setShown] = useState<Partial<Record<Target, string>>>({})
  const [busy, setBusy] = useState(false)

  const remote = access?.exposure === "remote"
  const publicAddress = access?.publicAddresses[0]
  const hasPublic = access?.exposure === "public" && Boolean(publicAddress)
  const host =
    target === "public" && publicAddress
      ? publicAddress.includes(":")
        ? `[${publicAddress}]`
        : publicAddress
      : conn.host
  const real = shown[target]
  const text = snippet(conn, real ?? previewDsn(conn, host), host, format)
  const stringless = target === "public" && !hasPublic

  const fetchUrl = async () => {
    const res = await get<{ url: string }>(`/databases/${conn.id}/url`, { target })
    return res.url
  }
  const reveal = async () => {
    if (real) {
      setShown((s) => ({ ...s, [target]: undefined }))
      return
    }
    setBusy(true)
    try {
      const url = await fetchUrl()
      setShown((s) => ({ ...s, [target]: url }))
    } catch (err) {
      notify.error("Could not read the connection string", err)
    } finally {
      setBusy(false)
    }
  }
  const copyIt = async () => {
    setBusy(true)
    try {
      const url = real ?? (await fetchUrl())
      await copy(snippet(conn, url, host, format), "Connection string copied")
    } catch (err) {
      notify.error("Could not read the connection string", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Panel plain className={className} data-slot="connection-strings">
      <PanelHeader
        title="Connect"
        actions={
          access &&
          !remote && (
            <ChipStrip role="group" aria-label="Reached from">
              <FilterChip selected={target === "host"} onClick={() => setTarget("host")}>
                On this server
              </FilterChip>
              <FilterChip selected={target === "public"} onClick={() => setTarget("public")}>
                From anywhere
              </FilterChip>
            </ChipStrip>
          )
        }
      />
      <PanelBody className="flex min-w-0 flex-col gap-3 pt-3">
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
          <ChipStrip role="group" aria-label="Shape">
            <FilterChip selected={format === "url"} onClick={() => setFormat("url")}>
              URL
            </FilterChip>
            <FilterChip selected={format === "env"} onClick={() => setFormat("env")}>
              .env
            </FilterChip>
            <FilterChip selected={format === "cli"} onClick={() => setFormat("cli")}>
              {CLI[conn.driver]}
            </FilterChip>
          </ChipStrip>
          {!stringless && (
            <div className="flex items-center gap-1">
              <IconAction
                label={real ? "Hide the connection string" : "Show the connection string"}
                onClick={reveal}
                disabled={busy}
              >
                {real ? <EyeOff /> : <Eye />}
              </IconAction>
              <Button size="sm" variant="outline" onClick={copyIt} disabled={busy}>
                {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
                Copy
              </Button>
            </div>
          )}
        </div>
        {stringless ? (
          <Well plain className="text-hint leading-relaxed text-muted-foreground">
            {whyNoPublicString(access)}
          </Well>
        ) : (
          <Well
            data-slot="connection-string"
            className={cn("break-all whitespace-pre-wrap select-all", real && "text-foreground")}
          >
            {text}
          </Well>
        )}
        {!stringless && (
          <p className="text-hint text-muted-foreground">
            {remote
              ? "As saved. Anything that can reach that address connects with it."
              : target === "public"
                ? "Reachable from anywhere with the password. Treat the string as a secret."
                : conn.driver === "sqlite"
                  ? "A file on this server; a program on it opens the path directly."
                  : "Reachable from programs on this server, including every container that shares its network."}
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}

/** Why there is no string for a laptop, in one sentence. */
function whyNoPublicString(access: DbAccess | undefined) {
  if (!access) return ""
  if (access.exposure === "public")
    return `This machine has no public address of its own — the provider maps one in front of it. Use that address with port ${access.port}.`
  if (access.exposure === "private")
    return "Published on one address only. Whoever can reach that address can connect on the same port; the binding is changed on the Docker page."
  return access.managed
    ? "Not reachable from outside this server. Open it up on the Connection page to connect from your own machine or share it."
    : "Not reachable from outside this server."
}
