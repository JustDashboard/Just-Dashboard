"use client"

import { useState } from "react"
import { External } from "@/components/icons"
import { bytes } from "@/lib/format"
import type { TailscalePeer } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useMediaQuery } from "@/hooks/use-mobile"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo, ProductLogos } from "@/components/product-logo"
import { EmptyNote, EmptyState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  filterMachines,
  machineReach,
  machineState,
  osProduct,
  type MachineFilter,
} from "./tunnel-reading"

/** From here a card has room for its readings beside the name; narrower, they take a line under it. */
const WIDE = "(min-width: 1180px)"

/** A search is worth its row once the list is longer than a screen of cards. */
const SEARCH_FROM = 9

/**
 * One machine as a card of one height: its mark, its name over a mono line,
 * its readings in columns that line up down the list, and its state last.
 * It has no page to open, so it is the card without the arrow (`ChoiceRow
 * disabled`), not a row dressed as a link.
 *
 * On a narrow screen the readings take a second line of the card, on every
 * card alike, where a table scrolled sideways for them.
 */
export function MachineRow({
  logo,
  name,
  tags,
  line,
  readings,
  state,
}: {
  logo: React.ReactNode
  name: string
  tags?: React.ReactNode
  line: React.ReactNode
  /** The columns between the name and the state, each of a fixed width. */
  readings: { key: string; className: string; node: React.ReactNode }[]
  state: React.ReactNode
}) {
  const wide = useMediaQuery(WIDE)
  return (
    <ChoiceRow
      disabled
      verb={name}
      leading={logo}
      title={
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate">{name}</span>
          {tags}
        </span>
      }
      description={line}
      trailing={
        <>
          {wide &&
            readings.map((reading) => (
              <span
                key={reading.key}
                className={cn("min-w-0 shrink-0 truncate text-xs", reading.className)}
              >
                {reading.node}
              </span>
            ))}
          <span className={cn("flex shrink-0", wide && "w-36")}>{state}</span>
        </>
      }
    >
      {!wide && (
        <p className="flex min-w-0 items-center gap-x-3 text-xs">
          {readings.map((reading) => (
            <span key={reading.key} className="min-w-0 truncate">
              {reading.node}
            </span>
          ))}
        </p>
      )}
    </ChoiceRow>
  )
}

/**
 * Every other machine on the tailnet, with how this server reaches it. The
 * list's count and its chips say what the page's "4 / 6" tile said; a tailnet
 * longer than a screen gets a search.
 */
export function TailnetMachines({
  machines,
  controlServer,
}: {
  machines: TailscalePeer[]
  controlServer: "tailscale" | "self-hosted"
}) {
  const [filter, setFilter] = useState<MachineFilter>("all")
  const [query, setQuery] = useState("")
  const online = machines.filter((m) => m.online).length
  const shown = filterMachines(machines, filter, query)
  const chips: { key: MachineFilter; label: string; count: number }[] = [
    { key: "all", label: "All", count: machines.length },
    { key: "online", label: "Online", count: online },
    { key: "offline", label: "Offline", count: machines.length - online },
  ]
  return (
    <Panel plain>
      <PanelHeader
        title="Machines"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {shown.length === machines.length
              ? machines.length
              : `${shown.length} of ${machines.length}`}
          </span>
        }
      />
      <PanelBody className="flex flex-col gap-3">
        {machines.length === 0 ? (
          <EmptyState
            title="No other machine on this tailnet"
            mark={<ProductLogos ids={["apple", "windows", "linux"]} />}
            description="A machine appears here once Tailscale is signed in on it with the same account."
            action={
              controlServer === "tailscale" ? (
                <Button size="sm" variant="outline" asChild>
                  <a href="https://tailscale.com/download" target="_blank" rel="noreferrer">
                    <External className="size-4" />
                    tailscale.com/download
                  </a>
                </Button>
              ) : undefined
            }
          />
        ) : (
          <>
            <div className="flex min-w-0 flex-wrap items-center gap-2">
              {machines.length >= SEARCH_FROM && (
                <SearchInput
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder="Name, address or account"
                  aria-label="Search the machines"
                />
              )}
              <ChipStrip aria-label="Machines shown">
                {chips
                  .filter((chip) => chip.key === "all" || chip.count > 0)
                  .map((chip) => (
                    <FilterChip
                      key={chip.key}
                      selected={filter === chip.key}
                      onClick={() => setFilter(chip.key)}
                    >
                      {chip.label}
                      <ChipCount>{chip.count}</ChipCount>
                    </FilterChip>
                  ))}
              </ChipStrip>
            </div>
            {shown.length === 0 ? (
              <EmptyNote>No machine matches.</EmptyNote>
            ) : (
              <ChoiceList aria-label="Tailnet machines">
                {shown.map((machine) => (
                  <Machine key={machine.id} machine={machine} />
                ))}
              </ChoiceList>
            )}
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

function Machine({ machine: p }: { machine: TailscalePeer }) {
  const state = machineState(p)
  const reach = machineReach(p)
  const moved = p.rxBytes > 0 || p.txBytes > 0
  return (
    <MachineRow
      logo={<ProductLogo id={osProduct(p.os)} size="sm" />}
      name={p.hostName}
      tags={
        <>
          {p.exitNodeOption && <Tag>exit node</Tag>}
          {p.exitNode && <Tag tone="warning">in use</Tag>}
        </>
      }
      line={
        <>
          <span className="font-mono">{p.tailscaleIps[0] ?? "no address"}</span>
          {" · "}
          {p.userLoginName ?? p.os}
          {p.primaryRoutes?.length ? (
            <>
              {" · routes "}
              <span className="font-mono">{p.primaryRoutes.join(", ")}</span>
            </>
          ) : null}
        </>
      }
      readings={[
        {
          key: "reached",
          className: "w-48",
          node: (
            <span className={cn(!p.direct && "text-muted-foreground")}>
              {reach.word}
              {reach.detail && (
                <span className="font-mono text-muted-foreground"> {reach.detail}</span>
              )}
            </span>
          ),
        },
        {
          key: "moved",
          className: "w-40",
          node: moved ? (
            <span className="numeric font-mono">
              <span className="text-[var(--chart-5)]">↓ {bytes(p.rxBytes)}</span>{" "}
              <span className="text-[var(--chart-2)]">↑ {bytes(p.txBytes)}</span>
            </span>
          ) : (
            <span className="text-muted-foreground">nothing moved</span>
          ),
        },
      ]}
      state={<Status tone={state.tone} label={state.label} live={state.live} />}
    />
  )
}
