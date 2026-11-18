"use client"

import { useSearchParams } from "next/navigation"
import { plural, rate } from "@/lib/format"
import type { NetworkLink } from "@/lib/types"
import { StatusDot } from "@/components/status-dot"
import { Rail, RailAction, RailLine, RailName, RailRow } from "@/components/rail"
import { RX, TX } from "@/components/network/rate-pair"
import { LinkMark, kindLabel } from "@/components/network/marks"
import { deviceHref, deviceState } from "./device-facts"

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
  error,
  onRetry,
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
  /** The list could not be read: said in place of the rows. */
  error?: Error
  onRetry?: () => void
  query: string
  onQuery: (value: string) => void
  everything: boolean
  onEverything: (value: boolean) => void
  /** How many devices Everything would add. */
  hidden: number
  /** Opens a device carrying the page's view; the row's link does the same without it. */
  onOpen?: (name: string) => void
  className?: string
}) {
  const tab = useSearchParams().get("tab") ?? undefined
  const rows = groups.flatMap((group) =>
    group.members.map((link, index) => ({ link, head: index === 0 ? group.title : undefined })),
  )
  return (
    <Rail
      slot="device-rail"
      label="Devices"
      search={{ placeholder: "Name, address or container", query, onQuery }}
      caption={
        loading && rows.length === 0 ? "Reading the devices…" : plural(rows.length, "device")
      }
      action={
        (hidden > 0 || everything) && (
          <RailAction pressed={everything} onClick={() => onEverything(!everything)}>
            {everything ? "Real devices only" : `Everything (+${hidden})`}
          </RailAction>
        )
      }
      rows={rows}
      keyOf={(row) => row.link.name}
      current={(row) => row.link.name === current}
      pinned={pinned && { link: pinned, head: undefined }}
      divideBefore={(row) => row.head}
      row={(row, on) => (
        <DeviceRow
          link={row.link}
          current={on}
          href={deviceHref(row.link.name, tab)}
          onOpen={onOpen}
        />
      )}
      loading={loading}
      loadingRows={8}
      error={error}
      onRetry={onRetry}
      empty="No device matches."
      className={className}
    />
  )
}

function DeviceRow({
  link,
  current,
  href,
  onOpen,
}: {
  link: NetworkLink
  current: boolean
  href: string
  onOpen?: (name: string) => void
}) {
  const state = deviceState(link)
  const address =
    link.addresses.find((a) => a.scope !== "link" && a.scope !== "host") ?? link.addresses[0]
  return (
    <RailRow
      item={link.name}
      name={link.name}
      label={`${link.name}, ${state.label.toLowerCase()}`}
      current={current}
      href={href}
      onOpen={onOpen && (() => onOpen(link.name))}
      mark={<LinkMark link={link} className="size-7 rounded-md [&_img]:size-4 [&_svg]:size-3.5" />}
      dot={<StatusDot tone={state.tone} className="size-1.5" />}
      end={
        <span className="numeric flex shrink-0 flex-col items-end font-mono text-micro leading-tight">
          <span style={{ color: RX }}>↓ {rate(link.rxRate)}</span>
          <span style={{ color: TX }}>↑ {rate(link.txRate)}</span>
        </span>
      }
    >
      <RailName current={current} mono>
        {link.name}
      </RailName>
      <RailLine>
        {address ? (
          <span className="font-mono">{address.cidr.split("/")[0]}</span>
        ) : (
          (link.container ?? kindLabel(link))
        )}
      </RailLine>
    </RailRow>
  )
}
