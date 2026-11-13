"use client"

import { useRouter } from "next/navigation"
import { ArrowRight, Bridge, Copy, Globe, Layers, Trash, type Icon } from "@/components/icons"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { InfoTip } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { Status, type DotTone } from "@/components/status-dot"
import { EmptyNote } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  stickyTableHeader,
} from "@/components/ui/table"
import { VerbActions, type Verb } from "@/components/verbs"
import { useMediaQuery } from "@/hooks/use-mobile"
import { copyText } from "@/lib/clipboard"
import {
  ownerLabel,
  reservationLink,
  reservationState,
  type IPAMCoverage,
  type IPAMOwner,
  type IPAMReservation,
} from "@/lib/network-ipam"

const OWNER_PRODUCT: Partial<Record<IPAMOwner, string>> = {
  docker_network: "docker",
  wireguard_server: "wireguard",
}
const OWNER_GLYPH: Record<IPAMOwner, Icon> = {
  docker_network: Layers,
  wireguard_server: Layers,
  interface_address: Bridge,
  namespace_address: Layers,
  provider_range: Globe,
}
/** A plan nobody answered for yet is not green: only an owner's recorded answer is. */
const STATE_TONE: Record<IPAMReservation["state"], DotTone> = {
  reserved: "notice",
  handing_off: "warning",
  observed: "running",
  review_required: "warning",
  released: "stopped",
}

/**
 * The reservations, a card each: the block, who it is for, and how its
 * hand-off to that owner stands. A plan for a Docker network or a WireGuard
 * tunnel that is still only reserved opens its owner's own creation form with
 * the block filled in; the rest are read here.
 */
export function IPAMReservations({
  reservations,
  loading,
  canRelease,
  busy,
  onRelease,
}: {
  reservations: IPAMReservation[]
  loading: boolean
  canRelease: boolean
  busy: boolean
  onRelease: (row: IPAMReservation) => void
}) {
  const router = useRouter()
  // From `sm` a card is one line; narrower, how the hand-off stands takes a
  // second line under the block on every card alike.
  const wide = useMediaQuery("(min-width: 640px)")
  const held = reservations.filter((row) => row.state !== "released").length
  return (
    <Panel plain aria-label="Reservations">
      <PanelHeader
        title="Reservations"
        actions={
          reservations.length > 0 && (
            <span className="numeric text-hint text-muted-foreground">
              {held} held
              {reservations.length > held && ` · ${reservations.length - held} released`}
            </span>
          )
        }
      />
      <PanelBody>
        {loading ? (
          <div aria-hidden className="space-y-2">
            {[0, 1].map((i) => (
              <Skeleton key={i} className="h-16 rounded-xl" />
            ))}
          </div>
        ) : reservations.length === 0 ? (
          <EmptyNote>No planning reservations.</EmptyNote>
        ) : (
          <ChoiceList aria-label="Planning reservations">
            {reservations.map((row) => {
              const link = reservationLink(row)
              const creates = row.owner === "docker_network" ? "Docker" : "WireGuard"
              const verbs: Verb[] = []
              if (link)
                verbs.push({
                  key: "open",
                  label: `Open ${creates} creation`,
                  icon: ArrowRight,
                  run: () => router.push(link),
                })
              if (row.nativeId)
                verbs.push({
                  key: "copy",
                  label: "Copy owner ID",
                  icon: Copy,
                  run: () => void copyText(row.nativeId!, "Owner ID copied"),
                })
              if (canRelease && row.state !== "released")
                verbs.push({
                  key: "release",
                  label: "Release plan",
                  icon: Trash,
                  danger: true,
                  disabled: busy || row.state === "handing_off",
                  run: () => onRelease(row),
                })
              return (
                <ChoiceRow
                  key={row.id}
                  leading={
                    <ProductLogo
                      id={OWNER_PRODUCT[row.owner]}
                      fallback={OWNER_GLYPH[row.owner]}
                      className="size-9 rounded-lg [&_img]:size-5 [&_svg]:size-4"
                    />
                  }
                  title={<span className="font-mono">{row.prefix}</span>}
                  verb={link ? `Open ${creates} creation for ${row.resource}` : row.prefix}
                  href={link}
                  disabled={!link}
                  description={
                    <>
                      <span className="font-mono">{row.resource}</span> · {ownerLabel[row.owner]}
                    </>
                  }
                  trailing={
                    <span className="flex items-center gap-2">
                      {wide && (
                        <Status tone={STATE_TONE[row.state]} label={reservationState[row.state]} />
                      )}
                      <InfoTip label={`About the plan for ${row.resource}`}>
                        {row.detail ||
                          "No native configuration has been applied by this reservation."}
                        {row.nativeId && (
                          <span className="mt-1 block font-mono break-all">
                            Observed owner ID: {row.nativeId}
                          </span>
                        )}
                      </InfoTip>
                      {/* Where a row with somewhere to go draws its arrow, so
                          every row's state ends on one edge. */}
                      {!link && <span aria-hidden className="size-3.5 shrink-0" />}
                    </span>
                  }
                  actions={
                    verbs.length > 0 && (
                      <VerbActions dim verbs={verbs} menuLabel={`Actions for ${row.resource}`} />
                    )
                  }
                >
                  {!wide && (
                    <Status
                      tone={STATE_TONE[row.state]}
                      label={reservationState[row.state]}
                      className="min-w-0"
                    />
                  )}
                </ChoiceRow>
              )
            })}
          </ChoiceList>
        )}
      </PanelBody>
    </Panel>
  )
}

const COVERAGE_TONE: Record<IPAMCoverage["state"], DotTone> = {
  observed: "running",
  unknown: "unknown",
  unreadable: "warning",
}

/**
 * What the planner could and could not read: every source it asked, the
 * state it came back in and the reason. A source that is "unknown" or
 * "unreadable" is why a free block is only free within readable evidence.
 */
export function IPAMCoverageTable({
  coverage,
  limitations,
  loading,
}: {
  coverage: IPAMCoverage[]
  limitations: string[]
  loading: boolean
}) {
  const gaps = coverage.filter((row) => row.state !== "observed").length
  if (!loading && coverage.length === 0 && limitations.length === 0) return null
  return (
    <div className="space-y-3">
      <Panel aria-label="Coverage">
        <PanelHeader
          title="Coverage"
          actions={
            !loading && (
              <span className="numeric text-hint text-muted-foreground">
                {gaps === 0 ? "every source read" : `${gaps} ${gaps === 1 ? "gap" : "gaps"}`}
              </span>
            )
          }
        />
        {loading ? (
          <div aria-hidden className="divide-y divide-hairline">
            {[0, 1].map((i) => (
              <div key={i} className="flex h-11 items-center gap-6 px-4">
                <Skeleton className="h-3.5 w-28" />
                <Skeleton className="h-3.5 w-20" />
                <Skeleton className="h-3.5 w-64" />
              </div>
            ))}
          </div>
        ) : coverage.length === 0 ? (
          <EmptyNote className="px-5 py-5">No sources reported.</EmptyNote>
        ) : (
          <Table className="table-fixed" containerClassName="max-h-[19.5rem]">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead className="w-[28%] sm:w-[22%]">Source</TableHead>
                <TableHead className="w-28">State</TableHead>
                <TableHead>What was read</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {coverage.map((row, i) => (
                <TableRow key={i} className="h-11">
                  <TableCell className="truncate font-mono" title={row.source}>
                    {row.source}
                  </TableCell>
                  <TableCell>
                    <Status tone={COVERAGE_TONE[row.state]} label={row.state} />
                  </TableCell>
                  <TableCell
                    className="text-muted-foreground max-sm:whitespace-normal sm:truncate"
                    title={row.detail}
                  >
                    {row.detail}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Panel>
      {limitations.map((detail, i) => (
        <p key={i} className="text-hint text-muted-foreground">
          {detail}
        </p>
      ))}
    </div>
  )
}
