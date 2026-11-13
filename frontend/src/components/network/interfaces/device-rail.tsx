"use client"

import { Fragment, useEffect, useRef } from "react"
import { rate } from "@/lib/format"
import type { NetworkLink } from "@/lib/types"
import { cn } from "@/lib/utils"
import { SearchInput } from "@/components/page"
import { StatusDot } from "@/components/status-dot"
import { LoadingRows } from "@/components/state"
import { RX, TX } from "@/components/network/rate-pair"
import { LinkMark, kindLabel } from "@/components/network/marks"
import { deviceState } from "./device-facts"

type Group = { key: string; title: string; members: NetworkLink[] }

/**
 * The Interfaces list, folded into a column beside the device it opened: the
 * same groups in the same order, the list's own search and its Everything
 * switch, so the next device to look at is one press away. Each row is the
 * device as what made it, its name, where it answers and its in and out; the
 * one on screen is filled.
 */
export function DeviceRail({
  groups,
  current,
  pinned,
  loading,
  query,
  onQuery,
  everything,
  onEverything,
  hidden,
  onOpen,
  className,
}: {
  groups: Group[]
  current: string
  /** The device on screen, drawn first when the list's filter leaves it out. */
  pinned?: NetworkLink
  loading: boolean
  query: string
  onQuery: (value: string) => void
  everything: boolean
  onEverything: (value: boolean) => void
  /** How many devices Everything would add. */
  hidden: number
  onOpen: (name: string) => void
  className?: string
}) {
  const list = useRef<HTMLOListElement>(null)
  // The device on screen is kept in view: opened from a link or by Alt+↓
  // past the column's edge, it would otherwise be off its own list.
  useEffect(() => {
    const row = list.current?.querySelector<HTMLElement>("[aria-current='page']")
    row?.scrollIntoView({ block: "nearest" })
  }, [current])
  const count = groups.reduce((sum, group) => sum + group.members.length, 0)

  return (
    <nav
      aria-label="Devices"
      data-slot="device-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="Name, address or container"
          aria-label="Search the devices"
          containerClassName="sm:w-full"
        />
        <p className="flex min-w-0 items-center justify-between gap-2 text-hint text-muted-foreground">
          <span className="numeric truncate">
            {loading && count === 0
              ? "Reading the devices…"
              : `${count} ${count === 1 ? "device" : "devices"}`}
          </span>
          {(hidden > 0 || everything) && (
            <button
              type="button"
              aria-pressed={everything}
              onClick={() => onEverything(!everything)}
              className="shrink-0 rounded-sm font-medium focus-ring hover:text-foreground"
            >
              {everything ? "Real devices only" : `Everything (+${hidden})`}
            </button>
          )}
        </p>
      </div>
      <ol
        ref={list}
        aria-label="Devices to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {pinned && <RailRow link={pinned} current onOpen={onOpen} />}
        {pinned && count > 0 && <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />}
        {groups.map((group) => (
          <Fragment key={group.key}>
            <li aria-hidden className="eyebrow px-2 pt-3 pb-1 first:pt-0">
              {group.title}
            </li>
            {group.members.map((link) => (
              <RailRow
                key={link.name}
                link={link}
                current={link.name === current}
                onOpen={onOpen}
              />
            ))}
          </Fragment>
        ))}
        {loading && count === 0 && <LoadingRows rows={8} />}
        {!loading && count === 0 && !pinned && (
          <li className="px-3 py-4 text-hint text-muted-foreground">No device matches.</li>
        )}
      </ol>
    </nav>
  )
}

function RailRow({
  link,
  current,
  onOpen,
}: {
  link: NetworkLink
  current: boolean
  onOpen: (name: string) => void
}) {
  const state = deviceState(link)
  const address =
    link.addresses.find((a) => a.scope !== "link" && a.scope !== "host") ?? link.addresses[0]
  return (
    <li className="min-w-0">
      <button
        type="button"
        data-workspace-item={link.name}
        data-workspace-name={link.name}
        aria-current={current ? "page" : undefined}
        aria-label={`${link.name}, ${state.label.toLowerCase()}`}
        onClick={() => onOpen(link.name)}
        className={cn(
          "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <span className="relative flex shrink-0">
          <LinkMark link={link} className="size-7 rounded-md [&_img]:size-4 [&_svg]:size-3.5" />
          <span
            aria-hidden
            className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
          >
            <StatusDot tone={state.tone} className="size-1.5" />
          </span>
        </span>
        <span className="min-w-0 flex-1">
          <span
            className={cn(
              "block truncate font-mono text-body",
              current ? "font-semibold" : "font-medium",
            )}
          >
            {link.name}
          </span>
          <span className="block truncate text-hint text-muted-foreground">
            {address ? (
              <span className="font-mono">{address.cidr.split("/")[0]}</span>
            ) : (
              (link.container ?? kindLabel(link))
            )}
          </span>
        </span>
        <span className="numeric flex shrink-0 flex-col items-end font-mono text-micro leading-tight">
          <span style={{ color: RX }}>↓ {rate(link.rxRate)}</span>
          <span style={{ color: TX }}>↑ {rate(link.txRate)}</span>
        </span>
      </button>
    </li>
  )
}
