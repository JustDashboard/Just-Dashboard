"use client"

import { useId, useState } from "react"
import { Plus, Trash } from "@/components/icons"
import { errorMessage, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { Field, FormNote, Statement } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { EmptyNote } from "@/components/state"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Textarea } from "@/components/ui/textarea"
import { ResultGrid } from "@/components/database/query/result-grid"
import { useDatabase } from "@/components/database/shell/database-context"
import { notesFor, quotedRelation } from "@/components/database/schema/engine-notes"
import {
  blankClaim,
  claimProblems,
  settingsRead,
  testBody,
  testQuery,
  type ClaimDraft,
} from "@/components/database/schema/policy"
import type {
  DbRoleTestResult,
  DbRowSecurity,
  DbTableDetail,
} from "@/components/database/schema/types"

type Outcome =
  | { status: "running" }
  | { status: "done"; answer: DbRoleTestResult; serial: number }
  | { status: "failed"; error: string }

/**
 * A table as another role sees it: a SELECT run after the server has been told
 * to act as that role and given the settings the policies read.
 *
 * Nothing it does stays. The server runs the statement in a read-only
 * transaction that it rolls back, accepts one SELECT and no more, and bounds
 * the rows, and says first everything it sent before the statement — so the
 * answer to "why does this role see that" can be read off the page.
 */
export function RoleTester({
  detail,
  security,
  roles,
}: {
  detail: DbTableDetail
  security: DbRowSecurity
  /** The accounts to choose from; none when the list could not be read, and the name is typed. */
  roles: string[]
}) {
  const { id, engine } = useDatabase()
  const ids = useId()
  const notes = notesFor(engine)
  const [role, setRole] = useState("")
  // The settings the policies read are the ones a test is most likely to set.
  const [claims, setClaims] = useState<ClaimDraft[]>(() =>
    settingsRead(security.policies).map((name) => blankClaim({ name })),
  )
  const [query, setQuery] = useState(() =>
    testQuery(quotedRelation(detail.schema, detail.name, notes)),
  )
  const [outcome, setOutcome] = useState<Outcome>()
  const [runs, setRuns] = useState(0)

  const draft = { role, claims, query }
  const body = testBody(detail.schema, detail.name, draft)
  const problems = claimProblems(claims)
  const unset = settingsRead(security.policies).filter(
    (name) => !claims.some((claim) => claim.name.trim() === name),
  )
  const setClaim = (key: string, patch: Partial<ClaimDraft>) =>
    setClaims(claims.map((claim) => (claim.key === key ? { ...claim, ...patch } : claim)))

  const run = async () => {
    if (!body || outcome?.status === "running") return
    setOutcome({ status: "running" })
    try {
      const answer = await post<DbRoleTestResult>(`/databases/${id}/rls/test`, body)
      setRuns(runs + 1)
      setOutcome({ status: "done", answer, serial: runs + 1 })
    } catch (err) {
      setOutcome({ status: "failed", error: errorMessage(err) })
    }
  }

  return (
    <section aria-label="Try it as a role" className="space-y-4 border-t border-hairline px-4 py-4">
      <h3 className="text-title font-medium">Try it as a role</h3>
      <div className="grid gap-4 md:grid-cols-[14rem_minmax(0,1fr)]">
        <Field label="Role" htmlFor={`${ids}-role`}>
          {roles.length > 0 ? (
            <Select value={role} onValueChange={setRole}>
              <SelectTrigger id={`${ids}-role`} className="h-8 font-mono text-xs">
                <SelectValue placeholder="Choose a role" />
              </SelectTrigger>
              <SelectContent>
                {roles.map((name) => (
                  <SelectItem key={name} value={name} className="font-mono text-xs">
                    {name}
                    {security.exempt.includes(name) ? " · not filtered" : ""}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          ) : (
            <Input
              id={`${ids}-role`}
              autoComplete="off"
              spellCheck={false}
              className="h-8 font-mono text-xs"
              value={role}
              onChange={(event) => setRole(event.target.value)}
            />
          )}
        </Field>
        <Field
          label="Settings it carries"
          hint="What current_setting reads in a policy, such as app.tenant_id. They last for the one query."
        >
          <div className="space-y-2">
            {claims.map((claim, index) => (
              <div key={claim.key} className="space-y-1">
                <div className="flex min-w-0 items-center gap-2">
                  <Input
                    aria-label={`Setting ${index + 1} name`}
                    aria-invalid={problems[claim.key] ? true : undefined}
                    autoComplete="off"
                    spellCheck={false}
                    placeholder="app.tenant_id"
                    className="h-8 min-w-0 flex-1 font-mono text-xs"
                    value={claim.name}
                    onChange={(event) => setClaim(claim.key, { name: event.target.value })}
                  />
                  <Input
                    aria-label={`Setting ${index + 1} value`}
                    autoComplete="off"
                    spellCheck={false}
                    placeholder="value"
                    className="h-8 min-w-0 flex-1 font-mono text-xs"
                    value={claim.value}
                    onChange={(event) => setClaim(claim.key, { value: event.target.value })}
                  />
                  <IconAction
                    label={`Remove setting ${index + 1}`}
                    className="size-8 shrink-0"
                    onClick={() => setClaims(claims.filter((one) => one.key !== claim.key))}
                  >
                    <Trash />
                  </IconAction>
                </div>
                {problems[claim.key] && (
                  <FormNote tone="danger" role="alert">
                    {problems[claim.key]}
                  </FormNote>
                )}
              </div>
            ))}
            <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1.5">
              <Button
                size="xs"
                variant="ghost"
                onClick={() => setClaims([...claims, blankClaim()])}
              >
                <Plus />
                Add a setting
              </Button>
              {unset.length > 0 && (
                <ChipStrip
                  role="group"
                  aria-label="Settings the policies read"
                  className="max-sm:mx-0 max-sm:px-0"
                >
                  {unset.map((name) => (
                    <FilterChip
                      key={name}
                      className="font-mono"
                      aria-label={`Add the setting ${name}`}
                      onClick={() => setClaims([...claims, blankClaim({ name })])}
                    >
                      {name}
                    </FilterChip>
                  ))}
                </ChipStrip>
              )}
            </div>
          </div>
        </Field>
      </div>
      <Field
        label="Query"
        htmlFor={`${ids}-query`}
        hint="One SELECT. It runs in a transaction that is rolled back, and the first hundred rows come back."
      >
        <Textarea
          id={`${ids}-query`}
          rows={3}
          autoComplete="off"
          spellCheck={false}
          className="font-mono text-xs"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
      </Field>
      <div className="flex items-center gap-3">
        <Button onClick={() => void run()} disabled={!body} pending={outcome?.status === "running"}>
          Run{role ? ` as ${role}` : ""}
        </Button>
        {!role && (
          <span className="text-hint text-muted-foreground">Choose the role to become.</span>
        )}
      </div>

      <div aria-live="polite" className="space-y-3">
        {outcome?.status === "running" && (
          <p className="flex text-body">
            <TextShimmer>{`Running as ${role}…`}</TextShimmer>
          </p>
        )}
        {outcome?.status === "failed" && (
          <FormNote tone="danger" role="alert" className="break-words">
            Nothing was run as {role}. {outcome.error}
          </FormNote>
        )}
        {outcome?.status === "done" && (
          <TestAnswer answer={outcome.answer} serial={outcome.serial} />
        )}
      </div>
    </section>
  )
}

function TestAnswer({ answer, serial }: { answer: DbRoleTestResult; serial: number }) {
  const { result } = answer
  if (!result) {
    return (
      <div className="space-y-3">
        {answer.notes.map((note) => (
          <FormNote key={note} tone="warning">
            {note}
          </FormNote>
        ))}
      </div>
    )
  }
  return (
    <div className="space-y-3">
      <p className="text-body">
        <span className="font-mono font-medium">{answer.role}</span> sees{" "}
        <span className="numeric font-medium">{plural(result.rowCount, "row")}</span>
        {result.truncated ? ", the first of more" : ""}
        <span className="text-muted-foreground"> · {result.duration}</span>
      </p>
      {answer.notes.map((note) => (
        <FormNote key={note} tone="warning">
          {note}
        </FormNote>
      ))}
      {result.columns.length > 0 && result.rowCount > 0 ? (
        <div className="flex h-72 min-h-0 flex-col overflow-hidden rounded-md border border-hairline">
          <ResultGrid
            label={`Rows ${answer.role} can see`}
            result={result}
            resultKey={`rls-${serial}`}
          />
        </div>
      ) : (
        <EmptyNote className="rounded-md border border-hairline">
          No row is visible to {answer.role}.
        </EmptyNote>
      )}
      <Statement
        label="Sent before the query"
        sql={answer.setup.join(";\n")}
        placeholder="Nothing."
      />
    </div>
  )
}
