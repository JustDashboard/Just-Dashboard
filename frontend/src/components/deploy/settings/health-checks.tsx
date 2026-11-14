"use client"

import { useState } from "react"
import { Connection, Globe, Heart, Plus, Terminal, Trash } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { DeploymentConfiguration } from "@/lib/types"
import { ChoiceList, ChoiceRow, GroupRule } from "@/components/flow"
import { Field, FieldRow, FormNote, OptionRow } from "@/components/form"
import { DimActions, IconAction } from "@/components/icon-action"
import { ProductGlyph, ProductLogo, programProduct } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote } from "@/components/state"
import { Status } from "@/components/status-dot"
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
  /** Any answer below 500 (but 400 and 421) counts, as detection sets for an API. */
  acceptAnyAnswer?: boolean
  command?: string[]
  /** A UDP probe's datagram and the prefix its answer must start with, in hex. */
  payload?: string
  expect?: string
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
    // The runner waits the interval between attempts of every kind; without
    // one, ten attempts at a container still starting take a millisecond.
    case "tcp":
      return { port: 0, attempts: 10, timeoutSeconds: 3, intervalSeconds: 3 }
    case "command":
      return { command: [], attempts: 10, timeoutSeconds: 3, intervalSeconds: 3 }
    case "docker_health":
      return { attempts: 20, timeoutSeconds: 3, intervalSeconds: 3 }
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
  // With no wait between them, "× 0 s" is a cadence that says nothing.
  const cadence = config.intervalSeconds
    ? `${attempts} × ${config.intervalSeconds} s`
    : `${attempts} tries`
  // The two game kinds come from a template's definition rather than this
  // editor's picker, so they are read here but not offered there.
  switch (check.kind as CheckKind | "game_handshake" | "udp_probe") {
    case "game_handshake":
      return `Minecraft status handshake on ${config.host ?? ""}:${config.port || port || "port"} · ${cadence}`
    case "udp_probe":
      return `UDP datagram to ${config.host ?? ""}:${config.port || port || "port"}${
        config.expect ? `, answer starting ${config.expect}` : ", any answer"
      } · ${cadence}`
    case "http": {
      const codes = config.acceptAnyAnswer
        ? "any answer < 500 but 400, 421"
        : config.expectedStatus?.length
          ? config.expectedStatus.join(", ")
          : "2xx"
      return `${config.method ?? "GET"} ${config.path || "/"} → ${codes} · ${cadence}`
    }
    case "tcp":
      return `TCP ${config.host ?? ""}:${config.port || port || "port"} · ${cadence}`
    case "docker_health":
      return `the image's HEALTHCHECK · ${cadence}`
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

/**
 * How long a check keeps trying before it gives up: every attempt's timeout
 * and the wait between attempts, which the runner applies to every kind.
 */
function givesUpAfter(check: Check) {
  const config = (check.config ?? {}) as Config
  const attempts = config.attempts ?? 10
  return attempts * ((config.timeoutSeconds ?? 3) + (config.intervalSeconds ?? 0))
}

/** What a kind of check is drawn as, where no product names it. */
const KIND_MARK: Record<string, React.ComponentType<{ className?: string }>> = {
  http: Globe,
  tcp: Connection,
  docker_health: Heart,
  command: Terminal,
  game_handshake: Connection,
  udp_probe: Connection,
}

/** The product a check is drawn as: Docker for its verdict, a command's own program. */
function checkProduct(check: Check) {
  if (check.kind === "docker_health") return "docker"
  if (check.kind === "command")
    return programProduct(argvOf((check.config as Config | undefined)?.command ?? [])[0])
  return undefined
}

/**
 * The evidence a release must produce before it takes traffic (readiness)
 * and once more right after (smoke), grouped by which of the two each is.
 *
 * Each check is one line: what it is drawn as, its name over what it does in
 * the shape that is written elsewhere — a request line, a socket, a command —
 * then how long it keeps trying and whether it is required, in columns. "20
 * attempts, 5 s timeout, 3 s interval" is three numbers a reader otherwise
 * multiplies in their head to find out how long a bad release is left running.
 *
 * The check's fields are a small form the line opens. Every check used to be
 * a framed box of them, open all the time, so two checks made the foot of the
 * Runtime page a stack of forms and the page could not be scanned. The form
 * edits the page's draft like any field on it: closing it saves nothing, and
 * the page's one Save sends the checks with everything else.
 *
 * A check rises as it is added, and again as it moves into the other phase's
 * group; a kind's own fields rise as the kind is chosen, because they replace
 * the last kind's rather than being edited in place.
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
  // The check whose form is open, by its place in the list.
  const [open, setOpen] = useState<number>()
  const update = (index: number, patch: Partial<Check>) =>
    onChange(checks.map((check, i) => (i === index ? { ...check, ...patch } : check)))
  const remove = (index: number) => {
    setOpen(undefined)
    onChange(checks.filter((_, i) => i !== index))
  }
  const editing = open !== undefined ? checks[open] : undefined

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
              <ChoiceList aria-label={`${phase.label} checks`}>
                {entries.map(({ check, index }) => {
                  const name = check.name || `Health check ${index + 1}`
                  const error = rowError?.(index)
                  return (
                    <ChoiceRow
                      key={index}
                      className="animate-rise"
                      verb={`${disabled ? "Open" : "Edit"} ${name}`}
                      onSelect={() => setOpen(index)}
                      leading={
                        <ProductLogo
                          size="sm"
                          id={checkProduct(check)}
                          fallback={KIND_MARK[check.kind] ?? Heart}
                        />
                      }
                      title={name}
                      // A refusal takes the line the summary was on, so a
                      // refused check is the height of every other.
                      description={
                        error ? (
                          <span role="alert" className="text-destructive">
                            {error}
                          </span>
                        ) : (
                          <span className="font-mono">{checkSummary(check, port)}</span>
                        )
                      }
                      trailing={
                        <>
                          <span className="numeric w-28 text-right text-hint whitespace-nowrap text-muted-foreground max-sm:hidden">
                            gives up ~{formatDuration(givesUpAfter(check))}
                          </span>
                          <Status
                            tone={error ? "danger" : check.required ? "running" : "stopped"}
                            label={error ? "Not saved" : check.required ? "Required" : "Optional"}
                            className="w-20"
                          />
                        </>
                      }
                      actions={
                        !disabled && (
                          <DimActions>
                            <IconAction
                              type="button"
                              label={`Remove health check ${check.name || index + 1}`}
                              onClick={() => remove(index)}
                              className="text-muted-foreground hover:text-destructive"
                            >
                              <Trash />
                            </IconAction>
                          </DimActions>
                        )
                      }
                    />
                  )
                })}
              </ChoiceList>
            </div>
          )
        })
      )}
      {!disabled && (
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => {
            onChange([...checks, newCheck("readiness")])
            setOpen(checks.length)
          }}
        >
          <Plus className="size-3.5" />
          Add check
        </Button>
      )}
      <SidePanel
        open={editing !== undefined}
        onOpenChange={(next) => !next && setOpen(undefined)}
        title={editing ? editing.name || `Health check ${(open ?? 0) + 1}` : "Health check"}
        description="What this check probes, when it runs and how long it keeps trying."
        width="sm"
        // The name already holds a sensible value; landing in it would select it.
        initialFocus="body"
        footer={
          <>
            {!disabled && open !== undefined && (
              <Button
                type="button"
                variant="ghost"
                className="mr-auto text-destructive"
                onClick={() => remove(open)}
              >
                <Trash className="size-3.5" />
                Remove
              </Button>
            )}
            {!disabled && <FormNote className="max-sm:hidden">Saved with the page</FormNote>}
            <Button type="button" onClick={() => setOpen(undefined)}>
              Done
            </Button>
          </>
        }
      >
        {editing && open !== undefined && (
          <CheckEditor
            check={editing}
            index={open}
            disabled={disabled}
            port={port}
            error={rowError?.(open)}
            onChange={(patch) => update(open, patch)}
          />
        )}
      </SidePanel>
    </div>
  )
}

/** One check's form: its name, what kind of probe and when it runs, then that probe's own fields. */
function CheckEditor({
  check,
  index,
  disabled,
  port,
  error,
  onChange,
}: {
  check: Check
  index: number
  disabled?: boolean
  port?: number
  error?: string
  onChange: (patch: Partial<Check>) => void
}) {
  const config = (check.config ?? {}) as Config
  const kind = check.kind as CheckKind
  const n = index + 1
  const id = (field: string) => `check-${index}-${field}`
  const updateConfig = (patch: Partial<Config>) =>
    onChange({ config: { ...config, ...patch } as Record<string, unknown> })
  const program = programProduct(argvOf(config.command ?? [])[0])
  const number = (value: string) => (value === "" ? 0 : Number(value))
  const portPlaceholder = port ? String(port) : "app port"

  return (
    <div className="space-y-4">
      <Field label="Name" htmlFor={id("name")}>
        <Input
          id={id("name")}
          aria-label={`Health check ${n} name`}
          value={check.name}
          onChange={(event) => onChange({ name: event.target.value })}
          readOnly={disabled}
          placeholder="Answers on /health"
        />
      </Field>

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

      <p className="min-w-0 font-mono text-xs break-words text-muted-foreground">
        <span className="text-foreground/80">{checkSummary(check, port)}</span> · gives up after{" "}
        <span className="whitespace-nowrap">~{formatDuration(givesUpAfter(check))}</span>
      </p>

      {kind === "http" && (
        <div className="animate-rise space-y-3">
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
                placeholder={
                  config.acceptAnyAnswer
                    ? "any answer below 500 but 400, 421"
                    : "200, 204 · any 2xx"
                }
                onChange={(event) => {
                  const expectedStatus = event.target.value
                    .split(",")
                    .map((part) => Number(part.trim()))
                    .filter((code) => Number.isInteger(code) && code > 0)
                  // Listed statuses replace any-answer; the server refuses both.
                  updateConfig({
                    expectedStatus,
                    ...(expectedStatus.length ? { acceptAnyAnswer: undefined } : {}),
                  })
                }}
              />
            </Field>
          </FieldRow>
          {/* What passes while it is off is the Expected status placeholder's
              to say, which is where the reader is looking when it matters. */}
          <OptionRow
            title="Any answer counts"
            hint="Anything below 500 but 400 and 421 passes"
            checked={Boolean(config.acceptAnyAnswer)}
            disabled={disabled}
            onCheckedChange={(acceptAnyAnswer) =>
              updateConfig(
                acceptAnyAnswer
                  ? { acceptAnyAnswer: true, expectedStatus: [] }
                  : { acceptAnyAnswer: undefined },
              )
            }
          />
        </div>
      )}

      {kind === "tcp" && (
        <FieldRow className={cn(ADDRESS, "animate-rise")}>
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

      {/* The placeholder shows one argument per line; the rest is behind ⓘ. */}
      {kind === "command" && (
        <Field
          label="Command argv"
          htmlFor={id("command")}
          // A textarea has no addon to carry the program's mark, so it rides
          // the label's edge, where it cannot move the words.
          trailing={program && <ProductGlyph id={program} />}
          info="One argument per line, run inside the new release's container; it passes when the command exits 0."
          className="animate-rise"
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
        columns={3}
        // Short numbers with their units: side by side even on a phone.
        className="grid-cols-3"
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
        <NumberField
          id={id("interval")}
          label="Interval"
          unit="s"
          max={60}
          value={config.intervalSeconds ?? 0}
          disabled={disabled}
          onChange={(intervalSeconds) => updateConfig({ intervalSeconds })}
        />
      </FieldRow>

      <OptionRow
        title="Required"
        hint="A failure stops the release"
        checked={check.required}
        onCheckedChange={(required) => onChange({ required })}
        disabled={disabled}
      />
      {error && (
        <p role="alert" className="animate-rise text-hint text-destructive">
          {error}
        </p>
      )}
    </div>
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
