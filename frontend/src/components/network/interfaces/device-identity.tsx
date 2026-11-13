"use client"

import Link from "next/link"
import { useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import {
  ArrowDown,
  ArrowUp,
  Layers,
  LockClosed,
  MagnifyingGlass,
  MoreHorizontal,
  Trash,
} from "@/components/icons"
import { del, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { NetworkLink } from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { InfoTip } from "@/components/form"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Status, StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { AddressLine } from "@/components/network/address"
import { LinkMark, OWNER_LABEL, ROLE_LABEL, kindLabel } from "@/components/network/marks"
import { describeDevice, deviceState } from "./device-facts"

const TILE = "size-10 rounded-lg [&_img]:size-6 [&_svg]:size-5"
const FACTS = "flex h-4 w-full min-w-0 items-center gap-x-3 overflow-hidden whitespace-nowrap"

/**
 * The line a device's page opens on, laid out as a project's header: the
 * mark of what made it, its name as the kernel spells it, whether it is up,
 * then what it is and where it answers. Setting a device up is the one
 * command the line shows, and only for a device that is down; setting one
 * down and deleting it can cost the operator the server, so they are words
 * in the menu, held with the guard's own sentence under them when the server
 * would refuse.
 */
export function DeviceIdentity({
  link,
  onChanged,
  onDeleted,
  onOpenNamespace,
}: {
  link: NetworkLink
  onChanged: () => void
  onDeleted: () => void
  onOpenNamespace: (kind: "named" | "container", name: string) => void
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const destructive = admin && can("destructive")
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState(false)
  const state = deviceState(link)
  const name = encodeURIComponent(link.name)

  const setUp = async () => {
    setBusy(true)
    try {
      await post(`/network/links/${name}/up`)
      notify.success(`${link.name} is up`)
      onChanged()
    } catch (err) {
      notify.error(`${link.name}: not changed`, err)
    } finally {
      setBusy(false)
    }
  }
  const setDown = () =>
    confirm({
      title: `Set ${link.name} down`,
      confirmLabel: "Set down",
      description: (
        <p>
          Everything on <span className="font-mono">{link.name}</span> stops until it is set up
          again
          {link.managed
            ? "."
            : ", and a device the dashboard did not make comes back up at the next boot."}
        </p>
      ),
      action: async () => {
        await post(`/network/links/${name}/down`)
        notify.success(`${link.name} is down`)
        onChanged()
        return "reported"
      },
    })
  const remove = () =>
    confirm({
      title: `Delete ${link.name}`,
      confirmLabel: "Delete",
      description: (
        <p>
          The device goes now and is not made again at boot. Anything using{" "}
          <span className="font-mono">{link.name}</span> (a bridge port, a route, a container) loses
          it.
        </p>
      ),
      action: async () => {
        await del(`/network/links/${name}`)
        notify.success(`${link.name} deleted`)
        onDeleted()
        return "reported"
      },
    })

  const canSetDown = destructive && link.adminUp
  const canDelete = destructive && link.managed
  const inside = link.peerNamespace
    ? ({ kind: "named", name: link.peerNamespace } as const)
    : link.container
      ? ({ kind: "container", name: link.container } as const)
      : undefined
  const hasMenu = canSetDown || canDelete || !!inside

  return (
    <>
      <HostIdentity
        slot="device-identity"
        label={`About ${link.name}`}
        className="border-b-0 pb-0"
        logo={<LinkMark link={link} className={TILE} />}
        title={<span className="truncate font-mono">{link.name}</span>}
        state={
          <>
            {busy ? (
              <span className="inline-flex items-center gap-1.5 text-xs font-medium">
                <StatusDot tone="notice" />
                <TextShimmer>Setting up…</TextShimmer>
              </span>
            ) : (
              <Status tone={state.tone} label={state.label} />
            )}
            {link.guard && (
              <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                <LockClosed aria-hidden className="size-3" />
                Guarded
                <InfoTip label={`Why ${link.name} is guarded`}>{link.guard}</InfoTip>
              </span>
            )}
          </>
        }
        facts={
          // One line at every width, so the line is as tall with a device on
          // it as while it waits for one: a phone gets the first address and
          // leaves who keeps the device to its Settings.
          <span className={FACTS}>
            <span className="shrink-0">
              {ROLE_LABEL[link.role]}
              {kindLabel(link).toLowerCase() !== ROLE_LABEL[link.role].toLowerCase() &&
                ` · ${kindLabel(link)}`}
            </span>
            {link.managed && (
              <>
                <FactDot />
                <span className="shrink-0">made here</span>
              </>
            )}
            <FactDot />
            <AddressLine addresses={link.addresses} max={1} className="sm:hidden" />
            <AddressLine addresses={link.addresses} className="shrink-0 max-sm:hidden" />
            <span className="contents max-sm:hidden">
              <FactDot />
              <span className="truncate">{describeDevice(link, OWNER_LABEL)}</span>
            </span>
          </span>
        }
        command={
          admin && !link.adminUp ? (
            <Button size="sm" pending={busy} disabled={busy} onClick={() => void setUp()}>
              <ArrowUp aria-hidden />
              Set up
            </Button>
          ) : undefined
        }
        menu={
          hasMenu ? (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label={`More for ${link.name}`}
                  className="[&_svg:not([class*='size-'])]:size-3.5"
                >
                  <MoreHorizontal />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="min-w-52">
                {inside && (
                  <DropdownMenuItem onSelect={() => onOpenNamespace(inside.kind, inside.name)}>
                    <Layers className="size-3.5" />
                    {inside.kind === "named"
                      ? `Open namespace ${inside.name}`
                      : `Open ${inside.name}’s namespace`}
                  </DropdownMenuItem>
                )}
                {admin && link.container && (
                  <DropdownMenuItem asChild>
                    <Link
                      href={`/network/investigate?container=${encodeURIComponent(link.container)}`}
                    >
                      <MagnifyingGlass className="size-3.5" />
                      Investigate a connection from {link.container}
                    </Link>
                  </DropdownMenuItem>
                )}
                {inside && (canSetDown || canDelete) && <DropdownMenuSeparator />}
                {canSetDown && (
                  <DropdownMenuItem disabled={!!link.guard} onSelect={setDown}>
                    <ArrowDown className="size-3.5" />
                    Set down
                  </DropdownMenuItem>
                )}
                {canDelete && (
                  <DropdownMenuItem variant="destructive" disabled={!!link.guard} onSelect={remove}>
                    <Trash className="size-3.5" />
                    Delete
                  </DropdownMenuItem>
                )}
                {link.guard && (canSetDown || canDelete) && (
                  <DropdownMenuLabel className="max-w-64 text-hint leading-snug font-normal whitespace-normal text-muted-foreground">
                    {link.guard}
                  </DropdownMenuLabel>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
          ) : undefined
        }
      />
      {dialog}
    </>
  )
}

/** The line while the devices are being read: the same line, with nothing on it yet. */
export function DeviceIdentityLoading({ name }: { name: string }) {
  return (
    <HostIdentity
      slot="device-identity"
      className="border-b-0 pb-0"
      logo={<span className="size-10 shrink-0 rounded-lg border border-hairline bg-control" />}
      title={<span className="truncate font-mono">{name}</span>}
      state={<TextShimmer className="text-xs font-medium">Reading…</TextShimmer>}
      facts={<span className={FACTS}>&nbsp;</span>}
    />
  )
}
