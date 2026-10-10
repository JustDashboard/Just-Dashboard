"use client"

import { useMemo, useState } from "react"
import { Copy, Eye, EyeOff, Route, ShieldOff, Warning } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { copyText } from "@/lib/clipboard"
import type { ContainerDetail, PortRoute } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Panel, PanelHeader, PanelToolbar } from "@/components/panel"
import { SearchInput } from "@/components/page"
import { EmptyNote, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Hint, Term } from "@/components/docker/explain"
import {
  bindingFamily,
  PublishedPathSheet,
  type PublishedBinding,
} from "@/components/docker/published-path"
import {
  envKind,
  envPrefix,
  ENV_KINDS,
  portRows,
  reachWords,
  type EnvKind,
} from "@/components/docker/container"
import { CODE } from "@/components/deploy/run-evidence"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { hueFor, LANES } from "@/lib/hue"

/** What a firewall's verdict means for a port Docker published, in words. */
function firewallWords(route: PortRoute | undefined): { word: string; tone?: "warning" } {
  if (!route || !route.firewall.known) return { word: "not read" }
  const { firewall } = route
  const backend = firewall.backend ?? "the firewall"
  if (firewall.enabled === false) return { word: `${backend} is off` }
  if (firewall.dockerBypass && firewall.verdict === "denied")
    return { word: `${backend} denies it, and Docker goes around`, tone: "warning" }
  switch (firewall.verdict) {
    case "allowed":
      return { word: `${backend} allows it` }
    case "denied":
      return { word: `${backend} denies it` }
    case "default":
      return { word: firewall.rule ? firewall.rule : `${backend} has no rule for it` }
    default:
      return { word: "not worked out" }
  }
}

/**
 * The published ports, each traced from its binding out through the reverse
 * proxy and the firewall: where it is reached from, and why.
 *
 * It was a run of port tags and, under them, a second list of the same ports
 * with the route each takes, each row three lines of prose. One table now,
 * read down its columns, with the reasoning on the row's title rather than
 * printed under every row — and the two sentences that change what an
 * operator does (the firewall does not apply; the proxy can be skipped) kept
 * as notices under it.
 */
export function PortsTable({
  detail,
  routes,
}: {
  detail: Pick<ContainerDetail, "id" | "exposure">
  routes?: PortRoute[]
}) {
  const { can } = useAuth()
  const [tracing, setTracing] = useState<PublishedBinding | null>(null)
  // The path reads the host's iptables and firewall, as the connection
  // investigator does, so it is an administrator's.
  const trace = can("system.admin")
  const rows = portRows(detail.exposure ?? [], routes).filter((row) => row.hostPort !== undefined)
  if (rows.length === 0) return null
  const bypassed = (routes ?? []).some(
    (route) => route.firewall.dockerBypass && route.firewall.verdict === "denied",
  )
  const skippable = (routes ?? []).some((route) => route.reach === "external" && route.vhost)

  return (
    <div className="flex min-w-0 flex-col gap-3">
      <Panel>
        <PanelHeader
          title={<Term name="port">Ports</Term>}
          actions={<span className="numeric text-hint text-muted-foreground">{rows.length}</span>}
        />
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="pl-5">Published</TableHead>
              <TableHead>Inside</TableHead>
              <TableHead>Reached</TableHead>
              <TableHead className={cn(!trace && "pr-5")}>Firewall</TableHead>
              {trace && (
                <TableHead className="pr-5">
                  <span className="sr-only">Actions</span>
                </TableHead>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => {
              const reach = reachWords(row)
              const firewall = firewallWords(row.route)
              return (
                <TableRow key={row.key} title={row.route?.reasoning}>
                  <TableCell className="py-2.5 pl-5 font-mono whitespace-nowrap">
                    {row.published}
                  </TableCell>
                  <TableCell className="numeric py-2.5 font-mono whitespace-nowrap text-muted-foreground">
                    {row.containerPort}/{row.protocol}
                  </TableCell>
                  <TableCell className="py-2.5 whitespace-normal">
                    <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
                      <Status tone={reach.tone} label={reach.word} />
                      {row.route?.inferred && <Tag>inferred</Tag>}
                    </span>
                  </TableCell>
                  <TableCell
                    className={cn(
                      "py-2.5 whitespace-normal",
                      !trace && "pr-5",
                      firewall.tone === "warning" ? "text-warning" : "text-muted-foreground",
                    )}
                  >
                    {firewall.word}
                  </TableCell>
                  {trace && (
                    <TableCell className="py-2 pr-5 text-right whitespace-nowrap">
                      {/* The verdict is the binding, proxy and firewall read together;
                          the path is every layer between the outside and the container,
                          with the ones nothing here can see said as unknown. */}
                      <Button
                        size="xs"
                        variant="outline"
                        aria-label={`Trace the path to port ${row.hostPort}/${row.protocol}`}
                        onClick={() =>
                          setTracing({
                            hostPort: row.hostPort!,
                            protocol: row.protocol,
                            family: bindingFamily(row.hostIp, row.ipv6),
                          })
                        }
                      >
                        <Route className="size-3" />
                        Trace the path
                      </Button>
                    </TableCell>
                  )}
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </Panel>
      <PublishedPathSheet
        container={detail.id}
        binding={tracing}
        onClose={() => setTracing(null)}
      />
      {bypassed && (
        <Notice title="The firewall does not apply to these ports" icon={Warning} tone="warning">
          Docker publishes a port by writing NAT rules that are consulted before the firewall&apos;s
          own filter chain, so a rule denying the port has no effect on it. The way to close a
          published port is to bind it to 127.0.0.1 rather than to deny it in the firewall.
        </Notice>
      )}
      {skippable && (
        <Hint>
          A port that is both published on every interface <em>and</em> proxied can be reached
          directly, skipping whatever the proxy site enforces in front of it. Binding it to
          127.0.0.1 leaves the proxy as the only way in.
        </Hint>
      )}
    </div>
  )
}

/**
 * The networks it is joined to, with the address and the names other
 * containers reach it by there. The container's own short id is always an
 * alias and says nothing, so it is left out.
 */
export function NetworksTable({
  detail,
}: {
  detail: Pick<ContainerDetail, "id" | "networkDetails">
}) {
  if (detail.networkDetails.length === 0) return null
  const short = detail.id.slice(0, 12)
  return (
    <Panel>
      <PanelHeader
        title={<Term name="network">Networks</Term>}
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {detail.networkDetails.length}
          </span>
        }
      />
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="pl-5">Network</TableHead>
            <TableHead>Address</TableHead>
            <TableHead>Gateway</TableHead>
            <TableHead className="pr-5">Reached as</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {detail.networkDetails.map((network) => {
            const aliases = network.aliases.filter((name) => name !== short)
            return (
              <TableRow key={network.networkId || network.name}>
                <TableCell className="py-2.5 pl-5 font-medium">{network.name}</TableCell>
                <TableCell className="numeric py-2.5 font-mono">
                  {network.ipAddress || <span className="text-muted-foreground">none</span>}
                </TableCell>
                <TableCell className="numeric py-2.5 font-mono text-muted-foreground">
                  {network.gateway || "—"}
                </TableCell>
                <TableCell className="py-2.5 pr-5 font-mono text-muted-foreground">
                  {aliases.length > 0 ? aliases.join(", ") : "—"}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </Panel>
  )
}

/**
 * Names that conventionally hold a credential. The server already withholds
 * these values from anyone below system.admin; this list is what decides
 * whether an admin's copy is printed on screen or kept behind a click, so it
 * errs towards hiding — a needless extra click costs less than a key read over
 * someone's shoulder or captured in a screen share.
 */
const SECRET_ENV_HINTS = [
  "SECRET",
  "PASSWORD",
  "PASSWD",
  "TOKEN",
  "CREDENTIAL",
  "PRIVATE",
  "SALT",
  "SIGNATURE",
  "CIPHER",
  "APIKEY",
  "API_KEY",
  "AUTH",
  "DSN",
  "_KEY",
  "KEY_",
  "MASTER_KEY",
  "ACCESS",
  "SESSION",
  // A connection string carries the password inside it, so the variable name
  // gives no hint that the value is a credential — DATABASE_URL is the single
  // most common way a password ends up on somebody's screen.
  "JWT",
  "DATABASE_URL",
  "DB_URL",
  "CONNECTION_STRING",
  "_URI",
  "WEBHOOK",
]

export function isSecretEnvKey(name: string) {
  const upper = name.toUpperCase()
  return upper === "KEY" || SECRET_ENV_HINTS.some((hint) => upper.includes(hint))
}

/** Each kind's value in the hue the product already gives it in code (`CODE`). */
const KIND_HUE: Record<EnvKind, string> = {
  credential: "text-muted-foreground",
  address: CODE.path,
  path: CODE.path,
  flag: CODE.literal,
  number: CODE.number,
  text: CODE.string,
}

/**
 * The environment as a table: the name in its own column, the value beside
 * it, and Copy and Reveal at the row's end where a credential has them.
 *
 * It was a column of grey `NAME= value` lines with the two buttons a screen's
 * width away at the right edge, so which row a Reveal belonged to was a
 * matter of following a line across the page. Each name's namespace — the
 * `DB` of `DB_POSTGRESDB_HOST` — takes its lane hue, so the variables that
 * set one part of the application are seen together; each value takes the
 * hue its kind has in code; and the kinds are chips that count and narrow,
 * the deployment Variables page's answer, beside a filter that never
 * searches a hidden value.
 */
export function EnvironmentTable({ env }: { env: string[] }) {
  const [revealed, setRevealed] = useState<Record<string, boolean>>({})
  const [query, setQuery] = useState("")
  const [only, setOnly] = useState<EnvKind>()
  const rows = useMemo(
    () =>
      env.map((line) => {
        const eq = line.indexOf("=")
        const name = eq === -1 ? line : line.slice(0, eq)
        const value = eq === -1 ? "" : line.slice(eq + 1)
        const secret = isSecretEnvKey(name)
        return { line, name, value, secret, kind: envKind(value, secret) }
      }),
    [env],
  )
  const counts = new Map<EnvKind, number>()
  for (const row of rows) counts.set(row.kind, (counts.get(row.kind) ?? 0) + 1)
  const secretCount = counts.get("credential") ?? 0
  const needle = query.trim().toLowerCase()
  // A hidden value is not searched: matching on it would say what it contains.
  const shown = rows.filter(
    (row) =>
      (!only || row.kind === only) &&
      (!needle ||
        row.name.toLowerCase().includes(needle) ||
        (!row.secret && row.value.toLowerCase().includes(needle))),
  )

  if (rows.length === 0) {
    return <EmptyNote>No environment variables set.</EmptyNote>
  }

  return (
    <Panel className="max-h-full min-h-0 animate-rise">
      <PanelHeader
        title="Environment"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {shown.length === rows.length ? rows.length : `${shown.length} of ${rows.length}`}
          </span>
        }
      />
      <PanelToolbar className="flex-wrap gap-y-2">
        <SearchInput
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Filter by name or value"
          aria-label="Filter the environment"
        />
        <ChipStrip aria-label="Kinds of value">
          <FilterChip selected={!only} onClick={() => setOnly(undefined)}>
            All <ChipCount>{rows.length}</ChipCount>
          </FilterChip>
          {ENV_KINDS.filter(({ kind }) => counts.has(kind)).map(({ kind, label }) => (
            <FilterChip
              key={kind}
              selected={only === kind}
              onClick={() => setOnly(only === kind ? undefined : kind)}
            >
              {kind === "credential" && <ShieldOff aria-hidden className="size-3" />}
              {label} <ChipCount>{counts.get(kind)}</ChipCount>
            </FilterChip>
          ))}
        </ChipStrip>
      </PanelToolbar>
      {secretCount > 0 && (
        <p className="flex min-w-0 items-center gap-2 border-b border-hairline px-5 py-2 text-hint text-muted-foreground">
          <ShieldOff className="size-3.5 shrink-0" />
          <span>
            {secretCount} {secretCount === 1 ? "value looks" : "values look"} like a credential and
            {secretCount === 1 ? " is" : " are"} hidden. Reveal only when nobody is watching your
            screen.
          </span>
        </p>
      )}
      <Table className="table-fixed" containerClassName="min-h-0">
        <colgroup>
          <col className="w-[34%]" />
          <col />
          <col className="w-20" />
        </colgroup>
        <TableHeader>
          <TableRow>
            <TableHead className="pl-5">Name</TableHead>
            <TableHead>Value</TableHead>
            <TableHead className="pr-5">
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {shown.map((row) => {
            const show = !row.secret || revealed[row.name]
            const prefix = envPrefix(row.name)
            return (
              <TableRow key={row.line} className="group">
                <TableCell className="py-2 pl-5 align-top font-mono break-all whitespace-normal">
                  <span style={{ color: hueFor(prefix, LANES) }}>{prefix}</span>
                  <span className="text-muted-foreground">{row.name.slice(prefix.length)}</span>
                </TableCell>
                <TableCell
                  className={cn(
                    "py-2 align-top font-mono break-all whitespace-normal",
                    show &&
                      KIND_HUE[row.kind === "credential" ? envKind(row.value, false) : row.kind],
                  )}
                >
                  {show ? (
                    row.value || <span className="text-muted-foreground">empty</span>
                  ) : (
                    <span className="text-muted-foreground select-none">••••••••••••</span>
                  )}
                </TableCell>
                <TableCell className="py-0 pr-5 text-right align-top">
                  {/*
                    Copy sits beside Reveal so the common case — pasting a
                    credential into a client — does not require putting it on
                    screen first. The value goes to the clipboard and nowhere
                    else: it is never logged, notified with, or sent anywhere.
                  */}
                  {row.secret && (
                    <span className="inline-flex h-8 items-center gap-0.5">
                      <IconAction
                        label={`Copy ${row.name}`}
                        onClick={() => void copyText(row.value, `${row.name} copied`)}
                      >
                        <Copy />
                      </IconAction>
                      <IconAction
                        label={`${revealed[row.name] ? "Hide" : "Reveal"} ${row.name}`}
                        onClick={() =>
                          setRevealed((prev) => ({ ...prev, [row.name]: !prev[row.name] }))
                        }
                      >
                        {revealed[row.name] ? <EyeOff /> : <Eye />}
                      </IconAction>
                    </span>
                  )}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
      {shown.length === 0 && (
        <EmptyNote className="px-5 py-4">Nothing in the environment matches.</EmptyNote>
      )}
    </Panel>
  )
}
