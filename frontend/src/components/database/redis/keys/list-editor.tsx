"use client"

import { useState } from "react"
import { Copy, Download, Pencil, Plus, Trash } from "@/components/icons"
import { ApiError, errorMessage } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { Segments } from "@/components/deploy/settings/segments"
import { Field, FormFact } from "@/components/form"
import { IconAction, RowActions } from "@/components/icon-action"
import { EmptyNote } from "@/components/state"
import { FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { EngineMark } from "@/components/database/kit"
import { redisDelete, redisRawUrl, redisWrite } from "@/components/database/redis/api"
import { boxSafe, bytesLabel } from "@/components/database/redis/bytes"
import { EditorStrip, saveFrom } from "@/components/database/redis/keys/editor-parts"
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
import type { RedisBytes, RedisRow } from "@/components/database/redis/types"

type Where = "head" | "tail" | "before"

type Draft = {
  /** The element being replaced: its position and what was there. Absent for a new one. */
  at?: { index: number; was: RedisBytes }
  value: string
  where: Where
  /** The position a new element goes before, as typed. */
  before: string
  /** What the reader saw at that position, where it was on screen. */
  expect?: RedisBytes
}

const NEW: Draft = { value: "", where: "tail", before: "" }

/**
 * A list: its elements by position, from either end.
 *
 * A position names an element only until somebody pushes or pops, so every
 * change by position carries what the reader saw there. If the list moved in
 * between the server refuses, the list is read again and nothing was written
 * over the wrong element. Adding at a position inserts; it never replaces.
 */
export function ListEditor({ redis, name, meta, epoch, onChanged, confirm }: KeyEditorProps) {
  const { target, db, engine, canWrite, canDestroy } = redis
  const [order, setOrder] = useState<"asc" | "desc">("asc")
  const members = useMembers(target, name, { order }, epoch)
  const rows = members.state?.rows ?? []

  const [draft, setDraft] = useState<Draft | null>(null)
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")

  const edit = (next: Draft) => {
    setDraft(next)
    setRefused("")
    setOpen(true)
  }

  /** The list is not what was on screen: say so, and read it again. */
  const moved = (err: unknown) => {
    if (!(err instanceof ApiError) || err.code !== "conflict") return false
    members.reload()
    onChanged()
    return true
  }

  const position = draft?.where === "before" ? Number(draft.before) : undefined
  const badPosition =
    draft?.where === "before" &&
    (!/^\d+$/.test(draft.before.trim()) || Number(draft.before) > meta.length)

  const save = async () => {
    if (!draft || badPosition) return
    setBusy(true)
    setRefused("")
    try {
      if (draft.at) {
        await redisWrite(target, {
          key: name,
          type: "list",
          index: draft.at.index,
          value: draft.value,
          expect: draft.at.was,
        })
        const at = draft.at.index
        members.patch((held) => ({
          ...held,
          rows: held.rows.map((row) => (row.index === at ? { ...row, value: draft.value } : row)),
        }))
      } else {
        const seen = rows.find((row) => row.index === position)?.value
        await redisWrite(
          target,
          draft.where === "before"
            ? {
                key: name,
                type: "list",
                index: position,
                insert: true,
                value: draft.value,
                ...(seen !== undefined ? { expect: draft.expect ?? seen } : {}),
              }
            : { key: name, type: "list", value: draft.value, position: draft.where },
        )
        // Every position after the new element moved by one.
        members.reload()
      }
      setOpen(false)
      setDraft(null)
      onChanged()
    } catch (err) {
      setRefused(
        moved(err)
          ? "The list changed since it was read, so nothing was written. It has been read again."
          : errorMessage(err),
      )
    } finally {
      setBusy(false)
    }
  }

  const saveValue = async (row: RedisRow, value: string) => {
    try {
      await redisWrite(target, {
        key: name,
        type: "list",
        index: row.index,
        value,
        expect: row.value ?? "",
      })
    } catch (err) {
      if (moved(err)) {
        throw new Error("The list changed since it was read. It has been read again.")
      }
      throw err
    }
    members.patch((held) => ({
      ...held,
      rows: held.rows.map((other) => (other.index === row.index ? { ...other, value } : other)),
    }))
    onChanged()
  }

  const remove = (row: RedisRow) =>
    confirm({
      title: "Remove element",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{bytesLabel(row.value ?? "").slice(0, 120)}</span>,
        facts: (
          <>
            <FormFact label="Position">{row.index}</FormFact>
            <FormFact label="Of" mono>
              {bytesLabel(name)}
            </FormFact>
            <FormFact label="In">db{db}</FormFact>
          </>
        ),
      },
      description:
        meta.length === 1
          ? "This is the list's last element: removing it removes the key."
          : "The element is removed and the ones after it move up by one.",
      confirmLabel: "Remove element",
      action: async () => {
        try {
          await redisDelete(target, {
            key: name,
            type: "list",
            index: row.index,
            expect: row.value ?? "",
          })
        } catch (err) {
          if (moved(err)) {
            throw new ApiError(
              409,
              "conflict",
              "The list changed since it was read, so nothing was removed. It has been read again.",
            )
          }
          throw err
        }
        members.reload()
      },
      onDone: onChanged,
    })

  const columns: MemberColumn<RedisRow>[] = [
    {
      id: "index",
      label: "Position",
      width: "4.5rem",
      align: "right",
      cell: (row) => (
        <span className="numeric text-muted-foreground">{row.index?.toLocaleString()}</span>
      ),
    },
    {
      id: "value",
      label: "Element",
      width: "minmax(8rem,1fr)",
      cell: (row) =>
        canWrite && editableInline(row.value, row.truncated) ? (
          <InlineEdit
            value={row.value}
            label={`element ${row.index}`}
            onSave={(next) => saveValue(row, next)}
          />
        ) : (
          <MemberText value={row.value ?? ""} truncated={row.truncated} />
        ),
    },
  ]

  return (
    <>
      <EditorStrip>
        <div role="group" aria-label="Which end the list is read from" className="flex gap-0.5">
          <FilterChip selected={order === "asc"} onClick={() => setOrder("asc")}>
            From the head
          </FilterChip>
          <FilterChip selected={order === "desc"} onClick={() => setOrder("desc")}>
            From the tail
          </FilterChip>
        </div>
        <span className="min-w-0 flex-1" />
        {canWrite && (
          <Button
            size="xs"
            variant="outline"
            onClick={() => edit(draft && !draft.at ? draft : NEW)}
          >
            <Plus />
            Add element
          </Button>
        )}
      </EditorStrip>
      <MemberTable
        label={`Elements of ${bytesLabel(name)}`}
        columns={columns}
        rows={rows}
        rowId={(row) => String(row.index)}
        paged={members}
        total={members.state?.length ?? meta.length}
        noun="element"
        nouns="elements"
        empty={<EmptyNote>The list is empty.</EmptyNote>}
        actionsWidth="6.5rem"
        actions={(row) => {
          const label = `element ${row.index}`
          return (
            <RowActions>
              {row.truncated && engine.can("valueDownload") ? (
                <IconAction
                  label={`Download the whole of ${label}`}
                  className="size-6"
                  onClick={() => saveFrom(redisRawUrl(target, name, { index: row.index }))}
                >
                  <Download />
                </IconAction>
              ) : (
                <IconAction
                  label={`Copy ${label}`}
                  className="size-6"
                  onClick={() => void copyText(bytesLabel(row.value ?? ""), "Element copied")}
                >
                  <Copy />
                </IconAction>
              )}
              {canWrite && (
                <IconAction
                  label={`Insert an element before ${label}`}
                  className="size-6"
                  onClick={() =>
                    edit({
                      value: "",
                      where: "before",
                      before: String(row.index),
                      expect: row.value,
                    })
                  }
                >
                  <Plus />
                </IconAction>
              )}
              {canWrite && boxSafe(row.value) && !row.truncated && (
                <IconAction
                  label={`Edit ${label}`}
                  className="size-6"
                  onClick={() =>
                    edit({
                      at: { index: row.index ?? 0, was: row.value ?? "" },
                      value: row.value as string,
                      where: "tail",
                      before: "",
                    })
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
          title={draft.at ? `Edit element ${draft.at.index}` : "Add element"}
          name={name}
          type="list"
          db={db}
          command={draft.at ? "Save element" : "Add element"}
          onSubmit={save}
          busy={busy}
          error={refused}
          disabled={badPosition}
        >
          <Field label="Element" htmlFor="redis-list-value">
            <Textarea
              id="redis-list-value"
              value={draft.value}
              spellCheck={false}
              className="max-h-80 min-h-24 font-mono text-xs sm:text-xs"
              onChange={(event) => setDraft({ ...draft, value: event.target.value })}
            />
          </Field>
          {!draft.at && (
            <Field label="Where it goes">
              <Segments
                label="Where the element goes"
                fill
                value={draft.where}
                onChange={(where) => setDraft({ ...draft, where })}
                options={[
                  { value: "head", label: "At the head" },
                  { value: "tail", label: "At the tail" },
                  { value: "before", label: "Before a position" },
                ]}
              />
            </Field>
          )}
          {!draft.at && draft.where === "before" && (
            <Field
              label="Position"
              htmlFor="redis-list-position"
              hint={`0 is the head; ${meta.length.toLocaleString()} adds after the last. Nothing is replaced: the elements from there on move down.`}
              error={
                badPosition && draft.before
                  ? `A whole number from 0 to ${meta.length.toLocaleString()}.`
                  : undefined
              }
            >
              <Input
                id="redis-list-position"
                inputMode="numeric"
                value={draft.before}
                className="font-mono"
                onChange={(event) =>
                  setDraft({ ...draft, before: event.target.value, expect: undefined })
                }
              />
            </Field>
          )}
        </MemberDialog>
      )}
    </>
  )
}
