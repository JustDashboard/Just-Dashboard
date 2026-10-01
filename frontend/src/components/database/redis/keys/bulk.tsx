"use client"

import { useState } from "react"
import Link from "next/link"
import { errorMessage } from "@/lib/api"
import { duration, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { Segments } from "@/components/deploy/settings/segments"
import { Field, FormFact, FormFacts, FormNote } from "@/components/form"
import { Modal } from "@/components/modal"
import { Group, Well } from "@/components/panel"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { EngineMark } from "@/components/database/kit"
import { redisBulk } from "@/components/database/redis/api"
import { bytesLabel } from "@/components/database/redis/bytes"
import { KIND_ORDER, KindMark, REDIS_KINDS, kindOf } from "@/components/database/redis/kinds"
import { parseTtl, ttlWord } from "@/components/database/redis/ttl"
import type {
  RedisBulkAction,
  RedisBulkResult,
  RedisBytes,
} from "@/components/database/redis/types"
import type { Redis } from "@/components/database/redis/use-redis"

/** What a dry run found, for the inputs it was asked with. */
type Count = {
  inputs: string
  matched: number
  complete: boolean
  cursor: string
  sample: RedisBytes[]
  total: number
}

const WORD: Record<RedisBulkAction, { segment: string; verb: string; done: string }> = {
  delete: { segment: "Delete", verb: "Delete", done: "deleted" },
  expire: { segment: "Set an expiry", verb: "Expire", done: "set to expire" },
  persist: { segment: "Remove expiries", verb: "Keep", done: "kept for good" },
}

/**
 * One action on every key a pattern matches.
 *
 * Nothing is changed until the keys have been counted: the count is a dry run
 * on the server, with the first names it found, and the command that follows
 * is named for that count. A pattern that is every key in the database is not
 * a bulk action — emptying a database asks for its name, in Settings — so it
 * can be counted here and not removed.
 */
export function BulkDialog({
  redis,
  open,
  onOpenChange,
  confirm,
  onDone,
}: {
  redis: Redis
  open: boolean
  onOpenChange: (open: boolean) => void
  confirm: (request: ConfirmRequest) => void
  onDone: () => void
}) {
  const { target, db, conn, engine, server, param, canDestroy, href } = redis
  // What the rail is narrowed to is what the dialog opens on.
  const [seed, setSeed] = useState({ pattern: param("pattern"), type: param("type") })
  const [pattern, setPattern] = useState(seed.pattern)
  const [type, setType] = useState(seed.type)
  if (!open && (seed.pattern !== param("pattern") || seed.type !== param("type"))) {
    setSeed({ pattern: param("pattern"), type: param("type") })
    setPattern(param("pattern"))
    setType(param("type"))
  }
  const actions: RedisBulkAction[] = canDestroy ? ["delete", "expire", "persist"] : ["persist"]
  const [action, setAction] = useState<RedisBulkAction>(actions[0])
  const [span, setSpan] = useState("")
  const [count, setCount] = useState<Count | null>(null)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")

  const glob = pattern.trim()
  const inputs = JSON.stringify([db, glob, type])
  const counted = count?.inputs === inputs ? count : null
  const ttl = action === "expire" ? parseTtl(span) : null
  // The server refuses it too: every key, of every type, removed or expired.
  const everything = /^\*+$/.test(glob) && !type && action !== "persist"
  const kinds = KIND_ORDER.filter(
    (kind) => kind !== "json" || (engine.can("json") && server.data?.features.json),
  )
  const where = `${conn.name} · db${db}`

  const run = async (more: boolean) => {
    setBusy(true)
    setRefused("")
    try {
      const answer = await redisBulk(target, {
        pattern: glob,
        ...(type ? { type } : {}),
        action,
        ...(ttl?.ok ? { ttl: ttl.seconds } : {}),
        dryRun: true,
        ...(more && counted ? { cursor: counted.cursor } : {}),
      })
      setCount({
        inputs,
        matched: (more && counted ? counted.matched : 0) + answer.matched,
        complete: answer.complete,
        cursor: answer.cursor,
        sample: more && counted ? counted.sample : answer.sample,
        total: answer.total,
      })
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  /** The action for real, call after call until the scan has come round. */
  const apply = async () => {
    let affected = 0
    let elapsed = 0
    let cursor: string | undefined
    for (;;) {
      const answer: RedisBulkResult = await redisBulk(target, {
        pattern: glob,
        ...(type ? { type } : {}),
        action,
        ...(ttl?.ok ? { ttl: ttl.seconds } : {}),
        ...(cursor ? { cursor } : {}),
      })
      affected += answer.affected
      elapsed += answer.elapsedMs
      if (answer.complete) break
      cursor = answer.cursor
    }
    notify.success(`${plural(affected, "key")} ${WORD[action].done}`, {
      description: `In ${where}, in ${duration(elapsed / 1000)}.`,
    })
    setCount(null)
  }

  const noun = counted ? plural(counted.matched, "key") : ""
  const command = counted
    ? `${WORD[action].verb} ${counted.complete ? "" : "at least "}${noun}`
    : ""
  const ready =
    counted !== null && counted.matched > 0 && !everything && (action !== "expire" || ttl?.ok)

  const ask = () => {
    if (!counted) return
    onOpenChange(false)
    const facts = (
      <>
        <FormFact label="Matching" mono>
          {glob}
        </FormFact>
        {type && <FormFact label="Of type">{kindOf(type).label}</FormFact>}
      </>
    )
    if (action === "persist") {
      void apply()
        .then(onDone)
        .catch((err: unknown) => notify.error("Could not remove the expiries", err))
      return
    }
    confirm({
      title: command,
      subject: { mark: <EngineMark engine={engine} size="sm" />, name: where, facts },
      description: (
        <>
          <p>
            {action === "delete"
              ? "Every key the pattern matches is removed, with what it holds. This cannot be undone."
              : `Every key the pattern matches is set to expire in ${ttl?.ok ? ttlWord(ttl.seconds) : ""}, and is removed then.`}
          </p>
          {counted.complete && counted.matched === counted.total && (
            <p className="font-medium text-warning">
              That is every key in db{db}: all {counted.total.toLocaleString()} of them.
            </p>
          )}
          {!counted.complete && (
            <p>
              The count stopped early, so more keys than were counted may match. All of them are{" "}
              {action === "delete" ? "removed" : "changed"}.
            </p>
          )}
        </>
      ),
      confirmLabel: command,
      action: async () => {
        await apply()
        return "reported"
      },
      onDone,
    })
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title="Bulk actions"
      description="Count the keys a pattern matches, then delete them, expire them or remove their expiries."
      size="lg"
      footer={
        <>
          <FormNote className="mr-auto">
            {ready
              ? "The next step names the count and asks once more."
              : "Nothing changes until the keys are counted."}
          </FormNote>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Close
          </Button>
          {ready ? (
            <Button variant={action === "persist" ? "default" : "destructive"} onClick={ask}>
              {command}…
            </Button>
          ) : (
            <Button
              pending={busy}
              disabled={glob === "" || (action === "expire" && !ttl?.ok)}
              onClick={() => void run(false)}
            >
              Count the keys
            </Button>
          )}
        </>
      }
    >
      <div className="space-y-4">
        <div className="flex min-w-0 items-center gap-3">
          <EngineMark engine={engine} size="sm" />
          <div className="min-w-0 space-y-0.5">
            <p className="truncate text-body font-medium">{conn.name}</p>
            <FormFacts>
              <FormFact label="Database">db{db}</FormFact>
              {server.data && (
                <FormFact label="Holds">
                  {plural(server.data.keyspace.find((space) => space.db === db)?.keys ?? 0, "key")}
                </FormFact>
              )}
            </FormFacts>
          </div>
        </div>

        <Field
          label="Pattern"
          htmlFor="redis-bulk-pattern"
          hint="A glob: session:* is every key that starts with session:."
        >
          <Input
            id="redis-bulk-pattern"
            value={pattern}
            spellCheck={false}
            autoComplete="off"
            placeholder="session:*"
            className="font-mono"
            onChange={(event) => setPattern(event.target.value)}
          />
        </Field>

        <Field label="Only keys of type">
          <ChipStrip
            role="group"
            aria-label="Only keys of type"
            className="max-sm:mx-0 max-sm:px-0"
          >
            <FilterChip selected={!type} onClick={() => setType("")}>
              Any type
            </FilterChip>
            {kinds.map((kind) => {
              const info = REDIS_KINDS[kind]
              return (
                <FilterChip
                  key={kind}
                  selected={type === info.wire}
                  onClick={() => setType(info.wire)}
                  className="gap-1 px-2"
                >
                  <KindMark type={info.wire} className="size-3" />
                  {info.label}
                </FilterChip>
              )
            })}
          </ChipStrip>
        </Field>

        <Field label="What happens to them">
          <Segments
            label="What happens to the keys"
            fill
            value={action}
            onChange={setAction}
            options={actions.map((value) => ({ value, label: WORD[value].segment }))}
          />
        </Field>

        {action === "expire" && (
          <Field
            label="Expire in"
            htmlFor="redis-bulk-ttl"
            hint="Seconds, or a span such as 5m, 2h or 7d."
            error={span.trim() !== "" && ttl && !ttl.ok ? ttl.why : undefined}
          >
            <Input
              id="redis-bulk-ttl"
              value={span}
              spellCheck={false}
              autoComplete="off"
              className="font-mono"
              onChange={(event) => setSpan(event.target.value)}
            />
          </Field>
        )}

        {everything && (
          <FormNote tone="warning" role="status">
            That pattern is every key in db{db}. Emptying a database is done from{" "}
            <Link href={href("settings")} className="underline underline-offset-2">
              Settings
            </Link>
            , which asks for its name. Give a type or a narrower pattern to act on some of them.
          </FormNote>
        )}

        {refused && (
          <FormNote tone="danger" role="alert">
            {refused}
          </FormNote>
        )}

        <div aria-live="polite">
          {counted && (
            <Group data-slot="redis-bulk-count" className="animate-rise space-y-2">
              <div className="flex flex-wrap items-baseline gap-x-2">
                <p className="numeric text-base font-semibold">
                  {counted.complete ? "" : "At least "}
                  {noun} {counted.matched === 1 ? "matches" : "match"}
                </p>
                <p className="text-hint text-muted-foreground">
                  of {counted.total.toLocaleString()} in db{db}
                </p>
                {!counted.complete && (
                  <Button
                    size="xs"
                    variant="outline"
                    className="ml-auto"
                    pending={busy}
                    onClick={() => void run(true)}
                  >
                    Keep counting
                  </Button>
                )}
              </div>
              {counted.sample.length > 0 && (
                <Well className="max-h-36 text-hint">
                  {counted.sample.map((name, index) => (
                    <div key={index} className="truncate">
                      {bytesLabel(name)}
                    </div>
                  ))}
                  {counted.matched > counted.sample.length && (
                    <div className="text-muted-foreground">
                      … and {(counted.matched - counted.sample.length).toLocaleString()} more
                    </div>
                  )}
                </Well>
              )}
            </Group>
          )}
        </div>
      </div>
    </Modal>
  )
}
