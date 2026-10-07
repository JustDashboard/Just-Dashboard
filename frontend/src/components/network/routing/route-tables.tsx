"use client"

import { Trash } from "@/components/icons"
import { del } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { NetworkRoute, NetworkRouting, NetworkRule } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Tag } from "@/components/tag"
import { ProductGlyph } from "@/components/product-logo"
import { useConfirm } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
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

/**
 * One routing table, framed because it is a table (§2): each route's
 * destination with its prefix in the port hue, the hop it takes, the device,
 * where it came from and its metric. A route the dashboard made can be
 * removed from its row; the rest say whose they are, and a guarded one says
 * why it stays in its title.
 */
export function RouteTable({
  table,
  onChanged,
  actions,
}: {
  table: NetworkRouting["tables"][number]
  onChanged: () => void
  actions?: React.ReactNode
}) {
  const { confirm, dialog } = useConfirm()
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
      },
    })
  return (
    <Panel>
      <PanelHeader
        title={
          <span className="inline-flex items-center gap-2">
            {table.id === 52 && <ProductGlyph id="tailscale" />}
            {table.name}
            <span className="numeric text-hint font-normal text-muted-foreground">table {table.id}</span>
          </span>
        }
        actions={
          <span className="flex items-center gap-3">
            <span className="numeric text-hint text-muted-foreground">
              {table.routes.length} route{table.routes.length === 1 ? "" : "s"}
            </span>
            {actions}
          </span>
        }
      />
      <PanelBody flush>
        <Table containerClassName="max-h-[28rem]">
          <TableHeader className={stickyTableHeader}>
            <TableRow>
              <TableHead>Destination</TableHead>
              <TableHead>Via</TableHead>
              <TableHead>Device</TableHead>
              <TableHead>From</TableHead>
              <TableHead className="text-right">Metric</TableHead>
              <TableHead className="w-px">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {table.routes.map((route, index) => (
              <TableRow key={`${route.family}:${route.destination}:${route.device}:${index}`}>
                <TableCell title={route.guard}>
                  <span className="inline-flex items-center gap-2">
                    {route.destination === "default" ? (
                      <span className="font-mono font-medium">default</span>
                    ) : (
                      <Cidr cidr={route.destination} />
                    )}
                    {route.type !== "unicast" && <Tag tone="danger">{route.type}</Tag>}
                    {route.family === "inet6" && <Tag>IPv6</Tag>}
                  </span>
                </TableCell>
                <TableCell className="font-mono text-xs">
                  {route.gateway ??
                    (route.nexthops.length > 0
                      ? route.nexthops.map((n) => n.gateway ?? n.device).join(", ")
                      : <span className="text-muted-foreground">on-link</span>)}
                </TableCell>
                <TableCell className="font-mono text-xs">{route.device ?? "—"}</TableCell>
                <TableCell>
                  <span className="inline-flex items-center gap-1.5 text-xs">
                    {OWNER_PRODUCT[route.owner] && <ProductGlyph id={OWNER_PRODUCT[route.owner]!} />}
                    <span className={cn(route.managed ? "text-brand" : "text-muted-foreground")}>
                      {OWNER_WORD[route.owner]}
                    </span>
                    <span className="text-muted-foreground/60">· {route.protocol}</span>
                  </span>
                </TableCell>
                <TableCell className="numeric text-right font-mono text-xs">
                  {route.metric || "—"}
                </TableCell>
                <TableCell>
                  {route.managed && !route.guard && (
                    <Button
                      size="icon-xs"
                      variant="ghost"
                      aria-label={`Remove the route to ${route.destination}`}
                      onClick={() => remove(route)}
                    >
                      <Trash aria-hidden />
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
            {table.routes.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-muted-foreground">
                  This table holds no routes; a rule sending here falls through to the next.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </PanelBody>
      {dialog}
    </Panel>
  )
}

/**
 * The policy rules, framed as a table, in the order the kernel asks them.
 * Only the dashboard's own (priorities 10000–19999) can be removed.
 */
export function RuleTable({
  rules,
  onChanged,
  actions,
}: {
  rules: NetworkRule[]
  onChanged: () => void
  actions?: React.ReactNode
}) {
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
      },
    })
  return (
    <Panel>
      <PanelHeader
        title="Policy rules"
        actions={
          <span className="flex items-center gap-3">
            <span className="numeric text-hint text-muted-foreground">{rules.length}</span>
            {actions}
          </span>
        }
      />
      <PanelBody flush>
        <Table>
          <TableHeader className={stickyTableHeader}>
            <TableRow>
              <TableHead className="text-right">Priority</TableHead>
              <TableHead>Matches</TableHead>
              <TableHead>Then</TableHead>
              <TableHead>Whose</TableHead>
              <TableHead className="w-px">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rules.map((rule) => (
              <TableRow key={`${rule.family}:${rule.priority}:${rule.id}:${rule.from}:${rule.fwmark}`}>
                <TableCell className="numeric text-right font-mono text-xs">{rule.priority}</TableCell>
                <TableCell className="font-mono text-xs" title={rule.guard}>
                  {[
                    rule.from && `from ${rule.from}`,
                    rule.to && `to ${rule.to}`,
                    rule.iif && `iif ${rule.iif}`,
                    rule.oif && `oif ${rule.oif}`,
                    rule.fwmark && `fwmark ${rule.fwmark}`,
                  ]
                    .filter(Boolean)
                    .join(" ") || <span className="text-muted-foreground">everything</span>}
                  {rule.family === "inet6" && <Tag className="ml-2">IPv6</Tag>}
                </TableCell>
                <TableCell className="text-xs">
                  {rule.action === "lookup" ? (
                    <>
                      look up <span className="font-medium">{rule.tableName ?? rule.table}</span>
                    </>
                  ) : (
                    <span className="text-destructive">{rule.action}</span>
                  )}
                </TableCell>
                <TableCell>
                  <span className="inline-flex items-center gap-1.5 text-xs">
                    {rule.owner === "tailscale" && <ProductGlyph id="tailscale" />}
                    <span className={cn(rule.managed ? "text-brand" : "text-muted-foreground")}>
                      {rule.owner === "just-dashboard" ? "made here" : rule.owner}
                    </span>
                  </span>
                </TableCell>
                <TableCell>
                  {rule.managed && !rule.guard && (
                    <Button
                      size="icon-xs"
                      variant="ghost"
                      aria-label={`Remove rule ${rule.priority}`}
                      onClick={() => remove(rule)}
                    >
                      <Trash aria-hidden />
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </PanelBody>
      {dialog}
    </Panel>
  )
}
