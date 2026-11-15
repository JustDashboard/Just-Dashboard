"use client"

import { useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ClockRewind, Location, Play, Plus, StopCircle, Trash } from "@/components/icons"
import { del, post } from "@/lib/api"
import { bytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { WGInterface } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { VerbMenu, type Verb } from "@/components/verbs"
import { findingsWord, tunnelHref, tunnelMoved, tunnelState } from "./tunnel-reading"

/**
 * What can be done to a tunnel, with its confirmations: bring it up, take it
 * down, remove it, and turn each family's exit on or off. One copy, for the
 * VPN page's line and the tunnel's own page.
 */
export function useTunnelControl(tunnel: WGInterface, onChanged: () => void) {
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState(false)
  const base = `/network/vpn/wireguard/${encodeURIComponent(tunnel.name)}`
  const act = async (run: () => Promise<unknown>, label: string) => {
    setBusy(true)
    try {
      await run()
      notify.success(label)
      onChanged()
    } catch (err) {
      notify.error(`${tunnel.name} was not changed`, err)
    } finally {
      setBusy(false)
    }
  }
  const up = () => void act(() => post(`${base}/up`), `${tunnel.name} is up`)
  const down = () =>
    confirm({
      title: `Take ${tunnel.name} down`,
      confirmLabel: "Take down",
      description: <p>Every device and site on it is disconnected until it is brought up again.</p>,
      action: async () => {
        await post(`${base}/down`)
        notify.success(`${tunnel.name} is down`)
        onChanged()
      },
    })
  const remove = () =>
    confirm({
      title: `Remove ${tunnel.name}`,
      confirmLabel: "Remove",
      description: (
        <p>
          The tunnel stops, its peers are cut off and it is not started at boot. Its file is moved
          aside rather than deleted, so its key is not lost if this was a mistake.
        </p>
      ),
      action: async () => {
        const result = await del<{ firewall: { removed: boolean; reason?: string } }>(base)
        notify.success(`${tunnel.name} removed`, {
          description: result.firewall?.removed
            ? "Its dashboard-created firewall opening was removed."
            : result.firewall?.reason,
        })
        onChanged()
      },
    })
  const setExit = (on: boolean) => {
    if (on)
      return void act(() => post(`${base}/exit`, { on }), `${tunnel.name} has IPv4 exit rules`)
    confirm({
      title: `Stop using ${tunnel.name} as an exit node`,
      confirmLabel: "Turn off",
      description: (
        <p>
          Clients using this tunnel for internet access lose that access until they change their
          configuration or this exit is enabled again.
        </p>
      ),
      action: async () => {
        await post(`${base}/exit`, { on: false })
        notify.success(`${tunnel.name} is a private network only`)
        onChanged()
      },
    })
  }
  const setIPv6Exit = (on: boolean) => {
    if (on)
      return void act(
        () => post(`${base}/exit`, { on: true, ipv6: true }),
        `${tunnel.name} has IPv4 and IPv6 exit rules`,
      )
    confirm({
      title: `Turn off ${tunnel.name}'s IPv6 exit`,
      confirmLabel: "Turn off IPv6",
      description: "Full-tunnel clients lose IPv6 internet access. IPv4 exit remains configured.",
      action: async () => {
        await post(`${base}/exit`, { on: true, ipv6: false })
        onChanged()
      },
    })
  }
  return { busy, up, down, remove, setExit, setIPv6Exit, dialog }
}

export type TunnelControl = ReturnType<typeof useTunnelControl>

/**
 * The line a tunnel opens on, laid out as a project's header: WireGuard's
 * mark, the tunnel's name with who is online after it, then its network, its
 * port and what it has moved. Adding a device is the one command; joining a
 * site, its history, taking it down and removing it are in the menu. A tunnel
 * written by hand says so and offers nothing that would change it.
 *
 * Taking a tunnel down and removing it are destructive routes, so an account
 * without that capability is not shown them; adding a peer is refused to a
 * session opened through Just Hub, which is not offered it (`onAdd`).
 */
export function TunnelIdentity({
  tunnel,
  control,
  onAdd,
  onCreate,
  linked,
  className,
}: {
  tunnel: WGInterface
  control: TunnelControl
  /** Undefined for a session the server refuses a new peer: nothing offers one. */
  onAdd?: (kind: "device" | "site") => void
  /** Offered in the menu on the VPN page, where another tunnel would appear. */
  onCreate?: () => void
  /** The name leads to the tunnel's own page, and the menu to its history. */
  linked?: boolean
  className?: string
}) {
  const router = useRouter()
  const { can } = useAuth()
  const destructive = can("destructive")
  const state = tunnelState(tunnel)
  const findings = tunnel.alerts?.length ?? 0
  const moved = tunnelMoved(tunnel)

  const verbs: Verb[] = []
  if (tunnel.managed && onAdd)
    verbs.push({ key: "site", label: "Join a site", icon: Location, run: () => onAdd("site") })
  if (linked)
    verbs.push({
      key: "history",
      label: "History",
      icon: ClockRewind,
      run: () => router.push(tunnelHref(tunnel.name, "history")),
    })
  if (tunnel.managed) {
    if (!tunnel.up)
      verbs.push({
        key: "up",
        label: "Bring up",
        icon: Play,
        run: control.up,
        disabled: control.busy,
      })
    else if (destructive)
      verbs.push({
        key: "down",
        label: "Take down",
        icon: StopCircle,
        run: control.down,
        disabled: control.busy,
      })
  }
  if (onCreate)
    verbs.push({ key: "create", label: "Create another tunnel", icon: Plus, run: onCreate })
  if (tunnel.managed && destructive)
    verbs.push({ key: "remove", label: "Remove", icon: Trash, run: control.remove, danger: true })

  return (
    <HostIdentity
      label={`About ${tunnel.name}`}
      slot="tunnel-identity"
      mark="wireguard"
      className={className}
      title={
        linked ? (
          <Link
            href={tunnelHref(tunnel.name)}
            className="truncate rounded-sm font-mono focus-ring hover:underline"
          >
            {tunnel.name}
          </Link>
        ) : (
          <span className="truncate font-mono">{tunnel.name}</span>
        )
      }
      state={
        <>
          <Status tone={state.tone} label={state.label} live={state.live} />
          {findings > 0 && <Status tone="warning" label={findingsWord(findings)} />}
        </>
      }
      facts={
        <>
          <HostFact>
            <span className="block truncate font-mono">
              {tunnel.subnet || tunnel.addresses[0] || "no network"}
            </span>
          </HostFact>
          <FactDot />
          <HostFact>UDP {tunnel.listenPort || "—"}</HostFact>
          {moved > 0 && (
            <>
              <FactDot />
              <HostFact>
                <span className="numeric">moved {bytes(moved)}</span>
              </HostFact>
            </>
          )}
          {!tunnel.managed && (
            <>
              <FactDot />
              <HostFact>written by hand, read and never changed here</HostFact>
            </>
          )}
        </>
      }
      command={
        tunnel.managed && onAdd ? (
          <Button size="sm" onClick={() => onAdd("device")} disabled={control.busy}>
            <Plus aria-hidden className="size-3.5" />
            Add a device
          </Button>
        ) : undefined
      }
      menu={verbs.length > 0 && <VerbMenu verbs={verbs} label={`${tunnel.name} actions`} />}
    />
  )
}

/** The line's place while the tunnels are read: WireGuard's mark and grey words. */
export function IdentitySkeleton({ mark }: { mark: string }) {
  return (
    <HostIdentity
      mark={mark}
      // Each grey bar stands in a box of the line it stands for, so the line
      // is the height it will be and nothing under it moves when it is read.
      title={
        <span className="flex h-[1lh] items-center">
          <Skeleton className="h-4 w-28" />
        </span>
      }
      state={
        <span className="flex h-4 items-center">
          <Skeleton className="h-3 w-24" />
        </span>
      }
      facts={<Skeleton className="my-0.5 h-3 w-64 max-w-full" />}
    />
  )
}
