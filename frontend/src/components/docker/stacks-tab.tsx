"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { Cross, FolderPlus, Layers, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { useSessionState } from "@/lib/view-state"
import type {
  ComposeStack,
  Container,
  ContainerSparkline,
  ContainerStats,
  DockerEventFeed,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useMetrics } from "@/hooks/use-metrics"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { useConfirm } from "@/components/confirm-dialog"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { StreamState } from "@/components/overview/readings"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { cn } from "@/lib/utils"
import { ExplainIcon, Field, Term } from "@/components/docker/explain"
import { useContainerControl } from "@/components/docker/container-actions"
import { StackBand } from "@/components/docker/stack-band"
import { StackRows } from "@/components/docker/stack-table"
import {
  exitCode,
  stackChanges,
  stackLines,
  type StackBucket,
  type StackLine,
} from "@/components/docker/stack-readings"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

/** The state chips, in the order they are asked about; the toned one only while there is one. */
const STATES: { value: StackBucket; label: string; dot: string; title: string }[] = [
  { value: "running", label: "Running", dot: "bg-success", title: "Up, and nothing to act on" },
  {
    value: "attention",
    label: "Needs attention",
    dot: "bg-warning",
    title: "A service down or failing its check, or a container the file no longer declares",
  },
  {
    value: "stopped",
    label: "Stopped",
    dot: "bg-muted-foreground/50",
    title: "Deployed, nothing running",
  },
  {
    value: "undeployed",
    label: "Not deployed",
    dot: "bg-muted-foreground/30",
    title: "A compose file with no containers",
  },
]

const BUCKETS = new Set<string>(STATES.map((s) => s.value))

/**
 * Every compose stack on the server, what each is using, and what just
 * happened to them.
 *
 * It opened on a title and a list of cards, each a stack's name over its
 * services as a line of 6px dots — a page nothing on which moved, where
 * "is the database up" was a dot and "which application is using the
 * memory" had no answer at all. It now reads as Services does (§15):
 *
 * The server first, as the identity line Services and Live open on — the
 * Compose mark, Docker's version, how many stacks are deployed and how many
 * of their services run, how many ports they publish — with the verdict at
 * its right end: the stacks that need attention, which narrows the table to
 * them, or that nothing does.
 *
 * Then `StackBand`: the stacks using the most processor and memory as spans
 * of one bar the size of the machine, summed over each one's containers from
 * the containers socket, and the last things Docker did to them — a start, an
 * exit and its code, a kill for memory, a failed check.
 *
 * Then the table, which is the stacks with their containers under them
 * (`stack-table.tsx`): each stack a row with its state and its sums, each
 * container a row of the containers page's own readings. The state chips in
 * its head count *and* narrow, Needs attention in its tone and first in the
 * table as it always was. A container's state moves the moment Docker's does,
 * because it is read off the socket, and the stack list is read again when
 * one does so a stack's own state follows within the second.
 */
export function StacksTab() {
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const { host, snapshot } = useMetrics()
  const wide = useMediaQuery("(min-width: 1280px)")

  /*
    `?stack=` opened a sheet on this page until 2026-09-21, and a container
    managed by compose linked to exactly that address. Those links outlive the
    panel, so they land on the stack.
  */
  const legacy = useSearchParams().get("stack")
  useEffect(() => {
    if (legacy) router.replace(`/docker/stacks/${encodeURIComponent(legacy)}`)
  }, [legacy, router])

  const [creating, setCreating] = useState(false)
  const [query, setQuery] = useSessionState("docker.stacks.query", "")
  const [remembered, setState] = useSessionState("docker.stacks.state", "")
  // The chips were All / Running / Not running / Needs attention until the
  // overhaul; a remembered "all" or "stopped" is no filter now.
  const state = (BUCKETS.has(remembered) ? remembered : "") as StackBucket | ""
  const [focus, setFocus] = useState("")
  const [collapsed, setCollapsed] = useSessionState<string[]>("docker.stacks.collapsed", [])
  const [deploying, setDeploying] = useState<Record<string, string>>({})

  const list = usePoll(
    (signal) => get<ComposeStack[]>("/docker/stacks/", undefined, signal),
    15_000,
  )
  const engine = usePoll(
    (signal) =>
      get<{ available: boolean; serverVersion?: string }>("/docker/ping", undefined, signal),
    60_000,
  )
  const feed = usePoll(
    (signal) => get<DockerEventFeed>("/docker/events", { limit: 200 }, signal),
    15_000,
  )
  // The hour behind each container's figure. A host with the recorder off
  // answers 503, and the rows simply draw no line.
  const trends = usePoll<ContainerSparkline[]>(
    (signal) =>
      get<ContainerSparkline[]>(
        "/docker/containers/stats/history",
        { range: "1h", points: 40 },
        signal,
      ),
    120_000,
    [],
  )
  const trendByName = useMemo(
    () => new Map((trends.data ?? []).map((line) => [line.name, line])),
    [trends.data],
  )

  const [containers, setContainers] = useState<Container[]>([])
  const [stats, setStats] = useState<Record<string, ContainerStats>>({})
  const refreshStacks = list.refresh
  const signature = useRef("")
  const onMessage = useCallback(
    (envelope: Envelope) => {
      if (envelope.type === "containers") {
        const next = envelope.data as Container[]
        setContainers(next)
        // A stack's own state is the list's to say; read it again the moment
        // a container's changes rather than up to fifteen seconds later.
        const changed = next.map((c) => `${c.id}:${c.state}:${c.health ?? ""}`).join(",")
        if (signature.current && signature.current !== changed) refreshStacks()
        signature.current = changed
      } else if (envelope.type === "stats") {
        const rows = envelope.data as ContainerStats[]
        setStats(Object.fromEntries(rows.map((r) => [r.id, r])))
      }
    },
    [refreshStacks],
  )
  const socket = useSocket("/docker/containers/stream", { onMessage })
  const { pending, act } = useContainerControl(list.refresh)

  const stacks = useMemo(() => list.data ?? [], [list.data])
  const lines = useMemo(() => stackLines(stacks, containers, stats), [stacks, containers, stats])
  const changes = useMemo(() => stackChanges(feed.data?.events ?? []), [feed.data])

  const counts = useMemo(() => {
    const bucket: Record<StackBucket, number> = {
      attention: 0,
      running: 0,
      stopped: 0,
      undeployed: 0,
    }
    for (const line of lines) bucket[line.bucket]++
    return bucket
  }, [lines])

  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return lines.flatMap((line): StackLine[] => {
      if (state && line.bucket !== state) return []
      if (focus && line.stack.name !== focus) return []
      if (!needle || line.stack.name.toLowerCase().includes(needle)) return [line]
      // A service's name or image narrows the stack to that service.
      const matching = line.lines.filter(
        (s) =>
          s.service.name.toLowerCase().includes(needle) ||
          s.service.image.toLowerCase().includes(needle),
      )
      return matching.length > 0 ? [{ ...line, lines: matching }] : []
    })
  }, [lines, query, state, focus])

  const toggleState = (next: StackBucket) => setState(state === next ? "" : next)
  const toggleCollapsed = (stack: string) =>
    setCollapsed(
      collapsed.includes(stack) ? collapsed.filter((s) => s !== stack) : [...collapsed, stack],
    )
  const openStack = (stack: string) => router.push(`/docker/stacks/${encodeURIComponent(stack)}`)
  const openContainer = (id: string, tab?: string) =>
    router.push(
      `/docker/containers/${encodeURIComponent(id)}${tab ? `?tab=${encodeURIComponent(tab)}` : ""}`,
    )

  // An application that is down and should not be is the one thing worth
  // doing from the list; the rest needs the stack's page, where the output
  // is and a deploy can be previewed before it runs.
  const deploy = async (name: string) => {
    setDeploying((d) => ({ ...d, [name]: "Deploying" }))
    try {
      await post(`/docker/stacks/${encodeURIComponent(name)}/up`)
      notify.success(`${name} deployed`)
      list.refresh()
    } catch (err) {
      notify.error(`Could not deploy ${name}`, err)
    } finally {
      setDeploying((d) => {
        const next = { ...d }
        delete next[name]
        return next
      })
    }
  }

  const refreshAll = () => {
    list.refresh()
    feed.refresh()
  }

  const canCreate = can("system.admin") && can("file.write")
  const createButton = canCreate && (
    <Button size="sm" onClick={() => setCreating(true)}>
      <FolderPlus className="size-4" />
      Create stack
    </Button>
  )
  const header = <PageContext eyebrow="Docker" title="Stacks" />
  const newStackDialog = (
    <NewStackDialog
      open={creating && canCreate}
      onOpenChange={setCreating}
      onCreated={(name) => {
        list.refresh()
        openStack(name)
      }}
    />
  )

  if (list.loading && !list.data) {
    return (
      <Page>
        {header}
        <LoadingPanel />
      </Page>
    )
  }
  if (list.error && !list.data) {
    return (
      <Page>
        {header}
        <ErrorState error={list.error} />
      </Page>
    )
  }
  if (stacks.length === 0) {
    return (
      <Page>
        {header}
        <EmptyState
          icon={Layers}
          title="No compose stacks found"
          description={
            <>
              A <Term name="stack">stack</Term> is a directory with a compose file in it. The
              dashboard finds them by the labels compose puts on the containers it creates, and by
              looking under the configured compose directories.
            </>
          }
          action={createButton}
        />
        {newStackDialog}
      </Page>
    )
  }

  const services = lines.reduce((n, l) => n + l.lines.length, 0)
  const running = lines.reduce((n, l) => n + l.lines.filter((s) => s.state === "running").length, 0)
  const deployed = stacks.filter((s) => s.deployed).length
  const published = lines.reduce(
    (n, l) =>
      n +
      l.lines.reduce(
        (m, s) =>
          m + (s.state === "running" ? s.service.ports.filter((p) => p.publicPort).length : 0),
        0,
      ),
    0,
  )
  // Red when one of them has a service failing its check or one that exited
  // on an error; amber for the rest — a part stopped, a leftover running.
  const failing = lines.some(
    (l) =>
      l.bucket === "attention" &&
      l.lines.some(
        (s) =>
          s.health === "unhealthy" ||
          (s.state !== "running" &&
            !s.service.missing &&
            (exitCode(s.container?.status ?? s.service.status) ?? 0) !== 0),
      ),
  )
  const shown = visible.reduce((n, l) => n + l.lines.length, 0)
  const filtered = query.trim().length > 0 || state !== "" || focus !== ""

  return (
    <Workspace
      name="Stacks"
      refresh={refreshAll}
      escape={() => {
        if (query) {
          setQuery("")
          return true
        }
        if (focus) {
          setFocus("")
          return true
        }
        if (state) {
          setState("")
          return true
        }
        return false
      }}
      commands={[
        {
          id: "attention",
          label: state === "attention" ? "Show every stack" : "Show stacks that need attention",
          run: () => toggleState("attention"),
        },
      ]}
    >
      <Page className="animate-rise">
        {header}

        <HostIdentity
          mark="docker-compose"
          fallback={Layers}
          title={host?.hostname ?? "Stacks"}
          facts={
            <>
              {engine.data?.serverVersion && (
                <>
                  <HostFact product="docker">Docker {engine.data.serverVersion}</HostFact>
                  <FactDot />
                </>
              )}
              <span className="numeric">
                {deployed} of {plural(stacks.length, "stack")} deployed
              </span>
              <FactDot />
              <span className="numeric">
                {running} of {plural(services, "service")} running
              </span>
              {published > 0 && (
                <>
                  <FactDot />
                  <span className="numeric">{plural(published, "port")} published</span>
                </>
              )}
            </>
          }
          aside={
            <div className="flex flex-wrap items-center gap-3">
              {counts.attention > 0 ? (
                <button
                  type="button"
                  aria-pressed={state === "attention"}
                  onClick={() => toggleState("attention")}
                  className="rounded-md px-1.5 py-1 focus-ring transition-colors hover:bg-row-hover"
                >
                  <Status
                    tone={failing ? "danger" : "warning"}
                    label={`${plural(counts.attention, "stack")} need${counts.attention === 1 ? "s" : ""} attention`}
                  />
                </button>
              ) : counts.running > 0 ? (
                <Status tone="running" label="Nothing needs attention" />
              ) : (
                <Status tone="stopped" label="Nothing running" />
              )}
              <WorkspaceHelp compact />
            </div>
          }
        />

        <StackBand
          lines={lines}
          snapshot={snapshot}
          changes={changes}
          listening={feed.data?.listening ?? true}
          selected={focus}
          onSelect={setFocus}
          onOpen={openStack}
        />

        {/* Framed, because it is a table: the grid owns a scroll region and
            the edge is what says so (§2). Everything above it stays plain. */}
        <Panel>
          <PanelHeader
            title={
              <span className="inline-flex items-center gap-1.5">
                Stacks
                <span className="numeric ml-0.5 text-body font-normal text-muted-foreground">
                  {stacks.length}
                </span>
                <ExplainIcon name="stack" />
              </span>
            }
            actions={
              <>
                <StreamState connection={socket.state} />
                {createButton}
              </>
            }
          >
            <ChipStrip aria-label="State" className="mr-auto">
              {STATES.map(({ value, label, dot, title }) => {
                const count = counts[value]
                if (value !== "running" && count === 0 && state !== value) return null
                const attention = value === "attention"
                return (
                  <FilterChip
                    key={value}
                    selected={state === value}
                    title={title}
                    onClick={() => toggleState(value)}
                  >
                    <span aria-hidden className={cn("size-1.5 rounded-full", dot)} />
                    {label}
                    <ChipCount className={cn(attention && "text-warning opacity-100")}>
                      {count}
                    </ChipCount>
                  </FilterChip>
                )
              })}
            </ChipStrip>
          </PanelHeader>
          <PanelToolbar>
            <SearchInput
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Stack, service or image"
              containerClassName="sm:w-64"
            />
            {focus && (
              <FilterChip
                selected
                aria-label={`Showing ${focus}; press to show every stack`}
                onClick={() => setFocus("")}
              >
                {focus}
                <Cross aria-hidden className="size-3" />
              </FilterChip>
            )}
            {state && (
              <FilterChip
                selected
                className="ml-auto"
                aria-label="Show stacks in every state"
                onClick={() => setState("")}
              >
                {STATES.find((s) => s.value === state)?.label}
                <Cross aria-hidden className="size-3" />
              </FilterChip>
            )}
          </PanelToolbar>
          <PanelBody flush>
            {visible.length === 0 ? (
              <EmptyState
                icon={Warning}
                title="Nothing matches those filters"
                description="Clear the search, or look under a different state."
                className="my-4"
                action={
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => {
                      setQuery("")
                      setFocus("")
                      setState("")
                    }}
                  >
                    Clear filters
                  </Button>
                }
              />
            ) : (
              <StackRows
                lines={visible}
                wide={wide}
                pending={pending}
                confirm={confirm}
                act={act}
                trends={trendByName}
                deploying={deploying}
                collapsed={collapsed}
                onToggle={toggleCollapsed}
                onOpenStack={openStack}
                onOpenContainer={openContainer}
                onDeploy={(name) => void deploy(name)}
              />
            )}
          </PanelBody>
          <PanelFooter className="text-hint text-muted-foreground">
            <span className="numeric">
              {filtered
                ? `${plural(visible.length, "stack")} of ${stacks.length} · ${plural(shown, "container")}`
                : `${plural(stacks.length, "stack")} · ${plural(services, "container")}`}
            </span>
            <span className="text-muted-foreground/40">·</span>
            <span>needs attention first, then by name</span>
          </PanelFooter>
        </Panel>

        {newStackDialog}
        {dialog}
      </Page>
    </Workspace>
  )
}

/**
 * A new stack is a directory and a compose file — nothing more, which is the
 * point. It is created with a working starter file rather than an empty one,
 * because the format is exactly the part somebody new does not know, and a
 * blank editor is the least useful thing to hand them.
 */
function NewStackDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (name: string) => void
}) {
  const [name, setName] = useState("")
  const [dir, setDir] = useState("")
  const [busy, setBusy] = useState(false)

  const create = async () => {
    setBusy(true)
    try {
      const res = await post<{ name: string; dir: string }>("/docker/stacks/", {
        name,
        dir: dir.trim() || undefined,
      })
      notify.success(`${res.name} created`, { description: res.dir })
      onOpenChange(false)
      onCreated(res.name)
      setName("")
      setDir("")
    } catch (err) {
      notify.error("Could not create the stack", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(o) => !busy && onOpenChange(o)}
      title="Create stack"
      description="Creates a directory with a starter compose file in it. Nothing runs until you bring it
            up."
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={create} disabled={busy || !name.trim()} pending={busy}>
            Create
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field
          label="Name"
          htmlFor="stack-name"
          required
          hint="Lower-case letters, digits, dashes and underscores. Compose uses it to name the containers and the network it creates."
        >
          <Input
            id="stack-name"
            value={name}
            spellCheck={false}
            placeholder="my-app"
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field
          label="Directory"
          htmlFor="stack-dir"
          hint="It has to be under one of the server's configured compose directories, or the dashboard will not find the stack again once it is stopped."
        >
          <Input
            id="stack-dir"
            value={dir}
            spellCheck={false}
            className="font-mono text-xs"
            placeholder="leave empty for the default compose directory"
            onChange={(e) => setDir(e.target.value)}
          />
        </Field>
      </div>
    </Modal>
  )
}
