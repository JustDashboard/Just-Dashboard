"use client"

import { useState } from "react"
import { Copy, Plus, Trash } from "@/components/icons"
import { ApiError, errorMessage } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { Field, FormFact } from "@/components/form"
import { IconAction, RowActions } from "@/components/icon-action"
import { EmptyNote } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { EngineMark } from "@/components/database/kit"
import { redisDelete, redisWrite } from "@/components/database/redis/api"
import { bytesId, bytesLabel, sameBytes } from "@/components/database/redis/bytes"
import { EditorStrip, FilterBox, matchOf } from "@/components/database/redis/keys/editor-parts"
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
import type { RedisRow } from "@/components/database/redis/types"

/**
 * A set: its members, in the order the server scans them.
 *
 * Editing a member swaps it for another in one step. If the new one is
 * already in the set there is nothing to swap for — that edit would only
 * remove the old member — so the server refuses it and the row says the
 * member is already there.
 */
export function SetEditor({ redis, name, meta, epoch, onChanged, confirm }: KeyEditorProps) {
  const { target, db, engine, canWrite, canDestroy } = redis
  const [filter, setFilter] = useState("")
  const members = useMembers(target, name, { match: matchOf(filter) }, epoch)
  const rows = members.state?.rows ?? []

  const [draft, setDraft] = useState<string | null>(null)
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")

  const add = async () => {
    if (draft === null) return
    setBusy(true)
    setRefused("")
    try {
      await redisWrite(target, { key: name, type: "set", value: draft })
      members.patch((held) =>
        held.rows.some((row) => sameBytes(row.value, draft))
          ? held
          : { rows: [...held.rows, { value: draft }], length: held.length + 1 },
      )
      setOpen(false)
      setDraft(null)
      onChanged()
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const swap = async (row: RedisRow, next: string) => {
    try {
      await redisWrite(target, { key: name, type: "set", value: next, replace: row.value ?? "" })
    } catch (err) {
      if (err instanceof ApiError && err.code === "conflict") {
        throw new Error(`${next} is already in the set. Remove this member instead.`)
      }
      throw err
    }
    members.patch((held) => ({
      ...held,
      rows: held.rows.map((other) =>
        sameBytes(other.value, row.value) ? { ...other, value: next } : other,
      ),
    }))
    onChanged()
  }

  const remove = (row: RedisRow) =>
    confirm({
      title: "Remove member",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{bytesLabel(row.value ?? "").slice(0, 120)}</span>,
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
          ? "This is the set's last member: removing it removes the key."
          : "The member is removed from the set.",
      confirmLabel: "Remove member",
      action: async () => {
        await redisDelete(target, { key: name, type: "set", member: row.value ?? "" })
        members.patch((held) => ({
          rows: held.rows.filter((other) => !sameBytes(other.value, row.value)),
          length: Math.max(held.length - 1, 0),
        }))
      },
      onDone: onChanged,
    })

  const columns: MemberColumn<RedisRow>[] = [
    {
      id: "member",
      label: "Member",
      width: "minmax(8rem,1fr)",
      cell: (row) =>
        canWrite && editableInline(row.value, row.truncated) ? (
          <InlineEdit
            value={row.value}
            label={`member ${row.value}`}
            onSave={(next) => swap(row, next)}
          />
        ) : (
          <MemberText value={row.value ?? ""} truncated={row.truncated} />
        ),
    },
  ]

  return (
    <>
      <EditorStrip>
        <FilterBox label="Filter members" placeholder="Filter members" onApply={setFilter} />
        <span className="min-w-0 flex-1" />
        {canWrite && (
          <Button
            size="xs"
            variant="outline"
            onClick={() => {
              setDraft(draft ?? "")
              setRefused("")
              setOpen(true)
            }}
          >
            <Plus />
            Add member
          </Button>
        )}
      </EditorStrip>
      <MemberTable
        label={`Members of ${bytesLabel(name)}`}
        columns={columns}
        rows={rows}
        rowId={(row) => bytesId(row.value ?? "")}
        paged={members}
        total={members.state?.length ?? meta.length}
        noun="member"
        nouns="members"
        filtered={Boolean(filter)}
        empty={<EmptyNote>No member matches {filter}.</EmptyNote>}
        actionsWidth="3.5rem"
        actions={(row) => {
          const label = bytesLabel(row.value ?? "").slice(0, 60)
          return (
            <RowActions>
              <IconAction
                label={`Copy ${label}`}
                className="size-6"
                onClick={() => void copyText(bytesLabel(row.value ?? ""), "Member copied")}
              >
                <Copy />
              </IconAction>
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
      {draft !== null && (
        <MemberDialog
          open={open}
          onOpenChange={setOpen}
          onCancel={() => {
            setOpen(false)
            setDraft(null)
          }}
          title="Add member"
          name={name}
          type="set"
          db={db}
          command="Add member"
          onSubmit={add}
          busy={busy}
          error={refused}
        >
          <Field
            label="Member"
            htmlFor="redis-set-member"
            hint="A member that is already in the set changes nothing."
          >
            <Textarea
              id="redis-set-member"
              value={draft}
              spellCheck={false}
              className="max-h-80 min-h-20 font-mono text-xs sm:text-xs"
              onChange={(event) => setDraft(event.target.value)}
            />
          </Field>
        </MemberDialog>
      )}
    </>
  )
}
