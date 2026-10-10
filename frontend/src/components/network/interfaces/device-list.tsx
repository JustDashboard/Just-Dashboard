"use client"

import { cn } from "@/lib/utils"
import type { NetworkLink, NetworkLivePoint } from "@/lib/types"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { AddressLine } from "@/components/network/address"
import { RatePair } from "@/components/network/rate-pair"
import { LinkMark, OWNER_LABEL, ROLE_HUE, kindLabel } from "@/components/network/marks"

/** The groups a device list is read in, top to bottom, and which of them hide by default. */
export const GROUPS: {
  key: string
  title: string
  match: (l: NetworkLink) => boolean
  everythingOnly?: boolean
}[] = [
  { key: "uplink", title: "Uplink", match: (l) => l.role === "uplink" },
  { key: "physical", title: "Network cards", match: (l) => l.role === "physical" },
  {
    key: "tunnel",
    title: "Tunnels",
    match: (l) => l.role === "tunnel" && l.owner !== "kernel",
  },
  { key: "bridge", title: "Bridges", match: (l) => l.role === "bridge" },
  {
    key: "vlan",
    title: "VLANs and virtual devices",
    match: (l) => (l.role === "vlan" || l.role === "virtual") && l.owner !== "kernel",
  },
  {
    key: "container",
    title: "Containers",
    match: (l) => l.role === "container",
    everythingOnly: true,
  },
  {
    key: "kernel",
    title: "The kernel's own",
    match: (l) => l.owner === "kernel",
    everythingOnly: true,
  },
]

/**
 * Every device, grouped by what it is for. Each row opens the device's sheet
 * — its addresses, its settings, its traffic — so each is a lit card
 * (§16): the mark it was made by, its name and kind, its addresses with the
 * prefix in the port hue, who manages it, and its in and out live with the
 * last two minutes under them. A row's edge takes its role's hue, the same
 * hue the topology's lane gives it.
 */
export function DeviceList({
  links,
  series,
  everything,
  query,
  onOpen,
}: {
  links: NetworkLink[]
  series: Record<string, NetworkLivePoint[]>
  everything: boolean
  query: string
  onOpen: (name: string) => void
}) {
  const needle = query.trim().toLowerCase()
  const matches = (l: NetworkLink) =>
    !needle ||
    l.name.toLowerCase().includes(needle) ||
    (l.container ?? "").toLowerCase().includes(needle) ||
    (l.dockerNetwork ?? "").toLowerCase().includes(needle) ||
    l.addresses.some((a) => a.cidr.includes(needle))
  const seen = new Set<string>()
  const groups = GROUPS.filter((g) => everything || !g.everythingOnly || needle)
    .map((g) => {
      const members = links.filter((l) => !seen.has(l.name) && g.match(l) && matches(l))
      members.forEach((l) => seen.add(l.name))
      return { ...g, members }
    })
    .filter((g) => g.members.length > 0)

  if (groups.length === 0) {
    return (
      <p className="py-6 text-body text-muted-foreground">
        {needle ? `Nothing matches “${query}”.` : "No devices to show."}
      </p>
    )
  }
  return (
    <div className="flex min-w-0 flex-col gap-8">
      {groups.map((group) => (
        <Panel plain key={group.key}>
          <PanelHeader
            title={group.title}
            actions={
              <span className="numeric text-hint text-muted-foreground">
                {group.members.length}
              </span>
            }
          />
          <PanelBody>
            <ChoiceList>
              {group.members.map((link) => (
                <DeviceRow
                  key={link.name}
                  link={link}
                  points={series[link.name]}
                  onOpen={() => onOpen(link.name)}
                />
              ))}
            </ChoiceList>
          </PanelBody>
        </Panel>
      ))}
    </div>
  )
}

function DeviceRow({
  link,
  points,
  onOpen,
}: {
  link: NetworkLink
  points?: NetworkLivePoint[]
  onOpen: () => void
}) {
  const up = link.adminUp && (link.carrier || link.state === "unknown")
  return (
    <ChoiceRow
      leading={
        <span className="relative flex">
          <LinkMark link={link} />
          <span
            aria-hidden
            className="absolute -bottom-0.5 -left-0.5 h-3 w-0.5 rounded-full"
            style={{ background: ROLE_HUE[link.role] }}
          />
        </span>
      }
      title={
        <span className="inline-flex min-w-0 items-center gap-2">
          <span className="truncate font-mono">{link.name}</span>
          <Tag>{kindLabel(link)}</Tag>
          {link.managed && <Tag>made here</Tag>}
        </span>
      }
      verb={`Open ${link.name}`}
      description={
        <span className="inline-flex min-w-0 items-center gap-2">
          <AddressLine addresses={link.addresses} />
          <span className="text-muted-foreground/50">·</span>
          <span className="truncate">{describe(link)}</span>
        </span>
      }
      trailing={
        <span className="flex items-center gap-4">
          <RatePair rx={link.rxRate} tx={link.txRate} points={points} />
          <Status
            tone={up ? "running" : link.adminUp ? "warning" : "stopped"}
            label={up ? "Up" : link.adminUp ? "No carrier" : "Down"}
            className={cn("hidden w-20 md:inline-flex")}
          />
        </span>
      }
      onSelect={onOpen}
    />
  )
}

/** The second half of a row's second line: what this device is attached to, and who keeps it. */
function describe(link: NetworkLink) {
  if (link.dockerJoin === "unresolved")
    return `container not joined · on ${link.master ?? "no bridge"}`
  if (link.dockerJoin === "unknown") return "Docker network unknown"
  if (link.container) return `${link.container} · on ${link.master ?? "no bridge"}`
  if (link.dockerNetwork) return `Docker network ${link.dockerNetwork}`
  if (link.role === "bridge" && link.members?.length)
    return `${link.members.length} port${link.members.length === 1 ? "" : "s"}: ${link.members.slice(0, 3).join(", ")}`
  if (link.parent) return `on ${link.parent}${link.master ? ` · port of ${link.master}` : ""}`
  if (link.remote) return `to ${link.remote}`
  if (link.master) return `port of ${link.master}`
  return OWNER_LABEL[link.owner]
}
