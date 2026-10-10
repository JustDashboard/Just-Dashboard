"use client"

import { bytes } from "@/lib/format"
import type { QdiscStat, ShapeDevice } from "@/lib/types"
import { delayLabel, type CakeTin } from "@/lib/network-traffic"
import { Notice } from "@/components/state"
import { Disclosure } from "@/components/form"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

const num = (value: number) => value.toLocaleString()

/** What the next change does with a device's queues, in one sentence. */
export function ownershipSentence(device: ShapeDevice): string | undefined {
  const o = device.ownership
  if (!o) return undefined
  switch (o.verdict) {
    case "managed":
      return undefined
    case "kernel":
      return "Next change replaces the kernel's default queue; clearing it brings the default back."
    case "preserved":
      return "Next change captures this fq_codel's parameters and puts them back when cleared."
    case "refused":
      return device.shapeable ? `Changes are refused: ${o.reason}` : undefined
  }
}

/** The CAKE classes' measured delay as one short reading for a table cell. */
export function tinSummary(tins: CakeTin[] | undefined): string | undefined {
  if (!tins?.length) return undefined
  const busiest = [...tins].sort((a, b) => b.sentPackets - a.sentPackets)[0]
  const peak = Math.max(...tins.map((t) => t.peakDelayUs))
  return `CAKE delay ${delayLabel(busiest.avgDelayUs)} average in ${busiest.name.toLowerCase()} · ${delayLabel(peak)} peak`
}

/**
 * CAKE's own measurement of the delay packets spent in each class — the one
 * latency figure a shaper can give about itself. It says how long this host
 * held packets, not what the path beyond it adds.
 */
export function CakeTins({ stat, label }: { stat: QdiscStat | null | undefined; label: string }) {
  const tins = stat?.tins
  if (!tins?.length) return null
  return (
    <div className="flex min-w-0 flex-col gap-2" aria-label={label}>
      <p className="text-title font-medium">{label}</p>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Class</TableHead>
            <TableHead className="text-right">Average delay</TableHead>
            <TableHead className="text-right">Peak delay</TableHead>
            <TableHead className="text-right max-sm:hidden">Sent</TableHead>
            <TableHead className="text-right">Dropped</TableHead>
            <TableHead className="text-right max-sm:hidden">Marked</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {tins.map((t) => (
            <TableRow key={t.name}>
              <TableCell>{t.name}</TableCell>
              <TableCell className="numeric text-right">{delayLabel(t.avgDelayUs)}</TableCell>
              <TableCell className="numeric text-right">{delayLabel(t.peakDelayUs)}</TableCell>
              <TableCell className="numeric text-right max-sm:hidden">
                {bytes(t.sentBytes)}
              </TableCell>
              <TableCell className="numeric text-right">{num(t.drops)}</TableCell>
              <TableCell className="numeric text-right max-sm:hidden">{num(t.ecnMarks)}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <p className="text-hint text-muted-foreground">
        Measured by CAKE on the packets it queued here; the delay of the path beyond this host is
        not in it.
      </p>
    </div>
  )
}

/**
 * The device's queues as the kernel lists them, the parameters of the queue
 * that carries them, and what was read back when the dashboard applied it.
 */
export function QueueParameters({ device }: { device: ShapeDevice }) {
  const effective = Object.entries(device.effective ?? {}).sort(([a], [b]) => a.localeCompare(b))
  const sentence = ownershipSentence(device)
  return (
    <div className="flex min-w-0 flex-col gap-3">
      {device.ownership?.verdict === "refused" && device.shapeable ? (
        <Notice tone="warning" title="This device's queues belong to something else">
          {device.ownership.reason}
        </Notice>
      ) : (
        sentence && <p className="text-body text-muted-foreground">{sentence}</p>
      )}
      <Disclosure summary="Queues and parameters">
        <div className="flex flex-col gap-3 pt-2">
          {device.tree && device.tree.length > 0 && (
            <ul
              className="font-mono text-hint text-muted-foreground"
              aria-label={`${device.name} queue tree`}
            >
              {device.tree.map((q) => (
                <li key={`${q.handle}-${q.parent ?? "root"}-${q.kind}`}>
                  {q.root ? "root" : `parent ${q.parent}`} · {q.kind} {q.handle}
                </li>
              ))}
            </ul>
          )}
          {effective.length > 0 ? (
            <dl
              className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 font-mono text-hint"
              aria-label={`${device.name} parameters`}
            >
              {effective.map(([key, value]) => (
                <div key={key} className="contents">
                  <dt className="text-muted-foreground">{key}</dt>
                  <dd>{value}</dd>
                </div>
              ))}
            </dl>
          ) : (
            <p className="text-hint text-muted-foreground">The queue reports no parameters.</p>
          )}
          {device.applied && (
            <p className="text-hint text-muted-foreground">
              Compared with {device.applied.kind} {device.applied.handle} as read back when it was
              applied, {new Date(device.applied.appliedAt).toLocaleString()}.
            </p>
          )}
        </div>
      </Disclosure>
    </div>
  )
}

/** What the card does to packets before a download queue sees them. */
export function OffloadLine({ device }: { device: ShapeDevice }) {
  const o = device.offload
  if (!o) return null
  if (!o.checked) return <span className="block">{o.error}</span>
  const lro = o.lro?.startsWith("on")
  return (
    <span className="block">
      Offloads: GRO {o.gro ?? "?"} · LRO {o.lro ?? "?"} · GSO {o.gso ?? "?"} · TSO {o.tso ?? "?"}
      {lro
        ? " — LRO merges packets in hardware before the IFB, so the download queue sees fewer, larger packets than the wire."
        : " — CAKE splits merged packets again (split-gso)."}
    </span>
  )
}
