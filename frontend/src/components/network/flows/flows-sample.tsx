"use client"

import { Disclosure } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { EmptyNote } from "@/components/state"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import type { FlowCycle } from "@/lib/network-flows"
import { UTCTime } from "@/components/network/flows/marks"

/**
 * The newest sample the recorder took: when, how long the read took, the
 * tool that read it, and each source's counts on a line of its own.
 */
export function FlowsSample({ cycle }: { cycle: FlowCycle | null }) {
  return (
    <Panel plain id="flow-sample" aria-label="Latest native sample" className="scroll-mt-6">
      <PanelHeader
        title="Latest native sample"
        actions={
          cycle && (
            <span className="text-hint text-muted-foreground">
              <UTCTime value={cycle.at} form="short" /> · {cycle.elapsedMillis}ms
            </span>
          )
        }
      />
      <PanelBody className="space-y-3">
        {!cycle ? (
          <EmptyNote>No native sample has been retained.</EmptyNote>
        ) : (
          <>
            <p className="flex flex-wrap gap-x-3 gap-y-1 text-hint text-muted-foreground">
              <span>
                <UTCTime value={cycle.at} /> → <UTCTime value={cycle.finishedAt} form="clock" />
              </span>
              <span>Docker sources {cycle.dockerStatus}</span>
              <span>Omitted {cycle.omittedSources}</span>
              <span className="font-mono">{cycle.toolVersion ?? "ss version unknown"}</span>
              <span>
                Kernel <span className="font-mono">{cycle.kernelRelease ?? "unknown"}</span>
              </span>
              <span>
                Version read <UTCTime value={cycle.toolVersionCheckedAt} form="short" />
              </span>
            </p>
            {cycle.dockerError && <p className="text-body">{cycle.dockerError}</p>}
            {cycle.toolVersionError && <p className="text-body">{cycle.toolVersionError}</p>}
            {/* Below `md` a source is two lines, its counts in words; the
                table's nine columns would have to scroll sideways there. */}
            <ul
              aria-label="Sampled sources"
              className="divide-y divide-hairline rounded-xl border md:hidden"
            >
              {cycle.sources.map((source) => (
                <li key={source.id} className="space-y-1 px-4 py-3">
                  <div className="flex min-w-0 items-center gap-2">
                    <span className="min-w-0 truncate text-body font-medium">{source.name}</span>
                    <span className="truncate font-mono text-hint text-muted-foreground">
                      {source.namespace ?? "namespace unknown"}
                    </span>
                    <Status
                      tone={source.status === "observed" ? "running" : "unknown"}
                      label={
                        source.status.replaceAll("_", " ") +
                        (source.truncated ? " · cap reached" : "")
                      }
                      className="ml-auto shrink-0"
                    />
                  </div>
                  <p className="numeric text-hint text-muted-foreground">
                    {source.tcp} TCP · {source.udp} UDP · sent counters {source.tcpTxCounters} ·
                    received counters {source.tcpRxCounters} · unknown owners{" "}
                    {source.unverifiedOwners} · missing identities {source.identityUnavailable} ·
                    observed <UTCTime value={source.observedAt} form="clock" />
                  </p>
                </li>
              ))}
            </ul>
            <div className="overflow-hidden rounded-xl border max-md:hidden">
              <Table className="min-w-[44rem]">
                <TableHeader>
                  <TableRow>
                    <TableHead>Source</TableHead>
                    <TableHead>State</TableHead>
                    <TableHead>TCP</TableHead>
                    <TableHead>UDP</TableHead>
                    <TableHead>Sent counters</TableHead>
                    <TableHead>Received counters</TableHead>
                    <TableHead>Unknown owners</TableHead>
                    <TableHead>Missing identities</TableHead>
                    <TableHead>Observed</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {cycle.sources.map((source) => (
                    <TableRow key={source.id} className="h-11">
                      <TableCell
                        className="max-w-48 truncate"
                        title={`Source ${source.id} · Namespace ${source.namespace ?? "unknown"}${
                          source.error ? ` · ${source.error}` : ""
                        }`}
                      >
                        <span className="font-medium">{source.name}</span>{" "}
                        <span className="font-mono text-hint text-muted-foreground">
                          {source.namespace ?? "namespace unknown"}
                        </span>
                      </TableCell>
                      <TableCell>
                        <Status
                          tone={source.status === "observed" ? "running" : "unknown"}
                          label={
                            source.status.replaceAll("_", " ") +
                            (source.truncated ? " · cap reached" : "")
                          }
                        />
                      </TableCell>
                      <TableCell className="numeric">{source.tcp}</TableCell>
                      <TableCell className="numeric">{source.udp}</TableCell>
                      <TableCell className="numeric">{source.tcpTxCounters}</TableCell>
                      <TableCell className="numeric">{source.tcpRxCounters}</TableCell>
                      <TableCell className="numeric">{source.unverifiedOwners}</TableCell>
                      <TableCell className="numeric">{source.identityUnavailable}</TableCell>
                      <TableCell className="font-mono text-hint text-muted-foreground">
                        <UTCTime value={source.observedAt} form="clock" />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            {cycle.sources
              .filter((source) => source.error)
              .map((source) => (
                <p key={source.id} className="text-hint text-warning">
                  {source.name}: {source.error}
                </p>
              ))}
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * What the record cannot say, kept beside it: the two channels' limits and
 * the recorder's own account of its coverage.
 */
export function FlowsBoundaries({ coverage }: { coverage: string[] }) {
  return (
    <Panel plain aria-label="Measurement boundaries">
      <PanelHeader title="Measurement boundaries" />
      <PanelBody className="space-y-3 text-body">
        <ul className="list-disc space-y-1.5 pl-5 text-muted-foreground">
          <li>
            Native snapshots leave UDP bytes and dropped-event counts unknown, and miss connections
            that open and close between reads.
          </li>
          <li>
            Kernel packet observations retain separate subtotals and coverage gaps. The two channels
            are never added together.
          </li>
          <li>Empty results do not prove no contact or zero bandwidth.</li>
        </ul>
        <Disclosure quiet summary="Native source and counter coverage">
          <ul className="list-disc space-y-2 pl-5 text-muted-foreground">
            {coverage.map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
        </Disclosure>
      </PanelBody>
    </Panel>
  )
}
