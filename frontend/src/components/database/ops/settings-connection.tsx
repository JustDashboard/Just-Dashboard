"use client"

import { useId, useState } from "react"
import Link from "next/link"
import { errorMessage, put } from "@/lib/api"
import { calendarDate, timestamp } from "@/lib/format"
import type { DbConnection } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { Field, FormNote, OptionList, OptionRow } from "@/components/form"
import { Detail, DetailList } from "@/components/page"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import {
  EnvironmentField,
  environmentProblem,
} from "@/components/database/connect/environment-field"
import { CONNECTION_NAME } from "@/components/database/connect/rules"
import { EngineGlyph, EnvironmentTag, ProtectedTag } from "@/components/database/kit"
import {
  connectionChanges,
  draftOf,
  notesProblem,
  type ConnectionDraft,
} from "@/components/database/ops/settings-model"
import { usePendingReport } from "@/components/database/ops/settings-pending"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"
import { supportFact } from "@/components/database/support"

/** The fields of the labels form, as `connectionChanges` names them, and as the strip does. */
const LABELS = ["name", "environment", "notes"] as const
const LABEL_WORDS = { name: "Name", environment: "Environment", notes: "Notes" } as const

/**
 * What the dashboard keeps about the connection: what it calls it, what it
 * is for, notes, and whether it may change what is in it.
 *
 * One form and one write over two sections, the way a project's Runtime is
 * one form over five: the labels and the protection are the same `PUT`, and
 * it sends only what changed. The form draws no Save of its own — the page's
 * bar carries it, with its count, from the first keystroke.
 */
export function ConnectionForm() {
  const { id, conn, engine, summary, status, href } = useDatabase()
  const { connections, refresh } = useDatabases()
  const { can } = useAuth()
  const admin = can("system.admin")
  const field = useId()
  const saved = draftOf(conn)
  const [edits, setEdits] = useState<Partial<ConnectionDraft>>({})
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<string>()

  const draft: ConnectionDraft = { ...saved, ...edits }
  const changes = connectionChanges(saved, draft)
  const count = Object.keys(changes).length
  const labelsEdited = LABELS.some((key) => key in changes)
  const protectionEdited = "readOnly" in changes
  const set = (patch: Partial<ConnectionDraft>) => {
    setEdits((held) => ({ ...held, ...patch }))
    setRefusal(undefined)
  }

  const typedName = draft.name.trim()
  const nameProblem = !typedName
    ? "A connection needs a name."
    : !CONNECTION_NAME.test(typedName) && typedName !== saved.name
      ? "Letters, digits, spaces, dots, dashes and underscores, starting with a letter or digit."
      : connections.some(
            (other) => other.id !== id && other.name.toLowerCase() === typedName.toLowerCase(),
          )
        ? "Another connection already has this name."
        : undefined
  const invalid =
    Boolean(nameProblem) ||
    Boolean(environmentProblem(draft.environment)) ||
    Boolean(notesProblem(draft.notes))

  const save = async () => {
    if (count === 0) return true
    if (invalid) return false
    setSaving(true)
    setRefusal(undefined)
    try {
      await put<DbConnection>(`/databases/${id}`, changes)
      setEdits({})
      refresh()
      status.refresh()
      return true
    } catch (err) {
      setRefusal(errorMessage(err))
      return false
    } finally {
      setSaving(false)
    }
  }

  // What the connection is, under its name: the product that answered, until
  // when its release is maintained, and when the dashboard was given it.
  const what = (
    <span className="flex min-w-0 flex-wrap items-center gap-x-1.5">
      <EngineGlyph engine={engine} className="size-3.5" />
      <span>
        {engine.label}
        {summary?.versionNumber ? ` ${summary.versionNumber}` : ""}
      </span>
      {summary?.endOfLife && (
        <>
          <span aria-hidden>·</span>
          <span>{supportFact(summary.endOfLife)}</span>
        </>
      )}
      <span aria-hidden>·</span>
      <span title={timestamp(conn.createdAt)}>added {calendarDate(conn.createdAt)}</span>
    </span>
  )
  const protection = conn.readOnly
    ? "On — every change to its data or schema made here is refused"
    : "Off — its data and schema can be changed from here"
  // One line. That a statement calling a function which writes still passes is
  // what "not a lock in the engine" means, and the line says what to do about it.
  const lock = (
    <FormNote>
      The dashboard reads each request; it is not a lock in the engine. For one, sign in as an
      account that cannot write
      {engine.has("access") ? (
        <>
          , made under{" "}
          <Link href={href("access")} className="rounded-sm underline focus-ring">
            Access
          </Link>
          .
        </>
      ) : (
        "."
      )}
    </FormNote>
  )
  usePendingReport("labels", {
    edits: [
      ...LABELS.filter((key) => key in changes).map((key) => ({
        name: LABEL_WORDS[key],
        state: "changed",
      })),
      ...(protectionEdited ? [{ name: "Protection", state: "changed" }] : []),
    ],
  })

  if (!admin) {
    return (
      <>
        <SettingSection id="connection" title="Name and labels" state={what}>
          <DetailList className="gap-y-2.5">
            <Detail label="Name">{conn.name}</Detail>
            <Detail label="Environment">
              {conn.environment ? (
                <span className="flex">
                  <EnvironmentTag environment={conn.environment} />
                </span>
              ) : (
                <span className="text-muted-foreground">None</span>
              )}
            </Detail>
            {conn.notes && (
              <Detail label="Notes" className="whitespace-pre-wrap">
                {conn.notes}
              </Detail>
            )}
          </DetailList>
        </SettingSection>
        <SettingSection id="protection" title="Protection" state={protection}>
          {conn.readOnly && (
            <span className="flex">
              <ProtectedTag />
            </span>
          )}
          {lock}
        </SettingSection>
      </>
    )
  }

  return (
    <SettingForm
      name="Connection labels"
      onSave={save}
      dirty={count > 0}
      changes={count}
      saving={saving}
      invalid={invalid}
      canEdit={admin}
      applies="immediately"
      onDiscard={() => {
        setEdits({})
        setRefusal(undefined)
      }}
      error={refusal && `It was not saved. ${refusal}`}
    >
      <SettingSection
        id="connection"
        title="Name and labels"
        state={what}
        status={settingStatus({ dirty: labelsEdited, refused: labelsEdited && Boolean(refusal) })}
      >
        <Field
          label="Name"
          htmlFor={`${field}-name`}
          info="How it is listed in the rail, the control center and the map."
          error={nameProblem}
        >
          <Input
            id={`${field}-name`}
            value={draft.name}
            onChange={(event) => set({ name: event.target.value })}
            autoComplete="off"
            maxLength={64}
          />
        </Field>
        <EnvironmentField
          id={`${field}-environment`}
          value={draft.environment}
          onChange={(environment) => set({ environment })}
          tip
        />
        <Field
          label="Notes"
          htmlFor={`${field}-notes`}
          info="Kept by the dashboard only; the server never sees them."
          error={notesProblem(draft.notes)}
        >
          <Textarea
            id={`${field}-notes`}
            value={draft.notes}
            onChange={(event) => set({ notes: event.target.value })}
            placeholder="Who owns it, what reads it, when it can be restarted"
            rows={3}
          />
        </Field>
      </SettingSection>
      <SettingSection
        id="protection"
        title="Protection"
        state={protection}
        status={settingStatus({
          dirty: protectionEdited,
          refused: protectionEdited && Boolean(refusal),
        })}
      >
        <OptionList>
          <OptionRow
            title="Refuse every change to its data or schema made from this dashboard"
            hint="For every role, on every page. Dumps are still taken."
            checked={draft.readOnly}
            onCheckedChange={(readOnly) => set({ readOnly })}
          />
        </OptionList>
        {lock}
      </SettingSection>
    </SettingForm>
  )
}
