"use client"

import { useState } from "react"
import { Cross, Plus } from "@/components/icons"
import { get, post } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { HeadscaleView, TailscaleView } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { HostFact, FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/** A peer's system as the product it is. */
function osProduct(os: string) {
  const o = os.toLowerCase()
  if (o.includes("windows")) return "windows"
  if (o.includes("mac") || o.includes("ios") || o.includes("darwin")) return "apple"
  if (o.includes("android")) return "android"
  if (o.includes("linux")) return "linux"
  return undefined
}

/**
 * This server on its tailnet: who it is there, what it offers — an exit
 * node, subnet routes — and every other machine with how it is reached,
 * direct or through which relay. The two offers are the only things that
 * change here: what this server does for the tailnet. Using another node as
 * an exit, shields up and logging out are not offered, because each can cut
 * off the browser that is reading this page.
 */
export function TailscaleBlock({
  tailscale,
  headscale,
  onChanged,
}: {
  tailscale: TailscaleView
  headscale: HeadscaleView
  onChanged: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [route, setRoute] = useState("")
  const [routeError, setRouteError] = useState<Error>()
  const self = tailscale.self
  const apply = async (
    body: { advertiseExitNode?: boolean; advertiseRoutes?: string[] },
    label: string,
  ) => {
    setBusy(true)
    try {
      const res = await post<{ note: string }>("/network/vpn/tailscale", body)
      notify.success(label, { description: res.note })
      onChanged()
      return true
    } catch (err) {
      notify.error("Tailscale was not changed", err)
      return false
    } finally {
      setBusy(false)
    }
  }
  const offer = async () => {
    const draft = route.trim()
    if (!draft || busy) return
    setBusy(true)
    setRouteError(undefined)
    try {
      // Refresh the preferences before retrying: a lost response may have
      // accepted the previous offer, and the poll may not have caught up.
      const latest = await get<{ tailscale: TailscaleView }>("/network/vpn")
      if (latest.tailscale.prefsReadable === false) {
        throw new Error(
          "Tailscale preferences could not be read. Refresh them before offering a network.",
        )
      }
      const current = latest.tailscale.prefs.advertiseRoutes
      if (current.includes(draft)) {
        notify.success(`${draft} is already offered`)
        onChanged()
        setRoute((held) => (held.trim() === draft ? "" : held))
        return
      }
      const result = await post<{ note: string }>("/network/vpn/tailscale", {
        advertiseRoutes: [...new Set([...current, draft])],
      })
      notify.success(`${draft} offered`, { description: result.note })
      onChanged()
      setRoute((held) => (held.trim() === draft ? "" : held))
    } catch (err) {
      const error = err instanceof Error ? err : new Error(String(err))
      setRouteError(error)
      notify.error("Tailscale was not changed", error)
    } finally {
      setBusy(false)
    }
  }
  const routes = tailscale.prefs.advertiseRoutes
  const online = tailscale.peers.filter((p) => p.online).length

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <HostIdentity
        mark="tailscale"
        title={self ? self.dnsName.replace(/\.$/, "") || self.hostName : "Tailscale"}
        facts={
          <>
            <HostFact>
              <span className="font-mono">{self?.tailscaleIps.join(", ") || "no address"}</span>
            </HostFact>
            <FactDot />
            <HostFact>{tailscale.tailnet || "tailnet"}</HostFact>
            <FactDot />
            <HostFact product={tailscale.controlServer === "tailscale" ? "tailscale" : "headscale"}>
              {tailscale.controlServer === "tailscale"
                ? "Tailscale's coordination"
                : tailscale.controlUrl}
            </HostFact>
            {tailscale.version && (
              <>
                <FactDot />
                <HostFact>v{tailscale.version.split("-")[0]}</HostFact>
              </>
            )}
          </>
        }
        aside={
          <Status
            tone={tailscale.running ? "running" : "warning"}
            label={
              tailscale.running
                ? `${online} of ${tailscale.peers.length} online`
                : tailscale.backendState
            }
            live={tailscale.running}
            className="text-body"
          />
        }
      />
      {tailscale.warnings.map((w) => (
        <Notice key={w} title="Tailscale">
          {w}
        </Notice>
      ))}

      <div className="grid min-w-0 gap-x-10 gap-y-6 md:grid-cols-2">
        <div className="flex min-w-0 flex-col gap-2">
          <div className="flex items-center justify-between gap-4">
            <div>
              <p className="text-title font-medium">Offer as an exit node</p>
              <p className="text-hint text-muted-foreground">
                Other devices can send their internet through this server.
              </p>
            </div>
            <Switch
              checked={tailscale.prefs.advertisingExitNode}
              disabled={busy || !tailscale.running}
              onCheckedChange={(on) =>
                void apply(
                  { advertiseExitNode: on },
                  on ? "Offered as an exit node" : "No longer an exit node",
                )
              }
              aria-label="Offer as an exit node"
            />
          </div>
          {tailscale.prefs.usingExitNode && (
            <p className="text-hint text-warning">
              This server sends its own traffic through another exit node.
            </p>
          )}
        </div>
        <div className="flex min-w-0 flex-col gap-2">
          <p className="text-title font-medium">Subnet routes</p>
          <p className="text-hint text-muted-foreground">
            Networks behind this server the tailnet may reach through it.
          </p>
          <div className="flex flex-wrap items-center gap-2">
            {routes.map((r) => (
              <span
                key={r}
                className="inline-flex items-center gap-1 rounded-md border border-hairline px-2 py-0.5 font-mono text-xs"
              >
                {r}
                <button
                  type="button"
                  aria-label={`Stop offering ${r}`}
                  className="rounded-sm text-muted-foreground focus-ring hover:text-foreground"
                  disabled={busy}
                  onClick={() =>
                    void apply({ advertiseRoutes: routes.filter((x) => x !== r) }, `${r} withdrawn`)
                  }
                >
                  <Cross className="size-3" />
                </button>
              </span>
            ))}
            <form
              className="flex items-center gap-1.5"
              onSubmit={(event) => {
                event.preventDefault()
                void offer()
              }}
            >
              <Input
                value={route}
                onChange={(event) => {
                  setRoute(event.target.value)
                  setRouteError(undefined)
                }}
                aria-describedby={routeError ? "tailscale-route-error" : undefined}
                placeholder="10.0.4.0/24"
                aria-label="A network to offer"
                className="h-7 w-36 font-mono text-xs"
              />
              <Button
                size="icon-xs"
                variant="outline"
                type="submit"
                disabled={busy || !route.trim()}
                aria-label="Offer it"
              >
                <Plus aria-hidden />
              </Button>
            </form>
          </div>
          {routeError && (
            <div
              id="tailscale-route-error"
              role="alert"
              className="space-y-2 text-hint text-destructive"
            >
              <p>{routeError.message} Your network draft has been kept.</p>
              <Button
                size="xs"
                variant="outline"
                disabled={busy || !route.trim()}
                onClick={() => void offer()}
              >
                Retry offer
              </Button>
            </div>
          )}
        </div>
      </div>

      <Panel>
        <PanelHeader
          title="Tailnet"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {tailscale.peers.length} machine{tailscale.peers.length === 1 ? "" : "s"}
            </span>
          }
        />
        <PanelBody flush>
          <Table containerClassName="max-h-[30rem]">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead>Machine</TableHead>
                <TableHead>Address</TableHead>
                <TableHead>Reached</TableHead>
                <TableHead className="text-right">Moved</TableHead>
                <TableHead>State</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {tailscale.peers.map((p) => (
                <TableRow key={p.id}>
                  <TableCell>
                    <span className="flex min-w-0 items-center gap-3">
                      <ProductLogo id={osProduct(p.os)} size="sm" />
                      <span className="min-w-0">
                        <span className="flex items-center gap-2 text-body font-medium">
                          <span className="truncate">{p.hostName}</span>
                          {p.exitNodeOption && <Tag>exit node</Tag>}
                          {p.exitNode && <Tag tone="warning">in use</Tag>}
                        </span>
                        <span className="block truncate text-hint text-muted-foreground">
                          {p.userLoginName ?? p.os}
                          {p.primaryRoutes?.length ? ` · routes ${p.primaryRoutes.join(", ")}` : ""}
                        </span>
                      </span>
                    </span>
                  </TableCell>
                  <TableCell className="font-mono text-xs">{p.tailscaleIps[0]}</TableCell>
                  <TableCell className="text-xs">
                    {p.direct ? (
                      <span>
                        direct <span className="font-mono text-muted-foreground">{p.curAddr}</span>
                      </span>
                    ) : p.relay ? (
                      <span className="text-muted-foreground">relayed · {p.relay}</span>
                    ) : (
                      <span className="text-muted-foreground">—</span>
                    )}
                  </TableCell>
                  <TableCell className="numeric text-right font-mono text-xs">
                    {p.rxBytes || p.txBytes ? `↓ ${bytes(p.rxBytes)} ↑ ${bytes(p.txBytes)}` : "—"}
                  </TableCell>
                  <TableCell>
                    <Status
                      tone={p.expired ? "danger" : p.online ? "running" : "stopped"}
                      label={
                        p.expired
                          ? "Key expired"
                          : p.online
                            ? p.active
                              ? "Active"
                              : "Online"
                            : p.lastSeen
                              ? `Seen ${relativeTime(p.lastSeen)}`
                              : "Offline"
                      }
                      live={p.active}
                    />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </PanelBody>
      </Panel>

      {headscale.installed && (
        <Panel plain>
          <PanelHeader
            title={
              <span className="inline-flex items-center gap-2">
                <ProductGlyph id="headscale" />
                Headscale
              </span>
            }
            actions={
              <span className="text-hint text-muted-foreground">
                {headscale.container ? `container ${headscale.container}` : "on this server"} ·{" "}
                {headscale.nodes.length} node{headscale.nodes.length === 1 ? "" : "s"}
              </span>
            }
          />
          <PanelBody>
            {headscale.error ? (
              <p className="text-body text-muted-foreground">{headscale.error}</p>
            ) : (
              <ul className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
                {headscale.nodes.map((n) => (
                  <li key={n.id} className="flex min-w-0 items-center gap-2 text-body">
                    <span
                      className={cn(
                        "size-1.5 shrink-0 rounded-full",
                        n.online ? "bg-success" : "bg-muted-foreground",
                      )}
                    />
                    <span className="truncate">{n.givenName || n.name}</span>
                    <span className="truncate font-mono text-hint text-muted-foreground">
                      {n.ipAddresses[0]}
                    </span>
                    <span className="ml-auto text-hint text-muted-foreground">{n.user}</span>
                  </li>
                ))}
              </ul>
            )}
          </PanelBody>
        </Panel>
      )}
    </div>
  )
}
