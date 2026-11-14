"use client"

import { ArrowRight, Download } from "@/components/icons"
import { InfoTip } from "@/components/form"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { EmptyNote } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
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
import { cn } from "@/lib/utils"
import {
  flowBytes,
  flowDateRange,
  flowIsKernelRow,
  flowKernelTotals,
  flowOwnerName,
  flowTotals,
  type FlowRow,
} from "@/lib/network-flows"
import { UTCTime, counted } from "@/components/network/flows/marks"

export type FlowFilters = { day: string; address: string; containerId: string }

const rowKey = (row: FlowRow) => `${row.id}:${row.hour}`
const channelWord = (row: FlowRow) =>
  flowIsKernelRow(row) ? "Kernel payload" : row.evidence ? "Unrecognized" : "Native TCP"
/** The bytes one direction of a row measured, in the channel that measured them. */
function bytesOf(row: FlowRow, direction: "tx" | "rx") {
  if (flowIsKernelRow(row))
    return flowBytes(direction === "tx" ? row.observedTxBytes : row.observedRxBytes)
  if (row.evidence) return "Unknown"
  if (row.socket.protocol !== "tcp") return "Unavailable for UDP"
  return flowBytes(direction === "tx" ? row.txBytes : row.rxBytes)
}
const localOf = (row: FlowRow) =>
  row.socket.localEndpoint || `${row.socket.localAddress}:${row.socket.localPort}`
const peerOf = (row: FlowRow) =>
  row.socket.remoteAddress ? row.socket.remoteEndpoint : "Peer unknown"

/**
 * The socket-hour rows of one UTC day, a line each, with the day and the two
 * narrowing filters as the table's toolbar and what the rows shown add up to
 * as its foot. Native counter deltas and kernel payload observations are two
 * channels and are totalled apart; a figure nothing measured reads "Unknown".
 */
export function FlowsTable({
  rows,
  loading,
  truncated,
  draft,
  onDraft,
  onRead,
  onExport,
  exporting,
  disabled,
  selected,
  onSelect,
}: {
  rows: FlowRow[]
  loading: boolean
  /** The period has more matching rows than are shown. */
  truncated: boolean
  draft: FlowFilters
  onDraft: (draft: FlowFilters) => void
  onRead: () => void
  onExport: () => void
  exporting: boolean
  /** No fresh reading to act on: the export waits for one. */
  disabled: boolean
  selected?: FlowRow
  onSelect: (key: string) => void
}) {
  const totals = flowTotals(rows)
  const observed = flowKernelTotals(rows)
  return (
    <Panel id="flow-rows" aria-label="Socket hours" className="scroll-mt-6">
      <PanelHeader
        title={
          <>
            Socket hours
            {!loading && rows.length > 0 && (
              <span className="numeric ml-2 text-body font-normal text-muted-foreground">
                {rows.length}
              </span>
            )}
          </>
        }
        actions={
          <>
            {truncated && (
              <span className="text-hint text-warning">
                Displayed rows are capped
                <span className="sr-only">
                  . The period has more matching rows; narrow the peer or container filter.
                </span>
              </span>
            )}
            {truncated && (
              <InfoTip label="About the cap">
                The period has more matching rows. These figures cover only the rows below; narrow
                the peer or container filter. Exports have separate row and byte caps.
              </InfoTip>
            )}
            <Button
              size="sm"
              variant="outline"
              disabled={disabled}
              pending={exporting}
              onClick={onExport}
            >
              <Download className="size-3.5" />
              Export bounded JSON
            </Button>
          </>
        }
      />
      <PanelToolbar>
        <form
          className="flex min-w-0 flex-1 flex-wrap items-center gap-2"
          onSubmit={(event) => {
            event.preventDefault()
            if (flowDateRange(draft.day)) onRead()
          }}
        >
          <Input
            type="date"
            aria-label="Day (UTC)"
            title="Day (UTC)"
            value={draft.day}
            onChange={(event) => onDraft({ ...draft, day: event.target.value })}
            className="w-40 font-mono max-sm:w-full"
          />
          <Input
            aria-label="Observed peer IP"
            placeholder="Any peer, or a literal IP"
            value={draft.address}
            onChange={(event) => onDraft({ ...draft, address: event.target.value })}
            className="min-w-40 flex-1 font-mono max-sm:basis-full sm:max-w-60"
            spellCheck={false}
          />
          <Input
            aria-label="Container identity"
            placeholder="Any owner, or a full Docker ID"
            value={draft.containerId}
            onChange={(event) => onDraft({ ...draft, containerId: event.target.value })}
            className="min-w-40 flex-1 font-mono max-sm:basis-full sm:max-w-72"
            spellCheck={false}
          />
          <Button type="submit" size="sm" variant="outline" disabled={!flowDateRange(draft.day)}>
            Read period
          </Button>
        </form>
      </PanelToolbar>
      {!loading && rows.length === 0 ? (
        <EmptyNote className="px-5 py-6">
          No retained socket rows match this period and filter. No contact or bandwidth conclusion
          can be drawn.
        </EmptyNote>
      ) : (
        <>
          <Table className="table-fixed" containerClassName="max-h-[25rem] max-lg:hidden">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead className="w-[18%]">Owner</TableHead>
                <TableHead className="w-24">Socket</TableHead>
                <TableHead>Local → observed peer</TableHead>
                <TableHead className="w-28">Measured by</TableHead>
                <TableHead className="w-32">Sent</TableHead>
                <TableHead className="w-32">Received</TableHead>
                <TableHead className="w-16">Hour</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading &&
                [0, 1, 2].map((i) => (
                  <TableRow key={i} aria-hidden className="h-11 hover:bg-transparent">
                    <TableCell colSpan={7}>
                      <Skeleton className="h-3.5 w-2/3" />
                    </TableCell>
                  </TableRow>
                ))}
              {rows.map((row) => {
                const key = rowKey(row)
                const chosen = selected !== undefined && rowKey(selected) === key
                return (
                  <TableRow
                    key={key}
                    data-state={chosen ? "selected" : undefined}
                    onActivate={() => onSelect(key)}
                    className="h-11"
                  >
                    <TableCell className="truncate">
                      <span className="flex min-w-0 items-center gap-2">
                        <OwnerGlyph row={row} />
                        <button
                          type="button"
                          aria-pressed={chosen}
                          title={ownerEvidence(row)}
                          onClick={() => onSelect(key)}
                          className="min-w-0 truncate rounded-sm text-body font-medium focus-ring hover:text-primary"
                        >
                          {flowOwnerName(row.socket.owner)}
                        </button>
                      </span>
                    </TableCell>
                    <TableCell className="truncate font-mono text-hint">
                      {row.socket.protocol.toUpperCase()}{" "}
                      <span className="text-muted-foreground">{row.socket.state}</span>
                    </TableCell>
                    <TableCell
                      className="truncate font-mono text-hint"
                      title={`${localOf(row)} → ${peerOf(row)}`}
                    >
                      {localOf(row)}{" "}
                      <ArrowRight aria-hidden className="inline size-3 text-muted-foreground" />{" "}
                      {peerOf(row)}
                    </TableCell>
                    <TableCell className="truncate text-hint text-muted-foreground">
                      {channelWord(row)}
                    </TableCell>
                    <TableCell className="numeric truncate text-hint">
                      <Bytes value={bytesOf(row, "tx")} />
                    </TableCell>
                    <TableCell className="numeric truncate text-hint">
                      <Bytes value={bytesOf(row, "rx")} />
                    </TableCell>
                    <TableCell className="font-mono text-hint text-muted-foreground">
                      <UTCTime value={row.hour} form="hour" />
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
          {/* Below `lg` a row is two lines of one height: who and what was
              measured, then the tuple. */}
          <ul aria-label="Socket hours" className="divide-y divide-hairline lg:hidden">
            {loading &&
              [0, 1, 2].map((i) => (
                <li key={i} aria-hidden className="flex h-16 items-center px-4">
                  <Skeleton className="h-3.5 w-2/3" />
                </li>
              ))}
            {rows.map((row) => {
              const key = rowKey(row)
              const chosen = selected !== undefined && rowKey(selected) === key
              return (
                <li
                  key={key}
                  data-state={chosen ? "selected" : undefined}
                  className="flex h-16 min-w-0 flex-col justify-center gap-1 px-4 data-[state=selected]:bg-accent"
                >
                  <div className="flex min-w-0 items-center gap-2">
                    <OwnerGlyph row={row} />
                    <button
                      type="button"
                      aria-pressed={chosen}
                      onClick={() => onSelect(key)}
                      className="min-w-0 truncate rounded-sm text-body font-medium focus-ring"
                    >
                      {flowOwnerName(row.socket.owner)}
                    </button>
                    <span className="numeric ml-auto shrink-0 text-hint">
                      <Bytes value={bytesOf(row, "tx")} short /> ↑{" "}
                      <Bytes value={bytesOf(row, "rx")} short /> ↓
                    </span>
                  </div>
                  <p className="truncate font-mono text-hint text-muted-foreground">
                    {row.socket.protocol.toUpperCase()} {localOf(row)} → {peerOf(row)}
                  </p>
                </li>
              )
            })}
          </ul>
        </>
      )}
      {!loading && rows.length > 0 && (
        <PanelFooter className="gap-x-6 gap-y-1 text-hint text-muted-foreground">
          <Total
            name="TCP sent"
            value={flowBytes(totals.tx.value)}
            basis={`${totals.tx.known} rows with measured sent deltas`}
          />
          <Total
            name="TCP received"
            value={flowBytes(totals.rx.value)}
            basis={`${totals.rx.known} rows with measured received deltas`}
          />
          <Total
            name="Retransmits"
            value={totals.retrans.value?.toString() ?? "Unknown"}
            basis="Sampled sender counter deltas"
          />
          {observed.rows > 0 && (
            <span
              aria-label="Kernel packet observations"
              role="group"
              className="flex basis-full flex-wrap items-center gap-x-6 gap-y-1"
            >
              <Total
                name="Kernel sent"
                value={flowBytes(observed.tx.value)}
                basis={`${observed.tx.known} rows with known payload bytes · ${observed.tx.unknown} without`}
              />
              <Total
                name="Kernel received"
                value={flowBytes(observed.rx.value)}
                basis={`${observed.rx.known} rows with known payload bytes · ${observed.rx.unknown} without`}
              />
              <Total
                name="Packets"
                value={observed.packets.value?.toString() ?? "Unknown"}
                basis={`${observed.packets.known} counted rows · ${observed.packets.unknown} unknown`}
              />
              <Total
                name="Byte gaps"
                value={`${observed.txGaps.value?.toString() ?? "Unknown"} sent · ${
                  observed.rxGaps.value?.toString() ?? "Unknown"
                } received`}
                basis={`Rows without gap counts: sent ${observed.txGaps.unknown}, received ${observed.rxGaps.unknown}`}
              />
              <InfoTip label="What the kernel subtotals leave out">
                Subtotals include only retained events with known transport payload lengths; they
                are not complete host bandwidth or billing. The two channels are never added
                together.
              </InfoTip>
            </span>
          )}
        </PanelFooter>
      )}
    </Panel>
  )
}

function OwnerGlyph({ row }: { row: FlowRow }) {
  return row.socket.owner.status === "verified_container" ? (
    <ProductGlyph id="docker" />
  ) : (
    <span aria-hidden className="size-3.5 shrink-0" />
  )
}

const ownerEvidence = (row: FlowRow) =>
  row.socket.owner.containerId ?? row.socket.owner.reason ?? row.socket.owner.status

/** A figure in ink where it was measured, and stepped back where it was not. */
function Bytes({ value, short }: { value: string; short?: boolean }) {
  const known = !/^(Unknown|Unavailable)/.test(value)
  return (
    <span className={cn(!known && "text-muted-foreground")}>
      {short && value === "Unavailable for UDP" ? "n/a" : value}
    </span>
  )
}

function Total({ name, value, basis }: { name: string; value: string; basis: string }) {
  return (
    <span data-slot="flow-total" title={basis} className="numeric">
      {name} <span className="font-medium text-foreground">{value}</span>
      <span className="sr-only"> ({basis})</span>
    </span>
  )
}

/**
 * The chosen socket hour in full: what was measured and by which channel,
 * how well the hour was covered, and the identities that tie the row to a
 * socket. Every counter nothing measured reads "Unknown".
 */
export function FlowRowReading({ row }: { row: FlowRow }) {
  const kernel = flowIsKernelRow(row)
  const facts: [string, React.ReactNode][] = kernel
    ? [
        ["Sent subtotal", flowBytes(row.observedTxBytes)],
        ["Received subtotal", flowBytes(row.observedRxBytes)],
        [
          "Observed flags",
          `SYN ${counted(row.observedSyn)} · FIN ${counted(row.observedFin)} · RST ${counted(
            row.observedRst,
          )}; lifecycle completeness unknown`,
        ],
        ["Observed packets", counted(row.observedPackets)],
        [
          "Sent known lengths",
          `${counted(row.observedTxKnownPackets)}/${counted(row.observedTxPackets)} · byte gaps ${counted(row.observedTxByteGaps)}`,
        ],
        [
          "Received known lengths",
          `${counted(row.observedRxKnownPackets)}/${counted(row.observedRxPackets)} · byte gaps ${counted(row.observedRxByteGaps)}`,
        ],
        ["Cgroup", <Mono key="cgroup">{row.socketCgroup ?? "unknown"}</Mono>],
      ]
    : row.evidence
      ? [["Bytes", "Unrecognized evidence source; byte interpretation unknown"]]
      : [
          ["Sent", bytesOf(row, "tx")],
          ["Received", bytesOf(row, "rx")],
          ["Retransmits", counted(row.retransmissions)],
          ["Outstanding-loss gauge max", counted(row.lostGaugeMax)],
          [
            "Samples",
            `${row.samples} samples · ${row.measuredIntervals} measured · ${row.skippedIntervals} skipped`,
          ],
          [
            "Intervals",
            `Sent ${row.txIntervals} · Received ${row.rxIntervals} · Retransmits ${row.retransIntervals}`,
          ],
        ]
  facts.push(
    [
      "Observed UTC window",
      <span key="window">
        <UTCTime value={row.firstSeen} /> → <UTCTime value={row.lastSeen} />
      </span>,
    ],
    [
      "Window",
      row.timestampUncertain
        ? "UTC timestamp uncertain; inspect observer clock evidence"
        : kernel
          ? "Retained packet window; full connection span unknown"
          : "Observed span; no close event",
    ],
    ["Owner evidence", <Mono key="owner">{ownerEvidence(row)}</Mono>],
    [
      "Socket identity",
      <Mono key="identity">
        Cookie {row.socket.cookie ?? "unknown"} · Inode {row.socket.inode ?? "unknown"} · Namespace{" "}
        {row.namespace}
      </Mono>,
    ],
    [
      "Read through",
      <Mono key="via">
        {row.sourceName} · Boot {row.bootId}
      </Mono>,
    ],
  )
  return (
    <Panel plain aria-label="Selected socket hour">
      <PanelHeader
        title={
          <span className="flex min-w-0 items-center gap-2">
            <span className="truncate">{flowOwnerName(row.socket.owner)}</span>
            <span className="truncate font-mono text-hint font-normal text-muted-foreground">
              {row.socket.protocol.toUpperCase()} {row.socket.state} · {localOf(row)} →{" "}
              {peerOf(row)}
            </span>
          </span>
        }
        actions={
          <span className="text-hint text-muted-foreground">
            {kernel
              ? "Kernel transport payload"
              : row.evidence
                ? "Unrecognized evidence"
                : "Native TCP counter deltas"}
          </span>
        }
      />
      <PanelBody>
        <dl className="grid gap-x-10 md:grid-cols-2">
          {facts.map(([name, value]) => (
            <div
              key={name}
              className="grid min-w-0 grid-cols-[minmax(0,11rem)_minmax(0,1fr)] gap-3 border-b border-hairline py-2 text-body"
            >
              <dt className="text-muted-foreground">{name}</dt>
              <dd className="min-w-0 break-words">{value}</dd>
            </div>
          ))}
        </dl>
      </PanelBody>
    </Panel>
  )
}

function Mono({ children }: { children: React.ReactNode }) {
  return <span className="font-mono text-hint break-all">{children}</span>
}

export { rowKey as flowRowKey }
