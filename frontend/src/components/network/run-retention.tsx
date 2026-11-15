"use client"

import { useState } from "react"
import { useConfirm } from "@/components/confirm-dialog"
import { Field, FieldRow } from "@/components/form"
import { Modal } from "@/components/modal"
import { ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { put } from "@/lib/api"
import { plural } from "@/lib/format"
import { diagnosticRetentionProblem, type DiagnosticRetention } from "@/lib/network-diagnostics"

/** The policy as the identity line says it: "keeps 100 for 7 days". */
export function retentionWords(policy: DiagnosticRetention) {
  const hours = policy.maxAgeHours
  const span = hours % 24 === 0 ? plural(hours / 24, "day") : plural(hours, "hour")
  return `keeps ${policy.maxRuns} finished for ${span}`
}

/**
 * How many finished runs are kept and for how long: a small form, so a
 * dialog (it was a disclosure under the list). Shrinking either erases
 * records at once, so saving asks first; running records are never erased.
 */
export function RetentionDialog({
  open,
  onOpenChange,
  policy,
  error,
  onRetry,
  onChanged,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  policy?: DiagnosticRetention
  error?: Error
  onRetry: () => void
  onChanged: () => void
}) {
  return (
    <Modal open={open} onOpenChange={onOpenChange} title="Retention policy">
      {policy ? (
        <RetentionEditor
          key={`${policy.maxRuns}:${policy.maxAgeHours}`}
          policy={policy}
          onChanged={() => {
            onChanged()
            onOpenChange(false)
          }}
        />
      ) : error ? (
        <ErrorState error={error} onRetry={onRetry} />
      ) : (
        <LoadingRows rows={2} />
      )}
    </Modal>
  )
}

function RetentionEditor({
  policy,
  onChanged,
}: {
  policy: DiagnosticRetention
  onChanged: () => void
}) {
  const [runs, setRuns] = useState(String(policy.maxRuns))
  const [hours, setHours] = useState(String(policy.maxAgeHours))
  const { confirm, dialog } = useConfirm()
  const problem = diagnosticRetentionProblem(runs, hours)
  const changed = Number(runs) !== policy.maxRuns || Number(hours) !== policy.maxAgeHours
  return (
    <div className="space-y-4">
      <p className="text-body text-muted-foreground">
        Now {retentionWords(policy)}. Running records are kept whatever the limits.
      </p>
      <FieldRow>
        <Field label="Finished runs to retain" htmlFor="retain-runs">
          <Input
            id="retain-runs"
            inputMode="numeric"
            value={runs}
            onChange={(event) => setRuns(event.target.value)}
          />
        </Field>
        <Field label="Retention hours" htmlFor="retain-hours">
          <Input
            id="retain-hours"
            inputMode="numeric"
            value={hours}
            onChange={(event) => setHours(event.target.value)}
          />
        </Field>
      </FieldRow>
      {problem && (
        <p role="alert" className="text-hint text-destructive">
          {problem}
        </p>
      )}
      <div className="flex justify-end">
        <Button
          disabled={!changed || Boolean(problem)}
          onClick={() =>
            confirm({
              title: "Change diagnostic retention",
              description:
                "Finished runs beyond the new count or age limits will be erased immediately. Running records remain available.",
              confirmLabel: "Save retention",
              action: async () => {
                await put("/network/diagnostics/policy", {
                  maxRuns: Number(runs),
                  maxAgeHours: Number(hours),
                })
              },
              onDone: onChanged,
            })
          }
        >
          Save retention
        </Button>
      </div>
      {dialog}
    </div>
  )
}
