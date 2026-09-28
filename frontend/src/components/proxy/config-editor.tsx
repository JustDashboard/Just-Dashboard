"use client"

import { useEffect, useState, type ReactNode } from "react"
import { ShieldCheck, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError, errorMessage, get, post, put } from "@/lib/api"
import type { ProxyValidation, VHost } from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { CodeEditor } from "@/components/code-editor"
import { Pane, Well } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"

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
  onSaved?: () => void
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
  const { confirm, dialog } = useConfirm()
  const [content, setContent] = useState("")
  const [original, setOriginal] = useState("")
  const [busy, setBusy] = useState(false)
  const [validation, setValidation] = useState<ProxyValidation | null>(null)
  // Whether the file has been read: undefined while the read is out.
  const [read, setRead] = useState<{ ok: true } | { ok: false; error: Error }>()
  const [attempt, setAttempt] = useState(0)

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

  const readAgain = () => {
    setRead(undefined)
    setAttempt((n) => n + 1)
  }

  const validate = async () => {
    setBusy(true)
    try {
      setValidation(await post<ProxyValidation>("/proxy/validate", { kind, path, content }))
    } catch (err) {
      notify.error("Validation failed", err)
    } finally {
      setBusy(false)
    }
  }

  const save = async (reload: boolean) => {
    setBusy(true)
    try {
      await put("/proxy/config", { kind, path, content, reload })
      notify.success(reload ? "Saved and reloaded" : "Saved")
      setOriginal(content)
      onSaved?.()
    } catch (err) {
      // The file passed the test and was written before the reload was
      // tried; a reload that failed — an engine that is stopped, say — leaves
      // it saved, which "Not applied" and a buffer still marked unsaved hid.
      if (err instanceof ApiError && err.code === "reload_failed") {
        notify.warning("Saved, not reloaded", { description: errorMessage(err) })
        setOriginal(content)
        onSaved?.()
      } else {
        notify.error("Not applied", err)
      }
    } finally {
      setBusy(false)
    }
  }

  const loaded = read?.ok === true
  const failed = read?.ok === false ? read.error : undefined
  const dirty = content !== original
  const unread = siteDisabled && !validation?.note && !failed

  return (
    <>
      <SidePanel
        open={open}
        onOpenChange={(o) => !busy && onOpenChange(o)}
        width="xl"
        title={title}
        description={path}
        actions={
          open && (
            <>
              <span className="truncate font-mono text-hint text-muted-foreground">{path}</span>
              {actions?.(busy)}
            </>
          )
        }
        bodyClassName="flex min-h-0 flex-1 flex-col gap-3 p-4"
        footer={
          !readOnly && open && !failed ? (
            <>
              <Button
                size="sm"
                variant="outline"
                onClick={validate}
                pending={busy}
                disabled={!loaded}
              >
                Test config
              </Button>
              <span className="flex-1" />
              <Button
                size="sm"
                variant="outline"
                onClick={() =>
                  dirty &&
                  confirm({
                    title: "Discard changes",
                    confirmLabel: "Discard",
                    description: (
                      <p>What you typed here is thrown away and the file is left as it is.</p>
                    ),
                    action: async () => {
                      setContent(original)
                      setValidation(null)
                    },
                  })
                }
                disabled={busy || !dirty}
              >
                Discard
              </Button>
              <Button
                size="sm"
                variant="outline"
                onClick={() => save(false)}
                disabled={busy || !dirty}
              >
                Save only
              </Button>
              <Button size="sm" onClick={() => save(true)} disabled={busy || !dirty}>
                Save and reload
              </Button>
            </>
          ) : undefined
        }
      >
        {!readOnly && !failed && (
          <Notice icon={ShieldCheck} title="Validated before it takes effect">
            The server runs its own config test first. A config that fails is rolled back and never
            reloaded, so a typo here cannot take your sites offline.
          </Notice>
        )}
        {unread && (
          <Notice tone="warning" icon={Warning} title="nginx is not reading this file">
            The site is disabled, so a config test cannot see it — it will pass whatever is in it.
            Enable the site before trusting the result.
          </Notice>
        )}

        {failed ? (
          <ErrorState error={failed} onRetry={readAgain} />
        ) : (
          <Pane className="min-h-0 flex-1" aria-busy={!loaded || undefined}>
            {loaded ? (
              <CodeEditor
                className="h-full"
                language="ini"
                value={content}
                readOnly={readOnly}
                revealLine={initialLine}
                onChange={(v) => {
                  setContent(v)
                  setValidation(null)
                }}
              />
            ) : (
              <LoadingRows rows={8} className="p-4" />
            )}
          </Pane>
        )}

        {validation && (
          <Notice
            tone={validation.valid ? "success" : "danger"}
            title={validation.valid ? "Config is valid" : "Config is refused"}
          >
            {validation.note && <p>{validation.note}</p>}
            {validation.output && (
              <Well className="mt-2 max-h-40 whitespace-pre-wrap">{validation.output}</Well>
            )}
          </Notice>
        )}
      </SidePanel>
      {dialog}
    </>
  )
}
