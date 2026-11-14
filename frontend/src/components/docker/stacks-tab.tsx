"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { Cross, FolderPlus, Layers, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { useSessionState } from "@/lib/view-state"
import type { ComposeStack, Container, ContainerStats, DockerEventFeed } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useMetrics } from "@/hooks/use-metrics"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { ChoiceList } from "@/components/flow"
import { FactDot } from "@/components/metrics/host-identity"
import { StreamState } from "@/components/overview/readings"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Workspace } from "@/components/workspace/workspace"
import { cn } from "@/lib/utils"
import { DockerIdentity } from "@/components/docker/docker-identity"
import { ExplainIcon, Field, Term } from "@/components/docker/explain"
import { StackBand } from "@/components/docker/stack-band"
import { StackChoiceRow } from "@/components/docker/stack-row"
import {
  STACK_FILTER_KEYS,
  stackChanges,
  stackLines,
  visibleStacks,
  type StackBucket,
} from "@/components/docker/stack-readings"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

/** The state chips, in the order they are asked about; the toned one only while there is one. */
const STATES: { value: StackBucket; label: string; dot: string; title: string }[] = [
  { value: "running", label: "Running", dot: "bg-success", title: "Up, and nothing to act on" },
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
 * Docker first, as the line every Docker page opens on (`DockerIdentity`):
 * its version, how many stacks run beside its name, how many are deployed,
 * how many of their services run and how many ports they publish, and Create
 * stack as the one command.
 *
 * Then `StackBand`: the stacks using the most processor and memory as spans
 * of one bar the size of the machine, summed over each one's containers from
 * the containers socket, and the last things Docker did to them — a start, an
 * exit and its code, a kill for memory, a failed check.
 *
 * Then the stacks, each a card you open (`StackChoiceRow`): one line, one
 * height, its state and its sums in columns that line up down the list. It
 * was a table whose every stack was a group row with its containers under
 * it, expanding in place; the containers are the stack's own page. The state
 * chips count and narrow by running, stopped and never deployed. A stack's
 * state moves the moment Docker's does: the containers are read off the
 * socket, and the stack list is read again when one changes.
 */
export function StacksTab() {
  const { can } = useAuth()
  const router = useRouter()
  const { host, snapshot } = useMetrics()

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
  const [query, setQuery] = useSessionState(STACK_FILTER_KEYS.query, "")
  const [remembered, setState] = useSessionState(STACK_FILTER_KEYS.state, "")
  // The chips were All / Running / Not running / Needs attention until the
  // overhaul; a remembered "all" or "stopped" is no filter now.
  const state = (BUCKETS.has(remembered) ? remembered : "") as StackBucket | ""
  const [focus, setFocus] = useState("")
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

  const stacks = useMemo(() => list.data ?? [], [list.data])
  const lines = useMemo(() => stackLines(stacks, containers, stats), [stacks, containers, stats])
  const changes = useMemo(() => stackChanges(feed.data?.events ?? []), [feed.data])

  const counts = useMemo(() => {
    const bucket: Record<StackBucket, number> = {
      running: 0,
      stopped: 0,
      undeployed: 0,
    }
    for (const line of lines) bucket[line.bucket]++
    return bucket
  }, [lines])

  const visible = useMemo(
    () => visibleStacks(lines, { query, state, focus }),
    [lines, query, state, focus],
  )

  const toggleState = (next: StackBucket) => setState(state === next ? "" : next)
  const openStack = (stack: string) => router.push(`/docker/stacks/${encodeURIComponent(stack)}`)

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

  if (list.error && !list.data) {
    return (
      <Page>
        {header}
        <ErrorState error={list.error} />
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
    >
      <Page className="animate-rise">
        {header}

        <DockerIdentity
          version={engine.data?.serverVersion}
          host={host ?? undefined}
          state={
            stacks.length > 0 ? (
              <Status
                tone={counts.running > 0 ? "running" : "stopped"}
                label={`${plural(counts.running, "stack")} running`}
              />
            ) : undefined
          }
          facts={
            !list.data ? (
              <TextShimmer>Reading the stacks…</TextShimmer>
            ) : stacks.length === 0 ? (
              <span>No stacks deployed</span>
            ) : (
              <>
                <span className="numeric">
                  {deployed} of {plural(stacks.length, "stack")} deployed
                </span>
                <FactDot />
                <span className="numeric">
                  {running} of {plural(services, "service")} running
                </span>
                {published > 0 && (
                  <span className="contents max-sm:hidden">
                    <FactDot />
                    <span className="numeric">{plural(published, "port")} published</span>
                  </span>
                )}
              </>
            )
          }
          // On a server with none, the empty state under the band offers it.
          command={list.data && stacks.length === 0 ? undefined : createButton}
        />

        <StackBand
          lines={lines}
          snapshot={snapshot}
          changes={changes}
          loaded={list.data !== undefined}
          eventsRead={feed.data !== undefined || feed.error !== undefined}
          listening={feed.data?.listening ?? feed.error === undefined}
          selected={focus}
          onSelect={setFocus}
          onOpen={openStack}
        />

        {/* Plain: the stacks are cards you open, not a table read down its
            columns, so the section is a title and a hairline (§2). The line
            and the band stay up while the list is read, and on a server with
            no stacks, so nothing above this moves when the answer lands. */}
        {!list.data ? (
          <LoadingPanel />
        ) : stacks.length === 0 ? (
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
        ) : (
          <Panel plain aria-label="Stacks">
            <PanelHeader
              title={
                <span className="inline-flex items-center gap-1.5">
                  Stacks
                  <span className="numeric ml-0.5 text-body font-normal text-muted-foreground">
                    {filtered ? `${visible.length} of ${stacks.length}` : stacks.length}
                  </span>
                  <ExplainIcon name="stack" />
                </span>
              }
              actions={<StreamState connection={socket.state} />}
            >
              <ChipStrip aria-label="State" className="mr-auto">
                {STATES.map(({ value, label, dot, title }) => {
                  const count = counts[value]
                  if (value !== "running" && count === 0 && state !== value) return null
                  return (
                    <FilterChip
                      key={value}
                      selected={state === value}
                      title={title}
                      onClick={() => toggleState(value)}
                    >
                      <span aria-hidden className={cn("size-1.5 rounded-full", dot)} />
                      {label}
                      <ChipCount>{count}</ChipCount>
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
                <ChoiceList aria-label="Stacks" className="pt-3">
                  {visible.map(({ line, matched }) => (
                    <StackChoiceRow
                      key={line.stack.name}
                      line={line}
                      matched={matched}
                      deploying={deploying[line.stack.name]}
                      onDeploy={(name) => void deploy(name)}
                    />
                  ))}
                </ChoiceList>
              )}
            </PanelBody>
          </Panel>
        )}

        {newStackDialog}
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
