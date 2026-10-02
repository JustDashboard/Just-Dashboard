"use client"

import { useState } from "react"
import { Copy, Download, Pencil, Plus, Trash } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { Field, FormFact, FormNote } from "@/components/form"
import { IconAction, RowActions } from "@/components/icon-action"
import { EmptyNote } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { EngineMark } from "@/components/database/kit"
import {
  redisDelete,
  redisMembers,
  redisRawUrl,
  redisRun,
  redisWrite,
  replyCodes,
} from "@/components/database/redis/api"
import {
  boxSafe,
  bytesId,
  bytesLabel,
  globEscape,
  lineSafe,
  sameBytes,
} from "@/components/database/redis/bytes"
import {
  EditorStrip,
  FilterBox,
  matchOf,
  saveFrom,
} from "@/components/database/redis/keys/editor-parts"
import type { KeyEditorProps } from "@/components/database/redis/keys/key-pane"
import { MemberDialog } from "@/components/database/redis/keys/member-dialog"
import {
  InlineEdit,
  MemberTable,
  MemberText,
  editableInline,
  type MemberColumn,
} from "@/components/database/redis/keys/member-table"
import { useMembers } from "@/components/database/redis/keys/members"
import { TtlControl, type TtlWhen } from "@/components/database/redis/keys/ttl-editor"
import { ttlWord } from "@/components/database/redis/ttl"
import type { RedisBytes, RedisRow } from "@/components/database/redis/types"

type Draft = {
  /** The field being edited; absent for a new one. */
  was?: RedisBytes
  field: string
  value: string
}

/**
 * A hash: its fields and their values, a page at a time.
 *
 * A value is edited on its row; the dialog is for a new field, a long value,
 * or a field's name — which is changed in one step on the server, so a
 * rename never leaves the old field beside the new one. A value that is not
 * text, or that the server sent cut, is shown for what it is and is not
 * offered to a text box: writing a cut value back would replace the whole
 * with its own beginning.
 *
 * Where the server gives a field an expiry of its own, each row states its
 * field's and is where it is changed. There is no route for it, so the change
 * is the server's own command, sent through the console's route: classified,
 * capability-checked and audited as a line typed there would be.
 */
export function HashEditor({ redis, name, meta, epoch, onChanged, confirm }: KeyEditorProps) {
  const { target, db, engine, canWrite, canDestroy } = redis
  const [filter, setFilter] = useState("")
  const members = useMembers(target, name, { match: matchOf(filter) }, epoch)
  const rows = members.state?.rows ?? []
  // The server says a field's expiry on every row where it has such a thing.
  // The column is drawn where there is one to read, or one could be set.
  const fieldTtl =
    rows.some((row) => row.ttl !== undefined && row.ttl >= 0) ||
    (canWrite && rows.some((row) => row.ttl !== undefined))

  const [draft, setDraft] = useState<Draft | null>(null)
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  /** A new field's name that is already taken, which the next save may replace. */
  const [taken, setTaken] = useState("")

  const edit = (next: Draft) => {
    setDraft(next)
    setRefused("")
    setTaken("")
    setOpen(true)
  }

  const save = async () => {
    if (!draft) return
    setBusy(true)
    setRefused("")
    try {
      const renamed = draft.was !== undefined && !sameBytes(draft.was, draft.field)
      if (draft.was === undefined && taken !== draft.field) {
        // HSET over a field that exists replaces its value without a word.
        const there = await redisMembers(target, name, {
          match: globEscape(draft.field),
          count: 10,
        })
        if (there.rows.some((row) => sameBytes(row.field, draft.field))) {
          setTaken(draft.field)
          return
        }
      }
      await redisWrite(target, {
        key: name,
        type: "hash",
        field: draft.field,
        value: draft.value,
        ...(renamed ? { replace: draft.was } : {}),
      })
      const written: RedisRow = { field: draft.field, value: draft.value }
      members.patch((held) => {
        // The row the write was about takes the new reading where it stands;
        // a field that was not listed yet goes last.
        const target = draft.was ?? draft.field
        const listed = held.rows.some((row) => sameBytes(row.field, target))
        const next = listed
          ? held.rows.map((row) => (sameBytes(row.field, target) ? written : row))
          : [...held.rows, written]
        return { rows: next, length: held.length + (listed ? 0 : 1) }
      })
      setOpen(false)
      setDraft(null)
      onChanged()
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const saveValue = async (row: RedisRow, value: string) => {
    await redisWrite(target, { key: name, type: "hash", field: row.field ?? "", value })
    members.patch((held) => ({
      ...held,
      rows: held.rows.map((other) =>
        sameBytes(other.field, row.field) ? { ...other, value } : other,
      ),
    }))
    onChanged()
  }

  const setTtl = (row: RedisRow, ttl: number) =>
    members.patch((held) => ({
      ...held,
      rows: held.rows.map((other) =>
        sameBytes(other.field, row.field) ? { ...other, ttl } : other,
      ),
    }))

  /** One field's expiry, set: the server answers a code for the field it was asked about. */
  const expireField = async (row: RedisRow, when: TtlWhen) => {
    const field = row.field ?? ""
    const [code] = replyCodes(
      "seconds" in when
        ? await redisRun(target, "HEXPIRE", [name, when.seconds, "FIELDS", 1, field])
        : await redisRun(target, "HPEXPIREAT", [name, when.at, "FIELDS", 1, field]),
    )
    if (code === -2) throw new Error("The field is not in the hash any more.")
    if (code !== 1) throw new Error("The server did not set the expiry.")
    const pttl = "seconds" in when ? when.seconds * 1000 : when.at - Date.now()
    setTtl(row, Math.ceil(pttl / 1000))
    return pttl
  }

  const persistField = async (row: RedisRow) => {
    const [code] = replyCodes(
      await redisRun(target, "HPERSIST", [name, "FIELDS", 1, row.field ?? ""]),
    )
    if (code === -2) throw new Error("The field is not in the hash any more.")
    setTtl(row, -1)
  }

  const remove = (row: RedisRow) =>
    confirm({
      title: "Remove field",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{bytesLabel(row.field ?? "")}</span>,
        facts: (
          <>
            <FormFact label="Of" mono>
              {bytesLabel(name)}
            </FormFact>
            <FormFact label="In">db{db}</FormFact>
          </>
        ),
      },
      description:
        meta.length === 1
          ? "This is the hash's last field: removing it removes the key."
          : "The field and its value are removed from the hash.",
      confirmLabel: "Remove field",
      action: async () => {
        // `member` present, even empty, is a request about one field; it
        // never falls through to removing the key.
        await redisDelete(target, { key: name, type: "hash", member: row.field ?? "" })
        members.patch((held) => ({
          rows: held.rows.filter((other) => !sameBytes(other.field, row.field)),
          length: Math.max(held.length - 1, 0),
        }))
      },
      onDone: onChanged,
    })

  const columns: MemberColumn<RedisRow>[] = [
    {
      id: "field",
      label: "Field",
      width: "minmax(7rem,2fr)",
      cell: (row) => <MemberText value={row.field ?? ""} />,
    },
    {
      id: "value",
      label: "Value",
      width: "minmax(8rem,5fr)",
      cell: (row) =>
        canWrite && editableInline(row.value, row.truncated) ? (
          <InlineEdit
            value={row.value}
            label={`the value of ${bytesLabel(row.field ?? "") || "the empty field"}`}
            onSave={(next) => saveValue(row, next)}
          />
        ) : (
          <MemberText value={row.value ?? ""} truncated={row.truncated} />
        ),
    },
    ...(fieldTtl
      ? [
          {
            id: "ttl",
            label: "Expires in",
            width: "5.5rem",
            align: "right" as const,
            // Said only for a field that does expire: on a hash where none
            // does, a column of the words would be the loudest thing in it.
            word: (row: RedisRow) =>
              row.ttl !== undefined && row.ttl >= 0 ? "Expires in" : undefined,
            cell: (row: RedisRow) => {
              const expires = row.ttl !== undefined && row.ttl >= 0
              const reading = expires ? ttlWord(row.ttl ?? 0) : "—"
              if (!canWrite || row.ttl === undefined) {
                return <span className="numeric text-muted-foreground">{reading}</span>
              }
              return (
                <TtlControl
                  quiet
                  reading={reading}
                  label={`Expiry of ${bytesLabel(row.field ?? "") || "the empty field"}`}
                  subject="field"
                  expires={expires}
                  onChanged={onChanged}
                  write={{
                    set: (when) => expireField(row, when),
                    remove: () => persistField(row),
                  }}
                />
              )
            },
          },
        ]
      : []),
  ]

  return (
    <>
      <EditorStrip>
        <FilterBox label="Filter fields" placeholder="Filter fields" onApply={setFilter} />
        <span className="min-w-0 flex-1" />
        {canWrite && (
          <Button
            size="xs"
            variant="outline"
            onClick={() =>
              edit(draft?.was === undefined && draft ? draft : { field: "", value: "" })
            }
          >
            <Plus />
            Add field
          </Button>
        )}
      </EditorStrip>
      <MemberTable
        label={`Fields of ${bytesLabel(name)}`}
        columns={columns}
        rows={rows}
        rowId={(row) => bytesId(row.field ?? "")}
        paged={members}
        total={members.state?.length ?? meta.length}
        noun="field"
        nouns="fields"
        filtered={Boolean(filter)}
        empty={<EmptyNote>No field matches {filter}.</EmptyNote>}
        actionsWidth="5rem"
        actions={(row) => {
          // A control named "Remove" and nothing else says nothing: the field
          // whose name is empty is named for what it is.
          const label = bytesLabel(row.field ?? "") || "the empty field"
          // The dialog is a field and a text box: only what both hand back
          // unchanged is put in them.
          const text = boxSafe(row.value) && !row.truncated
          return (
            <RowActions>
              {row.truncated && engine.can("valueDownload") ? (
                <IconAction
                  label={`Download the whole value of ${label}`}
                  className="size-6"
                  onClick={() => saveFrom(redisRawUrl(target, name, { field: row.field ?? "" }))}
                >
                  <Download />
                </IconAction>
              ) : (
                <IconAction
                  label={`Copy the value of ${label}`}
                  className="size-6"
                  onClick={() => void copyText(bytesLabel(row.value ?? ""), "Value copied")}
                >
                  <Copy />
                </IconAction>
              )}
              {canWrite && text && lineSafe(row.field) && (
                <IconAction
                  label={`Edit ${label}`}
                  className="size-6"
                  onClick={() =>
                    edit({ was: row.field, field: row.field as string, value: row.value as string })
                  }
                >
                  <Pencil />
                </IconAction>
              )}
              {canDestroy && (
                <IconAction
                  label={`Remove ${label}`}
                  className="size-6"
                  onClick={() => remove(row)}
                >
                  <Trash />
                </IconAction>
              )}
            </RowActions>
          )
        }}
      />
      {draft && (
        <MemberDialog
          open={open}
          onOpenChange={setOpen}
          onCancel={() => {
            setOpen(false)
            setDraft(null)
          }}
          title={draft.was === undefined ? "Add field" : "Edit field"}
          name={name}
          type="hash"
          db={db}
          command={
            taken === draft.field && draft.was === undefined ? "Replace its value" : "Save field"
          }
          onSubmit={save}
          busy={busy}
          error={refused}
        >
          <Field label="Field" htmlFor="redis-hash-field">
            <Input
              id="redis-hash-field"
              value={draft.field}
              spellCheck={false}
              autoComplete="off"
              className="font-mono"
              onChange={(event) => setDraft({ ...draft, field: event.target.value })}
            />
          </Field>
          <Field label="Value" htmlFor="redis-hash-value">
            <Textarea
              id="redis-hash-value"
              value={draft.value}
              spellCheck={false}
              className="max-h-80 min-h-28 font-mono text-xs sm:text-xs"
              onChange={(event) => setDraft({ ...draft, value: event.target.value })}
            />
          </Field>
          {taken === draft.field && draft.was === undefined && (
            <FormNote tone="warning" role="status">
              The hash already has a field named {draft.field || "(empty)"}. Saving replaces its
              value.
            </FormNote>
          )}
        </MemberDialog>
      )}
    </>
  )
}
