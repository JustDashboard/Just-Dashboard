"use client"

import Link from "next/link"
import { useMemo } from "react"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { ApiError, get } from "@/lib/api"
import { bytes, plural, relativeTime } from "@/lib/format"
import type { EBPFProgramDetail } from "@/lib/network-traffic"
import type { EBPFView } from "@/lib/types"
import { useSessionState } from "@/lib/view-state"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { Code } from "@/components/icons"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Detail, DetailList, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { TextShimmer } from "@/components/ui/text-shimmer"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { programHref, programId, trafficHref } from "./address"
import {
  BackLink,
  DetailRail,
  DetailShell,
  RailGhosts,
  RailRow,
  StepArrows,
  steps,
  type Step,
} from "./detail-shell"

const num = (value: number) => value.toLocaleString()

/**
 * One loaded eBPF program, as a place of its own and read-only like the
 * inventory: what bpftool says of it, every place it is attached — devices,
 * cgroups and bpf links — the maps it holds and, where the kernel keeps run
 * statistics, what one run costs on average. The dashboard's own observer is
 * named as such.
 *
 * It was a sheet over the Traffic page. A program on a busy host is attached
 * to dozens of cgroups and holds a table of maps, which is more than a glance
 * and wants the page's width; the programs it was picked from are the rail
 * from `xl`, narrowed to the type the list was narrowed to (`?type=`).
 */
export function EBPFProgramPage() {
  const params = useParams<{ id: string }>()
  const search = useSearchParams()
  const router = useRouter()
  const id = programId(params.id)
  const asked = search.get("type") ?? undefined
  const [query, setQuery] = useSessionState("network.traffic.programs.query", "")
  const list = usePoll<EBPFView>((signal) => get("/network/ebpf", undefined, signal), 30_000)
  const type = list.data?.byType.some((t) => t.type === asked) ? asked : undefined
  const programs = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return (list.data?.programs ?? []).filter(
      (p) =>
        (!type || p.type === type) &&
        (!needle ||
          (p.name ?? "").toLowerCase().includes(needle) ||
          p.type.toLowerCase().includes(needle) ||
          String(p.id).includes(needle)),
    )
  }, [list.data, query, type])
  const open = (next: number) => router.push(programHref(next, type))
  const step = steps(
    programs.map((p) => p.id),
    id,
    open,
  )
  const detail = usePoll<EBPFProgramDetail>(
    (signal) => get(`/network/ebpf/${id}`, undefined, signal),
    30_000,
    [id],
    { enabled: id !== null },
  )
  const observer = new Set(list.data?.observerProgramIds ?? [])
  const back = trafficHref({ tab: "ebpf", type })

  return (
    <DetailShell
      name="eBPF program"
      what="program"
      stateKey="network.traffic.program"
      refresh={() => {
        list.refresh()
        detail.refresh()
      }}
      previous={step.previous}
      next={step.next}
      rail={
        <DetailRail
          label="Programs"
          slot="program-rail"
          current={id}
          query={query}
          onQuery={setQuery}
          placeholder="Name, type or ID"
          caption={
            list.data ? (
              type ? (
                <>
                  {plural(programs.length, "program")} of type{" "}
                  <span className="font-mono">{type}</span>
                </>
              ) : (
                plural(programs.length, "program")
              )
            ) : list.error ? (
              "The programs could not be read"
            ) : (
              "Reading the programs…"
            )
          }
          filter={
            type && id !== null ? (
              <Link
                href={programHref(id)}
                replace
                className="shrink-0 rounded-sm font-medium focus-ring hover:text-foreground"
              >
                Every type
              </Link>
            ) : undefined
          }
          empty={list.data && programs.length === 0 ? "No program matches." : undefined}
        >
          {!list.data && !list.error && <RailGhosts />}
          {programs.map((p) => (
            <RailRow
              key={p.id}
              id={String(p.id)}
              name={p.name || "unnamed"}
              label={`${p.name || "unnamed"}, program ${p.id}`}
              current={p.id === id}
              onOpen={() => open(p.id)}
              leading={
                <ProductLogo
                  id="linux"
                  size="sm"
                  fallback={Code}
                  className="size-7 rounded-md [&_img]:size-4"
                />
              }
              detail={observer.has(p.id) ? `${p.type} · this dashboard’s observer` : p.type}
              trailing={p.id}
            />
          ))}
        </DetailRail>
      }
      header={
        <ProgramHeader
          id={id}
          back={back}
          detail={detail.data}
          missing={detail.error instanceof ApiError && detail.error.status === 404}
          previous={step.previous}
          next={step.next}
        />
      }
    >
      {id === null ? (
        <EmptyState
          icon={Code}
          title="That is not a program ID"
          action={
            <Link href={back} className="text-body font-medium text-brand hover:underline">
              Back to the programs
            </Link>
          }
        />
      ) : (
        <ProgramBody key={id} id={id} back={back} detail={detail} />
      )}
    </DetailShell>
  )
}

function ProgramHeader({
  id,
  back,
  detail: d,
  missing,
  previous,
  next,
}: {
  id: number | null
  back: string
  detail: EBPFProgramDetail | undefined
  missing: boolean
  previous: Step
  next: Step
}) {
  const places = d ? d.devices.length + d.cgroups.length + d.links.length : 0
  return (
    <>
      <PageContext
        eyebrow={<BackLink href={back}>eBPF programs</BackLink>}
        title={d?.name ? `${d.name}, eBPF program ${id}` : `eBPF program ${id ?? ""}`}
        actions={<StepArrows what="program" previous={previous} next={next} />}
      />
      {id !== null && (
        <HostIdentity
          label={`About program ${id}`}
          mark="linux"
          fallback={Code}
          title={<span className="truncate font-mono">{d?.name || `program ${id}`}</span>}
          state={
            d ? (
              places > 0 ? (
                <Status state="running" label={`attached in ${plural(places, "place")}`} />
              ) : (
                <Status tone="unknown" label="attached nowhere" />
              )
            ) : missing ? (
              <Status tone="unknown" label="not loaded" />
            ) : (
              <Status tone="unknown" label="reading" />
            )
          }
          facts={
            d ? (
              <>
                <HostFact>
                  <span className="font-mono">{d.type}</span>
                </HostFact>
                <FactDot />
                <span className="numeric">ID {d.id}</span>
                <FactDot />
                <span>
                  {d.loadedAt ? `loaded ${relativeTime(d.loadedAt)}` : "load time unknown"}
                </span>
                {d.owners && d.owners.length > 0 && (
                  <>
                    <FactDot />
                    <HostFact>held by {d.owners.join(", ")}</HostFact>
                  </>
                )}
                {d.observer && (
                  <>
                    <FactDot />
                    <Tag>this dashboard&rsquo;s observer</Tag>
                  </>
                )}
              </>
            ) : missing ? (
              <span>The kernel no longer lists it</span>
            ) : (
              <TextShimmer>Reading the program…</TextShimmer>
            )
          }
        />
      )}
    </>
  )
}

function ProgramBody({
  id,
  back,
  detail,
}: {
  id: number
  back: string
  detail: PollState<EBPFProgramDetail>
}) {
  const d = detail.data
  if (!d) {
    if (!detail.error) return <LoadingRows rows={6} />
    return detail.error instanceof ApiError && detail.error.status === 404 ? (
      <EmptyState
        icon={Code}
        title={`No program with ID ${id} is loaded`}
        description="A program is unloaded when what loaded it lets go: a service that stopped, a tracer that exited."
        action={
          <Link href={back} className="text-body font-medium text-brand hover:underline">
            Back to the programs
          </Link>
        }
      />
    ) : (
      <ErrorState error={detail.error} onRetry={detail.refresh} />
    )
  }
  const places = d.devices.length + d.cgroups.length + d.links.length
  return (
    <>
      <NetworkReadWarning
        error={detail.error}
        refresh={detail.refresh}
        lastSuccess={detail.lastSuccess}
        reading="program"
      />
      {d.errors.length > 0 && (
        <Notice tone="warning" title="Part of the detail could not be read">
          {d.errors.join("; ")}
        </Notice>
      )}
      <DetailList aria-label={`Program ${id} in the kernel`} className="max-w-2xl">
        <Detail label="Code" className="numeric font-mono">
          {num(d.bytesXlated)} B{d.jited ? `, ${num(d.bytesJited)} B native` : ", interpreted"}
          {d.verifiedInsns ? ` · ${num(d.verifiedInsns)} instructions verified` : ""}
        </Detail>
        <Detail label="Cost a run" className="numeric font-mono">
          {d.avgRunNs !== undefined ? (
            `${num(d.avgRunNs)} ns over ${num(d.runCount ?? 0)} runs`
          ) : (
            <span className="font-sans text-muted-foreground">
              not counted: the kernel times programs only with kernel.bpf_stats_enabled, which costs
              a little on every run
            </span>
          )}
        </Detail>
        <Detail label="Locked memory" className="numeric font-mono">
          {bytes(d.memlock)}
        </Detail>
        <Detail label="Tag" className="font-mono">
          {d.tag ?? "—"}
          {d.btfId ? ` · BTF ${d.btfId}` : ""}
        </Detail>
        <Detail label="Loaded by" className="font-mono">
          uid {d.uid} · {d.gplCompatible ? "GPL-compatible" : "not GPL-compatible"}
        </Detail>
        {d.pinned && d.pinned.length > 0 && (
          <Detail label="Pinned at" className="font-mono break-all">
            {d.pinned.join(", ")}
          </Detail>
        )}
      </DetailList>

      <Panel plain aria-label={`Where program ${id} is attached`}>
        <PanelHeader
          title="Attached to"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {plural(places, "place")}
            </span>
          }
        />
        <PanelBody flush>
          {places === 0 ? (
            <EmptyNote>Loaded and attached nowhere bpftool can see.</EmptyNote>
          ) : (
            <RowList>
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
        </PanelBody>
      </Panel>

      <Panel plain aria-label={`Maps of program ${id}`}>
        <PanelHeader
          title="Maps"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {plural(d.maps.length, "map")}
            </span>
          }
        />
        <PanelBody>
          {d.maps.length === 0 ? (
            <EmptyNote>The program holds no maps.</EmptyNote>
          ) : (
            <div className="min-w-0 overflow-hidden rounded-xl border bg-card">
              <Table>
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
            </div>
          )}
        </PanelBody>
      </Panel>
    </>
  )
}
