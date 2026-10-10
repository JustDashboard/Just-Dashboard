"use client"

import { useEffect, useMemo, useState } from "react"
import {
  ArrowRight,
  CheckCircle,
  ClockRewind,
  CodeWrap,
  FloppyDisk,
  MagnifyingGlass,
  Play,
  Warning,
} from "@/components/icons"
import { ApiError, get, post, put } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useMemoryState } from "@/lib/view-state"
import type { ComposeValidation, StackDetail } from "@/lib/types"
import { CodeEditor, type EditorCommands } from "@/components/code-editor"
import { IconAction } from "@/components/icon-action"
import { Pane, PaneFooter, PaneHeader } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { ComposeDiff, DiffCount, ServiceLabel } from "@/components/docker/stack-diff"
import { bucketTone, type ServiceReading } from "@/components/docker/stack-service-readings"
import {
  composeOutline,
  diffCounts,
  errorLine,
  lineDiff,
  outlineAt,
  type OutlineSection,
} from "@/components/docker/stack-views"

/**
 * An edit that has not been saved: kept for as long as the tab is open, so
 * looking at Deploy preview and coming back does not throw it away — and
 * only in memory, because a compose file holds whatever passwords were
 * written into it.
 */
export type ComposeDraft = { content: string; from?: string }

export function useComposeDraft(stack: string) {
  return useMemoryState<ComposeDraft | null>(`docker.stack.${stack}.draft`, null)
}

/**
 * The stack's compose file, as the place it is changed.
 *
 * It was an editor under a path and two buttons. Now it is the file with an
 * outline beside it — the services drawn as they are drawn everywhere on
 * the page, each with its live state, a press away from its line — a state
 * that says whether what is on screen is what is on disk and whether compose
 * would take it, and a status line that says where the cursor is in the
 * stack's terms ("services · api"), not only in lines.
 *
 * An edit can be read as a diff against the file on disk before it is
 * saved, and saving says what it did not do: the stack still runs the old
 * file until it is deployed, so the two ways on are right there.
 */
export function StackCompose({
  stack,
  onSaved,
  canWrite,
  canValidate,
  readings,
  productOf,
  onPreview,
  onDeploy,
  busy,
}: {
  stack: StackDetail
  onSaved: () => void
  canWrite: boolean
  canValidate: boolean
  readings: ServiceReading[]
  productOf: (service: string) => string | undefined
  onPreview: () => void
  onDeploy?: () => void
  busy: boolean
}) {
  const [original, setOriginal] = useState<string>()
  const [fetchError, setFetchError] = useState<string>()
  const [draft, setDraft] = useComposeDraft(stack.name)
  const [validation, setValidation] = useState<ComposeValidation>()
  const [working, setWorking] = useState(false)
  const [saved, setSaved] = useState(false)
  // A version handed over from History opens on its differences.
  const [review, setReview] = useState(() => Boolean(draft?.from))
  const [wrap, setWrap] = useState(false)
  const [commands, setCommands] = useState<EditorCommands>()
  const [cursor, setCursor] = useState({ line: 1, column: 1, selected: 0 })
  const [reveal, setReveal] = useState<{ line: number; again: number }>()
  // "There is no file" is a fact about the stack, not something that happens
  // while loading, so it is derived rather than written into state below.
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
      .then((res) => setOriginal(res.content))
      .catch((err) => !controller.signal.aborted && setFetchError(String(err)))
    return () => controller.abort()
  }, [stack.name, stack.configPath])

  const content = draft?.content ?? original
  const dirty = draft !== null && original !== undefined && draft.content !== original
  const outline = useMemo(() => composeOutline(content ?? ""), [content])
  const at = outlineAt(outline, cursor.line)
  const changes = useMemo(
    () =>
      dirty && original !== undefined && content !== undefined ? lineDiff(original, content) : [],
    [dirty, original, content],
  )
  const counts = diffCounts(changes)
  const badLine = validation && !validation.valid ? errorLine(validation.error) : undefined

  const edit = (next: string) => {
    setSaved(false)
    setValidation(undefined)
    setDraft(next === original ? null : { content: next, from: draft?.from })
  }
  const jump = (line: number) => {
    setReview(false)
    setReveal((prev) => ({ line, again: (prev?.again ?? 0) + 1 }))
  }

  const check = async () => {
    setWorking(true)
    try {
      const res = await post<ComposeValidation>(
        `/docker/stacks/${encodeURIComponent(stack.name)}/validate`,
        { content },
      )
      setValidation(res)
      const line = res.valid ? undefined : errorLine(res.error)
      if (line) jump(line)
    } catch (err) {
      notify.error("Could not check the file", err)
    } finally {
      setWorking(false)
    }
  }

  const save = async (force = false) => {
    if (!dirty || content === undefined) return
    setWorking(true)
    try {
      const res = await put<{ validation: ComposeValidation }>(
        `/docker/stacks/${encodeURIComponent(stack.name)}/config`,
        { content, force },
      )
      setOriginal(content)
      setDraft(null)
      setValidation(res.validation)
      setReview(false)
      setSaved(true)
      onSaved()
    } catch (err) {
      if (err instanceof ApiError && err.code === "compose_invalid") {
        setValidation({ valid: false, error: err.message, services: [] })
        const line = errorLine(err.message)
        if (line) jump(line)
      } else {
        notify.error("Could not save", err)
      }
    } finally {
      setWorking(false)
    }
  }

  if (loadError) return <ErrorState error={new Error(loadError)} />
  if (content === undefined) return <LoadingRows />

  const lines = content.split("\n").length
  const path = stack.configPath ?? ""
  const cut = path.lastIndexOf("/")

  return (
    <div className="flex h-full min-h-0 min-w-0 animate-rise flex-col gap-3 pb-4">
      {draft?.from && dirty && (
        <Notice title={`This is the file from ${draft.from}`} icon={ClockRewind}>
          <p className="text-hint leading-relaxed text-muted-foreground">
            Nothing is written until you save it, and the stack keeps running what it runs until it
            is deployed again.
          </p>
          <span className="mt-2 flex flex-wrap gap-1.5">
            <Button size="xs" variant="outline" onClick={() => setDraft(null)}>
              Back to the file on disk
            </Button>
          </span>
        </Notice>
      )}

      <Pane className="min-h-[28rem] flex-1">
        <PaneHeader className="flex-wrap gap-x-3 gap-y-1.5 px-3 py-2">
          <span className="flex min-w-0 flex-1 items-center gap-2">
            <ProductGlyph id="docker-compose" className="size-4" />
            <span className="min-w-0 truncate font-mono text-hint" title={path}>
              <span className="text-muted-foreground">{path.slice(0, cut + 1)}</span>
              <span className="font-medium text-foreground">{path.slice(cut + 1)}</span>
            </span>
            <FileState dirty={dirty} validation={validation} checking={working && !dirty} />
          </span>
          <span className="flex shrink-0 items-center gap-0.5">
            <IconAction
              label="Find"
              disabled={!commands || review}
              onClick={() => commands?.run("actions.find")}
            >
              <MagnifyingGlass />
            </IconAction>
            <IconAction
              label="Wrap long lines"
              aria-pressed={wrap}
              onClick={() => setWrap((v) => !v)}
              className={cn(wrap && "bg-accent text-accent-foreground")}
            >
              <CodeWrap />
            </IconAction>
            {dirty && (
              <Button
                size="xs"
                variant={review ? "secondary" : "ghost"}
                aria-pressed={review}
                onClick={() => setReview((v) => !v)}
                className="ml-1"
              >
                Review changes
                <DiffCount added={counts.added} removed={counts.removed} />
              </Button>
            )}
            {canValidate && (
              <Button
                size="xs"
                variant="outline"
                onClick={check}
                disabled={working}
                className="ml-1"
              >
                Check
              </Button>
            )}
            {canWrite && (
              <Button
                size="xs"
                onClick={() => save()}
                disabled={working || !dirty}
                pending={working && dirty}
                className="ml-1"
              >
                <FloppyDisk className="size-3" />
                Save
              </Button>
            )}
          </span>
        </PaneHeader>

        <div className="flex min-h-0 flex-1">
          <Outline
            outline={outline}
            at={at}
            readings={readings}
            productOf={productOf}
            onJump={jump}
          />
          <div className="relative min-h-0 min-w-0 flex-1">
            <CodeEditor
              value={content}
              onChange={edit}
              language="yaml"
              readOnly={!canWrite}
              wordWrap={wrap}
              filePath={path}
              onSave={canWrite ? () => void save() : undefined}
              onCursorChange={setCursor}
              onReady={setCommands}
              revealLine={reveal?.line}
              revealAgain={reveal?.again}
              className={cn("h-full", review && "invisible")}
            />
            {review && (
              <div className="absolute inset-0 overflow-y-auto bg-card p-4">
                <p className="mb-3 text-hint text-muted-foreground">
                  The file on disk, against what is in the editor.
                </p>
                <ComposeDiff lines={changes} productOf={productOf} />
              </div>
            )}
          </div>
        </div>

        <PaneFooter className="justify-between gap-x-4 px-3 text-hint text-muted-foreground">
          <span className="numeric min-w-0 truncate">
            Ln {cursor.line}, Col {cursor.column}
            {cursor.selected > 0 && ` · ${cursor.selected} selected`} · {plural(lines, "line")} ·{" "}
            {bytes(new TextEncoder().encode(content).length)} · YAML
          </span>
          {at.section && (
            <span className="min-w-0 truncate">
              {at.section}
              {at.child && <span className="text-foreground"> · {at.child}</span>}
            </span>
          )}
        </PaneFooter>
      </Pane>

      {validation && !validation.valid && (
        <Notice title="Compose will not accept this" icon={Warning} tone="danger">
          <pre className="mt-1 font-mono text-hint whitespace-pre-wrap">{validation.error}</pre>
          <span className="mt-2 flex flex-wrap gap-1.5">
            {badLine && (
              <Button size="xs" variant="outline" onClick={() => jump(badLine)}>
                Go to line {badLine}
              </Button>
            )}
            {canWrite && dirty && (
              <Button size="xs" variant="outline" onClick={() => save(true)}>
                Save it anyway
              </Button>
            )}
          </span>
        </Notice>
      )}
      {saved && !dirty && (
        /* Saying so explicitly, because this is the one thing an editor in a
           deployment tool is most likely to be misread about. */
        <Notice title="Saved. Nothing is running it yet" icon={CheckCircle} tone="success">
          <p className="text-hint leading-relaxed text-muted-foreground">
            The stack keeps running the file it was deployed with until it is deployed again.
          </p>
          <span className="mt-2 flex flex-wrap gap-1.5">
            <Button size="xs" variant="outline" onClick={onPreview}>
              See what Deploy will do
              <ArrowRight className="size-3" />
            </Button>
            {onDeploy && (
              <Button size="xs" onClick={onDeploy} pending={busy}>
                <Play className="size-3" />
                Deploy
              </Button>
            )}
          </span>
        </Notice>
      )}
    </div>
  )
}

/** Whether what is on screen is what is on disk, and whether compose would take it. */
function FileState({
  dirty,
  validation,
  checking,
}: {
  dirty: boolean
  validation?: ComposeValidation
  checking: boolean
}) {
  if (validation && !validation.valid) {
    return <Status tone="danger" label="Compose rejects this" className="shrink-0" />
  }
  if (dirty) return <Status tone="warning" label="Unsaved changes" className="shrink-0" />
  if (checking) return <Status tone="unknown" label="Checking…" className="shrink-0" />
  if (validation?.valid) {
    return (
      <Status
        tone="running"
        label={`Valid · ${plural(validation.services.length, "service")}`}
        className="shrink-0"
      />
    )
  }
  return <Status tone="stopped" label="Saved" className="shrink-0" />
}

/**
 * The file's sections and what is under each, a press from its line. A
 * service is drawn as it is in the table, with the state it is in now, so
 * the outline is also a reading of which part of the file is failing.
 */
function Outline({
  outline,
  at,
  readings,
  productOf,
  onJump,
}: {
  outline: OutlineSection[]
  at: { section?: string; child?: string }
  readings: ServiceReading[]
  productOf: (service: string) => string | undefined
  onJump: (line: number) => void
}) {
  const row =
    "flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1 text-left focus-ring-inset transition-colors hover:bg-row-hover"
  return (
    <nav
      aria-label="Outline"
      className="hidden w-60 shrink-0 overflow-y-auto border-r border-hairline p-2 lg:block"
    >
      {outline.map((section) => (
        <div key={section.key} className="mb-2 last:mb-0">
          <button
            type="button"
            onClick={() => onJump(section.line)}
            className={cn(
              row,
              "eyebrow py-1.5",
              at.section === section.key && !at.child && "bg-accent text-foreground",
            )}
          >
            <span className="min-w-0 flex-1 truncate">{section.key}</span>
            <span className="numeric font-normal tracking-normal text-muted-foreground/60 normal-case">
              {section.line}
            </span>
          </button>
          {section.children.length > 0 && (
            <ul>
              {section.children.map((child) => {
                const here = at.section === section.key && at.child === child.key
                const reading =
                  section.key === "services" ? readings.find((r) => r.key === child.key) : undefined
                return (
                  <li key={child.key}>
                    <button
                      type="button"
                      onClick={() => onJump(child.line)}
                      aria-current={here || undefined}
                      className={cn(row, here && "bg-accent")}
                    >
                      {section.key === "services" ? (
                        <ServiceLabel
                          name={child.key}
                          product={productOf(child.key)}
                          muted={reading?.bucket === "missing"}
                          className="flex-1"
                        />
                      ) : (
                        <span className="min-w-0 flex-1 truncate font-mono text-hint">
                          {child.key}
                        </span>
                      )}
                      {reading && (
                        <StatusDot
                          tone={bucketTone(reading.bucket)}
                          live={reading.state === "running"}
                          className="shrink-0"
                        />
                      )}
                      <span className="numeric w-6 shrink-0 text-right text-hint text-muted-foreground/60">
                        {child.line}
                      </span>
                    </button>
                  </li>
                )
              })}
            </ul>
          )}
        </div>
      ))}
    </nav>
  )
}
