"use client"

import { useState } from "react"
import { useConfirm } from "@/components/confirm-dialog"
import { Field } from "@/components/form"
import { Download, Pencil, RotateClockwise, StopCircle, Terminal, Trash } from "@/components/icons"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { Modal } from "@/components/modal"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import type { Verb } from "@/components/verbs"
import { useAuth } from "@/hooks/use-auth"
import { del, getText, patch, post } from "@/lib/api"
import {
  diagnosticFinished,
  diagnosticNameProblem,
  diagnosticReading,
  type DiagnosticRun,
} from "@/lib/network-diagnostics"
import { runRequestLine, runTool } from "@/lib/network-runs-view"
import { notify } from "@/lib/toast"
import { StateTile, toolGlyph } from "./diagnostic-marks"

/**
 * What can be done to a saved run, declared once for its row and its page
 * (`components/verbs.tsx`): cancel it while it runs, rerun it once it has
 * ended, export it, delete it — the last only for an account the server lets
 * delete. The page adds renaming it and watching its job.
 */
export function useRunActions(
  run: DiagnosticRun,
  {
    onChanged,
    onRerun,
    onDeleted,
    page,
  }: {
    onChanged: () => void
    onRerun: (id: string) => void
    onDeleted: () => void
    /** The run's own page: rename and the job console, and failures said in place. */
    page: boolean
  },
) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState<string>()
  const [error, setError] = useState<Error>()
  const [renaming, setRenaming] = useState(false)
  const console_ = useJobConsole({ onSuccess: onChanged })
  const finished = diagnosticFinished(run)
  const id = encodeURIComponent(run.id)

  const act = async (verb: string, failure: string, action: () => Promise<void>) => {
    if (busy) return
    setBusy(verb)
    setError(undefined)
    try {
      await action()
      onChanged()
    } catch (err) {
      const reason = err instanceof Error ? err : new Error(String(err))
      if (page) setError(reason)
      else notify.error(failure, reason)
    } finally {
      setBusy(undefined)
    }
  }
  const exportRun = async () => {
    const text = await getText(`/network/diagnostics/${id}/export`)
    const url = URL.createObjectURL(new Blob([text], { type: "application/json" }))
    const link = document.createElement("a")
    link.href = url
    link.download = `network-diagnostic-${run.id}.json`
    link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }

  const verbs: Verb[] = []
  if (finished)
    verbs.push({
      key: "rerun",
      label: "Rerun",
      progressive: "Rerunning",
      icon: RotateClockwise,
      disabled: Boolean(busy),
      run: () =>
        void act("rerun", `Could not rerun ${run.name}`, async () => {
          const next = await post<DiagnosticRun>(`/network/diagnostics/${id}/rerun`, {})
          onRerun(next.id)
        }),
    })
  else
    verbs.push({
      key: "cancel",
      label: run.status === "cancelling" ? "Stopping…" : "Cancel run",
      progressive: "Stopping",
      icon: StopCircle,
      disabled: Boolean(busy) || run.status === "cancelling",
      run: () =>
        void act("cancel", `Could not cancel ${run.name}`, async () => {
          await post(`/network/diagnostics/${id}/cancel`, {})
        }),
    })
  verbs.push({
    key: "export",
    label: "Export JSON",
    icon: Download,
    disabled: Boolean(busy),
    run: () => void act("export", `Could not export ${run.name}`, exportRun),
  })
  if (page) {
    verbs.push({ key: "rename", label: "Rename", icon: Pencil, run: () => setRenaming(true) })
    if (run.jobId)
      verbs.push({
        key: "job",
        label: "Watch job",
        icon: Terminal,
        run: () => void console_.open(run.jobId!),
      })
  }
  if (finished && can("destructive"))
    verbs.push({
      key: "delete",
      label: "Delete",
      icon: Trash,
      danger: true,
      disabled: Boolean(busy),
      run: () =>
        confirm({
          title: "Delete saved run",
          subject: {
            mark: <StateTile glyph={toolGlyph(run)} tone={diagnosticReading(run).tone} size="sm" />,
            name: run.name,
            facts: (
              <span>
                {runTool(run)} · <span className="font-mono">{runRequestLine(run)}</span>
              </span>
            ),
          },
          description: `Delete the retained request and result for ${run.name}.`,
          confirmLabel: "Delete run",
          action: async () => {
            await del(`/network/diagnostics/${id}`)
          },
          onDone: onDeleted,
        }),
    })

  return {
    verbs,
    busy,
    error,
    console: page ? (
      <JobConsole
        job={console_.job}
        lines={console_.lines}
        onDismiss={console_.dismiss}
        onCancel={console_.cancel}
      />
    ) : null,
    dialogs: (
      <>
        {dialog}
        {page && (
          <RenameDialog
            key={`${run.id}:${renaming}`}
            run={run}
            open={renaming}
            onOpenChange={setRenaming}
            onSaved={onChanged}
          />
        )}
      </>
    ),
  }
}

/** A saved run's name: the one thing about it that can be changed. */
function RenameDialog({
  run,
  open,
  onOpenChange,
  onSaved,
}: {
  run: DiagnosticRun
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const [name, setName] = useState(run.name)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Error>()
  const problem = diagnosticNameProblem(name)
  const save = async () => {
    if (busy || problem || name.trim() === run.name) return
    setBusy(true)
    setError(undefined)
    try {
      await patch(`/network/diagnostics/${encodeURIComponent(run.id)}`, { name: name.trim() })
      notify.success("Run name saved")
      onSaved()
      onOpenChange(false)
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title="Rename saved run"
      description="The name the run is listed under."
      footer={
        <Button
          disabled={busy || Boolean(problem) || name.trim() === run.name}
          pending={busy}
          onClick={() => void save()}
        >
          Save name
        </Button>
      }
    >
      <form
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault()
          void save()
        }}
      >
        {error && <ErrorState error={error} />}
        <Field label="Saved run name" htmlFor="saved-run-name" error={problem}>
          <Input
            id="saved-run-name"
            value={name}
            autoFocus
            onChange={(event) => setName(event.target.value)}
            aria-invalid={Boolean(problem)}
          />
        </Field>
      </form>
    </Modal>
  )
}
