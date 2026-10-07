"use client"

import { useState } from "react"
import { get } from "@/lib/api"
import { bytes, plural, relativeTime } from "@/lib/format"
import type { EBPFView } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { RowList, Row } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ErrorState, LoadingPanel, Notice } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { NumberTicker } from "@/components/ui/number-ticker"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { InstallHandoff } from "@/components/network/install"
import { LinkGlyph } from "@/components/network/marks"

const num = (value: number) => value.toLocaleString()

/**
 * The eBPF programs loaded into this kernel, read with `bpftool` and drawn as
 * an inventory: how many, of which types, what is attached to which device,
 * and each program with when it was loaded and what it holds. Everything here
 * is what the kernel reports at the moment — systemd's cgroup filters, an XDP
 * program on the uplink, whatever Cilium or a tracer loaded — and nothing is
 * a probe this dashboard runs. Without `bpftool` the section says what it
 * would read and offers the package, as every absent tool here does.
 *
 * The type counts are filters: a press narrows the table to that type, and the
 * run counts are drawn only where the kernel keeps them (`bpf_stats_enabled`),
 * because a column of zeros would read as programs that never ran.
 */
export function EBPFSection() {
  const ebpf = usePoll<EBPFView>((signal) => get("/network/ebpf", undefined, signal), 30_000)
  const [type, setType] = useState<string>()

  if (!ebpf.data) {
    return (
      <Section title="eBPF">
        {ebpf.error ? <ErrorState error={ebpf.error} onRetry={ebpf.refresh} /> : <LoadingPanel />}
      </Section>
    )
  }
  const view = ebpf.data
  if (!view.installed) {
    return (
      <Section title="eBPF">
        <InstallHandoff
          pkg={view.package ?? "bpftool"}
          products={["linux"]}
          title="bpftool is not installed"
          description="The kernel runs eBPF programs for its firewalls, tracers and packet filters; bpftool is the tool that lists them. Once it is in, what is loaded and where it is attached appears here."
          onInstalled={ebpf.refresh}
        />
      </Section>
    )
  }
  const programs = type ? view.programs.filter((p) => p.type === type) : view.programs
  const stats = view.bpfStatsEnabled
  const memlock = view.programs.reduce((n, p) => n + p.memlock, 0)

  return (
    <Section title="eBPF">
      {view.error && <Notice title="bpftool reported a problem">{view.error}</Notice>}
      <StatGrid columns={3}>
        <StatTile
          label="Programs"
          value={<NumberTicker value={view.total} />}
          hint={`${plural(view.byType.length, "type")} loaded`}
        />
        <StatTile
          label="Locked memory"
          value={bytes(memlock)}
          hint="held by the programs while they are loaded"
        />
        <StatTile
          label="Attached"
          value={<NumberTicker value={view.attachments.length} />}
          hint={
            view.attachments.length > 0
              ? [...new Set(view.attachments.map((a) => a.device))].join(", ")
              : "to no network device"
          }
        />
      </StatGrid>

      {view.attachments.length > 0 && (
        <Panel plain>
          <PanelHeader title="Attached to devices" />
          <PanelBody flush>
            <RowList>
              {view.attachments.map((a) => (
                <Row
                  key={`${a.device}:${a.kind}:${a.programId}`}
                  leading={<LinkGlyph kind="physical" className="size-4 text-muted-foreground" />}
                  title={<span className="font-mono">{a.device}</span>}
                  subtitle={
                    <>
                      {a.name ?? "unnamed"} · program <span className="numeric">{a.programId}</span>
                      {a.mode && ` · ${a.mode} mode`}
                    </>
                  }
                  trailing={<Tag>{a.kind}</Tag>}
                />
              ))}
            </RowList>
          </PanelBody>
        </Panel>
      )}

      <Panel>
        <PanelHeader
          title="Programs"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {type ? `${programs.length} of ${view.total}` : plural(view.total, "program")}
            </span>
          }
        />
        {view.byType.length > 1 && (
          <div className="border-b border-hairline px-4 py-2.5">
            <ChipStrip>
              <FilterChip selected={type === undefined} onClick={() => setType(undefined)}>
                All <ChipCount>{view.total}</ChipCount>
              </FilterChip>
              {view.byType.map((t) => (
                <FilterChip
                  key={t.type}
                  selected={type === t.type}
                  onClick={() => setType(type === t.type ? undefined : t.type)}
                >
                  <span className="font-mono">{t.type}</span> <ChipCount>{t.count}</ChipCount>
                </FilterChip>
              ))}
            </ChipStrip>
          </div>
        )}
        <PanelBody flush>
          <Table containerClassName="max-h-[28rem]">
            <TableHeader>
              <TableRow>
                <TableHead className="text-right">ID</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Name</TableHead>
                <TableHead className="max-md:hidden">Loaded</TableHead>
                <TableHead className="text-right">Memory</TableHead>
                {stats && <TableHead className="text-right">Runs</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {programs.map((p) => (
                <TableRow key={p.id}>
                  <TableCell className="numeric text-right font-mono text-muted-foreground">
                    {p.id}
                  </TableCell>
                  <TableCell className="font-mono whitespace-nowrap">{p.type}</TableCell>
                  <TableCell>
                    {p.name ? (
                      <span className="font-mono">{p.name}</span>
                    ) : (
                      <span className="text-muted-foreground">unnamed</span>
                    )}
                    {p.owners && p.owners.length > 0 && (
                      <span className="ml-2 text-hint text-muted-foreground">
                        {p.owners.join(", ")}
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground max-md:hidden">
                    {p.loadedAt ? relativeTime(p.loadedAt) : "—"}
                  </TableCell>
                  <TableCell className="numeric text-right whitespace-nowrap">
                    {bytes(p.memlock)}
                  </TableCell>
                  {stats && (
                    <TableCell className="numeric text-right">
                      {p.runCount === undefined ? "—" : num(p.runCount)}
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </PanelBody>
      </Panel>
      <p className="text-hint text-muted-foreground">
        This is what the kernel has loaded right now, read with bpftool; the dashboard runs no probe
        of its own.
        {!stats && " Run counts are off: kernel.bpf_stats_enabled is 0."}
      </p>
    </Section>
  )
}
