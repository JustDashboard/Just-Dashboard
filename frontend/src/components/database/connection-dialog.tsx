"use client"

import { useState } from "react"
import { CheckCircle, CrossCircle, Router } from "@/components/icons"
import { notify } from "@/lib/toast"
import { errorMessage, get, post, put } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import type { DbConnection, DbDriver, DbDriverInfo } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

import { Modal } from "@/components/modal"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

/**
 * Add or edit a connection, with a Test button that dials the DSN before it is
 * saved and reports the server version back — the one piece of feedback that
 * turns "did I get the host right" from a save-and-pray into an answer. On edit
 * the driver is fixed (changing it would strand the stored secret) and an empty
 * DSN keeps the existing one, so a rename never needs an unreadable password
 * re-typed.
 */
export function ConnectionDialog({
  open,
  onOpenChange,
  onDone,
  existing,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone: () => void
  existing?: DbConnection
}) {
  const editing = Boolean(existing)
  // The engine list comes from the server, which is the only thing that knows
  // which dialects are registered. A hard-coded copy here went stale the moment
  // an engine was added and offered a driver the backend would reject.
  const drivers = usePoll(
    (signal) => get<DbDriverInfo[]>("/databases/drivers", undefined, signal),
    0,
  )
  const [name, setName] = useState(existing?.name ?? "")
  const [driver, setDriver] = useState<DbDriver>(existing?.driver ?? "postgres")
  const info = drivers.data?.find((d) => d.id === driver)
  const [dsn, setDsn] = useState("")
  const [testResult, setTestResult] = useState<{
    ok: boolean
    version?: string
    error?: string
  } | null>(null)
  const [testing, setTesting] = useState(false)
  const [saving, setSaving] = useState(false)

  const test = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      const res = await post<{ ok: boolean; version?: string; error?: string }>("/databases/test", {
        driver,
        dsn,
      })
      setTestResult(res)
    } catch (err) {
      setTestResult({ ok: false, error: errorMessage(err) })
    } finally {
      setTesting(false)
    }
  }

  const save = async () => {
    setSaving(true)
    try {
      if (editing) {
        await put(`/databases/${existing!.id}`, { name, dsn })
        notify.success(`Updated ${name}`)
      } else {
        await post("/databases/", { name, driver, dsn })
        notify.success(`Added ${name}`)
      }
      onOpenChange(false)
      onDone()
    } catch (err) {
      notify.error(
        editing ? "Could not update connection" : "Could not add connection",
        errorMessage(err),
      )
    } finally {
      setSaving(false)
    }
  }

  const canSave = name !== "" && (editing || dsn !== "")

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={editing ? "Edit connection" : "Add database connection"}
      footer={
        <>
          <Button
            variant="outline"
            onClick={test}
            disabled={testing || dsn === ""}
            pending={testing}
          >
            <Router className="size-4" />
            Test connection
          </Button>
          <Button onClick={save} disabled={!canSave || saving} pending={saving}>
            {editing ? "Save" : "Add"}
          </Button>
        </>
      }
    >
      <div className="grid gap-3">
        <div className="space-y-1.5">
          <Label htmlFor="conn-name">Name</Label>
          <Input id="conn-name" value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label>Driver</Label>
          <Select value={driver} onValueChange={(v) => setDriver(v as DbDriver)} disabled={editing}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {(drivers.data ?? []).map((d) => (
                <SelectItem key={d.id} value={d.id}>
                  {d.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {editing && (
            <p className="text-xs text-muted-foreground">
              The driver is fixed once a connection exists.
            </p>
          )}
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="conn-dsn">
            {driver === "sqlite" ? "Database file path" : "Connection string"}
            {editing && <span className="ml-1.5 text-muted-foreground">(leave blank to keep)</span>}
          </Label>
          <Input
            id="conn-dsn"
            value={dsn}
            onChange={(e) => {
              setDsn(e.target.value)
              setTestResult(null)
            }}
            className="font-mono text-xs"
            placeholder={info?.placeholder}
          />
          <p className="text-xs text-muted-foreground">
            Encrypted with the dashboard&apos;s master key and never returned to a browser.
          </p>
        </div>

        {testResult && (
          <div
            className={
              "flex items-start gap-2 rounded-md border p-2.5 text-xs " +
              (testResult.ok
                ? "border-rule-success bg-wash-success text-success"
                : "border-rule-danger bg-wash-danger text-destructive")
            }
          >
            {testResult.ok ? (
              <CheckCircle className="mt-0.5 size-4 shrink-0" />
            ) : (
              <CrossCircle className="mt-0.5 size-4 shrink-0" />
            )}
            <span className="min-w-0 break-words">
              {testResult.ok
                ? testResult.version
                  ? `Connected — ${testResult.version}`
                  : "Connected"
                : testResult.error}
            </span>
          </div>
        )}
      </div>
    </Modal>
  )
}
