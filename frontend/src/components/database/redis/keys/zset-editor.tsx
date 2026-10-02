"use client"

import { useState } from "react"
import { Copy, Pencil, Plus, Trash } from "@/components/icons"
import { ApiError, errorMessage } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { Field, FieldRow, FormFact } from "@/components/form"
import { IconAction, RowActions } from "@/components/icon-action"
import { EmptyNote } from "@/components/state"
import { FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { EngineMark } from "@/components/database/kit"
import { redisDelete, redisWrite } from "@/components/database/redis/api"
import { bytesId, bytesLabel, lineSafe, sameBytes } from "@/components/database/redis/bytes"
import { EditorStrip, FilterBox, matchOf } from "@/components/database/redis/keys/editor-parts"
import type { KeyEditorProps } from "@/components/database/redis/keys/key-pane"
import { MemberDialog } from "@/components/database/redis/keys/member-dialog"
import {
  InlineEdit,
  MemberTable,
  MemberText,
  type MemberColumn,
} from "@/components/database/redis/keys/member-table"
import { useMembers } from "@/components/database/redis/keys/members"
import { parseScore, scoreText } from "@/components/database/redis/keys/score"
import type { RedisBytes, RedisRow } from "@/components/database/redis/types"

type Draft = {
  /** The member being edited; absent for a new one. */
  was?: RedisBytes
  member: string
  score: string
}

/**
 * A sorted set: its members with their scores, from either end.
 *
 * A row is its member — two members may share a score, so the score names
 * nothing. A score is edited on its row and the row stays where it is until
 * the set is read again, so the thing just edited does not jump away from
 * under the pointer. With a filter over the names the server scans rather
 * than ranks, and the rows are in no order.
 */
export function ZsetEditor({ redis, name, meta, epoch, onChanged, confirm }: KeyEditorProps) {
  const { target, db, engine, canWrite, canDestroy } = redis
  const [filter, setFilter] = useState("")
  const [order, setOrder] = useState<"asc" | "desc">("asc")
  const [range, setRange] = useState({ min: "", max: "" })
  const [bounds, setBounds] = useState({ min: "", max: "" })
  const match = matchOf(filter)
  const ranged = !match && Boolean(bounds.min || bounds.max)
  const members = useMembers(
    target,
    name,
    match
      ? { match }
      : ranged
        ? { min: bounds.min || "-inf", max: bounds.max || "+inf" }
        : { order },
    epoch,
  )
  const rows = members.state?.rows ?? []

  const [draft, setDraft] = useState<Draft | null>(null)
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  const score = draft ? parseScore(draft.score) : null

  const edit = (next: Draft) => {
    setDraft(next)
    setRefused("")
    setOpen(true)
  }

  const save = async () => {
    if (!draft || score === null) return
    setBusy(true)
    setRefused("")
    try {
      const renamed = draft.was !== undefined && !sameBytes(draft.was, draft.member)
      await redisWrite(target, {
        key: name,
        type: "zset",
        value: draft.member,
        score,
        ...(renamed ? { replace: draft.was } : {}),
      })
      if (draft.was === undefined) {
        // A new member has a rank of its own: read the order again.
        members.reload()
      } else {
        members.patch((held) => ({
          ...held,
          rows: held.rows.map((row) =>
            sameBytes(row.value, draft.was) ? { value: draft.member, score } : row,
          ),
        }))
      }
      setOpen(false)
      setDraft(null)
      onChanged()
    } catch (err) {
      setRefused(
        err instanceof ApiError && err.code === "conflict"
          ? `${draft.member} is already a member. Change its score on its own row.`
          : errorMessage(err),
      )
    } finally {
      setBusy(false)
    }
  }

  const saveScore = async (row: RedisRow, text: string) => {
    const next = parseScore(text)
    if (next === null) throw new Error("A score is a number, inf or -inf.")
    await redisWrite(target, { key: name, type: "zset", value: row.value ?? "", score: next })
    members.patch((held) => ({
      ...held,
      rows: held.rows.map((other) =>
        sameBytes(other.value, row.value) ? { ...other, score: next } : other,
      ),
    }))
    onChanged()
  }

  const remove = (row: RedisRow) =>
    confirm({
      title: "Remove member",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: (
          <span className="font-mono">
            {bytesLabel(row.value ?? "").slice(0, 120) || "the empty member"}
          </span>
        ),
        facts: (
          <>
            <FormFact label="Score">{scoreText(row.score)}</FormFact>
            <FormFact label="Of" mono>
              {bytesLabel(name)}
            </FormFact>
            <FormFact label="In">db{db}</FormFact>
          </>
        ),
      },
      description:
        meta.length === 1
          ? "This is the sorted set's last member: removing it removes the key."
          : "The member and its score are removed from the sorted set.",
      confirmLabel: "Remove member",
      action: async () => {
        await redisDelete(target, { key: name, type: "zset", member: row.value ?? "" })
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
      cell: (row) => <MemberText value={row.value ?? ""} truncated={row.truncated} />,
    },
    {
      id: "score",
      label: "Score",
      width: "9rem",
      align: "right",
      word: "Score",
      cell: (row) =>
        canWrite ? (
          <InlineEdit
            value={scoreText(row.score)}
            align="right"
            label={`the score of ${bytesLabel(row.value ?? "").slice(0, 60) || "the empty member"}`}
            onSave={(next) => saveScore(row, next)}
          />
        ) : (
          <span className="numeric">{scoreText(row.score)}</span>
        ),
    },
  ]

  const applyRange = () => setBounds({ min: range.min.trim(), max: range.max.trim() })

  return (
    <>
      <EditorStrip>
        <FilterBox label="Filter members" placeholder="Filter members" onApply={setFilter} />
        {!match && (
          <>
            <div role="group" aria-label="Order by score" className="flex gap-0.5">
              <FilterChip
                selected={order === "asc" && !ranged}
                disabled={ranged}
                onClick={() => setOrder("asc")}
              >
                Lowest first
              </FilterChip>
              <FilterChip
                selected={order === "desc" && !ranged}
                disabled={ranged}
                onClick={() => setOrder("desc")}
              >
                Highest first
              </FilterChip>
            </div>
            <form
              className="flex items-center gap-1 text-hint text-muted-foreground"
              onSubmit={(event) => {
                event.preventDefault()
                applyRange()
              }}
            >
              <Input
                aria-label="Lowest score"
                placeholder="Min score"
                value={range.min}
                onChange={(event) => setRange({ ...range, min: event.target.value })}
                onBlur={applyRange}
                className="h-7 w-24 px-2 font-mono text-xs sm:h-7 sm:text-xs"
              />
              <span>to</span>
              <Input
                aria-label="Highest score"
                placeholder="Max score"
                value={range.max}
                onChange={(event) => setRange({ ...range, max: event.target.value })}
                onBlur={applyRange}
                className="h-7 w-24 px-2 font-mono text-xs sm:h-7 sm:text-xs"
              />
              <button type="submit" className="sr-only">
                Apply the score range
              </button>
            </form>
          </>
        )}
        <span className="min-w-0 flex-1" />
        {canWrite && (
          <Button
            size="xs"
            variant="outline"
            onClick={() =>
              edit(draft && draft.was === undefined ? draft : { member: "", score: "" })
            }
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
        filtered={Boolean(match) || ranged}
        empty={
          <EmptyNote>
            {match ? `No member matches ${filter}.` : "No member has a score in that range."}
          </EmptyNote>
        }
        actionsWidth="5rem"
        actions={(row) => {
          const label = bytesLabel(row.value ?? "").slice(0, 60) || "the empty member"
          return (
            <RowActions>
              <IconAction
                label={`Copy ${label}`}
                className="size-6"
                onClick={() => void copyText(bytesLabel(row.value ?? ""), "Member copied")}
              >
                <Copy />
              </IconAction>
              {canWrite && lineSafe(row.value) && !row.truncated && (
                <IconAction
                  label={`Rename ${label}`}
                  className="size-6"
                  onClick={() =>
                    edit({
                      was: row.value,
                      member: row.value as string,
                      score: scoreText(row.score),
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
          title={draft.was === undefined ? "Add member" : "Edit member"}
          name={name}
          type="zset"
          db={db}
          command={draft.was === undefined ? "Add member" : "Save member"}
          onSubmit={save}
          busy={busy}
          error={refused}
          disabled={score === null}
        >
          <FieldRow>
            <Field label="Member" htmlFor="redis-zset-member">
              <Input
                id="redis-zset-member"
                value={draft.member}
                spellCheck={false}
                autoComplete="off"
                className="font-mono"
                onChange={(event) => setDraft({ ...draft, member: event.target.value })}
              />
            </Field>
            <Field
              label="Score"
              htmlFor="redis-zset-score"
              hint="A number, inf or -inf."
              error={
                draft.score && score === null ? "A score is a number, inf or -inf." : undefined
              }
            >
              <Input
                id="redis-zset-score"
                value={draft.score}
                inputMode="decimal"
                autoComplete="off"
                className="font-mono"
                onChange={(event) => setDraft({ ...draft, score: event.target.value })}
              />
            </Field>
          </FieldRow>
        </MemberDialog>
      )}
    </>
  )
}
