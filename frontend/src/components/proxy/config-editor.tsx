"use client"

import { useEffect, useState, type ReactNode } from "react"
import { ShieldCheck, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post, put } from "@/lib/api"
import type { ProxyValidation, VHost } from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { CodeEditor } from "@/components/code-editor"
import { Pane, Well } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { Notice } from "@/components/state"
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
 * who would rather see the nginx than the form. Keyed on the file so opening
 * another one never inherits the previous one's buffer; saving that to the
 * wrong path would be a real outage.
 */
export function ConfigEditor(props: ConfigEditorProps) {
  return <ConfigEditorBody key={props.path} {...props} />
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

  useEffect(() => {
    if (!path) return
    const controller = new AbortController()
    get<{ content: string }>("/proxy/config", { path }, controller.signal)
      .then((r) => {
        setContent(r.content)
        setOriginal(r.content)
      })
      .catch((err) => !controller.signal.aborted && notify.error("Could not read the file", err))
    return () => controller.abort()
  }, [path])

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
      notify.error("Not applied", err)
    } finally {
      setBusy(false)
    }
  }

  const dirty = content !== original
  const unread = siteDisabled && !validation?.note

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
          !readOnly && open ? (
            <>
              <Button size="sm" variant="outline" onClick={validate} pending={busy}>
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
        {!readOnly && (
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

        <Pane className="min-h-0 flex-1">
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
        </Pane>

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
