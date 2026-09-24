"use client"

import { useId } from "react"
import { Plus, Trash } from "@/components/icons"
import type { DeploymentConfiguration } from "@/lib/types"
import { GroupRule } from "@/components/flow"
import { Field, FieldRow, OptionRow } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Group } from "@/components/panel"
import { EmptyNote } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import { Textarea } from "@/components/ui/textarea"
import { formatDuration } from "@/components/deploy/vocabulary"
import { Segments } from "@/components/deploy/settings/segments"

export type Check = DeploymentConfiguration["checks"][number]
type CheckKind = "http" | "tcp" | "docker_health" | "command"
type Phase = Check["phase"]

type Config = {
  path?: string
  host?: string
  port?: number
  method?: string
  expectedStatus?: number[]
  command?: string[]
  attempts?: number
  timeoutSeconds?: number
  intervalSeconds?: number
}

const KINDS: { value: CheckKind; label: string }[] = [
  { value: "http", label: "HTTP" },
  { value: "tcp", label: "TCP" },
  { value: "docker_health", label: "Docker health" },
  { value: "command", label: "Command" },
]

const PHASES: { value: Phase; label: string; rule: string }[] = [
  { value: "readiness", label: "Readiness", rule: "Readiness · before traffic moves" },
  { value: "smoke", label: "Smoke", rule: "Smoke · once it is ready" },
]

function defaultsFor(kind: CheckKind): Config {
  switch (kind) {
    case "http":
      return { path: "/", method: "GET", attempts: 20, timeoutSeconds: 5, intervalSeconds: 3 }
    case "tcp":
      return { port: 0, attempts: 10, timeoutSeconds: 3 }
    case "command":
      return { command: [], attempts: 10, timeoutSeconds: 3 }
    case "docker_health":
      return { attempts: 10, timeoutSeconds: 3 }
  }
}

function newCheck(phase: Phase): Check {
  return {
    name: phase === "readiness" ? "Readiness check" : "Smoke check",
    kind: "http",
    phase,
    required: true,
    config: defaultsFor("http") as Record<string, unknown>,
  }
}

/**
 * Whether a release is verified before traffic moves to it: a required
 * readiness check of any kind, which is exactly what preflight's
 * `readiness_missing` looks for (`hasReadinessCheck`).
 */
export function hasReadiness(checks: Check[]) {
  return checks.some((check) => check.phase === "readiness" && check.required)
}

/** A command's argv as the server takes it: one argument per line, no blank ones. */
function argvOf(lines: string[]) {
  return lines.map((line) => line.trim()).filter(Boolean)
}

/**
 * The checks as they are saved. A command's argv is edited as the lines typed,
 * so that Enter can start the next argument; the blank lines and stray spaces
 * that allows are dropped here, on the way to the server.
 */
export function savedChecks(checks: Check[]): Check[] {
  return checks.map((check) => {
    const command = (check.config as Config | undefined)?.command
    return command ? { ...check, config: { ...check.config, command: argvOf(command) } } : check
  })
}

/**
 * What a check does, in one line of the shape it is written in elsewhere — a
 * request line, a socket, a command — so a list of them is scanned rather than
 * read field by field. `port` is the application's own, for a check that
 * leaves its port to follow it.
 */
function checkSummary(check: Check, port?: number) {
  const config = (check.config ?? {}) as Config
  const attempts = config.attempts ?? 10
  switch (check.kind as CheckKind) {
    case "http": {
      const codes = config.expectedStatus?.length ? config.expectedStatus.join(", ") : "2xx"
      // With no wait between them, "× 0 s" is a cadence that says nothing.
      const cadence = config.intervalSeconds
        ? `${attempts} × ${config.intervalSeconds} s`
        : `${attempts} tries`
      return `${config.method ?? "GET"} ${config.path || "/"} → ${codes} · ${cadence}`
    }
    case "tcp":
      return `TCP ${config.host ?? ""}:${config.port || port || "port"} · ${attempts} tries`
    case "docker_health":
      return `the image's HEALTHCHECK · ${attempts} tries`
    case "command": {
      const argv = argvOf(config.command ?? [])
      return argv.length ? `$ ${argv.join(" ")}` : "$ (no command yet)"
    }
    default:
      return check.kind
  }
}

/** A path or host beside the port it goes with: a port is five digits, so it keeps a narrow column. */
const ADDRESS = "grid-cols-[minmax(0,1fr)_7.5rem] sm:grid-cols-[minmax(0,1fr)_9rem]"

/** How long a check keeps trying before it gives up: every attempt's timeout and wait. */
function givesUpAfter(check: Check) {
  const config = (check.config ?? {}) as Config
  const attempts = config.attempts ?? 10
  const wait =
    (config.timeoutSeconds ?? 3) + (check.kind === "http" ? (config.intervalSeconds ?? 0) : 0)
  return attempts * wait
}

/**
 * The evidence a release must produce before it takes traffic (readiness)
 * and once more right after (smoke), grouped by which of the two each is.
 * Each kind asks only the questions its own probe can answer — a Docker
 * health check has nothing to configure beyond how long to wait for one.
 *
 * Each check says what it does in one line above its fields, and how long it
 * keeps trying, because "20 attempts, 5 s timeout, 3 s interval" is three
 * numbers a reader otherwise multiplies in their head to find out how long a
 * bad release is left running.
 */
export function HealthChecks({
  checks,
  disabled,
  onChange,
  rowError,
  port,
}: {
  checks: Check[]
  disabled?: boolean
  onChange: (checks: Check[]) => void
  /** The message a save refused for this check, when it named the check but no sub-field. */
  rowError?: (index: number) => string | undefined
  /** The application's port, which a check with no port of its own probes. */
  port?: number
}) {
  const update = (index: number, patch: Partial<Check>) =>
    onChange(checks.map((check, i) => (i === index ? { ...check, ...patch } : check)))

  return (
    <div className="space-y-5">
      {checks.length === 0 ? (
        <EmptyNote className="px-0 py-2 text-left">No health checks configured.</EmptyNote>
      ) : (
        PHASES.map((phase) => {
          const entries = checks
            .map((check, index) => ({ check, index }))
            .filter(({ check }) => check.phase === phase.value)
          if (entries.length === 0) return null
          return (
            <div key={phase.value} className="space-y-3">
              <GroupRule label={phase.rule} count={entries.length} />
              {entries.map(({ check, index }) => (
                <CheckEditor
                  key={index}
                  check={check}
                  index={index}
                  disabled={disabled}
                  port={port}
                  error={rowError?.(index)}
                  onChange={(patch) => update(index, patch)}
                  onRemove={() => onChange(checks.filter((_, i) => i !== index))}
                />
              ))}
            </div>
          )
        })
      )}
      {!disabled && (
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => onChange([...checks, newCheck("readiness")])}
        >
          <Plus className="size-3.5" />
          Add check
        </Button>
      )}
    </div>
  )
}

/** One check: its name, what kind of probe and when it runs, then that probe's own fields. */
function CheckEditor({
  check,
  index,
  disabled,
  port,
  error,
  onChange,
  onRemove,
}: {
  check: Check
  index: number
  disabled?: boolean
  port?: number
  error?: string
  onChange: (patch: Partial<Check>) => void
  onRemove: () => void
}) {
  const nameId = useId()
  const config = (check.config ?? {}) as Config
  const kind = check.kind as CheckKind
  const n = index + 1
  const id = (field: string) => `check-${index}-${field}`
  const updateConfig = (patch: Partial<Config>) =>
    onChange({ config: { ...config, ...patch } as Record<string, unknown> })
  const number = (value: string) => (value === "" ? 0 : Number(value))
  const portPlaceholder = port ? String(port) : "app port"

  return (
    <Group
      role="group"
      aria-labelledby={nameId}
      tone={error ? "danger" : "default"}
      className="space-y-3"
    >
      <span id={nameId} className="sr-only">
        {check.name || `Health check ${n}`}
      </span>
      <div className="flex min-w-0 items-center gap-2">
        <Input
          aria-label={`Health check ${n} name`}
          value={check.name}
          onChange={(event) => onChange({ name: event.target.value })}
          readOnly={disabled}
          placeholder="Answers on /health"
          className="min-w-0 flex-1 font-medium sm:text-sm"
        />
        {!disabled && (
          <IconAction
            type="button"
            label={`Remove health check ${check.name || n}`}
            onClick={onRemove}
            className="text-muted-foreground hover:text-destructive"
          >
            <Trash />
          </IconAction>
        )}
      </div>

      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <Segments
          label={`Health check ${n} kind`}
          value={kind}
          options={KINDS}
          disabled={disabled}
          // A new kind asks different questions, so it starts from that
          // kind's own defaults rather than carrying the old answers over.
          onChange={(next) =>
            onChange({ kind: next, config: defaultsFor(next) as Record<string, unknown> })
          }
        />
        <Segments
          label={`Health check ${n} phase`}
          value={check.phase}
          options={PHASES}
          disabled={disabled}
          onChange={(phase) => onChange({ phase })}
        />
      </div>

      <p className="min-w-0 font-mono text-hint break-words text-muted-foreground">
        <span className="text-foreground/80">{checkSummary(check, port)}</span> · gives up after{" "}
        <span className="whitespace-nowrap">~{formatDuration(givesUpAfter(check))}</span>
      </p>

      {kind === "http" && (
        <>
          <FieldRow className={ADDRESS}>
            <Field label="Path" htmlFor={id("path")}>
              <Input
                id={id("path")}
                value={config.path ?? "/"}
                readOnly={disabled}
                className="font-mono"
                placeholder="/health"
                onChange={(event) => updateConfig({ path: event.target.value })}
              />
            </Field>
            <Field label="Port" htmlFor={id("port")}>
              <InputGroup>
                <InputGroupAddon align="inline-start">
                  <InputGroupText className="font-mono">:</InputGroupText>
                </InputGroupAddon>
                <InputGroupInput
                  id={id("port")}
                  type="number"
                  min={0}
                  max={65535}
                  value={config.port ?? ""}
                  readOnly={disabled}
                  className="font-mono"
                  placeholder={portPlaceholder}
                  onChange={(event) =>
                    updateConfig({
                      port: event.target.value ? Number(event.target.value) : undefined,
                    })
                  }
                />
              </InputGroup>
            </Field>
          </FieldRow>
          <FieldRow>
            <Field label="Method">
              <Segments
                label={`Health check ${n} method`}
                value={(config.method ?? "GET") as "GET" | "HEAD"}
                options={[
                  { value: "GET", label: "GET", mono: true },
                  { value: "HEAD", label: "HEAD", mono: true },
                ]}
                fill
                disabled={disabled}
                onChange={(method) => updateConfig({ method })}
              />
            </Field>
            <Field label="Expected status" htmlFor={id("status")}>
              <Input
                id={id("status")}
                value={(config.expectedStatus ?? []).join(", ")}
                readOnly={disabled}
                className="font-mono"
                placeholder="200, 204 · any 2xx"
                onChange={(event) =>
                  updateConfig({
                    expectedStatus: event.target.value
                      .split(",")
                      .map((part) => Number(part.trim()))
                      .filter((code) => Number.isInteger(code) && code > 0),
                  })
                }
              />
            </Field>
          </FieldRow>
        </>
      )}

      {kind === "tcp" && (
        <FieldRow className={ADDRESS}>
          <Field label="Host" htmlFor={id("host")}>
            <Input
              id={id("host")}
              value={config.host ?? ""}
              readOnly={disabled}
              className="font-mono"
              placeholder="container"
              onChange={(event) => updateConfig({ host: event.target.value || undefined })}
            />
          </Field>
          <Field label="Port" htmlFor={id("tcp-port")}>
            <InputGroup>
              <InputGroupAddon align="inline-start">
                <InputGroupText className="font-mono">:</InputGroupText>
              </InputGroupAddon>
              <InputGroupInput
                id={id("tcp-port")}
                type="number"
                min={0}
                max={65535}
                value={config.port || ""}
                readOnly={disabled}
                className="font-mono"
                placeholder={portPlaceholder}
                onChange={(event) => updateConfig({ port: number(event.target.value) })}
              />
            </InputGroup>
          </Field>
        </FieldRow>
      )}

      {kind === "command" && (
        <Field
          label="Command argv"
          htmlFor={id("command")}
          hint="One argument per line, run inside the candidate container."
        >
          <Textarea
            id={id("command")}
            value={(config.command ?? []).join("\n")}
            readOnly={disabled}
            placeholder={"pg_isready\n-U\npostgres"}
            className="min-h-20 font-mono sm:text-xs"
            // The lines as typed: filtered here, the empty line Enter makes
            // vanished before the next argument could go on it.
            onChange={(event) => updateConfig({ command: event.target.value.split("\n") })}
          />
        </Field>
      )}

      <FieldRow
        columns={kind === "http" ? 3 : 2}
        // Short numbers with their units: side by side even on a phone.
        className={kind === "http" ? "grid-cols-3" : "grid-cols-2"}
      >
        <NumberField
          id={id("attempts")}
          label="Attempts"
          unit="×"
          max={60}
          value={config.attempts ?? 10}
          disabled={disabled}
          onChange={(attempts) => updateConfig({ attempts })}
        />
        <NumberField
          id={id("timeout")}
          label="Timeout"
          unit="s"
          max={60}
          value={config.timeoutSeconds ?? 3}
          disabled={disabled}
          onChange={(timeoutSeconds) => updateConfig({ timeoutSeconds })}
        />
        {kind === "http" && (
          <NumberField
            id={id("interval")}
            label="Interval"
            unit="s"
            max={60}
            value={config.intervalSeconds ?? 0}
            disabled={disabled}
            onChange={(intervalSeconds) => updateConfig({ intervalSeconds })}
          />
        )}
      </FieldRow>

      <OptionRow
        title="Required — a failing check blocks activation"
        checked={check.required}
        onCheckedChange={(required) => onChange({ required })}
        disabled={disabled}
      />
      {error && (
        <p role="alert" className="text-hint text-destructive">
          {error}
        </p>
      )}
    </Group>
  )
}

/** A count or a number of seconds, with its unit inside the field's edge. */
function NumberField({
  id,
  label,
  unit,
  max,
  value,
  disabled,
  onChange,
}: {
  id: string
  label: string
  unit: string
  max: number
  value: number
  disabled?: boolean
  onChange: (value: number) => void
}) {
  return (
    <Field label={label} htmlFor={id}>
      <InputGroup>
        <InputGroupInput
          id={id}
          type="number"
          min={0}
          max={max}
          value={value}
          readOnly={disabled}
          className="font-mono"
          onChange={(event) => onChange(Number(event.target.value))}
        />
        <InputGroupAddon align="inline-end">
          <InputGroupText className="font-mono">{unit}</InputGroupText>
        </InputGroupAddon>
      </InputGroup>
    </Field>
  )
}
