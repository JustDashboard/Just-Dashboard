"use client"

import { useState } from "react"
import { CodeBracket, Route } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { FormNote } from "@/components/form"
import { Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { explainStatement } from "@/components/database/ops/performance-api"
import {
  fillShape,
  planFor,
  shapeFields,
  shapeSlots,
} from "@/components/database/ops/performance-figures"
import type { DbExplainResponse } from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

/** How many values a shape can ask for before the form stops being the quicker way. */
const MOST_FIELDS = 16

/**
 * A statement's plan, asked of the engine without running anything.
 *
 * A statement with no placeholder in it is planned as it stands. Most are
 * not that: the server keeps a statement's shape, with `$1` or `?` where each
 * value was, and an engine plans values. So a shape is planned with values
 * the reader gives — one field per placeholder, labelled with the words it
 * follows in the statement — put into the text where the placeholders stand.
 * A field left empty is sent as `NULL`, which plans, though seldom the way a
 * real value would; the note says so.
 *
 * `onFilled` hears the statement as it stands with the values in, so the way
 * to the Query page carries what was typed.
 */
export function StatementPlan({
  text,
  onQuery,
}: {
  text: string
  /** Opens the statement on the Query page; absent where the reader may not run one. */
  onQuery?: (sql: string) => void
}) {
  const { id } = useDatabase()
  const [values, setValues] = useState<Record<string, string>>({})
  const [plan, setPlan] = useState<{
    busy?: boolean
    answer?: DbExplainResponse
    error?: string
    sql?: string
  } | null>(null)
  const plans = planFor(text)
  const fields = plans === "shape" ? shapeFields(shapeSlots(text)) : []
  const fillable = plans === "shape" && fields.length <= MOST_FIELDS
  const filled = plans === "shape" ? fillShape(text, values) : text
  const typed = Object.values(values).some((value) => value.trim())

  const explain = async () => {
    setPlan({ busy: true })
    try {
      setPlan({ answer: await explainStatement(id, filled), sql: filled })
    } catch (err) {
      setPlan({ error: errorMessage(err), sql: filled })
    }
  }

  return (
    <div className="space-y-2.5">
      <div className="flex min-h-7 items-center justify-between gap-3">
        <p className="eyebrow">Plan</p>
        {plans === "plan" && (
          <Button size="xs" variant="outline" pending={plan?.busy} onClick={() => void explain()}>
            <Route />
            {plan?.answer || plan?.error ? "Explain again" : "Explain"}
          </Button>
        )}
      </div>

      {plans === "none" ? (
        <FormNote>
          Only a statement that reads or changes rows has a plan; this one does neither.
        </FormNote>
      ) : plans === "plan" ? (
        !plan && (
          <FormNote>
            Explain asks the engine how it would run this statement. Nothing is executed.
          </FormNote>
        )
      ) : fillable ? (
        <form
          className="space-y-2.5"
          onSubmit={(event) => {
            event.preventDefault()
            void explain()
          }}
        >
          <FormNote>
            The server keeps a statement&apos;s shape, with a placeholder where each value was, and
            an engine plans values. Put in values like the real ones, written as they would be in
            the statement — <span className="font-mono">42</span>,{" "}
            <span className="font-mono">&apos;paid&apos;</span> — and the engine plans the result.
            Nothing is executed.
          </FormNote>
          <div className="grid gap-2 sm:grid-cols-2">
            {fields.map((field) => (
              <InputGroup key={field.key} className="h-9 sm:h-8">
                <InputGroupAddon>
                  <InputGroupText
                    className="max-w-40 truncate font-mono"
                    title={`${field.before} ${field.token}`}
                  >
                    {field.before && (
                      <span className="text-muted-foreground/70">{field.before} </span>
                    )}
                    <span className="text-foreground/80">{field.token}</span>
                  </InputGroupText>
                </InputGroupAddon>
                <InputGroupInput
                  aria-label={`The value for ${field.token}${field.before ? `, after ${field.before}` : ""}`}
                  value={values[field.key] ?? ""}
                  placeholder="NULL"
                  autoComplete="off"
                  spellCheck={false}
                  className="font-mono sm:text-xs"
                  onChange={(event) =>
                    setValues((held) => ({ ...held, [field.key]: event.target.value }))
                  }
                />
              </InputGroup>
            ))}
          </div>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            <Button type="submit" size="xs" variant="outline" pending={plan?.busy}>
              <Route />
              {plan?.answer || plan?.error ? "Explain again" : "Explain with these values"}
            </Button>
            {onQuery && typed && (
              <Button type="button" size="xs" variant="ghost" onClick={() => onQuery(filled)}>
                <CodeBracket />
                Open in Query with them
              </Button>
            )}
            {!typed && (
              <FormNote className="min-w-0">
                A value left empty is sent as NULL, which seldom plans the way a real one does.
              </FormNote>
            )}
          </div>
        </form>
      ) : (
        <FormNote>
          This shape has {fields.length} placeholders, more than a form here is quicker for.{" "}
          {onQuery
            ? "Open it in Query, put values where they stand, and explain it there."
            : "It can be explained on the Query page, with values put in."}
        </FormNote>
      )}

      {plan?.error ? (
        <Notice tone="warning" title="The engine did not plan it">
          <p className="wrap-anywhere">{plan.error}</p>
        </Notice>
      ) : plan?.answer ? (
        <Plan answer={plan.answer} />
      ) : null}
    </div>
  )
}

/** A plan as the engine printed it: one column is lines of text, several are a table. */
function Plan({ answer }: { answer: DbExplainResponse }) {
  const { columns, rows } = answer.result
  if (columns.length <= 1) {
    return (
      <Well className="max-h-96 animate-rise overflow-auto text-hint leading-relaxed whitespace-pre">
        {rows.map((row) => row[0] ?? "").join("\n")}
      </Well>
    )
  }
  return (
    // Framed: the plan is a table with a scroll of its own.
    <div className="animate-rise overflow-hidden rounded-lg border">
      <Table containerClassName="max-h-96">
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            {columns.map((column) => (
              <TableHead key={column} className="h-8 px-2.5">
                {column}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row, index) => (
            <TableRow key={index}>
              {row.map((cell, at) => (
                <TableCell key={at} className="px-2.5 py-1.5 font-mono whitespace-pre">
                  {cell === null ? <span className="text-muted-foreground/60">NULL</span> : cell}
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}
