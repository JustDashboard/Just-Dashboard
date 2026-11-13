"use client"

import { useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { Pencil, Plus, Trash } from "@/components/icons"
import { del } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { NetworkRoute, NetworkRouting, NetworkRule } from "@/lib/types"
import { cn } from "@/lib/utils"
import { InfoTip } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote } from "@/components/state"
import { Tag } from "@/components/tag"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { LogoGlyph } from "@/components/logo"
import { ProductGlyph } from "@/components/product-logo"
import { useConfirm } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Cidr } from "@/components/network/address"
import { routeRows, routeVia, ruleSelectors, ruleThen, type RouteRow } from "./route-reading"

const OWNER_PRODUCT: Partial<Record<NetworkRoute["owner"], string>> = {
  tailscale: "tailscale",
  docker: "docker",
  wireguard: "wireguard",
}

const OWNER_WORD: Record<NetworkRoute["owner"], string> = {
  kernel: "kernel",
  dhcp: "DHCP",
  tailscale: "Tailscale",
  docker: "Docker",
  wireguard: "WireGuard",
  "just-dashboard": "made here",
  system: "system",
}

// From here the seven columns fit beside the rail; narrower, a route is two lines.
const WIDE = "(min-width: 1100px)"
const RULES_WIDE = "(min-width: 768px)"

export type RouteTarget = {
  /** What was typed, trimmed. */
  text: string
  /** Per table: the routes covering it and the one that table selects. */
  matches: Map<number, { covering: NetworkRoute[]; selected?: NetworkRoute }>
  /** The table the kernel named for it, once it has been asked. */
  answered?: number
}

/**
 * Every route in one table, framed because it is a table (§2): where to, by
 * which hop, out of which device and from which source, the table it is in,
 * whose it is and its metric, a line each. There was a framed table per
 * routing table, each with its own header row, and a device cell that went to
 * two lines when a route had a source; the table is a column and a chip now,
 * and the device and its source share one mono line.
 *
 * A route the dashboard made can be edited and removed from its row; a
 * guarded one says why it stays behind the ⓘ where those would be. With a
 * target, the table holds the routes covering it, each table's own choice is
 * marked, and the row the kernel's answer came from is washed.
 */
export function RoutesTable({
  tables,
  hiddenLocal,
  target,
  onChanged,
  onEdit,
}: {
  tables: NetworkRouting["tables"]
  hiddenLocal: number
  target?: RouteTarget
  onChanged: () => void
  onEdit: (route: NetworkRoute, tableId: number) => void
}) {
  const { can } = useAuth()
  const wide = useMediaQuery(WIDE)
  const changeable = can("system.admin") && can("destructive")
  const [chosen, setChosen] = useState<number>()
  const scope = tables.some((table) => table.id === chosen) ? chosen : undefined
  const { confirm, dialog } = useConfirm()

  const all = routeRows(tables)
  const covering = target
    ? all.filter((row) => target.matches.get(row.table.id)?.covering.includes(row.route))
    : all
  const rows = scope === undefined ? covering : covering.filter((row) => row.table.id === scope)
  const count = (id: number) => covering.filter((row) => row.table.id === id).length
  const selected = (row: RouteRow) => target?.matches.get(row.table.id)?.selected === row.route
  // The kernel's table, where it was asked; before that every table's choice counts alike.
  const answers = (row: RouteRow) =>
    selected(row) && (target?.answered === undefined || target.answered === row.table.id)

  const remove = (route: NetworkRoute) =>
    confirm({
      title: `Remove the route to ${route.destination}`,
      confirmLabel: "Remove",
      description: (
        <p>
          Traffic for <span className="font-mono">{route.destination}</span> falls back to the next
          route that covers it, and the route is not made again at boot.
        </p>
      ),
      action: async () => {
        await del(`/network/routing/routes/${route.id}`)
        notify.success("Route removed")
        onChanged()
        return "reported"
      },
    })

  const verbs = (row: RouteRow) => (
    <RowVerbs
      guard={row.route.guard}
      why={`Why the route to ${row.route.destination} stays`}
      edit={
        changeable && row.route.managed && !row.route.guard
          ? {
              label: `Edit the route to ${row.route.destination}`,
              run: () => onEdit(row.route, row.table.id),
            }
          : undefined
      }
      remove={
        changeable && row.route.managed && !row.route.guard
          ? {
              label: `Remove the route to ${row.route.destination}`,
              run: () => remove(row.route),
            }
          : undefined
      }
    />
  )

  return (
    <Panel aria-label="Routes" data-routing="routes">
      <PanelHeader
        title={
          <>
            Routes
            <span className="numeric ml-2 text-body font-normal text-muted-foreground">
              {target ? `${covering.length} of ${all.length} cover ${target.text}` : all.length}
            </span>
          </>
        }
        actions={
          hiddenLocal > 0 && (
            <InfoTip label="What is not listed">
              The local table&rsquo;s {hiddenLocal} entries, the kernel&rsquo;s own record of which
              addresses are this machine&rsquo;s, are not listed.
            </InfoTip>
          )
        }
      >
        <ChipStrip aria-label="Routing table" className="mr-auto">
          <FilterChip selected={scope === undefined} onClick={() => setChosen(undefined)}>
            All <ChipCount>{covering.length}</ChipCount>
          </FilterChip>
          {tables.map((table) => (
            <FilterChip
              key={table.id}
              selected={scope === table.id}
              title={`table ${table.id}`}
              onClick={() => setChosen(scope === table.id ? undefined : table.id)}
            >
              {table.id === 52 && <ProductGlyph id="tailscale" className="size-3.5" />}
              {table.name}
              <ChipCount>{count(table.id)}</ChipCount>
            </FilterChip>
          ))}
        </ChipStrip>
      </PanelHeader>
      <PanelBody flush>
        {rows.length === 0 ? (
          <EmptyNote>
            {target
              ? scope === undefined
                ? `No route covers ${target.text}.`
                : `No route in this table covers ${target.text}.`
              : scope === undefined
                ? "No routes."
                : "This table holds no routes; a rule sending here falls through to the next."}
          </EmptyNote>
        ) : wide ? (
          <Table className="table-fixed" containerClassName="max-h-[calc(100svh-12rem)]">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead className="w-[22%]">Destination</TableHead>
                <TableHead className="w-[13%]">Via</TableHead>
                <TableHead>Device</TableHead>
                <TableHead className="w-[10%]">Table</TableHead>
                <TableHead className="w-[16%]">From</TableHead>
                <TableHead className="w-16 text-right">Metric</TableHead>
                <TableHead className="w-[4.5rem]">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => (
                <TableRow
                  key={row.key}
                  data-routing-row="route"
                  data-selected={answers(row) ? "" : undefined}
                  className={cn("h-11", answers(row) && "bg-wash-brand")}
                >
                  <TableCell className="py-0">
                    <Destination row={row} selected={selected(row)} lit={answers(row)} />
                  </TableCell>
                  <TableCell className="py-0">
                    <Via route={row.route} />
                  </TableCell>
                  <TableCell className="py-0">
                    <Device route={row.route} />
                  </TableCell>
                  <TableCell className="py-0">
                    <TableName table={row.table} />
                  </TableCell>
                  <TableCell className="py-0">
                    <Owner route={row.route} />
                  </TableCell>
                  <TableCell className="numeric py-0 text-right font-mono">
                    {row.route.metric || "—"}
                  </TableCell>
                  <TableCell className="py-0">{verbs(row)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <ul aria-label="Routes" className="divide-y divide-hairline">
            {rows.map((row) => (
              <li
                key={row.key}
                data-routing-row="route"
                data-selected={answers(row) ? "" : undefined}
                className={cn(
                  "grid h-14 min-w-0 grid-cols-[minmax(0,1fr)_auto_3.25rem] items-center gap-x-3 px-4 text-xs",
                  answers(row) && "bg-wash-brand",
                )}
              >
                <div className="min-w-0 space-y-1">
                  <Destination row={row} selected={selected(row)} lit={answers(row)} />
                  <p className="flex min-w-0 items-center gap-x-2 font-mono">
                    {/* A phone's line has room for the hop or for "on-link", not both
                        beside a device and its source; the hop is the reading. */}
                    {routeVia(row.route) && (
                      <span className="max-w-[45%] shrink-0 truncate">{routeVia(row.route)}</span>
                    )}
                    <Device route={row.route} />
                  </p>
                </div>
                <div className="flex min-w-0 flex-col items-end gap-1">
                  <TableName table={row.table} />
                  <Owner route={row.route} short />
                </div>
                {verbs(row)}
              </li>
            ))}
          </ul>
        )}
      </PanelBody>
      {dialog}
    </Panel>
  )
}

function Destination({ row, selected, lit }: { row: RouteRow; selected: boolean; lit: boolean }) {
  const route = row.route
  return (
    <span className="flex min-w-0 items-center gap-2 whitespace-nowrap">
      {route.destination === "default" ? (
        <span className="font-mono font-medium">default</span>
      ) : (
        <Cidr cidr={route.destination} />
      )}
      {route.type !== "unicast" && <Tag tone="danger">{route.type}</Tag>}
      {route.family === "inet6" && <Tag>IPv6</Tag>}
      {route.nexthops.length > 0 && <Tag>multipath</Tag>}
      {selected && <Tag className={lit ? "text-brand" : undefined}>selected</Tag>}
      {route.comment && (
        <span className="min-w-0 truncate text-hint text-muted-foreground" title={route.comment}>
          {route.comment}
        </span>
      )}
    </span>
  )
}

function Via({ route }: { route: NetworkRoute }) {
  const via = routeVia(route)
  return (
    <span className="block min-w-0 truncate font-mono" title={via}>
      {via ?? <span className="text-muted-foreground">on-link</span>}
    </span>
  )
}

/** The device and the source a packet leaves it with, on one mono line. */
function Device({ route }: { route: NetworkRoute }) {
  return (
    <span
      className="flex min-w-0 items-baseline gap-x-2 font-mono whitespace-nowrap"
      title={[route.device, route.source && `source ${route.source}`].filter(Boolean).join(" · ")}
    >
      <span className="max-w-full shrink-0 truncate">{route.device ?? "—"}</span>
      {route.source && (
        <span className="min-w-0 truncate text-muted-foreground">source {route.source}</span>
      )}
    </span>
  )
}

function TableName({ table }: { table: { id: number; name: string } }) {
  return (
    <span
      className="flex min-w-0 items-center gap-1.5 whitespace-nowrap text-muted-foreground"
      title={`table ${table.id}`}
    >
      {table.id === 52 && <ProductGlyph id="tailscale" className="size-3.5" />}
      <span className="truncate">{table.name}</span>
    </span>
  )
}

function Owner({ route, short }: { route: NetworkRoute; short?: boolean }) {
  return (
    <span className="flex min-w-0 items-center gap-1.5 whitespace-nowrap">
      {OWNER_PRODUCT[route.owner] && <ProductGlyph id={OWNER_PRODUCT[route.owner]!} />}
      {route.owner === "just-dashboard" && (
        <LogoGlyph small className="h-3.5 w-auto shrink-0 text-brand" />
      )}
      <span className={cn("truncate", route.managed ? "text-brand" : "text-muted-foreground")}>
        {OWNER_WORD[route.owner]}
      </span>
      {!short && <span className="text-muted-foreground/60">· {route.protocol}</span>}
    </span>
  )
}

/**
 * The end of a row: Edit and Remove where the row is the dashboard's and the
 * account may change it, or why the row stays, behind an ⓘ a keyboard and a
 * phone can reach (it was a `title` on the cell). One box in every state, so
 * a row with verbs and a row with none are the same row.
 */
function RowVerbs({
  guard,
  why,
  edit,
  remove,
}: {
  guard?: string
  why: string
  edit?: { label: string; run: () => void }
  remove?: { label: string; run: () => void }
}) {
  return (
    <span className="flex h-6 items-center justify-end gap-0.5">
      {edit && (
        <Button size="icon-xs" variant="ghost" aria-label={edit.label} onClick={edit.run}>
          <Pencil aria-hidden />
        </Button>
      )}
      {remove && (
        <Button size="icon-xs" variant="ghost" aria-label={remove.label} onClick={remove.run}>
          <Trash aria-hidden />
        </Button>
      )}
      {guard && !edit && !remove && (
        <span className="flex size-6 items-center justify-center">
          <InfoTip label={why}>{guard}</InfoTip>
        </span>
      )}
    </span>
  )
}

/**
 * The policy rules, framed as a table, in the order the kernel asks them:
 * both families and the kernel's own rule 0, which the picture leaves out.
 * Only the dashboard's own (priorities 10000–19999) can be removed.
 */
export function RulesTable({
  rules,
  onChanged,
  onAdd,
}: {
  rules: NetworkRule[]
  onChanged: () => void
  /** Absent for an account that cannot add one. */
  onAdd?: () => void
}) {
  const { can } = useAuth()
  const wide = useMediaQuery(RULES_WIDE)
  const removable = can("system.admin") && can("destructive")
  const { confirm, dialog } = useConfirm()
  const remove = (rule: NetworkRule) =>
    confirm({
      title: `Remove rule ${rule.priority}`,
      confirmLabel: "Remove",
      description: <p>The traffic it matched is decided by the rules after it again.</p>,
      action: async () => {
        await del(`/network/routing/rules/${rule.id}`)
        notify.success("Rule removed")
        onChanged()
        return "reported"
      },
    })
  const verbs = (rule: NetworkRule) => (
    <RowVerbs
      guard={rule.guard}
      why={`Why rule ${rule.priority} stays`}
      remove={
        removable && rule.managed && !rule.guard
          ? { label: `Remove rule ${rule.priority}`, run: () => remove(rule) }
          : undefined
      }
    />
  )
  return (
    <Panel aria-label="Policy rules" data-routing="rules">
      <PanelHeader
        title={
          <>
            Policy rules
            <span className="numeric ml-2 text-body font-normal text-muted-foreground">
              {rules.length}
            </span>
          </>
        }
        actions={
          onAdd && (
            <Button size="xs" variant="outline" onClick={onAdd}>
              <Plus aria-hidden />
              Add rule
            </Button>
          )
        }
      />
      <PanelBody flush>
        {wide ? (
          <Table className="table-fixed">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead className="w-24 text-right">Priority</TableHead>
                <TableHead>Matches</TableHead>
                <TableHead className="w-[30%]">Then</TableHead>
                <TableHead className="w-[16%]">Whose</TableHead>
                <TableHead className="w-[4.5rem]">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rules.map((rule) => (
                <TableRow key={ruleKey(rule)} data-routing-row="rule" className="h-11">
                  <TableCell className="numeric py-0 text-right font-mono">
                    {rule.priority}
                  </TableCell>
                  <TableCell className="py-0">
                    <Matches rule={rule} />
                  </TableCell>
                  <TableCell className="py-0">
                    <Then rule={rule} />
                  </TableCell>
                  <TableCell className="py-0">
                    <Whose rule={rule} />
                  </TableCell>
                  <TableCell className="py-0">{verbs(rule)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <ul aria-label="Policy rules" className="divide-y divide-hairline">
            {rules.map((rule) => (
              <li
                key={ruleKey(rule)}
                data-routing-row="rule"
                className="grid h-14 min-w-0 grid-cols-[2.75rem_minmax(0,1fr)_auto_1.5rem] items-center gap-x-3 px-4 text-xs"
              >
                <span className="numeric text-right font-mono">{rule.priority}</span>
                <div className="min-w-0 space-y-1">
                  <Matches rule={rule} />
                  <Then rule={rule} />
                </div>
                <Whose rule={rule} />
                {verbs(rule)}
              </li>
            ))}
          </ul>
        )}
      </PanelBody>
      {dialog}
    </Panel>
  )
}

const ruleKey = (rule: NetworkRule) =>
  `${rule.family}:${rule.priority}:${rule.id}:${rule.from}:${rule.fwmark}`

function Matches({ rule }: { rule: NetworkRule }) {
  const selectors = ruleSelectors(rule).join(" ")
  return (
    <span className="flex min-w-0 items-center gap-2 whitespace-nowrap">
      <span className="min-w-0 truncate font-mono" title={selectors || undefined}>
        {selectors || <span className="text-muted-foreground">everything</span>}
      </span>
      {rule.family === "inet6" && <Tag>IPv6</Tag>}
    </span>
  )
}

function Then({ rule }: { rule: NetworkRule }) {
  const then = ruleThen(rule)
  return (
    <span
      title={then}
      className={cn(
        "block min-w-0 truncate",
        rule.action !== "lookup" &&
          rule.action !== "goto" &&
          rule.action !== "nop" &&
          "text-destructive",
        rule.unresolved && "text-warning",
      )}
    >
      {then}
    </span>
  )
}

function Whose({ rule }: { rule: NetworkRule }) {
  return (
    <span className="flex min-w-0 items-center gap-1.5 whitespace-nowrap">
      {rule.owner === "tailscale" && <ProductGlyph id="tailscale" />}
      {rule.owner === "just-dashboard" && (
        <LogoGlyph small className="h-3.5 w-auto shrink-0 text-brand" />
      )}
      <span className={cn("truncate", rule.managed ? "text-brand" : "text-muted-foreground")}>
        {rule.owner === "just-dashboard" ? "made here" : rule.owner}
      </span>
    </span>
  )
}

/**
 * A table before the first read, at the height it had last time: its frame,
 * its title line and that many rows, in the layout the width will give it.
 */
export function TableWaiting({
  title,
  rows,
  wideFrom,
  chips,
}: {
  title: string
  rows: number
  /** The width from which the table is a table rather than two-line rows. */
  wideFrom: string
  /** The title line holds a strip of chips, which takes a line of its own on a phone. */
  chips?: boolean
}) {
  const wide = useMediaQuery(`(min-width: ${wideFrom})`)
  return (
    <Panel aria-label={title} aria-busy>
      <PanelHeader title={title}>
        {/* The line's own height: the chips or the verb that will stand on it. */}
        <span aria-hidden className={cn("h-7", chips && "max-sm:basis-full")} />
      </PanelHeader>
      <PanelBody flush>
        {wide && <div className="h-10 border-b" />}
        <ul className="divide-y divide-hairline">
          {Array.from({ length: rows }, (_, i) => (
            <li key={i} className={cn("flex items-center gap-4 px-4", wide ? "h-11" : "h-14")}>
              <Skeleton className="h-3 w-40 max-w-[40%]" />
              <Skeleton className="h-3 w-24 max-sm:hidden" />
              <Skeleton className="ml-auto h-3 w-20" />
            </li>
          ))}
        </ul>
      </PanelBody>
    </Panel>
  )
}
