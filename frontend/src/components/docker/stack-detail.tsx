"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import {
  ArrowCircleUp,
  Box,
  Code,
  FloppyDisk,
  FolderOpen,
  GitBranch,
  Layers,
  Play,
  RefreshClockwise,
  RotateClockwise,
  StopCircle,
  Terminal,
  Warning,
  Wrench,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post, put, ApiError } from "@/lib/api"
import type { ComposeService, ComposeValidation, LogLine, StackDetail } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { PortLink, type ConfirmFn } from "@/components/docker/shared"
import { RunConsole, useRunConsole } from "@/components/docker/run-console"
import { Hint, Term } from "@/components/docker/explain"
import { DeployPreviewPanel, DeploymentHistoryPanel } from "@/components/docker/deploy-preview"
import {
  COMPOSE_ACTIONS,
  StackStateBadge,
  type ComposeActionKey,
} from "@/components/docker/stack-state"
import { CodeEditor } from "@/components/code-editor"
import { LogViewer } from "@/components/log-viewer"
import { SidePanel } from "@/components/side-panel"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"

/**
 * A stack, as the application it is rather than as five container rows.
 *
 * Three things make this different from the stack cards it replaces, and all
 * three are borrowed from the tool that does compose best:
 *
 *   - **The file is editable here.** A compose file is the description of the
 *     application; sending someone to a file manager to change it and back
 *     here to apply it is two tools for one thought. It is validated before it
 *     is written and the previous version is kept beside it.
 *   - **Commands are watched, not waited for.** `up` on a stack that pulls and
 *     builds takes minutes.
 *   - **Its logs are one feed.** A stack is one story told by four processes,
 *     and reading it meant opening four panels and matching timestamps by eye.
 *
 * The fourth thing is ours: a stack is a directory, and this dashboard also
 * has a file manager, a git panel and a terminal. Knowing that a stack's
 * directory is a checkout with uncommitted changes, two commits behind its
 * remote, is exactly the context somebody needs before pressing redeploy — and
 * it is one link away rather than a different product.
 */
export function StackDetailPanel({
  name,
  onOpenChange,
  onChanged,
  confirm,
}: {
  name: string | null
  onOpenChange: (open: boolean) => void
  onChanged?: () => void
  confirm: ConfirmFn
}) {
  return (
    <StackBody
      key={name ?? "none"}
      name={name}
      onOpenChange={onOpenChange}
      onChanged={onChanged}
      confirm={confirm}
    />
  )
}

function StackBody({
  name,
  onOpenChange,
  onChanged,
  confirm,
}: {
  name: string | null
  onOpenChange: (open: boolean) => void
  onChanged?: () => void
  confirm: ConfirmFn
}) {
  const { can } = useAuth()
  const [tab, setTab] = useViewState("docker.stack.tab", "services")
  const runner = useRunConsole()

  const { data, error, loading, refresh } = usePoll<StackDetail>(
    (signal) =>
      get<StackDetail>(`/docker/stacks/${encodeURIComponent(name ?? "")}`, undefined, signal),
    // Slower while a command is running: the poll would otherwise fight the
    // console for attention, and the interesting output is in the console.
    runner.running ? 0 : 10000,
    [name, runner.running],
    // Not while closed: with no name the path is the stack *list*, and this
    // panel would render an array's missing fields.
    { enabled: name !== null },
  )

  const reload = useCallback(() => {
    refresh()
    onChanged?.()
  }, [refresh, onChanged])

  const run = async (action: string, opts: { confirmPhrase?: string; service?: string } = {}) => {
    const code = await runner.run(`/docker/stacks/${encodeURIComponent(name ?? "")}/run`, {
      action,
      service: opts.service,
      confirm: opts.confirmPhrase,
    })
    reload()
    if (code !== 0) throw new Error(`compose ${action} exited with status ${code}`)
  }

  /**
   * The destructive actions all pause for a confirmation, and `down` is the one
   * that asks for the stack's name to be typed — it is the only one that
   * removes the containers. Update and restart are the ordinary redeploy cycle,
   * run several times in an afternoon, and the server narrows the phrase the
   * same way so the two cannot disagree.
   */
  const confirmRun = (action: string, title: string, description: React.ReactNode) =>
    confirm({
      title,
      phrase: action === "down" ? (name ?? "") : undefined,
      confirmLabel: title.split(" ")[0],
      description,
      action: (phrase) => run(action, { confirmPhrase: phrase }),
    })

  return (
    <SidePanel
      open={name !== null}
      onOpenChange={onOpenChange}
      width="xl"
      icon={Layers}
      title={
        <>
          {name}
          {data && <StackStateBadge stack={data} />}
        </>
      }
      description={data ? `${data.summary} · ${data.workingDir}` : undefined}
      bodyClassName="flex min-h-0 flex-1 flex-col gap-3 p-4"
      actions={
        data && <StackActions data={data} run={run} confirmRun={confirmRun} runner={runner} />
      }
    >
      {error && <ErrorState error={error} />}
      {loading && !data && <LoadingRows />}

      {data && (
        <>
          {!data.managed && (
            <Notice title="Read-only here" icon={Warning}>
              No compose file for this stack is reachable from the dashboard, so it can be watched
              but not acted on. Compose records the project directory on the containers it creates —
              if the stack was started elsewhere, or its directory has moved, that record no longer
              points at anything.
            </Notice>
          )}
          {data.declaredError && (
            <Notice title="This stack's compose file does not parse" icon={Warning} tone="danger">
              {data.declaredError}
            </Notice>
          )}
          <StackLinks data={data} />
          <RunConsole
            lines={runner.lines}
            state={runner.state}
            exitCode={runner.exitCode}
            title={`compose · ${name}`}
            onDismiss={runner.reset}
          />

          <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col gap-3">
            <TabsList className="w-fit shrink-0">
              <TabsTrigger value="services">Services</TabsTrigger>
              {/* What a deploy would change, before it changes it. */}
              <TabsTrigger value="preview">Deploy preview</TabsTrigger>
              <TabsTrigger value="compose">Compose file</TabsTrigger>
              <TabsTrigger value="history">History</TabsTrigger>
              <TabsTrigger value="logs">Logs</TabsTrigger>
            </TabsList>
            <TabsContent value="services" className="min-h-0 flex-1 space-y-2 overflow-y-auto">
              {data.services.map((svc) => (
                <ServiceRow
                  key={svc.name}
                  stack={data.name}
                  service={svc}
                  managed={data.managed}
                  onRun={run}
                />
              ))}
              {data.services.length === 0 && (
                <EmptyState
                  icon={Box}
                  title="Nothing running"
                  description="This stack has a compose file but no containers. Bring it up to start them."
                />
              )}
            </TabsContent>
            <TabsContent value="preview" className="min-h-0 flex-1 overflow-y-auto">
              {tab === "preview" && <DeployPreviewPanel stack={data.name} />}
            </TabsContent>
            <TabsContent value="compose" className="min-h-0 flex-1">
              <ComposeEditor stack={data} onSaved={reload} canWrite={can("file.write")} />
            </TabsContent>
            <TabsContent value="history" className="min-h-0 flex-1 overflow-y-auto">
              {tab === "history" && <DeploymentHistoryPanel stack={data.name} />}
            </TabsContent>
            <TabsContent value="logs" className="min-h-0 flex-1">
              {tab === "logs" && <StackLogs stack={data.name} active />}
            </TabsContent>
          </Tabs>
        </>
      )}
    </SidePanel>
  )
}

function StackActions({
  data,
  run,
  confirmRun,
  runner,
}: {
  data: StackDetail
  run: (action: string, opts?: { confirmPhrase?: string; service?: string }) => Promise<void>
  confirmRun: (action: string, title: string, description: React.ReactNode) => void
  runner: ReturnType<typeof useRunConsole>
}) {
  const { can } = useAuth()
  if (!data.managed) return null
  const busy = runner.running
  const quiet = (fn: () => Promise<void>) => () => {
    fn().catch((err) => notify.error(String(err)))
  }

  /**
   * Compose's verbs, named for what they do to the server.
   *
   * `Up`, `Update` and `Down` are precise and mean nothing without the compose
   * reference — and `Down` is the worst of the three, because it sounds like
   * the opposite of `Up` and is not: it deletes the containers and the project
   * network. Every button here says what it does, and every confirmation
   * carries both the blast radius and the exact command being run, so an
   * operator who knows compose can check the translation.
   */
  const act = (action: ComposeActionKey, extra?: React.ReactNode) => {
    const meta = COMPOSE_ACTIONS[action]
    confirmRun(
      action,
      meta.label,
      <>
        <p>
          <b>{data.name}</b> — {meta.blastRadius}
        </p>
        {extra}
        <p className="font-mono text-hint text-muted-foreground">{meta.command}</p>
      </>,
    )
  }

  return (
    <>
      {can("service.control") && (
        <>
          {/* Deploy is not destructive: it starts what is missing and replaces
              what changed. It gets no confirmation for the same reason it is
              the primary button. */}
          <Button size="sm" onClick={quiet(() => run("up"))} pending={busy}>
            <Play className="size-3.5" />
            {COMPOSE_ACTIONS.up.label}
          </Button>
          {/*
            The one button most self-hosted stacks exist to press. Everywhere
            else it is two commands and a paragraph explaining the order;
            here it pulls first, so a registry that is down leaves the running
            stack alone rather than taking it half down and failing.
          */}
          {can("destructive") && (
            <Button size="sm" variant="outline" disabled={busy} onClick={() => act("update")}>
              <ArrowCircleUp className="size-3.5" />
              {COMPOSE_ACTIONS.update.label}
            </Button>
          )}
          <Button size="sm" variant="outline" disabled={busy} onClick={quiet(() => run("build"))}>
            <Wrench className="size-3.5" />
            {COMPOSE_ACTIONS.build.label}
          </Button>
        </>
      )}
      {can("destructive") && (
        <>
          <Button size="sm" variant="outline" disabled={busy} onClick={() => act("restart")}>
            <RotateClockwise className="size-3.5" />
            {COMPOSE_ACTIONS.restart.label}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="text-destructive"
            disabled={busy}
            onClick={() =>
              act(
                "down",
                <p>
                  Deploying afterwards brings the stack back from the same compose file, and the
                  volumes it left behind are still there for it.
                </p>,
              )
            }
          >
            <StopCircle className="size-3.5" />
            {COMPOSE_ACTIONS.down.label}
          </Button>
        </>
      )}
    </>
  )
}

/**
 * The links out.
 *
 * A stack is a directory; this dashboard has a file manager, a git panel and a
 * terminal that can each be pointed at one. The git line is the load-bearing
 * part — "uncommitted changes" means compose will deploy something that is in
 * no commit, and "2 behind" means a pull would change what deploying does.
 */
function StackLinks({ data }: { data: StackDetail }) {
  const { can } = useAuth()
  if (!data.workingDir) return null
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <Button size="xs" variant="outline" asChild>
        <Link href={`/files?path=${encodeURIComponent(data.workingDir)}`}>
          <FolderOpen className="size-3" />
          Files
        </Link>
      </Button>
      {can("terminal") && (
        <Button size="xs" variant="outline" asChild>
          <Link href={`/terminal?cwd=${encodeURIComponent(data.workingDir)}`}>
            <Terminal className="size-3" />
            Shell here
          </Link>
        </Button>
      )}
      {data.git && (
        <Button size="xs" variant="outline" asChild>
          <Link href={`/git?repo=${encodeURIComponent(data.git.path)}`}>
            <GitBranch className="size-3" />
            {data.git.branch ?? "repository"}
            {data.git.dirty && (
              <span className="numeric text-hint text-warning">{data.git.changes} uncommitted</span>
            )}
            {data.git.behind > 0 && (
              <span className="numeric text-hint text-muted-foreground">
                {data.git.behind} behind
              </span>
            )}
          </Link>
        </Button>
      )}
    </div>
  )
}

function ServiceRow({
  stack,
  service,
  managed,
  onRun,
}: {
  stack: string
  service: ComposeService
  managed: boolean
  onRun: (action: string, opts?: { confirmPhrase?: string; service?: string }) => Promise<void>
}) {
  const { can } = useAuth()
  const published = service.ports.filter((p) => p.publicPort)

  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 rounded-lg border border-hairline px-3 py-2.5">
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate text-body font-medium">{service.name}</span>
          {service.missing ? (
            <Status verdict="warning" label="not created" />
          ) : (
            <Status state={service.state} />
          )}
          {service.health && service.health !== "healthy" && (
            <Status
              verdict={service.health === "unhealthy" ? "critical" : "notice"}
              label={service.health}
            />
          )}
        </div>
        <p className="truncate font-mono text-hint text-muted-foreground">
          {service.missing
            ? "declared in the compose file, but no container exists for it"
            : service.image}
        </p>
      </div>

      {published.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {published.map((p, i) => (
            <PortLink key={i} ip={p.ip} port={p.publicPort ?? 0} target={p.privatePort} />
          ))}
        </div>
      )}

      {managed && can("service.control") && !service.missing && (
        <Button
          size="xs"
          variant="ghost"
          onClick={() =>
            onRun("up", { service: service.name }).catch((err) => notify.error(String(err)))
          }
        >
          <RefreshClockwise className="size-3" />
          Re-apply
        </Button>
      )}
      {managed && can("service.control") && service.missing && (
        <Button
          size="xs"
          variant="outline"
          onClick={() =>
            onRun("up", { service: service.name }).catch((err) => notify.error(String(err)))
          }
        >
          <Play className="size-3" />
          Create it
        </Button>
      )}
      <span className="sr-only">{stack}</span>
    </div>
  )
}

/* ---------------------------------------------------------------- editor -- */

function ComposeEditor({
  stack,
  onSaved,
  canWrite,
}: {
  stack: StackDetail
  onSaved: () => void
  canWrite: boolean
}) {
  const [content, setContent] = useState<string>()
  const [original, setOriginal] = useState("")
  const [validation, setValidation] = useState<ComposeValidation>()
  const [busy, setBusy] = useState(false)
  const [fetchError, setFetchError] = useState<string>()
  // "There is no file" is a fact about the stack, not something that happens
  // while loading, so it is derived rather than written into state by the
  // effect below.
  const loadError = stack.configPath
    ? fetchError
    : "This stack has no compose file the dashboard can read."

  useEffect(() => {
    if (!stack.configPath) return
    const controller = new AbortController()
    get<{ path: string; content: string }>(
      `/docker/stacks/${encodeURIComponent(stack.name)}/config`,
      undefined,
      controller.signal,
    )
      .then((res) => {
        setContent(res.content)
        setOriginal(res.content)
      })
      .catch((err) => !controller.signal.aborted && setFetchError(String(err)))
    return () => controller.abort()
  }, [stack.name, stack.configPath])

  const dirty = content !== undefined && content !== original

  const check = async () => {
    setBusy(true)
    try {
      const res = await post<ComposeValidation>(
        `/docker/stacks/${encodeURIComponent(stack.name)}/validate`,
        { content },
      )
      setValidation(res)
      if (res.valid) notify.success(`Valid — ${res.services.length} service(s)`)
    } catch (err) {
      notify.error("Could not check the file", err)
    } finally {
      setBusy(false)
    }
  }

  const save = async (force = false) => {
    setBusy(true)
    try {
      const res = await put<{ validation: ComposeValidation }>(
        `/docker/stacks/${encodeURIComponent(stack.name)}/config`,
        { content, force },
      )
      setOriginal(content ?? "")
      setValidation(res.validation)
      // Saying so explicitly, because this is the one thing an editor in a
      // deployment tool is most likely to be misread about.
      notify.success("Saved", {
        description: "Nothing changed yet — bring the stack up to apply it.",
      })
      onSaved()
    } catch (err) {
      if (err instanceof ApiError && err.code === "compose_invalid") {
        setValidation({ valid: false, error: err.message, services: [] })
        notify.error("Compose rejected this file", err.message)
      } else {
        notify.error("Could not save", err)
      }
    } finally {
      setBusy(false)
    }
  }

  if (loadError) return <ErrorState error={new Error(loadError)} />
  if (content === undefined) return <LoadingRows />

  return (
    <div className="flex h-full min-h-0 flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <Code className="size-3.5 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate font-mono text-hint text-muted-foreground">
          {stack.configPath}
        </span>
        {dirty && <Tag tone="warning">unsaved</Tag>}
        <Button size="xs" variant="outline" onClick={check} disabled={busy}>
          Check
        </Button>
        {canWrite && (
          <Button size="xs" onClick={() => save()} disabled={busy || !dirty} pending={busy}>
            <FloppyDisk className="size-3" />
            Save
          </Button>
        )}
      </div>

      <CodeEditor
        value={content}
        onChange={setContent}
        language="yaml"
        readOnly={!canWrite}
        className="min-h-0 flex-1 overflow-hidden rounded-lg border"
      />

      {validation && !validation.valid && (
        <Notice title="Compose will not accept this" icon={Warning} tone="danger">
          <pre className="mt-1 font-mono text-hint whitespace-pre-wrap">{validation.error}</pre>
          {canWrite && (
            <Button size="xs" variant="outline" className="mt-2" onClick={() => save(true)}>
              Save it anyway
            </Button>
          )}
        </Notice>
      )}
      {validation?.valid && (
        <Hint>
          Valid. Defines {validation.services.join(", ") || "no services"}.{" "}
          <Term name="compose">Saving does not deploy</Term> — bring the stack up to apply it.
        </Hint>
      )}
    </div>
  )
}

/* ------------------------------------------------------------------ logs -- */

/** Every container in the stack, merged into one feed and tagged by service. */
function StackLogs({ stack, active }: { stack: string; active: boolean }) {
  const [lines, setLines] = useState<LogLine[]>([])

  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type !== "logs") return
    const batch = envelope.data as { stream: string; text: string; service?: string }[]
    setLines((prev) => {
      const next = [
        ...prev,
        ...batch.map((l) => ({
          // The service prefix goes into the text rather than a column so the
          // filter box searches it too — "show me only what the database
          // said" is the commonest thing to want from a merged feed.
          text: l.service ? `${l.service} | ${l.text}` : l.text,
          level: l.stream === "stderr" ? "error" : undefined,
        })),
      ]
      return next.length > 5000 ? next.slice(next.length - 5000) : next
    })
  }, [])

  const query = useMemo(() => ({ tail: 200 }), [])
  const { state } = useSocket(`/docker/stacks/${encodeURIComponent(stack)}/logs/stream`, {
    onMessage,
    enabled: active,
    query,
  })

  return (
    <LogViewer
      className="h-full"
      lines={lines}
      showTimestamps={false}
      onClear={() => setLines([])}
      emptyMessage={state === "open" ? "No output yet." : "Connecting…"}
    />
  )
}
