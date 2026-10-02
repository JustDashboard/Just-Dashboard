"use client"

import { useState } from "react"
import { ApiError, errorMessage } from "@/lib/api"
import { notify } from "@/lib/toast"
import { Field, OptionList, OptionRow } from "@/components/form"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { redisCopy, redisRename } from "@/components/database/redis/api"
import { lineSafe } from "@/components/database/redis/bytes"
import { MemberDialog } from "@/components/database/redis/keys/member-dialog"
import type { RedisBytes } from "@/components/database/redis/types"
import type { Redis } from "@/components/database/redis/use-redis"

/**
 * A key given another name.
 *
 * The server renames only onto a name nobody has: one that is taken is
 * refused, and the field says so. Replacing the key that holds the name is a
 * choice of its own — it deletes that key — and is offered only to a role
 * that may remove things.
 */
export function RenameDialog({
  redis,
  name,
  type,
  onOpenChange,
  onRenamed,
}: {
  redis: Redis
  name: RedisBytes
  type: string | undefined
  onOpenChange: (open: boolean) => void
  onRenamed: (to: RedisBytes) => void
}) {
  // The field starts on the name only where it can hold it: a name that is
  // bytes, or has a line break in it, is renamed to one typed afresh.
  const [to, setTo] = useState(lineSafe(name) ? name : "")
  const [overwrite, setOverwrite] = useState(false)
  const [busy, setBusy] = useState(false)
  const [taken, setTaken] = useState("")
  const [refused, setRefused] = useState("")
  const same = typeof name === "string" && to === name

  const run = async () => {
    setBusy(true)
    setRefused("")
    setTaken("")
    try {
      await redisRename(redis.target, { key: name, to, ...(overwrite ? { overwrite: true } : {}) })
      notify.success(`Renamed to ${to}`)
      onOpenChange(false)
      onRenamed(to)
    } catch (err) {
      if (err instanceof ApiError && err.code === "key_exists") setTaken(to)
      else setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <MemberDialog
      open
      onOpenChange={onOpenChange}
      onCancel={() => onOpenChange(false)}
      title="Rename key"
      name={name}
      type={type ?? ""}
      db={redis.db}
      command={overwrite ? "Rename and replace" : "Rename"}
      onSubmit={run}
      busy={busy}
      error={refused}
      disabled={same || to === ""}
    >
      <Field
        label="New name"
        htmlFor="redis-rename-to"
        hint="Refused if a key of that name exists."
        error={
          taken && taken === to && !overwrite
            ? `A key named ${taken} already exists, so nothing was renamed.`
            : undefined
        }
      >
        <Input
          id="redis-rename-to"
          value={to}
          spellCheck={false}
          autoComplete="off"
          className="font-mono"
          onChange={(event) => setTo(event.target.value)}
        />
      </Field>
      {redis.canDestroy && (
        <OptionList>
          <OptionRow
            title="Replace a key that already has this name, deleting what it holds"
            tone={overwrite ? "warning" : "default"}
            checked={overwrite}
            onCheckedChange={setOverwrite}
          />
        </OptionList>
      )}
    </MemberDialog>
  )
}

/** A copy of a key under another name, here or in another numbered database. */
export function DuplicateDialog({
  redis,
  name,
  type,
  onOpenChange,
  onCopied,
}: {
  redis: Redis
  name: RedisBytes
  type: string | undefined
  onOpenChange: (open: boolean) => void
  onCopied: (to: RedisBytes, toDb: number | undefined) => void
}) {
  const server = redis.server.data
  const elsewhere = Boolean(server?.features.copy && server.features.databases)
  const [to, setTo] = useState(lineSafe(name) ? `${name}:copy` : "")
  const [toDb, setToDb] = useState<number | undefined>(redis.db)
  const [overwrite, setOverwrite] = useState(false)
  const [busy, setBusy] = useState(false)
  const [taken, setTaken] = useState("")
  const [refused, setRefused] = useState("")
  const other = toDb !== undefined && toDb !== redis.db

  const run = async () => {
    setBusy(true)
    setRefused("")
    setTaken("")
    try {
      await redisCopy(redis.target, {
        key: name,
        to,
        ...(other ? { toDb } : {}),
        ...(overwrite ? { overwrite: true } : {}),
      })
      notify.success(`Copied to ${to}${other ? ` in db${toDb}` : ""}`)
      onOpenChange(false)
      onCopied(to, toDb)
    } catch (err) {
      if (err instanceof ApiError && err.code === "key_exists") setTaken(to)
      else setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <MemberDialog
      open
      onOpenChange={onOpenChange}
      onCancel={() => onOpenChange(false)}
      title="Duplicate key"
      name={name}
      type={type ?? ""}
      db={redis.db}
      command={overwrite ? "Copy and replace" : "Copy"}
      onSubmit={run}
      busy={busy}
      error={refused}
      disabled={to === "" || (!other && typeof name === "string" && to === name)}
    >
      <Field
        label="Name of the copy"
        htmlFor="redis-copy-to"
        hint="The copy holds the same value and the same expiry."
        error={
          taken && taken === to && !overwrite
            ? `A key named ${taken} already exists there, so nothing was copied.`
            : undefined
        }
      >
        <Input
          id="redis-copy-to"
          value={to}
          spellCheck={false}
          autoComplete="off"
          className="font-mono"
          onChange={(event) => setTo(event.target.value)}
        />
      </Field>
      {elsewhere && server && toDb !== undefined && (
        <Field label="Into" htmlFor="redis-copy-db">
          <Select value={String(toDb)} onValueChange={(value) => setToDb(Number(value))}>
            <SelectTrigger id="redis-copy-db" size="sm" className="w-full font-mono">
              <SelectValue />
            </SelectTrigger>
            <SelectContent className="max-h-72">
              {Array.from({ length: Math.max(server.databases, toDb + 1) }, (_, n) => (
                <SelectItem
                  key={n}
                  value={String(n)}
                  className="font-mono"
                  hint={n === redis.db ? "this database" : undefined}
                >
                  db{n}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      )}
      {redis.canDestroy && (
        <OptionList>
          <OptionRow
            title="Replace a key that already has this name, deleting what it holds"
            tone={overwrite ? "warning" : "default"}
            checked={overwrite}
            onCheckedChange={setOverwrite}
          />
        </OptionList>
      )}
    </MemberDialog>
  )
}
