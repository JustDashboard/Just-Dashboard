"use client"

import { useId, useState } from "react"
import { cn } from "@/lib/utils"
import { Code, Warning } from "@/components/icons"
import { Notice } from "@/components/state"
import { tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { CodeView } from "@/components/database/kit"
import {
  canHandToQuery,
  scriptFilename,
  stepCounts,
  stepsLine,
  type Comparison,
  type MigrationStep,
} from "@/components/database/compare/model"

type View = "sql" | "steps"

/**
 * The migration: the SQL that turns the "from" schema into the "to" one, to
 * copy, save or open in Query — and the same thing as steps, each marked when
 * it destroys something or when it is left to the reader.
 *
 * Nothing here runs a statement. The script is text; running it is the Query
 * tab's, where it is classified and recorded like anything typed there.
 */
export function Migration({
  comparison,
  engineEditor,
  onOpenInQuery,
  protectedFrom,
}: {
  comparison: Comparison
  /** The editor's grammar for the engine's SQL. */
  engineEditor: string
  /** Absent where the engine has no query page. */
  onOpenInQuery?: (script: string) => void
  /** The "from" connection refuses changes, so the script cannot be run on it as it is. */
  protectedFrom: boolean
}) {
  const ids = useId()
  const [view, setView] = useState<View>("sql")
  const { steps, script, from, to } = comparison
  const counts = stepCounts(steps)
  const handable = canHandToQuery(script)

  if (steps.length === 0) return null

  return (
    <section aria-label="Migration" className="min-w-0 space-y-3">
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <div
          role="tablist"
          aria-label="Migration"
          className="flex items-stretch border-b border-hairline"
        >
          {(
            [
              ["sql", "SQL"],
              ["steps", "Steps"],
            ] as const
          ).map(([id, label]) => (
            <button
              key={id}
              type="button"
              role="tab"
              id={`${ids}-${id}`}
              aria-selected={view === id}
              aria-controls={`${ids}-panel`}
              className={tabClasses(view === id, "h-9")}
              onClick={() => setView(id)}
            >
              {label}
            </button>
          ))}
        </div>
        <p className="numeric text-hint text-muted-foreground">{stepsLine(steps)}</p>
      </div>

      {counts.destructive > 0 && (
        <Notice tone="warning" icon={Warning} title="This script removes things">
          {counts.destructive === 1
            ? "One step drops or rewrites something that cannot be restored from the script."
            : `${counts.destructive} steps drop or rewrite something that cannot be restored from the script.`}{" "}
          Each is marked DESTRUCTIVE in it. Read them before you run it.
        </Notice>
      )}
      {counts.manual > 0 && (
        <Notice tone="default" icon={Code} title="Some changes are left to you">
          {counts.manual === 1
            ? "The engine cannot make one of these in place, so the script holds a comment where it would be."
            : `The engine cannot make ${counts.manual} of these in place, so the script holds a comment where each would be.`}{" "}
          Each says why.
        </Notice>
      )}
      {protectedFrom && (
        <Notice tone="default" title={`${from.name} is protected`}>
          The dashboard refuses changes to it, so the script can be read and copied but not run here
          until protection is taken off.
        </Notice>
      )}

      <div id={`${ids}-panel`} role="tabpanel" aria-labelledby={`${ids}-${view}`}>
        {view === "sql" ? (
          <CodeView
            code={script}
            language={engineEditor}
            filename={scriptFilename(comparison)}
            label={
              <span className="flex min-w-0 items-baseline gap-3">
                <span className="min-w-0 truncate font-mono">
                  {from.name} → {to.name}
                </span>
              </span>
            }
            className="h-[calc(100svh-22rem)] min-h-80"
            actions={
              onOpenInQuery && (
                <Button
                  size="xs"
                  variant="outline"
                  disabled={!handable}
                  title={
                    handable
                      ? undefined
                      : "Too long to hand over in an address. Copy it or save it, and paste it in Query."
                  }
                  onClick={() => onOpenInQuery(script)}
                >
                  <Code />
                  Open in Query
                </Button>
              )
            }
          />
        ) : (
          <StepList steps={steps} />
        )}
      </div>
      {view === "sql" && onOpenInQuery && !handable && (
        <p className="text-hint text-muted-foreground">
          The script is too long to be handed to Query in an address. Copy it or save it, and paste
          it there.
        </p>
      )}
    </section>
  )
}

function StepList({ steps }: { steps: MigrationStep[] }) {
  return (
    <ol aria-label="Steps" className="min-w-0 divide-y divide-hairline rounded-xl border bg-card">
      {steps.map((step, index) => (
        <li key={index} className="min-w-0 space-y-2 px-4 py-3">
          <p className="flex min-w-0 items-baseline gap-3">
            <span className="numeric w-6 shrink-0 text-hint text-muted-foreground">
              {index + 1}
            </span>
            <span className="min-w-0 flex-1 text-body font-medium break-words">{step.title}</span>
            {step.destructive && (
              <Tag tone="danger" icon={Warning}>
                Destructive
              </Tag>
            )}
            {step.manual && <Tag tone="warning">For you</Tag>}
          </p>
          {step.note && (
            <p className="pl-9 text-xs leading-relaxed whitespace-pre-line text-muted-foreground">
              {step.note}
            </p>
          )}
          {step.statements.length > 0 && (
            <pre
              className={cn(
                "ml-9 max-h-60 overflow-auto rounded-md border border-hairline bg-surface-header px-3 py-2",
                "font-mono text-xs leading-relaxed break-words whitespace-pre-wrap",
              )}
            >
              {step.statements.map((statement) => `${statement.replace(/;\s*$/, "")};`).join("\n")}
            </pre>
          )}
        </li>
      ))}
    </ol>
  )
}
