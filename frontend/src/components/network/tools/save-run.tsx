"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { Field } from "@/components/form"
import { Modal } from "@/components/modal"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import type { PathRequest } from "@/lib/network-investigator-types"
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
  return <SaveRunSnapshot snapshot={{ kind: "probe", request }} label={label} disabled={disabled} />
}

export function SaveRunSnapshot({
  snapshot,
  label,
  disabled,
}: {
  snapshot:
    { kind: "probe"; request: DiagnosticRequest } | { kind: "investigation"; request: PathRequest }
  label: string
  disabled: boolean
}) {
  const router = useRouter()
  const [draft, setDraft] = useState<typeof snapshot>()
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Error>()
  const problem = diagnosticNameProblem(name)
  const run = async () => {
    if (!draft || problem || busy) return
    setBusy(true)
    setError(undefined)
    try {
      const saved = await post<DiagnosticRun>(
        draft.kind === "investigation"
          ? "/network/diagnostics/investigate"
          : "/network/diagnostics/",
        {
          name: name.trim(),
          ...(draft.kind === "investigation"
            ? { investigation: draft.request }
            : { request: draft.request }),
        },
      )
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
        type="button"
        variant="outline"
        disabled={disabled}
        onClick={() => {
          setName(`${label} · ${snapshot.request.target || "this host"}`)
          setDraft(
            snapshot.kind === "probe"
              ? { kind: "probe", request: { ...snapshot.request } }
              : { kind: "investigation", request: { ...snapshot.request } },
          )
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
              {label} ·{" "}
              <span className="font-mono break-all">{draft.request.target || "this host"}</span>
              {draft.request.port ? ` · port ${draft.request.port}` : ""}
              {draft.kind === "investigation" && (
                <span>
                  {" "}
                  ·{" "}
                  {draft.request.sourceKind === "container"
                    ? `container ${draft.request.containerId?.slice(0, 12)}`
                    : "this host"}{" "}
                  · {draft.request.family === "inet6" ? "IPv6" : "IPv4"} ·{" "}
                  {draft.request.protocol.toUpperCase()}
                  {draft.request.sourceAddress ? ` · source ${draft.request.sourceAddress}` : ""}
                  {draft.request.mark ? ` · mark ${draft.request.mark}` : ""}
                  {draft.request.measure
                    ? " · bounded TCP measurement requested"
                    : " · evidence without connection measurement"}
                </span>
              )}
              {draft.kind === "probe" && draft.request.record ? ` · ${draft.request.record}` : ""}
              {draft.kind === "probe" && draft.request.option ? ` · ${draft.request.option}` : ""}
            </p>
            <p className="text-hint text-muted-foreground">
              {draft.kind === "investigation"
                ? "Investigates the selected source and tuple with a 30-second operation deadline, retaining each layer’s evidence and unknowns."
                : "Starts one explicit test from this server with a 90-second deadline and retains its bounded result."}{" "}
              Leaving the page keeps it running; a backend restart interrupts it without rerunning
              it.
            </p>
            {error && <ErrorState error={error} />}
          </div>
        </Modal>
      )}
    </>
  )
}

/**
 * Keep a quick result exactly as it was shown. The server held it briefly
 * under an ID; saving sends no new traffic, and an expired hold says so rather
 * than quietly running the tool again.
 */
export function SaveHeldResult({
  resultId,
  label,
  target,
}: {
  resultId: string
  label: string
  target: string
}) {
  const router = useRouter()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Error>()
  const problem = diagnosticNameProblem(name)
  const save = async () => {
    if (problem || busy) return
    setBusy(true)
    setError(undefined)
    try {
      const saved = await post<DiagnosticRun>("/network/diagnostics/results", {
        resultId,
        name: name.trim(),
      })
      router.push(`/network/runs?run=${encodeURIComponent(saved.id)}`)
      setOpen(false)
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <Button
        type="button"
        size="sm"
        variant="outline"
        onClick={() => {
          setName(`${label} · ${target || "this host"}`)
          setError(undefined)
          setOpen(true)
        }}
      >
        Save this result
      </Button>
      {open && (
        <Modal
          open
          title="Save this result"
          onOpenChange={(next) => !next && !busy && setOpen(false)}
          footer={
            <>
              <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>
                Cancel
              </Button>
              <Button onClick={save} disabled={Boolean(problem) || busy} pending={busy}>
                Save result
              </Button>
            </>
          }
        >
          <div className="space-y-4">
            <Field label="Run name" htmlFor="held-result-name" error={problem}>
              <Input
                id="held-result-name"
                autoFocus
                value={name}
                onChange={(event) => setName(event.target.value)}
                aria-invalid={Boolean(problem)}
              />
            </Field>
            <p className="text-hint text-muted-foreground">
              Saves the result shown above to Saved runs without running the tool again. Results are
              held for fifteen minutes after they arrive.
            </p>
            {error && <ErrorState error={error} />}
          </div>
        </Modal>
      )}
    </>
  )
}
