"use client"

import { useState } from "react"
import { useConfirm } from "@/components/confirm-dialog"
import { Field } from "@/components/form"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { usePoll } from "@/hooks/use-poll"
import { get, put } from "@/lib/api"
import { diagnosticRetentionProblem, type DiagnosticRetention } from "@/lib/network-diagnostics"
import { NetworkReadWarning } from "./read-warning"

export function RunRetention({ onChanged }: { onChanged: () => void }) {
  const poll = usePoll(
    (signal) => get<DiagnosticRetention>("/network/diagnostics/policy", undefined, signal),
    60_000,
  )
  return (
    <details className="text-body">
      <summary className="cursor-pointer focus-ring">Retention policy</summary>
      <div className="mt-4 space-y-3">
        {poll.data ? (
          <>
            <NetworkReadWarning
              error={poll.error}
              refresh={poll.refresh}
              lastSuccess={poll.lastSuccess}
              reading="retention policy"
            />
            <RetentionEditor
              policy={poll.data}
              onChanged={() => {
                poll.refresh()
                onChanged()
              }}
            />
          </>
        ) : poll.error ? (
          <ErrorState error={poll.error} onRetry={poll.refresh} />
        ) : (
          <p className="text-muted-foreground">Reading retention policy…</p>
        )}
      </div>
    </details>
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
    <>
      <p className="text-hint text-muted-foreground">
        Currently {policy.maxRuns} finished runs for {policy.maxAgeHours} hours. Running records are
        preserved. Reads, writes and startup remove expired records.
      </p>
      <div className="flex flex-wrap items-end gap-3">
        <Field label="Finished runs to retain" htmlFor="retain-runs">
          <Input
            id="retain-runs"
            className="w-32"
            inputMode="numeric"
            value={runs}
            onChange={(event) => setRuns(event.target.value)}
          />
        </Field>
        <Field label="Retention hours" htmlFor="retain-hours">
          <Input
            id="retain-hours"
            className="w-32"
            inputMode="numeric"
            value={hours}
            onChange={(event) => setHours(event.target.value)}
          />
        </Field>
        <Button
          variant="outline"
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
      {problem && (
        <p role="alert" className="text-hint text-destructive">
          {problem}
        </p>
      )}
      {dialog}
    </>
  )
}
