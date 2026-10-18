"use client"

import { useId } from "react"
import { CloudDownload, Warning } from "@/components/icons"
import { bytes, plural } from "@/lib/format"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { FlowActions, FlowPanel, FlowPanelBody, FlowPanelHeader } from "@/components/flow"
import { Disclosure, Field, FormNote, OptionList, OptionRow, Statement } from "@/components/form"
import { Notice } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import type { Engine } from "@/components/database/engine"
import { newDatabaseProblem } from "@/components/database/ops/backups-model"
import { importNameProblem, sourceTotals } from "@/components/database/import/model"
import type { DbImportInspection, DbImportMode } from "@/components/database/import/types"

/** What the plan step asks, kept by the page so a step back loses none of it. */
export type Plan = {
  mode: DbImportMode
  database: string
  name: string
  structureOnly: boolean
  publicationExists: boolean
}

/**
 * The second step: how it comes over, and what it is called here. What the
 * source said about itself leads — a replication's tables and what each would
 * cost — then the one choice there is to make, the names, and what stands in
 * the way, which is said before the command is pressed and not after.
 */
export function PlanStep({
  engine,
  current,
  inspection,
  plan,
  starting,
  checking,
  refusal,
  onPlan,
  onStart,
  onRecheck,
  onChangeSource,
}: {
  engine: Engine
  /** The database this connection is on, which the new one cannot be. */
  current: string
  inspection: DbImportInspection
  plan: Plan
  starting: boolean
  checking: boolean
  refusal?: string
  onPlan: (plan: Plan) => void
  onStart: () => void
  onRecheck: () => void
  onChangeSource: () => void
}) {
  const id = useId()
  const numbered = engine.can("dumpDatabases")
  const replication = plan.mode === "replication"
  const report = inspection.replication
  const blockers = [...inspection.blockers, ...(replication ? (report?.blockers ?? []) : [])]
  const warnings = [...inspection.warnings, ...(replication ? (report?.warnings ?? []) : [])]
  const databaseProblem = newDatabaseProblem(plan.database, { numbered, current })
  const nameProblem = replication ? importNameProblem(plan.name) : undefined
  const ready = blockers.length === 0 && !databaseProblem && !nameProblem
  const totals = report ? sourceTotals(report.tables) : undefined
  const sourceLine = [
    inspection.source.flavorLabel ?? inspection.source.flavor,
    inspection.source.version,
  ]
    .filter(Boolean)
    .join(" ")

  return (
    <FlowPanel aria-label="Plan" aria-busy={starting} className="min-w-0">
      <FlowPanelHeader
        title={
          <span className="flex min-w-0 flex-col">
            <span className="truncate">
              {inspection.source.database
                ? `${inspection.source.database} on ${inspection.source.address}`
                : inspection.source.address}
            </span>
            {sourceLine && (
              <span className="truncate text-hint font-normal text-muted-foreground">
                {sourceLine}
                {inspection.source.user ? ` · as ${inspection.source.user}` : ""}
              </span>
            )}
          </span>
        }
      />
      {replication && totals && (
        <StatGrid columns={4} dense className="border-b border-hairline">
          <StatTile label="Tables" value={totals.tables.toLocaleString()} />
          <StatTile
            label="Rows"
            value={`${totals.uncounted ? "≥ " : "≈ "}${totals.rows.toLocaleString()}`}
            hint={totals.uncounted ? "Some tables were never counted" : undefined}
          />
          <StatTile label="Size" value={bytes(totals.size)} />
          <StatTile label="Sequences" value={(report?.sequences ?? 0).toLocaleString()} />
        </StatGrid>
      )}
      <FlowPanelBody className="space-y-4">
        {refusal && (
          <Notice tone="danger" title="It was not started">
            <span className="break-words whitespace-pre-wrap">{refusal}</span>
          </Notice>
        )}

        {inspection.forms.length > 1 && (
          <ChoiceGrid columns={2} role="group" aria-label="How it comes over">
            <ChoiceCard
              verb="Keep it current"
              title="Keep it current"
              description="Replication: the rows are copied, then every change on the source follows until you cut over. Needs a login on the source that may publish."
              selected={replication}
              onClick={() => onPlan({ ...plan, mode: "replication" })}
            />
            <ChoiceCard
              verb="Copy it once"
              title="Copy it once"
              description="A dump read from the source and loaded as it is read. Nothing is kept on the source, and changes made there after it starts are not copied."
              selected={!replication}
              onClick={() => onPlan({ ...plan, mode: "stream" })}
            />
          </ChoiceGrid>
        )}

        <Field
          label={numbered ? "Database number to load into" : "New database on this server"}
          htmlFor={`${id}-database`}
          hint={
            numbered
              ? "A numbered database that holds no key."
              : "Made for it. The import stops, and changes nothing, if a database of that name exists."
          }
          error={plan.database.trim() ? databaseProblem : undefined}
        >
          <Input
            id={`${id}-database`}
            value={plan.database}
            onChange={(event) => onPlan({ ...plan, database: event.target.value })}
            inputMode={numbered ? "numeric" : undefined}
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
          />
        </Field>

        {replication ? (
          <Disclosure
            summary="The publication, and who makes it"
            facts={`Called ${plan.name}${plan.publicationExists ? ", made on the source by hand" : ", made by the dashboard"}`}
          >
            <div className="space-y-4">
              <Field
                label="Name of the publication, the subscription and the slot"
                htmlFor={`${id}-name`}
                hint="One name for the three, so each can be found on its own server."
                error={plan.name.trim() ? nameProblem : undefined}
              >
                <Input
                  id={`${id}-name`}
                  value={plan.name}
                  onChange={(event) => onPlan({ ...plan, name: event.target.value })}
                  className="font-mono"
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
              <OptionList>
                <OptionRow
                  title="I will make the publication on the source myself"
                  hint="For a login that may read but not publish. Run the statement below there first."
                  checked={plan.publicationExists}
                  onCheckedChange={(publicationExists) => onPlan({ ...plan, publicationExists })}
                />
              </OptionList>
              <Statement
                label="Run on the source"
                sql={inspection.publicationSql ?? ""}
                placeholder="The source names no table to publish."
              />
            </div>
          </Disclosure>
        ) : (
          engine.can("copyStructureOnly") && (
            <OptionList>
              <OptionRow
                title={`Bring the structure only, with no ${engine.nouns.rows}`}
                checked={plan.structureOnly}
                onCheckedChange={(structureOnly) => onPlan({ ...plan, structureOnly })}
              />
            </OptionList>
          )
        )}

        {blockers.length > 0 && (
          <Notice tone="danger" icon={Warning} title="Not yet">
            <ul className="list-disc space-y-1 pl-4">
              {blockers.map((line) => (
                <li key={line} className="break-words">
                  {line}
                </li>
              ))}
            </ul>
          </Notice>
        )}
        {warnings.length > 0 && (
          <Notice tone="warning" icon={Warning} title="Worth knowing">
            <ul className="list-disc space-y-1 pl-4">
              {warnings.map((line) => (
                <li key={line} className="break-words">
                  {line}
                </li>
              ))}
            </ul>
          </Notice>
        )}
        {replication && report && report.tables.length > 0 && (
          <FormNote>
            Publishes {plural(report.tables.length, "table")}. Sequences are not replicated: the
            cutover sets them to the source&apos;s positions.
          </FormNote>
        )}
      </FlowPanelBody>
      <FlowActions
        note={
          replication
            ? "Makes the database here, copies the structure, then the rows, and keeps following the source."
            : "Makes the database here and loads it from the source as it reads."
        }
        secondary={
          <>
            <Button
              variant="ghost"
              className="h-11 sm:h-9"
              disabled={starting}
              onClick={onChangeSource}
            >
              Change source
            </Button>
            {blockers.length > 0 && (
              <Button
                variant="outline"
                className="h-11 sm:h-9"
                pending={checking}
                disabled={starting}
                onClick={onRecheck}
              >
                Check again
              </Button>
            )}
          </>
        }
      >
        <Button className="h-11 sm:h-9" disabled={!ready} pending={starting} onClick={onStart}>
          <CloudDownload />
          Start the import
        </Button>
      </FlowActions>
    </FlowPanel>
  )
}
