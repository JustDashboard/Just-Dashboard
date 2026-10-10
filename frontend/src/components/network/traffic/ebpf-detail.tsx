"use client"

import { get } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import type { EBPFProgramDetail } from "@/lib/network-traffic"
import { usePoll } from "@/hooks/use-poll"
import { SidePanel } from "@/components/side-panel"
import { FormFact, FormFacts } from "@/components/form"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

const num = (value: number) => value.toLocaleString()

/**
 * One loaded program in full, read-only like the inventory: what bpftool
 * says of it, the maps it holds, every place it is attached — devices,
 * cgroups and bpf links — and, where the kernel keeps run statistics, what one
 * run costs on average. The dashboard's own observer is named as such.
 */
export function EBPFProgramSheet({ id, onClose }: { id: number; onClose: () => void }) {
  const detail = usePoll<EBPFProgramDetail>(
    (signal) => get(`/network/ebpf/${id}`, undefined, signal),
    30_000,
    [id],
  )
  const d = detail.data
  return (
    <SidePanel
      open
      onOpenChange={(open) => !open && onClose()}
      width="lg"
      title={
        <span className="font-mono">
          {d?.name || `program ${id}`}
          {d?.observer && <Tag className="ml-2">this dashboard&rsquo;s observer</Tag>}
        </span>
      }
      description={`eBPF program ${id}`}
    >
      {!d ? (
        detail.error ? (
          <ErrorState error={detail.error} onRetry={detail.refresh} />
        ) : (
          <LoadingPanel />
        )
      ) : (
        <div className="flex flex-col gap-6">
          <FormFacts>
            <FormFact label="Type" mono>
              {d.type}
            </FormFact>
            <FormFact label="Loaded" mono>
              {d.loadedAt ? relativeTime(d.loadedAt) : "—"}
            </FormFact>
            <FormFact label="Code" mono>
              {num(d.bytesXlated)} B{d.jited ? `, ${num(d.bytesJited)} B native` : ", interpreted"}
            </FormFact>
            <FormFact label="Cost a run" mono>
              {d.avgRunNs !== undefined
                ? `${num(d.avgRunNs)} ns over ${num(d.runCount ?? 0)} runs`
                : "not counted"}
            </FormFact>
          </FormFacts>
          <p className="text-hint text-muted-foreground">
            Tag <span className="font-mono">{d.tag ?? "—"}</span> · uid {d.uid} ·{" "}
            {d.gplCompatible ? "GPL-compatible" : "not GPL-compatible"}
            {d.verifiedInsns ? ` · ${num(d.verifiedInsns)} instructions verified` : ""}
            {d.btfId ? ` · BTF ${d.btfId}` : ""} · {bytes(d.memlock)} locked
            {d.owners?.length ? ` · held by ${d.owners.join(", ")}` : ""}
            {d.pinned?.length ? ` · pinned at ${d.pinned.join(", ")}` : ""}
          </p>
          {d.avgRunNs === undefined && (
            <Notice title="Run cost is not counted">
              The kernel times programs only with kernel.bpf_stats_enabled, which costs a little on
              every run; the dashboard does not turn it on.
            </Notice>
          )}
          {d.errors.length > 0 && (
            <Notice tone="warning" title="Part of the detail could not be read">
              {d.errors.join("; ")}
            </Notice>
          )}
          <div className="flex min-w-0 flex-col gap-2">
            <p className="text-title font-medium">Attached to</p>
            {d.devices.length + d.cgroups.length + d.links.length === 0 ? (
              <EmptyNote>Loaded and attached nowhere bpftool can see.</EmptyNote>
            ) : (
              <RowList aria-label={`Where program ${id} is attached`}>
                {d.devices.map((a) => (
                  <Row
                    key={`dev-${a.device}-${a.kind}`}
                    title={<span className="font-mono">{a.device}</span>}
                    subtitle={a.mode ? `${a.mode} mode` : "network device"}
                    trailing={<Tag>{a.kind}</Tag>}
                  />
                ))}
                {d.cgroups.map((c) => (
                  <Row
                    key={`cg-${c.cgroup}-${c.attachType}`}
                    title={<span className="font-mono break-all">{c.cgroup}</span>}
                    subtitle={c.flags ? `cgroup · ${c.flags}` : "cgroup"}
                    trailing={<Tag>{c.attachType}</Tag>}
                  />
                ))}
                {d.links.map((l) => (
                  <Row
                    key={`link-${l.id}`}
                    title={<span className="font-mono">link {l.id}</span>}
                    subtitle={[
                      l.type,
                      l.cgroupId ? `cgroup ${l.cgroupId}` : "",
                      l.ifindex ? `ifindex ${l.ifindex}` : "",
                      l.netnsIno ? `netns ${l.netnsIno}` : "",
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                    trailing={l.attachType ? <Tag>{l.attachType}</Tag> : undefined}
                  />
                ))}
              </RowList>
            )}
          </div>
          <div className="flex min-w-0 flex-col gap-2">
            <p className="text-title font-medium">Maps</p>
            {d.maps.length === 0 ? (
              <EmptyNote>The program holds no maps.</EmptyNote>
            ) : (
              <Table aria-label={`Maps of program ${id}`}>
                <TableHeader>
                  <TableRow>
                    <TableHead className="text-right">ID</TableHead>
                    <TableHead>Type</TableHead>
                    <TableHead>Name</TableHead>
                    <TableHead className="text-right max-sm:hidden">Entries</TableHead>
                    <TableHead className="text-right">Memory</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {d.maps.map((m) => (
                    <TableRow key={m.id}>
                      <TableCell className="numeric text-right font-mono">{m.id}</TableCell>
                      <TableCell className="font-mono">{m.error ? "—" : m.type}</TableCell>
                      <TableCell>
                        {m.error ? (
                          <span className="text-hint text-muted-foreground">{m.error}</span>
                        ) : (
                          <span className="font-mono">
                            {m.name || "unnamed"}
                            {m.frozen && " · frozen"}
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="numeric text-right max-sm:hidden">
                        {m.error ? "—" : `${num(m.maxEntries)} × ${m.keyBytes}+${m.valueBytes} B`}
                      </TableCell>
                      <TableCell className="numeric text-right">
                        {m.error ? "—" : bytes(m.memlock)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </div>
        </div>
      )}
    </SidePanel>
  )
}
