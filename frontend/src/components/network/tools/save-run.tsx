"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { Field } from "@/components/form"
import { Modal } from "@/components/modal"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { post } from "@/lib/api"
import {
  diagnosticNameProblem,
  type DiagnosticRequest,
  type DiagnosticRun,
} from "@/lib/network-diagnostics"

/** Save an explicit new run from the draft; opening the dialog emits no probe. */
export function SaveDiagnosticRun({
  request,
  label,
  disabled,
}: {
  request: DiagnosticRequest
  label: string
  disabled: boolean
}) {
  const router = useRouter()
  const [draft, setDraft] = useState<DiagnosticRequest>()
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Error>()
  const problem = diagnosticNameProblem(name)
  const run = async () => {
    if (!draft || problem || busy) return
    setBusy(true)
    setError(undefined)
    try {
      const saved = await post<DiagnosticRun>("/network/diagnostics/", {
        name: name.trim(),
        request: draft,
      })
      router.push(`/network/runs?run=${encodeURIComponent(saved.id)}`)
      setDraft(undefined)
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <Button
        variant="outline"
        disabled={disabled}
        onClick={() => {
          setName(`${label} · ${request.target || "this host"}`)
          setDraft({ ...request })
          setError(undefined)
        }}
      >
        Run and save
      </Button>
      {draft && (
        <Modal
          open
          title="Save a diagnostic run"
          onOpenChange={(open) => !open && !busy && setDraft(undefined)}
          footer={
            <>
              <Button variant="outline" onClick={() => setDraft(undefined)} disabled={busy}>
                Cancel
              </Button>
              <Button onClick={run} disabled={Boolean(problem) || busy} pending={busy}>
                Run and save
              </Button>
            </>
          }
        >
          <div className="space-y-4">
            <Field label="Run name" htmlFor="diagnostic-run-name" error={problem}>
              <Input
                id="diagnostic-run-name"
                autoFocus
                value={name}
                onChange={(event) => setName(event.target.value)}
                aria-invalid={Boolean(problem)}
              />
            </Field>
            <p className="text-body text-muted-foreground">
              {label} · <span className="font-mono break-all">{draft.target || "this host"}</span>
              {draft.port ? ` · port ${draft.port}` : ""}
              {draft.record ? ` · ${draft.record}` : ""}
              {draft.option ? ` · ${draft.option}` : ""}
            </p>
            <p className="text-hint text-muted-foreground">
              Starts one explicit test from this server with a 90-second deadline and retains its
              bounded result. Leaving the page keeps it running; a backend restart interrupts it
              without rerunning it.
            </p>
            {error && <ErrorState error={error} />}
          </div>
        </Modal>
      )}
    </>
  )
}
