"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { Box } from "@/components/icons"
import { hueFor, LANES } from "@/lib/hue"
import type { Container, ContainerStats } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useArrivals } from "@/hooks/use-arrivals"
import { useMediaQuery } from "@/hooks/use-mobile"
import { WIDE_BESIDE_RAIL } from "@/components/docker/beside-rail"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { Panel, PanelHeader } from "@/components/panel"
import { ProductLogo, containerProduct } from "@/components/product-logo"
import { Tag } from "@/components/tag"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { PortList } from "@/components/docker/exposure"
import {
  ContainerStatus,
  CpuReading,
  MemoryReading,
  statusWord,
} from "@/components/docker/container-cells"
import { companyOf } from "@/components/docker/container"
import { ROW_BLEED } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import { useBreakpoint } from "@/lib/breakpoints"
import { bytes, percent } from "@/lib/format"

/**
 * The containers this one runs beside, as a table: its compose project, or
 * the containers on its own networks when it has none.
 *
 * The page answered "is it this container, or the one it talks to" with a
 * link to the stack and nothing else, so a web container failing because its
 * database was restarting looked healthy right up to the moment the reader
 * left the page to find out. Every row here is live, from the same socket the
 * containers table reads, so the database's restart loop is on screen beside
 * the container that depends on it.
 *
 * A table, framed, because its body is one (§2): readings in columns, read
 * down. The container the page is about is the selected row and is not a
 * link; every other name opens its own page, keeping the tab the reader is
 * on, so stepping through a project's containers is one press each.
 *
 * On a phone the table is the containers list's phone rows instead: the name,
 * then its state, CPU and memory on one line under it, in a section with no
 * frame. The table kept a 40rem floor and scrolled sideways in a 350px box,
 * so the state was cut mid-word and CPU and memory were off the screen.
 */
export function ContainerCompany({
  container,
  tab,
}: {
  container: Pick<Container, "id" | "composeStack" | "networks">
  /** The tab a neighbour opens on: the one the reader is comparing. */
  tab: string
}) {
  const router = useRouter()
  // The ports take a column only where the four before it keep their width.
  const wide = useMediaQuery(WIDE_BESIDE_RAIL)
  const phone = !useBreakpoint("sm")
  const [inventory, setInventory] = useState<Container[]>()
  const [stats, setStats] = useState<Record<string, ContainerStats>>({})
  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type === "containers") setInventory(envelope.data as Container[])
    else if (envelope.type === "stats")
      setStats(Object.fromEntries((envelope.data as ContainerStats[]).map((row) => [row.id, row])))
  }, [])
  useSocket("/docker/containers/stream", { onMessage })

  const company = useMemo(
    () => (inventory ? companyOf(container, inventory) : undefined),
    [container, inventory],
  )
  const arrived = useArrivals(company?.containers.map((one) => one.id) ?? [])
  if (!company) return null

  const running = company.containers.filter((one) => one.state === "running").length
  const href = (id: string) =>
    `/docker/containers/${encodeURIComponent(id)}?tab=${encodeURIComponent(tab)}`

  return (
    <Panel
      // Framed around a table: the frame is the edge of its scroll (§2). A
      // list of rows on a phone is a section, a title and a hairline.
      framed={!phone}
      className="animate-rise"
      aria-label="Containers beside it"
    >
      <PanelHeader
        eyebrow={company.kind === "stack" ? "Compose project" : "On its network"}
        title={
          <span className="inline-flex items-center gap-2">
            <span
              aria-hidden
              className="h-3.5 w-1 shrink-0 rounded-full"
              style={{ background: hueFor(company.name, LANES) }}
            />
            {company.name}
          </span>
        }
        count={`${running} of ${company.containers.length} running`}
        link={
          company.kind === "stack"
            ? {
                href: `/docker/stacks/${encodeURIComponent(company.name)}`,
                label: "Open the stack",
              }
            : undefined
        }
      />
      {phone ? (
        <ul className="divide-y divide-hairline">
          {company.containers.map((one) => (
            <CompanyRow
              key={one.id}
              one={one}
              self={one.id === container.id}
              stat={stats[one.id]}
              href={href(one.id)}
              arrived={arrived.has(one.id)}
              onOpen={() => router.push(href(one.id))}
            />
          ))}
        </ul>
      ) : (
        /* Fixed columns at a floor of 40rem: narrower, the table scrolls inside
          its frame rather than squeezing a reading onto two lines. */
        <Table className="min-w-[40rem] table-fixed">
          <colgroup>
            <col className={wide ? "w-[32%]" : "w-[38%]"} />
            <col className={wide ? "w-[22%]" : "w-[24%]"} />
            <col className={wide ? "w-[14%]" : "w-[18%]"} />
            <col className={wide ? "w-[16%]" : "w-[20%]"} />
            {wide && <col className="w-[16%]" />}
          </colgroup>
          <TableHeader>
            <TableRow>
              <TableHead className="pl-5">Container</TableHead>
              <TableHead>State</TableHead>
              <TableHead>CPU</TableHead>
              <TableHead>Memory</TableHead>
              {wide && <TableHead className="pr-5">Ports</TableHead>}
            </TableRow>
          </TableHeader>
          <TableBody>
            {company.containers.map((one) => {
              const self = one.id === container.id
              return (
                <TableRow
                  key={one.id}
                  data-state={self ? "selected" : undefined}
                  aria-current={self ? "page" : undefined}
                  onClick={self ? undefined : () => router.push(href(one.id))}
                  className={cn(
                    "transition-colors",
                    self ? "bg-accent hover:bg-accent" : "cursor-pointer hover:bg-row-hover",
                    arrived.has(one.id) && "animate-rise",
                  )}
                >
                  <TableCell className="py-2.5 pl-5">
                    <div className="flex min-w-0 items-center gap-3">
                      <ProductLogo id={containerProduct(one)} size="sm" fallback={Box} />
                      <div className="min-w-0">
                        <div className="flex min-w-0 items-center gap-2">
                          {self ? (
                            <span className="truncate text-body font-medium">
                              {one.composeService ?? one.name}
                            </span>
                          ) : (
                            <Link
                              href={href(one.id)}
                              onClick={(event) => event.stopPropagation()}
                              className="truncate rounded-sm text-body font-medium focus-ring hover:underline"
                            >
                              {one.composeService ?? one.name}
                            </Link>
                          )}
                          {self && <Tag>this one</Tag>}
                        </div>
                        <p
                          className="truncate font-mono text-hint text-muted-foreground"
                          title={one.image}
                        >
                          {one.composeService ? one.name : one.image}
                        </p>
                      </div>
                    </div>
                  </TableCell>
                  <TableCell className="py-2.5">
                    <ContainerStatus container={one} />
                  </TableCell>
                  <TableCell className="py-2.5">
                    <CpuReading stat={stats[one.id]} container={one} />
                  </TableCell>
                  <TableCell className={cn("py-2.5", !wide && "pr-5")}>
                    <MemoryReading stat={stats[one.id]} container={one} />
                  </TableCell>
                  {wide && (
                    <TableCell className="py-2.5 pr-5">
                      <PortList ports={one.exposure ?? []} max={2} />
                    </TableCell>
                  )}
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      )}
    </Panel>
  )
}

/**
 * One neighbour on a phone, as the containers list draws one: its mark and
 * name, then its state, CPU and memory on one line. Two lines whatever it
 * holds, so the rows keep one height. The name is the link; the press on the
 * rest of the row is the pointer's larger target.
 */
function CompanyRow({
  one,
  self,
  stat,
  href,
  arrived,
  onOpen,
}: {
  one: Container
  self: boolean
  stat?: ContainerStats
  href: string
  arrived: boolean
  onOpen: () => void
}) {
  const name = one.composeService ?? one.name
  return (
    // eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-noninteractive-element-interactions -- the pointer's larger target: the row's name is the link a keyboard opens it by
    <li
      aria-current={self ? "page" : undefined}
      className={cn(
        "flex min-w-0 items-center gap-3 py-2.5 transition-colors",
        ROW_BLEED,
        self ? "bg-accent" : "cursor-pointer hover:bg-row-hover",
        arrived && "animate-rise",
      )}
      onClick={(event) => {
        if (self || (event.target as HTMLElement).closest("a, button")) return
        onOpen()
      }}
    >
      <ProductLogo id={containerProduct(one)} size="sm" fallback={Box} />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-2">
          {self ? (
            <span className="truncate text-body font-medium">{name}</span>
          ) : (
            <Link
              href={href}
              className="truncate rounded-sm text-body font-medium focus-ring hover:underline"
            >
              {name}
            </Link>
          )}
          {self && <Tag>this one</Tag>}
        </div>
        <p className="numeric flex min-w-0 items-center gap-x-3 text-hint text-muted-foreground [&>span]:whitespace-nowrap">
          <Status state={one.state} live={one.state === "running"} label={statusWord(one)} />
          {stat && stat.cpuReady !== false && <span>{percent(stat.cpuPercent)} CPU</span>}
          {stat && <span className="truncate">{bytes(stat.memUsage)}</span>}
        </p>
      </div>
    </li>
  )
}
