"use client"

import { useState } from "react"
import Link from "next/link"
import { CodeBracket, Copy, Wrench } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { FormNote, Statement } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { nameHue } from "@/components/database/home/kinds"
import type { ApplyRequest } from "@/components/database/ops/advisor-apply"
import {
  applyVerdict,
  kindWord,
  maintenanceFix,
  statementsOf,
  targetName,
  type DbAdvice,
  type DbAdviceTarget,
} from "@/components/database/ops/advisor-fix"
import { classifyStatement } from "@/components/database/ops/performance-api"
import type { Maintenance } from "@/components/database/ops/performance-maintenance"
import { useDatabase } from "@/components/database/shell/database-context"

/** How many objects a finding lists before the rest fold away. */
const SHOWN = 8

/**
 * What a finding holds behind its row: the objects it is about, each named,
 * and its fix.
 *
 * The fix is the statement itself, shown before anything runs it. It can
 * always be copied, and opened on the Query page by a role that may run
 * statements. It is applied from here only where the server marks it safe —
 * one of the engine's own maintenance actions that locks nothing, or a
 * statement its classifier says destroys nothing — and then behind a question
 * that names the object and says what will run (`ApplyDialog`). Why a fix is
 * not offered here is said in a line, so the absence of the button is not a
 * puzzle.
 */
export function FindingBody({
  advice,
  maintenance,
  onApply,
}: {
  advice: DbAdvice
  maintenance: Maintenance
  /** Asks about a fix before it is applied. */
  onApply: (request: ApplyRequest) => void
}) {
  const { id, engine, readOnly, goto, href } = useDatabase()
  const { can } = useAuth()
  const [all, setAll] = useState(false)
  const targets = advice.targets ?? []
  const shown = all ? targets : targets.slice(0, SHOWN)
  const mayControl = can("service.control")
  const mayQuery = engine.has("query") && mayControl
  const actions = maintenance.actions ?? []

  // A fix that is a maintenance run for every object it names is run as one.
  const runs = targets.map((target) => maintenanceFix(advice, target, actions))
  const maintained = advice.sql && runs.length > 0 && runs.every(Boolean) ? runs : undefined
  // Otherwise the server is asked what it makes of the statement — the same
  // verdict the Query page would run it under.
  const asks = Boolean(advice.sql) && !maintained && !readOnly && mayControl
  const risk = usePoll(
    (signal) => classifyStatement(id, advice.sql ?? "", signal),
    0,
    [id, advice.sql],
    { enabled: asks },
  )

  const verdictFor = (index: number) =>
    applyVerdict({
      maintenance: maintained?.[index],
      destructive: maintained ? undefined : risk.data?.destructive,
      readOnly,
      mayControl,
    })
  const whole = targets.length > 0 ? verdictFor(0) : applyVerdict({ readOnly, mayControl })
  const several = targets.filter((target) => target.sql).length > 1

  const applyOne = (target: DbAdviceTarget, index: number) => {
    const verdict = verdictFor(index)
    if (!verdict.apply) return
    const about = { finding: advice.title, on: targetName(target), named: true }
    if (verdict.via === "maintenance") onApply({ ...about, via: "maintenance", fix: verdict.fix })
    else onApply({ ...about, via: "statement", sql: target.sql ?? "" })
  }

  const applyAll = () => {
    if (!whole.apply || !advice.sql) return
    if (whole.via === "maintenance") return applyOne(targets[0], 0)
    onApply({
      finding: advice.title,
      on:
        targets.length === 1
          ? targetName(targets[0])
          : `${targets.length} ${kindWord(targets[0]?.kind ?? "object", targets.length)}`,
      named: targets.length === 1,
      via: "statement",
      sql: advice.sql,
    })
  }

  // One maintenance run per object: several objects are applied one at a
  // time, each from its own row, since each run reports its own output.
  const applyWhole = whole.apply && (whole.via === "statement" || targets.length === 1)

  return (
    <div className="space-y-3 pt-1">
      {targets.length > 0 && (
        <ul
          aria-label="What it is about"
          className="divide-y divide-hairline border-y border-hairline"
        >
          {shown.map((target, index) => {
            const verdict = verdictFor(index)
            const place =
              target.kind === "table" && engine.has("schema")
                ? href("schema", { schema: target.schema, table: target.name })
                : undefined
            return (
              <li
                key={`${target.kind}:${targetName(target)}:${target.table ?? ""}:${index}`}
                className="flex min-w-0 items-center gap-3 py-1.5"
              >
                <span className="flex w-16 shrink-0">
                  <Tag>{kindWord(target.kind)}</Tag>
                </span>
                <span className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2.5 gap-y-0.5">
                  <span className="min-w-0 truncate font-mono text-foreground">
                    {target.schema && (
                      <>
                        <span style={{ color: nameHue(target.schema) }}>{target.schema}</span>
                        <span className="text-muted-foreground">.</span>
                      </>
                    )}
                    {place ? (
                      <Link href={place} className="rounded-sm focus-ring hover:underline">
                        {target.name}
                      </Link>
                    ) : (
                      target.name
                    )}
                  </span>
                  {target.kind === "index" && target.table && (
                    <span className="font-mono text-hint">on {target.table}</span>
                  )}
                  {target.detail && <span className="min-w-0 text-hint">{target.detail}</span>}
                </span>
                {several && target.sql && (
                  <span className="flex shrink-0 items-center gap-0.5">
                    {verdict.apply && (
                      <IconAction
                        label={`Apply the fix to ${targetName(target)}`}
                        onClick={() => applyOne(target, index)}
                      >
                        <Wrench />
                      </IconAction>
                    )}
                    {mayQuery && (
                      <IconAction
                        label={`Open the fix for ${targetName(target)} in Query`}
                        onClick={() => goto("query", { sql: target.sql })}
                      >
                        <CodeBracket />
                      </IconAction>
                    )}
                    <IconAction
                      label={`Copy the fix for ${targetName(target)}`}
                      onClick={() => void copyText(target.sql ?? "", "Statement copied")}
                    >
                      <Copy />
                    </IconAction>
                  </span>
                )}
              </li>
            )
          })}
        </ul>
      )}
      {targets.length > SHOWN && !all && (
        <Button size="xs" variant="ghost" onClick={() => setAll(true)}>
          Show all {targets.length}
        </Button>
      )}

      {advice.sql && (
        <div className="space-y-2">
          <Statement
            label={several ? "The fix, for all of them" : "The fix"}
            sql={advice.sql}
            placeholder=""
            className="text-foreground"
          />
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            {applyWhole && (
              <Button size="xs" variant="outline" onClick={applyAll}>
                <Wrench />
                {whole.via === "maintenance"
                  ? `Apply: ${whole.fix.action.label.toLowerCase()}`
                  : several
                    ? `Apply all ${statementsOf(advice.sql).length}`
                    : "Apply"}
              </Button>
            )}
            {mayQuery && (
              <Button
                size="xs"
                variant="outline"
                onClick={() => goto("query", { sql: advice.sql })}
              >
                <CodeBracket />
                Open in Query
              </Button>
            )}
            {!whole.apply && whole.reason && (
              <FormNote className="min-w-0">{whole.reason}</FormNote>
            )}
            {whole.apply && whole.via === "maintenance" && targets.length > 1 && (
              <FormNote className="min-w-0">
                Each is run on its own, from its row: a run reports its own output.
              </FormNote>
            )}
            {asks && risk.error && !risk.data && (
              <FormNote className="min-w-0">
                The server could not be asked whether this is safe to apply from here.
              </FormNote>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
