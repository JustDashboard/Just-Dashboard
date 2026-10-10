"use client"

import { useState } from "react"
import { get } from "@/lib/api"
import { bytes, plural, relativeTime } from "@/lib/format"
import type { EBPFView } from "@/lib/types"
import { NetworkReadWarning } from "@/components/network/read-warning"
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
import { InstallHandoff, InstallFollowUp } from "@/components/network/install"
import { LinkGlyph } from "@/components/network/marks"
import { EBPFProgramSheet } from "@/components/network/traffic/ebpf-detail"
import type { EBPFPlatform } from "@/lib/network-traffic"

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
 * because a column of zeros would read as programs that never ran. What the
 * kernel offers eBPF — JIT, unprivileged loading, BTF, the bpf filesystem —
 * is read from its own files and stated under the figures; a program opens
 * into its maps, every place it is attached and its cost per run, and the
 * programs of the dashboard's own kernel observer are marked as such.
 */
export function EBPFSection() {
  const ebpf = usePoll<EBPFView>((signal) => get("/network/ebpf", undefined, signal), 30_000)
  const [type, setType] = useState<string>()
  const [opened, setOpened] = useState<number>()

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
        <NetworkReadWarning
          error={ebpf.error}
          refresh={ebpf.refresh}
          lastSuccess={ebpf.lastSuccess}
          reading="eBPF inventory"
        />
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
  const observer = new Set(view.observerProgramIds ?? [])

  return (
    <Section title="eBPF">
      <NetworkReadWarning
        error={ebpf.error}
        refresh={ebpf.refresh}
        lastSuccess={ebpf.lastSuccess}
        reading="eBPF inventory"
      />
      <InstallFollowUp pkg={view.package ?? "bpftool"} />
      {view.error && <Notice title="bpftool reported a problem">{view.error}</Notice>}
      <StatGrid columns={4}>
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
        <StatTile
          label="On cgroups"
          value={
            view.cgroupAttachments === undefined ? (
              "—"
            ) : (
              <NumberTicker value={view.cgroupAttachments} />
            )
          }
          hint={
            view.cgroupAttachments === undefined
              ? "the cgroup tree could not be read"
              : "service firewalls, device filters and observers"
          }
        />
      </StatGrid>
      {view.platform && <PlatformFacts platform={view.platform} />}

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
                    <button
                      type="button"
                      aria-label={`Open program ${p.id}`}
                      onClick={() => setOpened(p.id)}
                      className="rounded-sm text-left underline-offset-4 focus-ring hover:underline"
                    >
                      {p.name ? (
                        <span className="font-mono">{p.name}</span>
                      ) : (
                        <span className="text-muted-foreground">unnamed</span>
                      )}
                    </button>
                    {observer.has(p.id) && (
                      <Tag className="ml-2">this dashboard&rsquo;s observer</Tag>
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
        This is what the kernel has loaded right now, read with bpftool. The dashboard loads
        programs only when its kernel observer is turned on in Socket history, and they are marked
        here.
        {!stats && " Run counts are off: kernel.bpf_stats_enabled is 0."}
      </p>
      {opened !== undefined && (
        <EBPFProgramSheet id={opened} onClose={() => setOpened(undefined)} />
      )}
    </Section>
  )
}

const JIT_WORD: Record<string, string> = { "0": "off", "1": "on", "2": "on, with debug output" }
const UNPRIVILEGED_WORD: Record<string, string> = {
  "0": "allowed",
  "1": "refused until reboot",
  "2": "refused",
}

/** What the kernel offers eBPF, from its own files. */
function PlatformFacts({ platform }: { platform: EBPFPlatform }) {
  const facts = [
    platform.kernel && `Kernel ${platform.kernel}`,
    platform.jit &&
      `JIT ${JIT_WORD[platform.jit] ?? platform.jit}${platform.jitHarden && platform.jitHarden !== "0" ? ", hardened" : ""}`,
    platform.unprivilegedDisabled &&
      `unprivileged loading ${UNPRIVILEGED_WORD[platform.unprivilegedDisabled] ?? platform.unprivilegedDisabled}`,
    platform.btf ? "BTF type information present" : "no BTF type information",
    platform.bpffs ? "bpf filesystem mounted" : "bpf filesystem not mounted",
    platform.statsEnabled ? "run statistics on" : "run statistics off",
  ].filter(Boolean)
  return (
    <p className="text-hint text-muted-foreground" aria-label="eBPF platform">
      {facts.join(" · ")}
    </p>
  )
}
