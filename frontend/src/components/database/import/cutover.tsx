"use client"

import { useState } from "react"
import { ApiError, errorMessage, post } from "@/lib/api"
import type { Job } from "@/lib/types"
import { FormFact, FormNote, OptionList, OptionRow } from "@/components/form"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import { lagWords } from "@/components/database/import/model"
import type { DbImportLive } from "@/components/database/import/types"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The cutover: wait for the target to apply everything the source has written,
 * put the sequences where the source's are, and take the subscription away.
 *
 * It can only wait for what was written before it began, so the one thing it
 * asks is whether the writes have stopped. The source is not changed, and an
 * application can be pointed at the new database as soon as the job ends.
 */
export function CutoverDialog({
  live,
  onStarted,
  onRunning,
  onClose,
}: {
  live: DbImportLive
  onStarted: (job: Job) => void
  onRunning: (jobId: string) => void
  onClose: () => void
}) {
  const { id, conn, engine } = useDatabase()
  const imp = live.import
  const [stopped, setStopped] = useState(false)
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()

  const run = async () => {
    setBusy(true)
    setRefusal(undefined)
    try {
      onStarted(
        await post<Job>(`/databases/${id}/imports/${imp.id}/cutover`, { writesStopped: true }),
      )
    } catch (err) {
      if (err instanceof ApiError && err.code === "transfer_running" && err.resource) {
        onRunning(err.resource)
        return
      }
      setRefusal(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <TaskDialog
      title="Cut over"
      description={`Cut ${imp.database} over from ${imp.source.address}`}
      subject={databaseSubject(
        conn,
        engine,
        <>
          <FormFact label="Into" mono>
            {imp.database}
          </FormFact>
          <FormFact label="From" mono>
            {imp.source.address}
          </FormFact>
          <FormFact label="Behind">{lagWords(live.lag)}</FormFact>
        </>,
      )}
      dirty={stopped}
      busy={busy}
      refusal={refusal}
      note="Nothing is changed on the source."
      command="Cut over"
      disabled={!stopped}
      onRun={() => void run()}
      onClose={onClose}
    >
      <FormNote>
        The cutover waits until {imp.database} has everything the source wrote up to the moment it
        starts. What the source writes after that is not copied, so stop whatever writes to it first
        — stop the application, or put it in maintenance — and leave it stopped until it is pointed
        at {imp.database}.
      </FormNote>
      <OptionList>
        <OptionRow
          title="Nothing is writing to the source any more"
          checked={stopped}
          onCheckedChange={setStopped}
        />
      </OptionList>
    </TaskDialog>
  )
}
