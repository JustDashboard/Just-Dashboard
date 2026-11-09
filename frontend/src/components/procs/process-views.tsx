"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  ArrowUpRight,
  Cpu,
  FileText,
  FolderOpen,
  Globe,
  NetworkDevice,
  Warning,
} from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, duration, percent, plural } from "@/lib/format"
import { dockerSource, journalSource, pm2Source } from "@/lib/log-sources"
import type {
  ContainerDetail,
  PM2Process,
  ProcessInspect,
  ProcessRow,
  SystemdUnit,
} from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmFn } from "@/components/docker/shared"
import { EnvironmentTable } from "@/components/docker/container-tables"
import { useContainerControl, useContainerVerbs } from "@/components/docker/container-actions"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { Detail, DetailList, SearchInput, Section } from "@/components/page"
import { Panel, PanelHeader, PanelToolbar } from "@/components/panel"
import {
  ProductGlyph,
  hasProductLogo,
  pm2Product,
  processProduct,
  unitProduct,
} from "@/components/product-logo"
import { EmptyNote, EmptyState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { VerbBar } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { OOMControl, PriorityControl } from "@/components/procs/process-detail"
import {
  SIGNALS,
  capabilityWord,
  signalEffect,
  type SignalSpec,
} from "@/components/procs/process-facts"
import type { SignalFn } from "@/components/procs/process-actions"
import { usePM2Control, usePM2Verbs } from "@/components/procs/pm2-actions"
import { useUnitControl, useUnitVerbs } from "@/components/procs/unit-actions"
import { managerHref, ownerName, reach, uncontrollable } from "@/components/procs/shared"

/**
 * What supervises the process, read as its own page reads it: the unit's
 * `systemctl show`, PM2's record of the application, the container's detail.
 * The Control tab draws that supervisor's verbs and the Logs tab its log, so
 * "stop it for good" is on the page that found the process.
 */
export type Owner =
  | { kind: "systemd"; unit: SystemdUnit; canReload: boolean }
  | { kind: "pm2"; app: PM2Process }
  | { kind: "container"; container: ContainerDetail }

export function useOwner(row: ProcessRow | undefined) {
  const manager = row?.manager
  const name = row?.managerName ?? ""
  const pid = row?.pid ?? 0
  const wanted =
    Boolean(name) && (manager === "systemd" || manager === "pm2" || manager === "container")
  const read = usePoll<Owner | null>(
    async (signal) => {
      if (manager === "systemd") {
        const shown = await get<{ unit: SystemdUnit; properties: Record<string, string> }>(
          `/systemd/${encodeURIComponent(name)}`,
          undefined,
          signal,
        )
        return {
          kind: "systemd",
          unit: shown.unit,
          canReload: shown.properties?.CanReload === "yes",
        }
      }
      if (manager === "pm2") {
        const list = await get<{ processes: PM2Process[] }>("/pm2/", undefined, signal)
        const app =
          list.processes?.find((p) => p.pid === pid) ?? list.processes?.find((p) => p.name === name)
        return app ? { kind: "pm2", app } : null
      }
      const container = await get<ContainerDetail>(
        `/docker/containers/${encodeURIComponent(name)}`,
        undefined,
        signal,
      )
      return { kind: "container", container }
    },
    10_000,
    [manager, name, pid],
    { enabled: wanted },
  )
  return { owner: wanted ? (read.data ?? undefined) : undefined, refresh: read.refresh }
}

/** The supervisor's log as a source the service logs pane reads. */
export function ownerLogSources(row: ProcessRow, owner: Owner | undefined): ServiceLogSource[] {
  if (row.manager === "systemd" && row.managerName) {
    return [
      {
        id: journalSource(row.managerName),
        label: row.managerName,
        kind: "journal",
        product: unitProduct(row.managerName),
      },
    ]
  }
  if (row.manager === "container" && row.managerName) {
    return [
      {
        id: dockerSource(row.managerName),
        label: ownerName(row),
        kind: "docker",
        status: owner?.kind === "container" ? owner.container.state : undefined,
        product: processProduct(row.managerLabel ?? "") ?? "docker",
      },
    ]
  }
  if (owner?.kind === "pm2") {
    return [
      {
        id: pm2Source(owner.app.daemonId, owner.app.id, owner.app.name),
        label: owner.app.name,
        kind: "pm2",
        product: pm2Product(owner.app.interpreter),
      },
    ]
  }
  return []
}

export function ProcessLogs({ row, owner }: { row: ProcessRow; owner: Owner | undefined }) {
  const sources = useMemo(() => ownerLogSources(row, owner), [row, owner])
  if (sources.length === 0) {
    return (
      <EmptyState
        icon={FileText}
        title="Nothing keeps a log of it"
        description="It is not run by a systemd unit, a PM2 application or a container, so whatever it prints goes wherever it was started from."
      />
    )
  }
  return (
    <div className="flex h-full min-h-0 flex-col gap-3">
      <p className="text-hint text-muted-foreground">
        What {ownerName(row) || row.name} has logged — every process it runs writes here, not only
        PID {row.pid}.
      </p>
      <ServiceLogs
        sources={sources}
        storageKey={`processes.process.logs.${sources[0].id}`}
        className="min-h-0 flex-1"
        paneClassName="min-h-[30rem]"
      />
    </div>
  )
}

/**
 * Every thing that can be done to the process, each said in this process's
 * terms before it is done: the signals with what each will do to it — read
 * from the masks of the signals it handles, so a hang-up to nginx says it
 * reloads and to a script that it ends it — then its priority, its standing
 * with the out-of-memory killer, and what supervises it, which is where
 * "stop it for good" is.
 */
export function ProcessControl({
  row,
  gone,
  inspect,
  childCount: children,
  owner,
  confirm,
  signal,
  pending,
  onChanged,
}: {
  row: ProcessRow
  gone?: string
  inspect?: ProcessInspect
  childCount: number
  owner: Owner | undefined
  confirm: ConfirmFn
  signal: SignalFn
  pending?: string
  onChanged: () => void
}) {
  const { can } = useAuth()
  const [tree, setTree] = useState(false)
  const kind = uncontrollable(row)
  const signalling = !gone && !kind && can("destructive")
  const send = (spec: SignalSpec) => {
    const withChildren = tree && children > 0
    const action = (phrase?: string) =>
      signal(row, spec.signal, spec.progressive, phrase, withChildren)
    if (!spec.confirm && !withChildren) {
      void action().catch(() => undefined)
      return
    }
    confirm({
      title: `Send ${spec.signal}`,
      confirmLabel: spec.danger ? spec.label : "Send",
      description: (
        <>
          <p>{signalEffect(spec, row.name, inspect?.signals).sentence}</p>
          {withChildren && (
            <p>
              It goes to {plural(children, "process", "processes")} it started as well, and to
              everything those started.
            </p>
          )}
        </>
      ),
      action: (phrase) => action(phrase),
    })
  }
  return (
    <div className="flex min-w-0 flex-col gap-8 pb-6">
      <Owners row={row} owner={owner} confirm={confirm} onChanged={onChanged} />

      <Section
        title="Signals"
        actions={
          signalling &&
          children > 0 && (
            <label className="flex items-center gap-2 text-xs text-muted-foreground">
              <Switch checked={tree} onCheckedChange={setTree} aria-label="Include its children" />
              With the {plural(children, "process", "processes")} it started
            </label>
          )
        }
      >
        {kind === "kernel" && (
          <Notice title="The kernel does not deliver signals to its own threads">
            Its priority below is the only thing that can be changed.
          </Notice>
        )}
        {kind === "zombie" && (
          <Notice tone="danger" title="It has already exited">
            A signal reaches nothing. Its parent, PID {row.ppid}, is the process to restart or end.
          </Notice>
        )}
        {!gone && !kind && !can("destructive") && (
          <p className="text-hint text-muted-foreground">
            Sending signals needs destructive actions.
          </p>
        )}
        <ul aria-label="Signals" className="-mx-2 flex min-w-0 flex-col">
          {SIGNALS.map((spec) => {
            const effect = signalEffect(spec, row.name, inspect?.signals)
            return (
              <li
                key={spec.signal}
                className="flex h-14 min-w-0 items-center gap-3 rounded-md px-2 transition-colors hover:bg-row-hover"
              >
                <div className="min-w-0 flex-1">
                  <p className="flex min-w-0 items-baseline gap-2">
                    <span className="truncate text-body font-medium">{spec.label}</span>
                    <span className="shrink-0 font-mono text-hint text-muted-foreground">
                      {spec.signal}
                    </span>
                    <span
                      className={cn(
                        "shrink-0 text-hint font-medium",
                        effect.tone === "success" && "text-success",
                        effect.tone === "warning" && "text-warning",
                        effect.tone === "danger" && "text-destructive",
                        effect.tone === "muted" && "text-muted-foreground",
                      )}
                    >
                      {effect.word}
                    </span>
                  </p>
                  <p className="truncate text-hint text-muted-foreground" title={effect.sentence}>
                    {effect.tone === "success" ? spec.usual : effect.sentence}
                  </p>
                </div>
                <Button
                  size="xs"
                  variant="outline"
                  disabled={!signalling || Boolean(pending)}
                  className={cn(spec.danger && "text-destructive")}
                  aria-label={`Send ${spec.signal} to ${row.name}`}
                  onClick={() => send(spec)}
                >
                  Send
                </Button>
              </li>
            )
          })}
        </ul>
      </Section>

      <div className="grid min-w-0 gap-8 xl:grid-cols-2 [&>*]:min-w-0">
        <Section title="Priority">
          {gone || kind === "zombie" ? (
            <p className="text-hint text-muted-foreground">It is not running.</p>
          ) : (
            <PriorityControl process={row} onChanged={onChanged} />
          )}
        </Section>
        {kind === null && !gone && (
          <Section title="When memory runs out">
            <OOMControl process={row} inspect={inspect} onChanged={onChanged} />
          </Section>
        )}
      </div>
    </div>
  )
}

/** The supervisor's own verbs, on its mark, with why it is the place to stop the process. */
function Owners({
  row,
  owner,
  confirm,
  onChanged,
}: {
  row: ProcessRow
  owner: Owner | undefined
  confirm: ConfirmFn
  onChanged: () => void
}) {
  const href = managerHref(row)
  if (!href) {
    if (row.manager === "kernel") return null
    return (
      <Notice
        tone={row.manager === "unmanaged" ? "warning" : "default"}
        title={row.manager === "session" ? "Started from a login session" : "Nothing supervises it"}
      >
        {row.manager === "session"
          ? "It lives as long as that session, or until it is ended here. Nothing starts it again."
          : "Nothing would start it again if it exits, and it does not survive a reboot. A systemd unit or PM2 would keep it running."}
      </Notice>
    )
  }
  const name = ownerName(row)
  const product =
    row.manager === "systemd"
      ? (unitProduct(row.managerName ?? "") ?? "systemd")
      : row.manager === "pm2"
        ? "pm2"
        : (processProduct(row.managerLabel ?? "") ?? "docker")
  return (
    <Section
      title="What supervises it"
      actions={
        <Link
          href={href}
          className="inline-flex items-center gap-1 rounded-sm text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
        >
          Open {name} <ArrowUpRight className="size-3" />
        </Link>
      }
    >
      {/* The mark and its sentence keep 18rem of their own, so on a phone the
          verbs go under them rather than squeezing the sentence to a word a
          line. */}
      <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-3 rounded-lg border border-hairline px-4 py-3">
        <div className="flex min-w-0 flex-[1_1_18rem] items-center gap-4">
          <span className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-hairline bg-background [&>img]:size-6">
            {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Cpu className="size-4" />}
          </span>
          <div className="min-w-0 flex-1">
            <p className="truncate text-body font-medium">{name}</p>
            <p className="text-hint text-muted-foreground">
              {row.manager === "systemd"
                ? "systemd starts this process again when it exits. To stop it for good, stop the unit."
                : row.manager === "pm2"
                  ? "PM2 starts this process again when it exits. To stop it for good, stop the application."
                  : "The container runs this process; ending its main process stops the container."}
            </p>
          </div>
        </div>
        {owner?.kind === "systemd" && (
          <UnitVerbs owner={owner} confirm={confirm} onChanged={onChanged} />
        )}
        {owner?.kind === "pm2" && (
          <PM2Verbs owner={owner} confirm={confirm} onChanged={onChanged} />
        )}
        {owner?.kind === "container" && (
          <ContainerVerbs owner={owner} confirm={confirm} onChanged={onChanged} />
        )}
      </div>
    </Section>
  )
}

function UnitVerbs({
  owner,
  confirm,
  onChanged,
}: {
  owner: Extract<Owner, { kind: "systemd" }>
  confirm: ConfirmFn
  onChanged: () => void
}) {
  const { act, pending } = useUnitControl(onChanged)
  const verbs = useUnitVerbs({ unit: owner.unit, confirm, act, canReload: owner.canReload })
  const busy = pending[owner.unit.name]
  return (
    <span className="flex flex-wrap items-center gap-3">
      {busy ? (
        <Status tone="notice" label={`${busy}…`} />
      ) : (
        <Status
          state={owner.unit.activeState}
          label={owner.unit.subState || owner.unit.activeState}
        />
      )}
      <VerbBar verbs={verbs} menuLabel={`More for ${owner.unit.name}`} />
    </span>
  )
}

function PM2Verbs({
  owner,
  confirm,
  onChanged,
}: {
  owner: Extract<Owner, { kind: "pm2" }>
  confirm: ConfirmFn
  onChanged: () => void
}) {
  const { act, pending } = usePM2Control(onChanged)
  const verbs = usePM2Verbs({ process: owner.app, confirm, act, onChanged })
  const busy = Object.values(pending)[0]
  return (
    <span className="flex flex-wrap items-center gap-3">
      {busy ? (
        <Status tone="notice" label={`${busy}…`} />
      ) : (
        <Status state={owner.app.status} label={owner.app.status} />
      )}
      <VerbBar verbs={verbs} menuLabel={`More for ${owner.app.name}`} />
    </span>
  )
}

function ContainerVerbs({
  owner,
  confirm,
  onChanged,
}: {
  owner: Extract<Owner, { kind: "container" }>
  confirm: ConfirmFn
  onChanged: () => void
}) {
  const router = useRouter()
  const { act, pending } = useContainerControl(onChanged)
  const container = owner.container
  const verbs = useContainerVerbs({
    container,
    confirm,
    act,
    onChanged,
    onOpenTab: (tab) =>
      router.push(
        `/docker/containers/${encodeURIComponent(container.id)}?tab=${encodeURIComponent(tab)}`,
      ),
  })
  const busy = pending[container.id]
  return (
    <span className="flex flex-wrap items-center gap-3">
      {busy ? (
        <Status tone="notice" label={`${busy}…`} />
      ) : (
        <Status state={container.state} label={container.state} />
      )}
      <VerbBar verbs={verbs} menuLabel={`More for ${container.name}`} />
    </span>
  )
}

/** Its threads, busiest first: a process at 300% is one thread spinning, and its name says which. */
export function ProcessThreads({ inspect }: { inspect?: ProcessInspect }) {
  const [query, setQuery] = useState("")
  if (!inspect) return <EmptyNote>Reading its threads…</EmptyNote>
  const total = inspect.threads.reduce((n, t) => n + t.cpuSeconds, 0)
  const needle = query.trim().toLowerCase()
  const shown = inspect.threads.filter(
    (t) => !needle || t.name.toLowerCase().includes(needle) || String(t.tid).includes(needle),
  )
  return (
    <Panel className="max-h-full min-h-0 animate-rise">
      <PanelHeader
        title="Threads"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {inspect.threadsTotal > inspect.threads.length
              ? `the busiest ${inspect.threads.length} of ${inspect.threadsTotal}`
              : inspect.threadsTotal}
          </span>
        }
      />
      {inspect.threads.length > 12 && (
        <PanelToolbar>
          <SearchInput
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Name or thread ID"
            aria-label="Filter the threads"
          />
        </PanelToolbar>
      )}
      <Table className="table-fixed" containerClassName="min-h-0">
        <TableHeader className={stickyTableHeader}>
          <TableRow>
            <TableHead className="w-24 pl-5">Thread</TableHead>
            <TableHead>Name</TableHead>
            <TableHead className="w-28">State</TableHead>
            <TableHead className="w-32 text-right">Processor time</TableHead>
            <TableHead className="w-24 text-right">Share</TableHead>
            <TableHead className="w-24 pr-5 text-right">Last CPU</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {shown.map((thread) => (
            <TableRow key={thread.tid}>
              <TableCell className="numeric py-2 pl-5 font-mono text-muted-foreground">
                {thread.tid}
              </TableCell>
              <TableCell className="truncate py-2 font-mono">{thread.name}</TableCell>
              <TableCell className="py-2">
                <Status
                  state={thread.state === "sleeping" ? "inactive" : thread.state}
                  label={thread.state}
                />
              </TableCell>
              <TableCell className="numeric py-2 text-right font-mono">
                {duration(thread.cpuSeconds)}
              </TableCell>
              <TableCell className="numeric py-2 text-right font-mono text-muted-foreground">
                {total > 0 ? percent((thread.cpuSeconds / total) * 100) : "—"}
              </TableCell>
              <TableCell className="numeric py-2 pr-5 text-right font-mono text-muted-foreground">
                {thread.lastCpu}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {shown.length === 0 && <EmptyNote className="px-5 py-4">No thread matches.</EmptyNote>}
    </Panel>
  )
}

type FileKind = "file" | "socket" | "pipe" | "device" | "other" | "deleted"

const FILE_KINDS: { kind: FileKind; label: string }[] = [
  { kind: "file", label: "Files" },
  { kind: "socket", label: "Sockets" },
  { kind: "pipe", label: "Pipes" },
  { kind: "device", label: "Devices" },
  { kind: "other", label: "Other" },
  { kind: "deleted", label: "Deleted" },
]

/** What it holds open, one descriptor at a time, and the libraries it has loaded. */
export function ProcessFiles({ inspect }: { inspect?: ProcessInspect }) {
  const [query, setQuery] = useState("")
  const [only, setOnly] = useState<FileKind>()
  if (!inspect) return <EmptyNote>Reading its open files…</EmptyNote>
  const files = inspect.files
  if (!files.ready) {
    return (
      <EmptyState
        icon={FolderOpen}
        title="Its open files are not readable as this account"
        description="The kernel shows a process's descriptors only to its own user and to root."
      />
    )
  }
  const counts: Record<FileKind, number> = {
    file: files.files,
    socket: files.sockets,
    pipe: files.pipes,
    device: files.devices,
    other: files.other,
    deleted: files.deleted,
  }
  const needle = query.trim().toLowerCase()
  const shown = files.entries.filter(
    (entry) =>
      (!only || (only === "deleted" ? entry.deleted : entry.kind === only)) &&
      (!needle || entry.target.toLowerCase().includes(needle) || String(entry.fd) === needle),
  )
  const replaced = inspect.libraries.filter((library) => library.deleted).length
  return (
    <div className="flex min-w-0 flex-col gap-6 pb-6">
      <Panel className="animate-rise">
        <PanelHeader
          title="Open descriptors"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {files.truncated
                ? `the first ${files.entries.length} of ${files.total}`
                : files.total}
            </span>
          }
        />
        <PanelToolbar className="flex-wrap gap-y-2">
          <SearchInput
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Path or descriptor"
            aria-label="Filter the descriptors"
          />
          <ChipStrip aria-label="Kinds of descriptor">
            <FilterChip selected={!only} onClick={() => setOnly(undefined)}>
              All <ChipCount>{files.total}</ChipCount>
            </FilterChip>
            {FILE_KINDS.filter(({ kind }) => counts[kind] > 0).map(({ kind, label }) => (
              <FilterChip
                key={kind}
                selected={only === kind}
                onClick={() => setOnly(only === kind ? undefined : kind)}
              >
                {label}
                <ChipCount className={cn(kind === "deleted" && "text-warning opacity-100")}>
                  {counts[kind]}
                </ChipCount>
              </FilterChip>
            ))}
          </ChipStrip>
        </PanelToolbar>
        <Table className="table-fixed" containerClassName="max-h-[32rem]">
          <TableHeader className={stickyTableHeader}>
            <TableRow>
              <TableHead className="w-20 pl-5">FD</TableHead>
              <TableHead className="w-28">Kind</TableHead>
              <TableHead className="pr-5">Target</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {shown.map((entry) => (
              <TableRow key={entry.fd}>
                <TableCell className="numeric py-2 pl-5 font-mono text-muted-foreground">
                  {entry.fd}
                </TableCell>
                <TableCell className="py-2">
                  <span className="inline-flex items-center gap-1.5 text-xs">
                    <FileKindMark kind={entry.kind} />
                    {entry.kind}
                  </span>
                </TableCell>
                <TableCell className="truncate py-2 pr-5 font-mono text-xs" title={entry.target}>
                  {entry.kind === "file" ? (
                    <Link
                      href={`/files?path=${encodeURIComponent(entry.target.replace(/\/[^/]*$/, "") || "/")}`}
                      className="hover:underline"
                    >
                      {entry.target}
                    </Link>
                  ) : (
                    <span className="text-muted-foreground">{entry.target}</span>
                  )}
                  {entry.deleted && <span className="ml-2 font-sans text-warning">deleted</span>}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        {shown.length === 0 && <EmptyNote className="px-5 py-4">No descriptor matches.</EmptyNote>}
      </Panel>

      {inspect.libraries.length > 0 && (
        <Section
          title={
            <>
              Libraries it loaded
              <span className="numeric ml-2 text-body font-normal text-muted-foreground">
                {inspect.libraries.length}
              </span>
            </>
          }
          actions={
            replaced > 0 && (
              <span className="text-hint text-warning">
                {plural(replaced, "library", "libraries")} replaced since it loaded them
              </span>
            )
          }
        >
          <ul className="grid min-w-0 gap-x-8 gap-y-1 md:grid-cols-2">
            {inspect.libraries.map((library) => (
              <li
                key={library.path}
                className="flex h-6 min-w-0 items-center gap-2 font-mono text-xs"
                title={library.path}
              >
                <span
                  className={cn(
                    "truncate",
                    library.deleted ? "text-warning" : "text-muted-foreground",
                  )}
                >
                  {library.path}
                </span>
                {library.deleted && <Tag tone="warning">replaced</Tag>}
              </li>
            ))}
          </ul>
        </Section>
      )}
    </div>
  )
}

function FileKindMark({ kind }: { kind: string }) {
  const Glyph =
    kind === "file"
      ? FileText
      : kind === "socket"
        ? NetworkDevice
        : kind === "device"
          ? Cpu
          : FolderOpen
  return <Glyph aria-hidden className="size-3.5 text-muted-foreground" />
}

/** What it listens on and who it is talking to. */
export function ProcessNetwork({ row, inspect }: { row: ProcessRow; inspect?: ProcessInspect }) {
  const listening = row.listening ?? []
  const connections = inspect?.connections ?? []
  return (
    <div className="flex min-w-0 flex-col gap-6 pb-6">
      <Panel className="animate-rise">
        <PanelHeader
          title="Listening"
          actions={
            <span className="numeric text-hint text-muted-foreground">{listening.length}</span>
          }
        />
        {listening.length === 0 ? (
          <EmptyNote className="px-5 py-4">It accepts no connections.</EmptyNote>
        ) : (
          <Table className="table-fixed">
            <TableHeader>
              <TableRow>
                <TableHead className="w-28 pl-5">Port</TableHead>
                <TableHead className="w-24">Protocol</TableHead>
                <TableHead>Reached from</TableHead>
                <TableHead className="w-48 pr-5">Address</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {listening.map((port) => {
                const open = reach(port.address) === "every interface"
                return (
                  <TableRow key={`${port.proto}/${port.address}/${port.port}`}>
                    <TableCell className="numeric py-2 pl-5 font-mono font-medium">
                      :{port.port}
                    </TableCell>
                    <TableCell className="py-2 font-mono text-xs text-muted-foreground uppercase">
                      {port.proto}
                    </TableCell>
                    <TableCell className="py-2">
                      <span
                        className={cn(
                          "inline-flex items-center gap-1.5 text-xs",
                          open && "text-warning",
                        )}
                      >
                        {open ? (
                          <Warning aria-hidden className="size-3.5" />
                        ) : (
                          <Globe aria-hidden className="size-3.5 text-muted-foreground" />
                        )}
                        {reach(port.address)}
                      </span>
                    </TableCell>
                    <TableCell className="truncate py-2 pr-5 font-mono text-xs text-muted-foreground">
                      {port.address || "*"}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
      </Panel>

      <Panel className="animate-rise">
        <PanelHeader
          title="Connections"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {inspect && inspect.connectionsTotal > connections.length
                ? `${connections.length} of ${inspect.connectionsTotal}`
                : (inspect?.connectionsTotal ?? row.connections ?? 0)}
            </span>
          }
        />
        {!inspect ? (
          <EmptyNote className="px-5 py-4">Reading its connections…</EmptyNote>
        ) : connections.length === 0 ? (
          <EmptyNote className="px-5 py-4">It holds no connection open.</EmptyNote>
        ) : (
          <Table className="table-fixed" containerClassName="max-h-[32rem]">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead className="w-24 pl-5">Protocol</TableHead>
                <TableHead>Local</TableHead>
                <TableHead>Remote</TableHead>
                <TableHead className="w-32 pr-5">State</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {connections.map((conn, index) => (
                <TableRow key={`${conn.local}-${conn.remote}-${index}`}>
                  <TableCell className="py-2 pl-5 font-mono text-xs text-muted-foreground uppercase">
                    {conn.proto}
                  </TableCell>
                  <TableCell className="truncate py-2 font-mono text-xs">{conn.local}</TableCell>
                  <TableCell className="truncate py-2 font-mono text-xs">
                    {conn.remote || "—"}
                  </TableCell>
                  <TableCell className="py-2 pr-5">
                    <Status
                      state={conn.state === "established" ? "running" : "inactive"}
                      label={conn.state?.replaceAll("_", " ") || "open"}
                    />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Panel>
    </div>
  )
}

export function ProcessEnvironment({ inspect }: { inspect?: ProcessInspect }) {
  if (!inspect) return <EmptyNote>Reading its environment…</EmptyNote>
  if (!inspect.environmentReadable) {
    return (
      <EmptyState
        icon={FileText}
        title="Its environment is not readable as this account"
        description="The kernel shows a process's environment only to its own user and to root."
      />
    )
  }
  return <EnvironmentTable env={inspect.environment} />
}

/** Every limit the kernel holds it to, and what it may do beyond an ordinary user. */
export function ProcessLimits({ row, inspect }: { row: ProcessRow; inspect?: ProcessInspect }) {
  if (!inspect) return <EmptyNote>Reading its limits…</EmptyNote>
  const cgroup = inspect.cgroup
  const sec = inspect.security
  const sig = inspect.signals
  const capped = Boolean(cgroup.memoryLimit || cgroup.cpuLimit || cgroup.pidsLimit)
  return (
    <div className="flex min-w-0 flex-col gap-8 pb-6">
      {capped && (
        <Section title="Its control group's limits">
          <DetailList>
            {cgroup.memoryLimit ? (
              <Detail label="Memory" className="numeric font-mono">
                {bytes(cgroup.memoryCurrent ?? 0)} of {bytes(cgroup.memoryLimit)}
                <span className="font-sans text-muted-foreground">
                  {" "}
                  · {percent(((cgroup.memoryCurrent ?? 0) / cgroup.memoryLimit) * 100)} used; past
                  it the kernel ends a process in the group
                </span>
              </Detail>
            ) : null}
            {cgroup.cpuLimit ? (
              <Detail label="Processor" className="numeric">
                {cgroup.cpuLimit.toFixed(cgroup.cpuLimit < 1 ? 2 : 1)} cores at most
              </Detail>
            ) : null}
            {cgroup.pidsLimit ? (
              <Detail label="Processes" className="numeric">
                {cgroup.pidsCurrent ?? 0} of {cgroup.pidsLimit}
              </Detail>
            ) : null}
          </DetailList>
        </Section>
      )}
      <Panel className="animate-rise">
        <PanelHeader title="Resource limits" />
        {inspect.limits.length === 0 ? (
          <EmptyNote className="px-5 py-4">Not readable as this account.</EmptyNote>
        ) : (
          <Table className="table-fixed">
            <TableHeader>
              <TableRow>
                <TableHead className="pl-5">Limit</TableHead>
                <TableHead className="w-40 text-right">Soft</TableHead>
                <TableHead className="w-40 text-right">Hard</TableHead>
                <TableHead className="w-32 pr-5">Unit</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {inspect.limits.map((limit) => {
                const files = limit.name === "Max open files"
                const near =
                  files &&
                  row.fileDescriptors !== undefined &&
                  Number(limit.soft) > 0 &&
                  row.fileDescriptors / Number(limit.soft) >= 0.75
                return (
                  <TableRow key={limit.name}>
                    <TableCell className="py-2 pl-5">
                      {limit.name.replace(/^Max /, "")}
                      {files && row.fileDescriptors !== undefined && (
                        <span
                          className={cn(
                            "numeric ml-2 text-hint",
                            near ? "text-warning" : "text-muted-foreground",
                          )}
                        >
                          {row.fileDescriptors} open now
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="numeric py-2 text-right font-mono text-xs">
                      {limit.soft}
                    </TableCell>
                    <TableCell className="numeric py-2 text-right font-mono text-xs text-muted-foreground">
                      {limit.hard}
                    </TableCell>
                    <TableCell className="py-2 pr-5 text-xs text-muted-foreground">
                      {limit.unit}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
      </Panel>

      <div className="grid min-w-0 gap-8 xl:grid-cols-2 [&>*]:min-w-0">
        <Section title="What it may do">
          {!sec.ready ? (
            <p className="text-hint text-muted-foreground">Not readable as this account.</p>
          ) : (
            <>
              <DetailList>
                <Detail label="Capabilities">
                  {sec.allCapabilities ? (
                    <span className="text-warning">every one — it can do anything root can</span>
                  ) : sec.capabilities.length === 0 ? (
                    "none beyond an ordinary user's"
                  ) : (
                    plural(sec.capabilities.length, "capability", "capabilities")
                  )}
                </Detail>
                <Detail label="Can gain more">
                  {sec.noNewPrivs
                    ? "no — it is locked to what it has"
                    : "yes, through a setuid program"}
                </Detail>
                <Detail label="System calls">
                  {sec.seccomp === "filter"
                    ? `filtered${sec.seccompFilters ? ` by ${plural(sec.seccompFilters, "filter")}` : ""}`
                    : sec.seccomp === "strict"
                      ? "only read, write and exit"
                      : "unrestricted"}
                </Detail>
                {sec.label && (
                  <Detail label="Security profile" className="font-mono break-all">
                    {sec.label}
                  </Detail>
                )}
                <Detail label="Ceiling">
                  {sec.boundingAll
                    ? "the full set"
                    : "narrowed — some capabilities can never be gained"}
                </Detail>
              </DetailList>
              {!sec.allCapabilities && sec.capabilities.length > 0 && (
                <ul aria-label="Capabilities" className="flex flex-wrap gap-1.5 pt-1">
                  {sec.capabilities.map((cap) => (
                    <li key={cap}>
                      <Tag
                        title={cap}
                        tone={
                          /SYS_ADMIN|SYS_MODULE|SYS_PTRACE|DAC_OVERRIDE/.test(cap)
                            ? "warning"
                            : undefined
                        }
                      >
                        {capabilityWord(cap)}
                      </Tag>
                    </li>
                  ))}
                </ul>
              )}
            </>
          )}
        </Section>
        <Section title="Signals it handles">
          {!sig.ready ? (
            <p className="text-hint text-muted-foreground">Not readable as this account.</p>
          ) : (
            <DetailList>
              <Detail label="Handles" className="font-mono break-words">
                {sig.caught.length ? (
                  sig.caught.join(" ")
                ) : (
                  <span className="font-sans text-muted-foreground">
                    none — every signal does what the kernel does by default
                  </span>
                )}
              </Detail>
              <Detail label="Ignores" className="font-mono break-words">
                {sig.ignored.length ? (
                  sig.ignored.join(" ")
                ) : (
                  <span className="font-sans text-muted-foreground">none</span>
                )}
              </Detail>
              <Detail label="Holds back" className="font-mono break-words">
                {sig.blocked.length ? (
                  sig.blocked.join(" ")
                ) : (
                  <span className="font-sans text-muted-foreground">none</span>
                )}
              </Detail>
              {sig.pending.length > 0 && (
                <Detail label="Waiting" className="font-mono text-warning">
                  {sig.pending.join(" ")}
                </Detail>
              )}
            </DetailList>
          )}
        </Section>
      </div>
    </div>
  )
}
