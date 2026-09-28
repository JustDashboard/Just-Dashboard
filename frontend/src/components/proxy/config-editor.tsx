"use client"

import { useEffect, useState, type ReactNode } from "react"
import { CheckCircle, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError, errorMessage, get, post, put } from "@/lib/api"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { ProxyValidation, VHost } from "@/lib/types"
import { CodeEditor } from "@/components/code-editor"
import { DiffView } from "@/components/files/diff-view"
import { unifiedDiff } from "@/components/files/diff"
import { Disclosure } from "@/components/form"
import { Modal } from "@/components/modal"
import { Pane, Well } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { DiagnosticList } from "@/components/proxy/diagnostic-list"
import { diffStat } from "@/components/proxy/config-tree"
import { refusalOf } from "@/components/proxy/engine-lifecycle"
import { warningCount } from "@/components/proxy/config-test"

type ConfigEditorProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The file on the host, read and written through /proxy/config. */
  path: string
  kind: VHost["kind"]
  title: string
  readOnly?: boolean
  /** A line to reveal and mark once the file has loaded: where a config test points. */
  initialLine?: number
  /**
   * Commands for what the file belongs to, drawn beside its path. Given
   * whether a save is in flight, so a verb that would race it can wait.
   */
  actions?: (busy: boolean) => ReactNode
  /** The file is a disabled site's, which nginx does not read, so a test passes whatever it says. */
  siteDisabled?: boolean
  /** Told whether nginx was reloaded with the save. */
  onSaved?: (reloaded: boolean) => void
}

/**
 * The file itself, for a site the form does not own — and for the operator
 * who would rather see the nginx than the form. Every opening starts over:
 * the file is read afresh and nothing typed before a close survives it, and
 * opening another file never inherits the previous one's buffer. Saving a
 * stale buffer over the file on disk, or over the wrong file, would be a real
 * outage. `open` and `path` are independent, so a caller may keep the path
 * while the editor is closed.
 *
 * Until the read answers there is no buffer at all: an editor drawn empty
 * while loading, or after a read that failed, read as an empty site file,
 * and a save from it would have written that emptiness over the real one.
 */
export function ConfigEditor(props: ConfigEditorProps) {
  return <ConfigEditorBody key={props.open ? props.path : ""} {...props} />
}

/** A test's result as the editor shows it: Test config's, or the one that refused a save. */
type Shown = { from: "test" | "save"; validation: ProxyValidation }

/**
 * Saving is two steps. Save only and Save and reload show what the save
 * changes, as a diff against the file as it was read, and the same command
 * under it writes it. nginx's own test still decides; the diff is the
 * reader's look at what they are about to put in front of it, on a file
 * that keeps every site up. Closing with changes, by any of the ways out,
 * asks first, and so does the tab.
 */
function ConfigEditorBody({
  open,
  onOpenChange,
  path,
  kind,
  title,
  readOnly = false,
  initialLine,
  actions,
  siteDisabled = false,
  onSaved,
}: ConfigEditorProps) {
  const [content, setContent] = useState("")
  const [original, setOriginal] = useState("")
  const [busy, setBusy] = useState<"test" | "save">()
  const [shown, setShown] = useState<Shown | null>(null)
  // Whether the file has been read: undefined while the read is out.
  const [read, setRead] = useState<{ ok: true } | { ok: false; error: Error }>()
  const [attempt, setAttempt] = useState(0)
  // The save waiting on its diff, by the command that asked for it.
  const [review, setReview] = useState<{ reload: boolean } | null>(null)
  // A way out that would throw the changes away, waiting on the reader.
  const [leaving, setLeaving] = useState<"close" | "discard" | null>(null)
  // A line of this file a refusal or a test named, and how often it was asked for.
  const [goTo, setGoTo] = useState<{ line: number; asked: number }>()

  useEffect(() => {
    if (!open || !path) return
    const controller = new AbortController()
    get<{ content: string }>("/proxy/config", { path }, controller.signal)
      .then((r) => {
        setContent(r.content)
        setOriginal(r.content)
        setRead({ ok: true })
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return
        setRead({ ok: false, error: err instanceof Error ? err : new Error(String(err)) })
      })
    return () => controller.abort()
  }, [open, path, attempt])

  const loaded = read?.ok === true
  const failed = read?.ok === false ? read.error : undefined
  const dirty = content !== original
  const name = path.split("/").pop() || path

  // A tab closed or reloaded over unsaved changes loses them as surely as the
  // panel's own close did, so the browser asks as well.
  useEffect(() => {
    if (!open || !dirty) return
    const warn = (event: BeforeUnloadEvent) => event.preventDefault()
    window.addEventListener("beforeunload", warn)
    return () => window.removeEventListener("beforeunload", warn)
  }, [open, dirty])

  const readAgain = () => {
    setRead(undefined)
    setAttempt((n) => n + 1)
  }

  const validate = async () => {
    setBusy("test")
    try {
      setShown({
        from: "test",
        validation: await post<ProxyValidation>("/proxy/validate", { kind, path, content }),
      })
    } catch (err) {
      notify.error("The config test did not run", err)
    } finally {
      setBusy(undefined)
    }
  }

  const save = async (reload: boolean) => {
    setBusy("save")
    try {
      const res = await put<{ validation?: ProxyValidation }>("/proxy/config", {
        kind,
        path,
        content,
        reload,
      })
      setOriginal(content)
      setReview(null)
      setShown(null)
      // Saved and tested, but nginx never reads the file, so neither the
      // test nor a reload says anything about it.
      if (res.validation?.note) {
        notify.warning("Saved, but nginx does not read this file", {
          description: res.validation.note,
        })
      } else {
        notify.success(reload ? "Saved and reloaded" : "Saved")
      }
      onSaved?.(reload)
    } catch (err) {
      const refusal = refusalOf(err)
      if (refusal) {
        // Nothing was written. The refusal is drawn under the editor with
        // each of nginx's lines, which a toast would cut short.
        setReview(null)
        setShown({
          from: "save",
          validation: refusal.validation ?? {
            valid: false,
            output: refusal.message,
            command: kind === "caddy" ? "caddy validate" : "nginx -t",
            diagnostics: [],
          },
        })
      } else if (err instanceof ApiError && err.code === "reload_failed") {
        // The file passed the test and was written before the reload was
        // tried; a reload that failed — an engine that is stopped, say —
        // leaves it saved, which "Not applied" and a buffer still marked
        // unsaved hid.
        notify.warning("Saved, not reloaded", { description: errorMessage(err) })
        setOriginal(content)
        setReview(null)
        onSaved?.(false)
      } else {
        notify.error("Not applied", err)
      }
    } finally {
      setBusy(undefined)
    }
  }

  // The second Ctrl+S, with the diff on screen, writes it. Monaco is not
  // mounted there to take the keystroke from the browser's own save.
  useEffect(() => {
    if (!review) return
    const onKey = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") {
        event.preventDefault()
        if (!busy && !leaving) void save(review.reload)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  })

  const requestClose = (next: boolean) => {
    if (busy) return
    if (next || !dirty) onOpenChange(next)
    else setLeaving("close")
  }

  const editable = !readOnly && open && !failed
  const unread = siteDisabled && !shown?.validation.note && !failed
  const diff = review ? unifiedDiff(original, content, name) : undefined
  const stat = diff ? diffStat(diff) : undefined
  const saving = busy === "save"

  return (
    <>
      <SidePanel
        open={open}
        onOpenChange={requestClose}
        width="xl"
        title={
          <>
            {title}
            {dirty && <Tag tone="warning">unsaved</Tag>}
          </>
        }
        description={path}
        actions={
          open && (
            <>
              <span className="truncate font-mono text-hint text-muted-foreground">{path}</span>
              {actions?.(busy !== undefined)}
            </>
          )
        }
        bodyClassName="flex min-h-0 flex-1 flex-col gap-3 p-4"
        footer={
          !editable ? undefined : review ? (
            <>
              <Button size="sm" variant="outline" onClick={() => setReview(null)} disabled={saving}>
                Back to editing
              </Button>
              <span className="flex-1" />
              <Button size="sm" onClick={() => void save(review.reload)} pending={saving}>
                {saving
                  ? review.reload
                    ? "Saving and reloading…"
                    : "Saving…"
                  : review.reload
                    ? "Save and reload"
                    : "Save only"}
              </Button>
            </>
          ) : (
            <>
              <Button
                size="sm"
                variant="outline"
                onClick={validate}
                pending={busy === "test"}
                disabled={!loaded || busy !== undefined}
              >
                {busy === "test" ? "Testing…" : "Test config"}
              </Button>
              <span className="flex-1" />
              <Button
                size="sm"
                variant="outline"
                onClick={() => setLeaving("discard")}
                disabled={busy !== undefined || !dirty}
              >
                Discard
              </Button>
              <Button
                size="sm"
                variant="outline"
                onClick={() => setReview({ reload: false })}
                disabled={busy !== undefined || !dirty}
              >
                Save only
              </Button>
              <Button
                size="sm"
                onClick={() => setReview({ reload: true })}
                disabled={busy !== undefined || !dirty}
              >
                Save and reload
              </Button>
            </>
          )
        }
      >
        {unread && (
          <Notice tone="warning" icon={Warning} title="nginx is not reading this file">
            The site is disabled, so a config test cannot see it — it will pass whatever is in it.
            Enable the site before trusting the result.
          </Notice>
        )}

        {failed ? (
          <ErrorState error={failed} onRetry={readAgain} />
        ) : (
          <>
            {review && (
              <div className="flex min-h-0 flex-1 flex-col gap-3">
                <p className="text-hint text-muted-foreground">
                  {stat && (stat.added > 0 || stat.removed > 0) && (
                    <>
                      <span className="numeric font-medium text-success">+{stat.added}</span>{" "}
                      <span className="numeric font-medium text-destructive">−{stat.removed}</span>{" "}
                      in <span className="font-mono">{name}</span>.{" "}
                    </>
                  )}
                  {kind === "caddy" ? "Caddy" : "nginx"} tests the file before it is written, and a
                  file it refuses is not saved. Ctrl+S again saves.
                </p>
                {diff === null ? (
                  <Notice title="Too many changes to show">
                    The buffer differs from the file in more places than can be lined up here.
                    Saving writes the whole buffer; Back to editing returns to it.
                  </Notice>
                ) : diff === "" ? (
                  <Notice title="Only the end of the file changes">
                    The last line gains or loses its line feed; every line reads as it did.
                  </Notice>
                ) : (
                  <Pane className="min-h-0 flex-1 overflow-hidden">
                    <DiffView body={diff ?? ""} singleFile lineNumbers className="h-full" />
                  </Pane>
                )}
              </div>
            )}
            {/* Hidden rather than unmounted while the diff is up, so Back to
                editing returns to the same editor: its undo history, its
                cursor and where it was scrolled. */}
            <Pane
              className={cn("min-h-0 flex-1", review && "hidden")}
              aria-busy={!loaded || undefined}
            >
              {loaded ? (
                <CodeEditor
                  className="h-full"
                  language="ini"
                  value={content}
                  readOnly={readOnly}
                  revealLine={goTo?.line ?? initialLine}
                  revealAgain={goTo?.asked}
                  onSave={() => {
                    if (!readOnly && dirty && !busy) setReview({ reload: false })
                  }}
                  onChange={(v) => {
                    setContent(v)
                    setShown(null)
                  }}
                />
              ) : (
                <LoadingRows rows={8} className="p-4" />
              )}
            </Pane>
          </>
        )}

        {shown && !review && (
          <TestShown
            shown={shown}
            path={path}
            onGoTo={(line) => setGoTo((was) => ({ line, asked: (was?.asked ?? 0) + 1 }))}
          />
        )}
      </SidePanel>

      <Modal
        open={leaving !== null}
        onOpenChange={(next) => !next && setLeaving(null)}
        size="sm"
        title={leaving === "discard" ? "Discard changes?" : "Close without saving?"}
        description={`What was typed in ${name} is thrown away.`}
        footer={
          <>
            <Button variant="outline" onClick={() => setLeaving(null)}>
              Keep editing
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                const was = leaving
                setLeaving(null)
                if (was === "close") {
                  onOpenChange(false)
                } else {
                  setContent(original)
                  setShown(null)
                }
              }}
            >
              {leaving === "discard" ? "Discard" : "Close and discard"}
            </Button>
          </>
        }
      >
        <p className="text-body leading-relaxed">
          What you typed in <span className="font-mono">{name}</span> is thrown away, and the file
          stays as it is on disk.
        </p>
      </Modal>
    </>
  )
}

/**
 * What a test of the buffer said, or why a save was refused: the verdict, a
 * row for each of the engine's lines — a line in this file one press from
 * the editor — and the whole output folded under them.
 */
function TestShown({
  shown,
  path,
  onGoTo,
}: {
  shown: Shown
  path: string
  onGoTo: (line: number) => void
}) {
  const { validation, from } = shown
  const warnings = warningCount(validation)
  const diagnostics = validation.diagnostics ?? []
  const title = !validation.valid
    ? from === "save"
      ? "Not saved: the config test refuses it"
      : "Config is refused"
    : warnings > 0
      ? `Valid with ${plural(warnings, "warning")}`
      : "Config is valid"
  const tone = !validation.valid ? "danger" : warnings > 0 ? "warning" : "success"
  return (
    <div className="max-h-[45%] shrink-0 space-y-3 overflow-y-auto">
      <Notice tone={tone} icon={tone === "success" ? CheckCircle : Warning} title={title}>
        {validation.note && <p>{validation.note}</p>}
      </Notice>
      {diagnostics.length > 0 && (
        <DiagnosticList
          diagnostics={diagnostics}
          canOpen={(file, line) => file === path && line !== undefined}
          openLabel={(line) => `Go to line ${line}`}
          onOpen={(place) => place.line && onGoTo(place.line)}
        />
      )}
      {validation.output &&
        (diagnostics.length > 0 ? (
          <Disclosure quiet summary={`${validation.command || "Test"} output`}>
            <Well className="max-h-40 break-words whitespace-pre-wrap">{validation.output}</Well>
          </Disclosure>
        ) : (
          <Well className="max-h-40 break-words whitespace-pre-wrap">{validation.output}</Well>
        ))}
    </div>
  )
}
