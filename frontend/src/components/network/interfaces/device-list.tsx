"use client"

import type { NetworkLink, NetworkLivePoint } from "@/lib/types"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { AddressLine } from "@/components/network/address"
import { RatePair } from "@/components/network/rate-pair"
import { LinkMark, OWNER_LABEL, ROLE_HUE, kindLabel } from "@/components/network/marks"
import { describeDevice, deviceHref, deviceState, groupDevices } from "./device-facts"

export { GROUPS } from "./device-facts"

/**
 * Every device, grouped by what it is for. Each row opens the device's page
 * (its traffic, its addresses, its settings), so each is a lit card (§16):
 * the mark it was made by, its name and kind, its addresses with the prefix
 * in the port hue, who manages it, and its in and out live with the last two
 * minutes under them. A row's edge takes its role's hue, the same hue the
 * topology's lane gives it.
 */
export function DeviceList({
  links,
  series,
  everything,
  query,
}: {
  links: NetworkLink[]
  series: Record<string, NetworkLivePoint[]>
  everything: boolean
  query: string
}) {
  const groups = groupDevices(links, { everything, query })

  if (groups.length === 0) {
    return (
      <p className="py-6 text-body text-muted-foreground">
        {query.trim() ? `Nothing matches “${query}”.` : "No devices to show."}
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
                <DeviceRow key={link.name} link={link} points={series[link.name]} />
              ))}
            </ChoiceList>
          </PanelBody>
        </Panel>
      ))}
    </div>
  )
}

function DeviceRow({ link, points }: { link: NetworkLink; points?: NetworkLivePoint[] }) {
  const state = deviceState(link)
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
          <span className="truncate">{describeDevice(link, OWNER_LABEL)}</span>
        </span>
      }
      trailing={
        <span className="flex items-center gap-4">
          <RatePair rx={link.rxRate} tx={link.txRate} points={points} />
          <Status tone={state.tone} label={state.label} className="hidden w-20 md:inline-flex" />
        </span>
      }
      href={deviceHref(link.name)}
    />
  )
}
