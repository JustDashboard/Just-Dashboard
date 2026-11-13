"use client"

import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { get } from "@/lib/api"
import { bytes, plural, relativeTime } from "@/lib/format"
import type { EBPFView } from "@/lib/types"
import type { EBPFPlatform } from "@/lib/network-traffic"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { RowList, Row } from "@/components/row-list"
import { ErrorState, Notice } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Skeleton } from "@/components/ui/skeleton"
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
import { programHref } from "./address"
import { ebpfWords } from "./words"

const num = (value: number) => value.toLocaleString()

/**
 * The eBPF programs loaded into this kernel, read with `bpftool` and drawn as
 * an inventory: each program with its type, when it was loaded and what it
 * holds, and what is attached to which device. Everything here is what the
 * kernel reports at the moment — systemd's cgroup filters, an XDP program on
 * the uplink, whatever Cilium or a tracer loaded — and nothing is a probe
 * this dashboard runs. Without `bpftool` the view says what it would read and
 * offers the package, as every absent tool here does.
 *
 * It opened on four tiles. The program count is the table's own and its
 * chips'; the locked memory, the device attachments and the cgroup
 * attachments are one line of words over the table, with what the kernel
 * offers eBPF — JIT, unprivileged loading, BTF, the bpf filesystem — under
 * it. The table stays a table: an inventory of what may be hundreds of
 * programs is read down its columns, one line each, the name the way in.
 *
 * The type chips are filters and the type is in the address (`?type=`), so a
 * program's page knows which list it was opened from. Run counts are drawn
 * only where the kernel keeps them (`bpf_stats_enabled`): a column of zeros
 * would read as programs that never ran. A program opens its own page — its
 * maps, every place it is attached and its cost per run — and the programs of
 * the dashboard's own kernel observer are marked as such.
 */
export function EBPFPrograms() {
  const ebpf = usePoll<EBPFView>((signal) => get("/network/ebpf", undefined, signal), 30_000)
  const params = useSearchParams()
  const type = params.get("type") ?? undefined
  const setType = (next: string | undefined) => {
    const url = new URL(window.location.href)
    if (next) url.searchParams.set("type", next)
    else url.searchParams.delete("type")
    window.history.replaceState(null, "", `${url.pathname}${url.search}`)
  }

  if (!ebpf.data) {
    return ebpf.error ? (
      <ErrorState error={ebpf.error} onRetry={ebpf.refresh} />
    ) : (
      <Panel plain aria-label="eBPF programs">
        <PanelHeader title="Programs" />
        <PanelBody className="flex flex-col gap-4">
          <span aria-hidden className="flex flex-col gap-2">
            <Skeleton className="my-0.5 h-3.5 w-2/3" />
            <Skeleton className="my-0.5 h-3 w-1/2" />
          </span>
          <div aria-hidden className={FRAME}>
            {Array.from({ length: 6 }, (_, i) => (
              <div key={i} className="flex h-10 items-center gap-6 border-b border-hairline px-4">
                <Skeleton className="h-3 w-10" />
                <Skeleton className="h-3 w-24" />
                <Skeleton className="h-3 w-40" />
              </div>
            ))}
          </div>
        </PanelBody>
      </Panel>
    )
  }
  const view = ebpf.data
  const warning = (
    <NetworkReadWarning
      error={ebpf.error}
      refresh={ebpf.refresh}
      lastSuccess={ebpf.lastSuccess}
      reading="eBPF inventory"
    />
  )
  if (!view.installed) {
    return (
      <div className="flex min-w-0 flex-col gap-4">
        {warning}
        <InstallHandoff
          pkg={view.package ?? "bpftool"}
          products={["linux"]}
          title="bpftool is not installed"
          description="The kernel runs eBPF programs for its firewalls, tracers and packet filters; bpftool is the tool that lists them. Once it is in, what is loaded and where it is attached appears here."
          onInstalled={ebpf.refresh}
        />
      </div>
    )
  }
  const known = view.byType.some((t) => t.type === type) ? type : undefined
  const programs = known ? view.programs.filter((p) => p.type === known) : view.programs
  const stats = view.bpfStatsEnabled
  const observer = new Set(view.observerProgramIds ?? [])

  return (
    <div className="flex min-w-0 flex-col gap-8">
      <Panel plain aria-label="eBPF programs">
        <PanelHeader
          title="Programs"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {known ? `${programs.length} of ${view.total}` : plural(view.total, "program")}
            </span>
          }
        />
        <PanelBody className="flex flex-col gap-4">
          {warning}
          <InstallFollowUp pkg={view.package ?? "bpftool"} />
          {view.error && <Notice title="bpftool reported a problem">{view.error}</Notice>}
          <div className="flex min-w-0 flex-col gap-1">
            <p className="numeric text-body" aria-label="eBPF inventory">
              {ebpfWords(view)}
            </p>
            {view.platform && <PlatformFacts platform={view.platform} />}
          </div>
          <div className={FRAME}>
            {view.byType.length > 1 && (
              <div className="border-b border-hairline px-4 py-2.5">
                <ChipStrip>
                  <FilterChip selected={known === undefined} onClick={() => setType(undefined)}>
                    All <ChipCount>{view.total}</ChipCount>
                  </FilterChip>
                  {view.byType.map((t) => (
                    <FilterChip
                      key={t.type}
                      selected={known === t.type}
                      onClick={() => setType(known === t.type ? undefined : t.type)}
                    >
                      <span className="font-mono">{t.type}</span> <ChipCount>{t.count}</ChipCount>
                    </FilterChip>
                  ))}
                </ChipStrip>
              </div>
            )}
            <Table containerClassName="max-h-[32rem]" aria-label="Loaded eBPF programs">
              <TableHeader>
                <TableRow>
                  <TableHead className="text-right">ID</TableHead>
                  <TableHead className="max-sm:hidden">Type</TableHead>
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
                    <TableCell className="font-mono whitespace-nowrap max-sm:hidden">
                      {p.type}
                    </TableCell>
                    <TableCell className="w-full max-w-0">
                      <span className="flex min-w-0 items-baseline gap-2 whitespace-nowrap">
                        <Link
                          href={programHref(p.id, known)}
                          aria-label={`Open program ${p.id}`}
                          className="min-w-0 truncate rounded-sm underline-offset-4 focus-ring hover:underline"
                        >
                          {p.name ? (
                            <span className="font-mono">{p.name}</span>
                          ) : (
                            <span className="text-muted-foreground">unnamed</span>
                          )}
                        </Link>
                        {observer.has(p.id) && (
                          <Tag className="shrink-0">this dashboard&rsquo;s observer</Tag>
                        )}
                        {p.owners && p.owners.length > 0 && (
                          <span className="min-w-0 truncate text-hint text-muted-foreground">
                            {p.owners.join(", ")}
                          </span>
                        )}
                      </span>
                      <span className="block truncate font-mono text-hint text-muted-foreground sm:hidden">
                        {p.type}
                      </span>
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
          </div>
          <p className="text-hint text-muted-foreground">
            Read with bpftool. The dashboard loads programs only when its kernel observer is on in{" "}
            <Link href="/network/flows" className="underline underline-offset-4">
              Socket history
            </Link>
            .{!stats && " Run counts are off: kernel.bpf_stats_enabled is 0."}
          </p>
        </PanelBody>
      </Panel>

      {view.attachments.length > 0 && (
        <Panel plain aria-label="Attached to devices">
          <PanelHeader
            title="Attached to devices"
            actions={
              <span className="numeric text-hint text-muted-foreground">
                {plural(view.attachments.length, "attachment")}
              </span>
            }
          />
          <PanelBody flush>
            <RowList>
              {view.attachments.map((a) => (
                <Row
                  key={`${a.device}:${a.kind}:${a.programId}`}
                  leading={<LinkGlyph kind="physical" className="size-4 text-muted-foreground" />}
                  title={<span className="font-mono">{a.device}</span>}
                  subtitle={
                    <>
                      <Link
                        href={programHref(a.programId)}
                        className="underline-offset-4 hover:underline"
                      >
                        {a.name ?? "unnamed"} · program{" "}
                        <span className="numeric">{a.programId}</span>
                      </Link>
                      {a.mode && ` · ${a.mode} mode`}
                    </>
                  }
                  trailing={<span className="font-mono text-hint">{a.kind}</span>}
                />
              ))}
            </RowList>
          </PanelBody>
        </Panel>
      )}
    </div>
  )
}

const FRAME = "min-w-0 overflow-hidden rounded-xl border bg-card"

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
