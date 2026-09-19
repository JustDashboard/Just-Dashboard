"use client"

import { useId, useMemo, useState } from "react"
import { useMemoryState } from "@/lib/view-state"
import { CheckCircle, CrossCircle, Router } from "@/components/icons"
import { notify } from "@/lib/toast"
import { errorMessage, get, post, put } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import type { DbConnection, DbDriver, DbDriverInfo } from "@/lib/types"
import { buildDsn, databaseLabel, DEFAULT_PORT, EMPTY_FIELDS, maskDsn } from "@/lib/db-dsn"
import type { DsnFields } from "@/lib/db-dsn"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Modal } from "@/components/modal"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle } from "@/components/choice-card"
import { FilterChip } from "@/components/tabs"
import { Notice, Spinner } from "@/components/state"
import { Field, FieldRow, FormFact, FormFacts, FormNote, FormSection } from "@/components/form"

/**
 * Add or edit a connection.
 *
 * The address is asked for as the fields an operator knows — host, port, user,
 * password, database — and rendered into the engine's own string, because the
 * one form that asked for the whole string produced no error on a typo, only a
 * connection that refused everything asked of it afterwards. The string is
 * still shown, and can still be pasted whole for the engine whose URL carries
 * an option this form has no field for.
 *
 * Test dials the string before it is saved and reports the server version
 * back — the one piece of feedback that turns "did I get the host right" from
 * a save-and-pray into an answer. On edit the driver is fixed (changing it
 * would strand the stored secret) and the address is kept unless a password is
 * typed, so a rename never needs an unreadable password re-typed.
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
  const id = useId()
  const editing = Boolean(existing)
  // The engine list comes from the server, which is the only thing that knows
  // which dialects are registered. A hard-coded copy here went stale the moment
  // an engine was added and offered a driver the backend would reject.
  const drivers = usePoll(
    (signal) => get<DbDriverInfo[]>("/databases/drivers", undefined, signal),
    0,
  )
  // Kept in memory for the tab — memory, because a password is typed here —
  // until the dialog is closed, so checking a port on the way does not mean
  // filling the address in again.
  const draft = `databases.connect.${existing?.id ?? "new"}`
  const [name, setName] = useMemoryState(`${draft}.name`, existing?.name ?? "")
  const [driver, setDriver] = useMemoryState<DbDriver>(
    `${draft}.driver`,
    existing?.driver ?? "postgres",
  )
  const [mode, setMode] = useMemoryState<"fields" | "string">(
    `${draft}.mode`,
    existing?.driver === "sqlite" ? "string" : "fields",
  )
  const [fields, setFields] = useMemoryState<DsnFields>(`${draft}.fields`, {
    ...EMPTY_FIELDS,
    host: existing?.host || EMPTY_FIELDS.host,
    port: existing?.port ?? "",
    user: existing?.user ?? "",
    database: existing?.database ?? "",
  })
  const [raw, setRaw] = useMemoryState(`${draft}.raw`, "")
  const [testResult, setTestResult] = useState<{
    ok: boolean
    version?: string
    error?: string
  } | null>(null)
  const [testing, setTesting] = useState(false)
  const [saving, setSaving] = useState(false)

  const info = drivers.data?.find((d) => d.id === driver)
  const set = (patch: Partial<DsnFields>) => {
    setFields((f) => ({ ...f, ...patch }))
    setTestResult(null)
  }

  // What will be sent. On edit, an empty string keeps the stored one — and
  // the fields can only produce a string once the password is known, since
  // the stored one is never handed back to a browser.
  const dsn = useMemo(() => {
    if (mode === "string") return raw.trim()
    if (driver === "sqlite") return fields.database.trim()
    if (editing && !fields.password) return ""
    return buildDsn(driver, fields)
  }, [mode, raw, driver, fields, editing])
  const addressTouched =
    editing &&
    mode === "fields" &&
    driver !== "sqlite" &&
    !fields.password &&
    (fields.host !== (existing?.host || EMPTY_FIELDS.host) ||
      fields.port !== (existing?.port ?? "") ||
      fields.user !== (existing?.user ?? "") ||
      fields.database !== (existing?.database ?? ""))

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
        await put(`/databases/${existing!.id}`, { name: name.trim(), dsn })
        notify.success(`Updated ${name.trim()}`)
      } else {
        await post("/databases/", { name: name.trim(), driver, dsn })
        notify.success(`Added ${name.trim()}`)
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

  const canSave = name.trim() !== "" && (editing ? !addressTouched : dsn !== "")
  const dbLabel = databaseLabel(driver)

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="lg"
      title={editing ? "Edit connection" : "Connect a database"}
      description={
        editing
          ? "Rename the connection or replace its address. The stored password is kept unless a new one is typed."
          : "Point the dashboard at a database it did not start itself: a managed server, another machine, a file."
      }
      footer={
        <>
          <Button
            variant="outline"
            onClick={test}
            disabled={testing || dsn === ""}
            pending={testing}
            className="mr-auto"
          >
            <Router />
            Test connection
          </Button>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={saving}>
            Cancel
          </Button>
          <Button onClick={save} disabled={!canSave || saving} pending={saving}>
            {editing ? "Save" : "Add connection"}
          </Button>
        </>
      }
    >
      <div className="grid gap-5">
        {editing ? (
          <FormFacts>
            <FormFact label="Engine">{info?.label ?? existing?.driver}</FormFact>
            {existing?.host && (
              <FormFact label="Address" mono>
                {existing.host}
                {existing.port ? `:${existing.port}` : ""}
              </FormFact>
            )}
          </FormFacts>
        ) : (
          <FormSection title="Engine">
            {!drivers.data ? (
              <Spinner className="text-muted-foreground" />
            ) : (
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                {drivers.data.map((d) => (
                  <ChoiceCard
                    key={d.id}
                    selected={driver === d.id}
                    onClick={() => {
                      setDriver(d.id)
                      setMode(d.id === "sqlite" ? "string" : "fields")
                      set({ port: "" })
                    }}
                    className="min-h-0 gap-0.5 px-2.5 py-2"
                  >
                    <ChoiceCardTitle className="truncate">{d.label}</ChoiceCardTitle>
                    <ChoiceCardHint>
                      {d.kind === "sql" ? "SQL" : d.kind === "document" ? "Documents" : "Key–value"}
                    </ChoiceCardHint>
                  </ChoiceCard>
                ))}
              </div>
            )}
          </FormSection>
        )}

        <Field label="Name" htmlFor={`${id}-name`} hint="How it is listed in the picker.">
          <Input
            id={`${id}-name`}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={info ? `${info.label.toLowerCase()} on prod` : "production"}
            autoFocus={editing}
          />
        </Field>

        <FormSection
          title="Address"
          hint={
            editing && driver !== "sqlite"
              ? "Leave the password empty to keep the stored connection string."
              : undefined
          }
          actions={
            driver !== "sqlite" && (
              <>
                <FilterChip selected={mode === "fields"} onClick={() => setMode("fields")}>
                  Fields
                </FilterChip>
                <FilterChip selected={mode === "string"} onClick={() => setMode("string")}>
                  Connection string
                </FilterChip>
              </>
            )
          }
        >
          {driver === "sqlite" ? (
            <Field
              label="Database file"
              htmlFor={`${id}-path`}
              hint="A path on this server, inside the dashboard's file roots. It is created if it does not exist."
            >
              <Input
                id={`${id}-path`}
                value={mode === "string" ? raw : fields.database}
                onChange={(e) => {
                  setRaw(e.target.value)
                  set({ database: e.target.value })
                }}
                className="font-mono"
                placeholder={info?.placeholder}
              />
            </Field>
          ) : mode === "string" ? (
            <Field
              label="Connection string"
              htmlFor={`${id}-dsn`}
              hint="Pasted whole, for an address with options the fields do not offer."
            >
              <Input
                id={`${id}-dsn`}
                value={raw}
                onChange={(e) => {
                  setRaw(e.target.value)
                  setTestResult(null)
                }}
                className="font-mono"
                placeholder={info?.placeholder}
                autoComplete="off"
              />
            </Field>
          ) : (
            <div className="grid gap-3">
              <FieldRow columns={3}>
                <Field label="Host" htmlFor={`${id}-host`} className="sm:col-span-2">
                  <Input
                    id={`${id}-host`}
                    value={fields.host}
                    onChange={(e) => set({ host: e.target.value })}
                    className="font-mono"
                    placeholder="127.0.0.1"
                  />
                </Field>
                <Field label="Port" htmlFor={`${id}-port`}>
                  <Input
                    id={`${id}-port`}
                    value={fields.port}
                    onChange={(e) => set({ port: e.target.value.replace(/[^0-9]/g, "") })}
                    className="font-mono"
                    placeholder={DEFAULT_PORT[driver]}
                    inputMode="numeric"
                  />
                </Field>
              </FieldRow>
              <FieldRow>
                <Field
                  label="User"
                  htmlFor={`${id}-user`}
                  hint={driver === "redis" ? "Empty unless the server uses ACL users." : undefined}
                >
                  <Input
                    id={`${id}-user`}
                    value={fields.user}
                    onChange={(e) => set({ user: e.target.value })}
                    className="font-mono"
                    autoComplete="off"
                  />
                </Field>
                <Field
                  label="Password"
                  htmlFor={`${id}-password`}
                  error={
                    addressTouched
                      ? "Type the password to change the address — the stored one cannot be read back."
                      : undefined
                  }
                >
                  <Input
                    id={`${id}-password`}
                    type="password"
                    value={fields.password}
                    onChange={(e) => set({ password: e.target.value })}
                    className="font-mono"
                    autoComplete="new-password"
                    placeholder={editing ? "unchanged" : undefined}
                  />
                </Field>
              </FieldRow>
              <FieldRow>
                <Field
                  label={dbLabel}
                  htmlFor={`${id}-database`}
                  hint={
                    driver === "redis"
                      ? "0 to 15 on a stock server."
                      : driver === "sqlserver"
                        ? "Empty connects to the login's default database."
                        : undefined
                  }
                >
                  <Input
                    id={`${id}-database`}
                    value={fields.database}
                    onChange={(e) => set({ database: e.target.value })}
                    className="font-mono"
                    placeholder={
                      driver === "redis" ? "0" : driver === "oracle" ? "ORCLPDB1" : "app"
                    }
                  />
                </Field>
                {driver === "postgres" && (
                  <Field
                    label="TLS"
                    hint="Disable for a server on this host; require or verify-full over a network."
                  >
                    <Select
                      value={fields.option || "disable"}
                      onValueChange={(v) => set({ option: v })}
                    >
                      <SelectTrigger className="w-full font-mono">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {["disable", "prefer", "require", "verify-ca", "verify-full"].map((m) => (
                          <SelectItem key={m} value={m} className="font-mono">
                            sslmode={m}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </Field>
                )}
                {driver === "mongodb" && (
                  <Field
                    label="Auth source"
                    htmlFor={`${id}-auth`}
                    hint="admin for a root account created by the official image."
                  >
                    <Input
                      id={`${id}-auth`}
                      value={fields.option}
                      onChange={(e) => set({ option: e.target.value })}
                      className="font-mono"
                      placeholder="admin"
                    />
                  </Field>
                )}
              </FieldRow>
              {(!editing || fields.password) && (
                <FormNote className="truncate font-mono" title={maskDsn(driver, fields)}>
                  {maskDsn(driver, fields)}
                </FormNote>
              )}
            </div>
          )}
          <FormNote>
            Encrypted with the dashboard&apos;s master key and never returned to a browser.
          </FormNote>
        </FormSection>

        {testResult && (
          <Notice
            tone={testResult.ok ? "success" : "danger"}
            icon={testResult.ok ? CheckCircle : CrossCircle}
            title={
              testResult.ok
                ? testResult.version
                  ? `Connected — ${testResult.version}`
                  : "Connected"
                : "It refused the connection"
            }
          >
            {!testResult.ok && <span className="break-words">{testResult.error}</span>}
          </Notice>
        )}
      </div>
    </Modal>
  )
}
