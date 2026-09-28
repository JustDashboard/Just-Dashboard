"use client"

import { useState } from "react"
import { Warning } from "@/components/icons"
import { post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { EngineAction, EngineControlResult, ProxyDiagnostic } from "@/lib/types"
import { Disclosure } from "@/components/form"
import { Modal } from "@/components/modal"
import { Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { ConfigEditor } from "@/components/proxy/config-editor"
import { DiagnosticList } from "@/components/proxy/diagnostic-list"
import {
  openableFile,
  refusalHeadline,
  refusalOf,
  type Refusal,
} from "@/components/proxy/engine-lifecycle"
import type { ProxyStatus } from "@/components/proxy/proxy-context"

/** The verbs that open the dialog: the two that confirm, and a start the test refused. */
type Asked = "start" | "restart" | "stop"

const DONE: Record<EngineAction, (engine: string) => string> = {
  start: (engine) => `${engine} started`,
  restart: (engine) => `${engine} restarted`,
  stop: (engine) => `${engine} stopped`,
  enable: (engine) => `${engine} starts at boot`,
  "reset-failed": (engine) => `${engine}'s failed state cleared`,
}

const FAILED: Record<EngineAction, (engine: string) => string> = {
  start: (engine) => `${engine} did not start`,
  restart: (engine) => `${engine} did not restart`,
  stop: (engine) => `${engine} did not stop`,
  enable: (engine) => `${engine} was not set to start at boot`,
  "reset-failed": (engine) => `${engine}'s failed state was not cleared`,
}

export type EngineControl = {
  /** The engine as its name reads: nginx, or Caddy. */
  engine: string
  /**
   * The verb in flight, or one that landed whose unit has not been read
   * since, so the line does not say "not running" under "nginx started".
   */
  pending: EngineAction | undefined
  run: (action: EngineAction) => void
  /** Restart and stop, which say what they interrupt before they run. */
  ask: (action: "restart" | "stop") => void
  /** The confirmation, a refusal's diagnostics, and the file one of them opens. */
  dialog: React.ReactNode
}

/**
 * The engine's service verbs, through the proxy's own route. A start or
 * restart the config test turns down opens the dialog on nginx's own lines,
 * each at its file and line, rather than a toast of the output: the reason is
 * the thing to act on, and a toast is gone in twelve seconds.
 */
export function useEngineControl({
  status,
  unit,
  onChanged,
}: {
  status: ProxyStatus | undefined
  /** The unit's last read, which ends a landed verb's pending state. */
  unit: { fetchedAt?: number; error?: Error }
  onChanged: () => void
}): EngineControl {
  const engine = status?.nginx ? "nginx" : "Caddy"
  const [busy, setBusy] = useState<EngineAction>()
  const [landed, setLanded] = useState<{ action: EngineAction; at: number }>()
  // What the dialog says, kept while it closes so its words do not change
  // under the fade.
  const [asked, setAsked] = useState<{ action: Asked; refusal?: Refusal }>()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<{ path: string; line?: number } | null>(null)

  const run = async (action: EngineAction) => {
    if (busy) return
    setBusy(action)
    try {
      await post<EngineControlResult>(`/proxy/engine/${action}`)
      setLanded({ action, at: Date.now() })
      setDialogOpen(false)
      notify.success(DONE[action](engine))
      onChanged()
    } catch (err) {
      const refusal = refusalOf(err)
      if (refusal && (action === "start" || action === "restart")) {
        setAsked({ action, refusal })
        setDialogOpen(true)
      } else {
        setDialogOpen(false)
        notify.error(FAILED[action](engine), err)
        // systemctl may have got as far as leaving the unit failed.
        onChanged()
      }
    } finally {
      setBusy(undefined)
    }
  }

  const reread = landed && !unit.error && (unit.fetchedAt ?? 0) <= landed.at
  const pending = busy ?? (reread ? landed.action : undefined)

  const roots = status
    ? status.nginx
      ? [status.nginxDir]
      : [status.caddyFile.replace(/\/[^/]*$/, "")]
    : []
  const open = (d: ProxyDiagnostic) => {
    if (!d.file) return
    setDialogOpen(false)
    setEditing({ path: d.file, line: d.line })
  }

  const dialog = (
    <>
      <EngineDialog
        engine={engine}
        open={dialogOpen}
        asked={asked}
        busy={busy !== undefined}
        roots={roots}
        onOpenFile={open}
        onRun={(action) => void run(action)}
        onClose={() => setDialogOpen(false)}
      />
      <ConfigEditor
        open={editing !== null}
        onOpenChange={(next) => !next && setEditing(null)}
        path={editing?.path ?? ""}
        kind={status?.nginx ? "nginx" : "caddy"}
        title={editing?.path.split("/").pop() ?? "Configuration"}
        initialLine={editing?.line}
      />
    </>
  )

  return {
    engine,
    pending,
    run: (action) => void run(action),
    ask: (action) => {
      setAsked({ action })
      setDialogOpen(true)
    },
    dialog,
  }
}

/**
 * Restart or stop, said before it happens; or a start or restart the config
 * test refused, said after. The command stays at the dialog's foot either way,
 * so a file fixed in another window is one press from being tried again.
 */
function EngineDialog({
  engine,
  open,
  asked,
  busy,
  roots,
  onOpenFile,
  onRun,
  onClose,
}: {
  engine: string
  open: boolean
  asked: { action: Asked; refusal?: Refusal } | undefined
  busy: boolean
  roots: string[]
  onOpenFile: (diagnostic: ProxyDiagnostic) => void
  onRun: (action: Asked) => void
  onClose: () => void
}) {
  const action = asked?.action ?? "restart"
  const refusal = asked?.refusal
  const verb = { start: "Start", restart: "Restart", stop: "Stop" }[action]

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !next && !busy && onClose()}
      title={`${verb} ${engine}`}
      description={
        refusal
          ? `${engine} refused its configuration.`
          : `What ${verb.toLowerCase()}ing ${engine} interrupts.`
      }
      size={refusal ? "lg" : "md"}
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            {refusal ? "Close" : "Cancel"}
          </Button>
          <Button
            variant={action === "start" ? "default" : "destructive"}
            onClick={() => onRun(action)}
            pending={busy}
          >
            {verb}
          </Button>
        </>
      }
    >
      {refusal ? (
        <RefusalView
          engine={engine}
          action={action}
          refusal={refusal}
          roots={roots}
          onOpenFile={onOpenFile}
        />
      ) : action === "stop" ? (
        <p className="text-body leading-relaxed">
          Every site this proxy serves goes offline until it is started again.
        </p>
      ) : (
        <div className="space-y-2 text-body leading-relaxed">
          <p>
            Every connection is dropped and every site is unreachable until the process is back. A
            reload applies configuration changes without either.
          </p>
          <p>
            The configuration is tested first. If {engine} refuses it, nothing is restarted and the
            reason is shown.
          </p>
        </div>
      )}
    </Modal>
  )
}

/**
 * What the test said, a line per diagnostic with its file one press away, and
 * nginx's whole output folded under them. Output the parser found nothing in
 * is shown as it is.
 */
function RefusalView({
  engine,
  action,
  refusal,
  roots,
  onOpenFile,
}: {
  engine: string
  action: Asked
  refusal: Refusal
  roots: string[]
  onOpenFile: (diagnostic: ProxyDiagnostic) => void
}) {
  const diagnostics = refusal.validation?.diagnostics ?? []
  const output = refusal.validation?.output ?? refusal.message.split("\n").slice(1).join("\n")
  return (
    <div className="space-y-4">
      <Notice tone="danger" icon={Warning} title={refusalHeadline(refusal)}>
        {action === "restart"
          ? `Nothing ${engine} serves was interrupted. Fix what the test found, then restart again.`
          : `Fix what the test found, then start ${engine} again.`}
      </Notice>
      {diagnostics.length > 0 ? (
        <>
          <DiagnosticList
            diagnostics={diagnostics}
            canOpen={(file) => openableFile(file, roots)}
            onOpen={onOpenFile}
          />
          {output && (
            <Disclosure quiet summary={`${refusal.validation?.command ?? "Test"} output`}>
              <Well className="max-h-64 break-words whitespace-pre-wrap">{output}</Well>
            </Disclosure>
          )}
        </>
      ) : (
        output && <Well className="max-h-64 break-words whitespace-pre-wrap">{output}</Well>
      )}
    </div>
  )
}
