"use client"

import { useState } from "react"
import { Segments } from "@/components/deploy/settings/segments"
import { FormFact, FormFacts, FormNote, Statement } from "@/components/form"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { EngineMark } from "@/components/database/kit"
import {
  buildsIndex,
  concurrently,
  statementsOf,
  type MaintenanceFix,
} from "@/components/database/ops/advisor-fix"
import { targetWords } from "@/components/database/ops/performance-storage"
import { useDatabase } from "@/components/database/shell/database-context"

/** A fix the reader has asked to apply, as the question about it is put. */
export type ApplyRequest = {
  /** The finding the fix answers: "3 foreign keys with no index". */
  finding: string
  /** What it is applied to: one object by its name, or how many of what. */
  on: string
  /** A name is a literal; "3 tables" is words. */
  named: boolean
} & ({ via: "maintenance"; fix: MaintenanceFix } | { via: "statement"; sql: string })

/**
 * The question before a fix is applied from the Advisor.
 *
 * It is the product's ordinary dialog and its command wears the brand: a fix
 * applied from here creates an index or runs a vacuum — it destroys nothing,
 * and the red button is for what does. It opens on what the fix is applied
 * to, then says what will run.
 *
 * A maintenance fix is run by the engine's own action, whose statement is
 * the server's to write and is shown, with the engine's output, in the run's
 * own dialog; so this one names the action and does not print a statement
 * that would not be the one run.
 *
 * A statement fix prints its statements. Where they build an index on an
 * engine that can also build one alongside the table's writes, the reader
 * chooses which, and the choice already made is the one that does not stop
 * the table: that the plain build "destroys nothing" is true and is not the
 * whole of what it does.
 */
export function ApplyDialog({
  request,
  onCancel,
  onStatements,
  onMaintenance,
}: {
  request: ApplyRequest
  onCancel: () => void
  /** Runs the statements and reports what happened; settles when it has. */
  onStatements: (sql: string) => Promise<void>
  onMaintenance: (fix: MaintenanceFix) => void
}) {
  const { engine } = useDatabase()
  const [busy, setBusy] = useState(false)
  const [build, setBuild] = useState<"concurrently" | "blocking">("concurrently")

  const given = request.via === "statement" ? statementsOf(request.sql) : []
  const indexes = given.filter(buildsIndex).length
  // The engine has a concurrent build: its plain one is the kind that blocks.
  const choice = indexes > 0 && engine.capabilities.ddlOperations.includes("indexConcurrently")
  const statements = choice && build === "concurrently" ? given.map(concurrently) : given
  const sql = statements.join("\n")

  const on =
    request.via === "maintenance"
      ? targetWords(request.fix.request, engine.nouns.object)
      : request.on
  const command =
    request.via === "maintenance"
      ? request.fix.action.label
      : statements.length === 1
        ? "Apply"
        : `Apply ${statements.length} statements`

  const run = async () => {
    if (request.via === "maintenance") return onMaintenance(request.fix)
    setBusy(true)
    try {
      await onStatements(sql)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(open) => !open && !busy && onCancel()}
      title="Apply the fix"
      description={`Apply the fix for: ${request.finding}`}
      initialFocus="body"
      footer={
        <>
          <FormNote className="mr-auto">
            {request.via === "maintenance"
              ? "Runs now. Its statement and output are shown as it goes."
              : "Runs on the server now."}
          </FormNote>
          <Button variant="outline" onClick={onCancel} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={() => void run()} pending={busy}>
            {command}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="flex min-w-0 items-center gap-3">
          <EngineMark engine={engine} size="sm" />
          <div className="min-w-0 space-y-0.5">
            <p
              className={
                request.named || request.via === "maintenance"
                  ? "truncate font-mono text-body font-medium"
                  : "truncate text-body font-medium"
              }
            >
              {on}
            </p>
            <FormFacts>
              <FormFact label="Fixes">{request.finding}</FormFact>
            </FormFacts>
          </div>
        </div>

        {request.via === "maintenance" ? (
          <div className="space-y-2 text-body leading-relaxed">
            <p>
              <span className="font-medium">{request.fix.action.label}.</span>{" "}
              {request.fix.action.description}
            </p>
          </div>
        ) : (
          <>
            {choice && (
              <div className="space-y-2">
                <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
                  <p className="text-body font-medium">
                    Build {indexes === 1 ? "the index" : `the ${indexes} indexes`}
                  </p>
                  <Segments
                    label="How the index is built"
                    value={build}
                    options={[
                      { value: "concurrently", label: "Concurrently" },
                      { value: "blocking", label: "Blocking writes" },
                    ]}
                    onChange={setBuild}
                  />
                </div>
                {build === "concurrently" ? (
                  <FormNote>
                    The table stays open to reads and writes while the index is built. It takes
                    longer, and a build that fails part-way leaves an invalid index, which the
                    report then lists.
                  </FormNote>
                ) : (
                  <Notice tone="warning" title="The table takes no writes until the index is built">
                    <p>
                      Every insert, update and delete on it waits for the build to finish, and
                      everything behind those waits too. On a large table that is minutes.
                    </p>
                  </Notice>
                )}
              </div>
            )}
            {indexes > 0 && !choice && (
              <FormNote>
                Building an index reads the whole table, and how much of its work the table keeps
                doing meanwhile is the engine&apos;s to say. On a large table, choose a quiet hour.
              </FormNote>
            )}
            <Statement
              label={statements.length === 1 ? "Statement" : `Statements, run in this order`}
              sql={sql}
              placeholder=""
              className="text-foreground"
            />
            {statements.length > 1 && (
              <FormNote>The run stops at the first statement the server refuses.</FormNote>
            )}
          </>
        )}
      </div>
    </Modal>
  )
}
