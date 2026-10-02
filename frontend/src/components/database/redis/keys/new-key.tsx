"use client"

import { useState } from "react"
import { Minus, Plus } from "@/components/icons"
import { ApiError, errorMessage } from "@/lib/api"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { Field, FieldRow } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { redisWrite } from "@/components/database/redis/api"
import { parseJsonDoc } from "@/components/database/redis/keys/json-doc"
import { MemberDialog } from "@/components/database/redis/keys/member-dialog"
import { parseScore } from "@/components/database/redis/keys/score"
import {
  KIND_ORDER,
  KindMark,
  REDIS_KINDS,
  type RedisKind,
} from "@/components/database/redis/kinds"
import { parseTtl } from "@/components/database/redis/ttl"
import type {
  RedisBytes,
  RedisWriteRequest,
  RedisWriteType,
} from "@/components/database/redis/types"
import type { Redis } from "@/components/database/redis/use-redis"

/** What each type is for, in the line under its name. */
const HINT: Record<Exclude<RedisKind, "other">, string> = {
  string: "One value: text, a number, or bytes.",
  hash: "Named fields, each with a value.",
  list: "Elements in order, pushed and popped at either end.",
  set: "Distinct members, in no order.",
  zset: "Distinct members, each ranked by a score.",
  stream: "An append-only log of entries.",
  json: "A document, addressed by path.",
}

type Draft = {
  kind: Exclude<RedisKind, "other">
  name: string
  ttl: string
  value: string
  field: string
  score: string
  pairs: { field: string; value: string }[]
}

const EMPTY: Draft = {
  kind: "string",
  name: "",
  ttl: "",
  value: "",
  field: "",
  score: "0",
  pairs: [{ field: "", value: "" }],
}

/**
 * A new key: its type first, then what that type needs to exist.
 *
 * A collection has no empty form in Redis — it is made by its first member —
 * so each type asks for exactly that: a string its value, a hash its first
 * field, a stream its first entry. The key is created only if the name is
 * free; a name that is taken is refused and nothing is written over.
 */
export function NewKeyDialog({
  redis,
  open,
  onOpenChange,
  onCreated,
}: {
  redis: Redis
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (key: RedisBytes) => void
}) {
  const { target, db, server } = redis
  const [draft, setDraft] = useState<Draft>(EMPTY)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  const [taken, setTaken] = useState("")
  const set = (change: Partial<Draft>) => setDraft((held) => ({ ...held, ...change }))

  const kinds = KIND_ORDER.filter(
    (kind): kind is Draft["kind"] =>
      kind !== "other" &&
      (kind !== "json" || (redis.engine.can("json") && Boolean(server.data?.features.json))) &&
      (kind !== "stream" || redis.engine.can("streams")),
  )
  const ttl = draft.ttl.trim() === "" ? null : parseTtl(draft.ttl)
  const score = parseScore(draft.score)
  const pairs = draft.pairs.filter((pair) => pair.field !== "" || pair.value !== "")
  const invalid =
    draft.name === "" ||
    (ttl !== null && !ttl.ok) ||
    (draft.kind === "zset" && score === null) ||
    (draft.kind === "stream" && pairs.length === 0) ||
    (draft.kind === "json" && !parseJsonDoc(draft.value))

  const body = (): RedisWriteRequest => {
    const base = {
      key: draft.name,
      type: draft.kind as RedisWriteType,
      create: true,
      ...(ttl?.ok ? { ttl: ttl.seconds } : {}),
    }
    switch (draft.kind) {
      case "hash":
        return { ...base, field: draft.field, value: draft.value }
      case "zset":
        return { ...base, value: draft.value, score: score ?? 0 }
      case "stream":
        return { ...base, entries: pairs.map((pair) => [pair.field, pair.value]) }
      default:
        return { ...base, value: draft.value }
    }
  }

  const create = async () => {
    setBusy(true)
    setRefused("")
    setTaken("")
    try {
      await redisWrite(target, body())
      const made = draft.name
      onOpenChange(false)
      setDraft(EMPTY)
      onCreated(made)
    } catch (err) {
      if (err instanceof ApiError && err.code === "key_exists") setTaken(draft.name)
      else setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const valueLabel =
    draft.kind === "string"
      ? "Value"
      : draft.kind === "hash"
        ? "Its value"
        : draft.kind === "list"
          ? "First element"
          : draft.kind === "json"
            ? "Document, as JSON"
            : "First member"

  return (
    <MemberDialog
      open={open}
      onOpenChange={onOpenChange}
      onCancel={() => {
        onOpenChange(false)
        setDraft(EMPTY)
        setRefused("")
        setTaken("")
      }}
      title={`New key in db${db ?? ""}`}
      type={REDIS_KINDS[draft.kind].wire}
      db={db}
      command="Create key"
      onSubmit={create}
      busy={busy}
      error={refused}
      disabled={invalid}
      size="lg"
    >
      <ChoiceGrid columns={3} role="group" aria-label="Type of key" className="gap-2">
        {kinds.map((kind) => (
          <ChoiceCard
            key={kind}
            selected={draft.kind === kind}
            onClick={() => set({ kind })}
            className="h-full min-h-0 gap-1 p-2.5"
          >
            <span className="flex items-center gap-1.5">
              <KindMark type={REDIS_KINDS[kind].wire} />
              <ChoiceCardTitle>{REDIS_KINDS[kind].label}</ChoiceCardTitle>
            </span>
            <ChoiceCardHint>{HINT[kind]}</ChoiceCardHint>
          </ChoiceCard>
        ))}
      </ChoiceGrid>

      <FieldRow>
        <Field
          label="Key name"
          htmlFor="redis-new-name"
          error={
            taken && taken === draft.name
              ? `A key named ${taken} already exists, so nothing was written.`
              : undefined
          }
        >
          <Input
            id="redis-new-name"
            value={draft.name}
            spellCheck={false}
            autoComplete="off"
            placeholder="user:42:profile"
            className="font-mono"
            onChange={(event) => set({ name: event.target.value })}
          />
        </Field>
        <Field
          label="Expires in"
          htmlFor="redis-new-ttl"
          hint="Blank keeps it for good. Seconds, or 5m, 2h, 7d."
          error={ttl !== null && !ttl.ok ? ttl.why : undefined}
        >
          <Input
            id="redis-new-ttl"
            value={draft.ttl}
            spellCheck={false}
            autoComplete="off"
            placeholder="No expiry"
            className="font-mono"
            onChange={(event) => set({ ttl: event.target.value })}
          />
        </Field>
      </FieldRow>

      {draft.kind === "hash" && (
        <Field label="First field" htmlFor="redis-new-field">
          <Input
            id="redis-new-field"
            value={draft.field}
            spellCheck={false}
            autoComplete="off"
            className="font-mono"
            onChange={(event) => set({ field: event.target.value })}
          />
        </Field>
      )}

      {draft.kind === "stream" ? (
        <div role="group" aria-labelledby="redis-new-pairs" className="space-y-1.5">
          <p id="redis-new-pairs" className="text-body font-medium">
            First entry
          </p>
          {draft.pairs.map((pair, index) => (
            <div key={index} className="flex items-center gap-1.5">
              <Input
                aria-label={`Field ${index + 1} name`}
                placeholder="field"
                value={pair.field}
                spellCheck={false}
                autoComplete="off"
                className="font-mono sm:w-2/5"
                onChange={(event) =>
                  set({
                    pairs: draft.pairs.map((other, at) =>
                      at === index ? { ...other, field: event.target.value } : other,
                    ),
                  })
                }
              />
              <Input
                aria-label={`Field ${index + 1} value`}
                placeholder="value"
                value={pair.value}
                spellCheck={false}
                autoComplete="off"
                className="font-mono"
                onChange={(event) =>
                  set({
                    pairs: draft.pairs.map((other, at) =>
                      at === index ? { ...other, value: event.target.value } : other,
                    ),
                  })
                }
              />
              <IconAction
                label={`Remove field ${index + 1}`}
                disabled={draft.pairs.length === 1}
                onClick={() => set({ pairs: draft.pairs.filter((_, at) => at !== index) })}
              >
                <Minus />
              </IconAction>
            </div>
          ))}
          <Button
            type="button"
            size="xs"
            variant="ghost"
            onClick={() => set({ pairs: [...draft.pairs, { field: "", value: "" }] })}
          >
            <Plus />
            Another field
          </Button>
        </div>
      ) : (
        <div
          className={draft.kind === "zset" ? "grid gap-3 sm:grid-cols-[minmax(0,1fr)_9rem]" : ""}
        >
          <Field
            label={valueLabel}
            htmlFor="redis-new-value"
            error={
              draft.kind === "json" && draft.value.trim() !== "" && !parseJsonDoc(draft.value)
                ? "That is not valid JSON."
                : undefined
            }
          >
            <Textarea
              id="redis-new-value"
              value={draft.value}
              spellCheck={false}
              placeholder={draft.kind === "json" ? '{ "name": "value" }' : undefined}
              className="max-h-64 min-h-20 font-mono text-xs sm:text-xs"
              onChange={(event) => set({ value: event.target.value })}
            />
          </Field>
          {draft.kind === "zset" && (
            <Field
              label="Score"
              htmlFor="redis-new-score"
              error={score === null ? "A number, inf or -inf." : undefined}
            >
              <Input
                id="redis-new-score"
                value={draft.score}
                inputMode="decimal"
                autoComplete="off"
                className="font-mono"
                onChange={(event) => set({ score: event.target.value })}
              />
            </Field>
          )}
        </div>
      )}
    </MemberDialog>
  )
}
