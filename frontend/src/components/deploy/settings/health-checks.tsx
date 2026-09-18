"use client"

import { Plus, Trash } from "@/components/icons"
import type { DeploymentConfiguration } from "@/lib/types"
import { Field, FieldRow, OptionRow } from "@/components/form"
import { Group } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"

export type Check = DeploymentConfiguration["checks"][number]
type CheckKind = "http" | "tcp" | "docker_health" | "command"

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

const KIND_LABELS: Record<CheckKind, string> = {
  http: "HTTP",
  tcp: "TCP",
  docker_health: "Docker health",
  command: "Command",
}

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

function newCheck(phase: "readiness" | "smoke"): Check {
  return {
    name: phase === "readiness" ? "Readiness check" : "Smoke check",
    kind: "http",
    phase,
    required: true,
    config: defaultsFor("http") as Record<string, unknown>,
  }
}

/**
 * The evidence a release must produce before it takes traffic (readiness)
 * and once more right after (smoke). Each kind asks only the questions its
 * own probe can answer — a Docker health check has nothing to configure
 * beyond how long to wait for one.
 */
export function HealthChecks({
  checks,
  disabled,
  onChange,
  rowError,
}: {
  checks: Check[]
  disabled?: boolean
  onChange: (checks: Check[]) => void
  /** The message a save refused for this check, when it named the check but no sub-field. */
  rowError?: (index: number) => string | undefined
}) {
  const update = (index: number, patch: Partial<Check>) =>
    onChange(checks.map((check, i) => (i === index ? { ...check, ...patch } : check)))
  const updateConfig = (index: number, patch: Partial<Config>) =>
    update(index, { config: { ...(checks[index].config as Config), ...patch } })

  return (
    <div className="space-y-3">
      {checks.length === 0 ? (
        <p className="text-body text-muted-foreground">No health checks configured.</p>
      ) : (
        <div className="space-y-3">
          {checks.map((check, index) => {
            const config = (check.config ?? {}) as Config
            const kind = check.kind as CheckKind
            return (
              <Group key={index} className="space-y-3">
                <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_9rem_9rem_auto]">
                  <Input
                    aria-label={`Health check ${index + 1} name`}
                    value={check.name}
                    onChange={(event) => update(index, { name: event.target.value })}
                    readOnly={disabled}
                  />
                  <Select
                    value={kind}
                    disabled={disabled}
                    onValueChange={(next: CheckKind) =>
                      update(index, {
                        kind: next,
                        config: defaultsFor(next) as Record<string, unknown>,
                      })
                    }
                  >
                    <SelectTrigger aria-label={`Health check ${index + 1} kind`}>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {(Object.entries(KIND_LABELS) as [CheckKind, string][]).map(
                        ([value, label]) => (
                          <SelectItem key={value} value={value}>
                            {label}
                          </SelectItem>
                        ),
                      )}
                    </SelectContent>
                  </Select>
                  <Select
                    value={check.phase}
                    disabled={disabled}
                    onValueChange={(phase: "readiness" | "smoke") => update(index, { phase })}
                  >
                    <SelectTrigger aria-label={`Health check ${index + 1} phase`}>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="readiness">Readiness</SelectItem>
                      <SelectItem value="smoke">Smoke</SelectItem>
                    </SelectContent>
                  </Select>
                  {!disabled && (
                    <Button
                      type="button"
                      size="icon-sm"
                      variant="ghost"
                      aria-label={`Remove health check ${check.name || index + 1}`}
                      onClick={() => onChange(checks.filter((_, i) => i !== index))}
                    >
                      <Trash />
                    </Button>
                  )}
                </div>

                {kind === "http" && (
                  <>
                    <FieldRow columns={3}>
                      <Field label="Path" htmlFor={`check-${index}-path`}>
                        <Input
                          id={`check-${index}-path`}
                          value={config.path ?? "/"}
                          readOnly={disabled}
                          className="font-mono"
                          onChange={(event) => updateConfig(index, { path: event.target.value })}
                        />
                      </Field>
                      <Field
                        label="Port"
                        htmlFor={`check-${index}-port`}
                        hint="Leave blank to follow the runtime publication."
                      >
                        <Input
                          id={`check-${index}-port`}
                          type="number"
                          min={0}
                          max={65535}
                          value={config.port ?? ""}
                          readOnly={disabled}
                          onChange={(event) =>
                            updateConfig(index, {
                              port: event.target.value ? Number(event.target.value) : undefined,
                            })
                          }
                        />
                      </Field>
                      <Field label="Method" htmlFor={`check-${index}-method`}>
                        <Select
                          value={config.method ?? "GET"}
                          disabled={disabled}
                          onValueChange={(method) => updateConfig(index, { method })}
                        >
                          <SelectTrigger id={`check-${index}-method`}>
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectItem value="GET">GET</SelectItem>
                            <SelectItem value="HEAD">HEAD</SelectItem>
                          </SelectContent>
                        </Select>
                      </Field>
                    </FieldRow>
                    <Field
                      label="Expected status codes"
                      htmlFor={`check-${index}-status`}
                      hint="Comma separated. Leave blank to accept any final 2xx."
                    >
                      <Input
                        id={`check-${index}-status`}
                        value={(config.expectedStatus ?? []).join(", ")}
                        readOnly={disabled}
                        className="font-mono"
                        placeholder="200, 204"
                        onChange={(event) =>
                          updateConfig(index, {
                            expectedStatus: event.target.value
                              .split(",")
                              .map((part) => Number(part.trim()))
                              .filter((n) => Number.isInteger(n) && n > 0),
                          })
                        }
                      />
                    </Field>
                  </>
                )}

                {kind === "tcp" && (
                  <FieldRow columns={2}>
                    <Field label="Host (optional)" htmlFor={`check-${index}-host`}>
                      <Input
                        id={`check-${index}-host`}
                        value={config.host ?? ""}
                        readOnly={disabled}
                        className="font-mono"
                        onChange={(event) => updateConfig(index, { host: event.target.value })}
                      />
                    </Field>
                    <Field label="Port" htmlFor={`check-${index}-tcp-port`}>
                      <Input
                        id={`check-${index}-tcp-port`}
                        type="number"
                        min={0}
                        max={65535}
                        value={config.port ?? 0}
                        readOnly={disabled}
                        onChange={(event) =>
                          updateConfig(index, { port: Number(event.target.value) })
                        }
                      />
                    </Field>
                  </FieldRow>
                )}

                {kind === "command" && (
                  <Field
                    label="Command argv"
                    htmlFor={`check-${index}-command`}
                    hint="One argument per line, run inside the candidate container."
                  >
                    <Textarea
                      id={`check-${index}-command`}
                      value={(config.command ?? []).join("\n")}
                      readOnly={disabled}
                      className="min-h-20 font-mono text-xs"
                      onChange={(event) =>
                        updateConfig(index, {
                          command: event.target.value
                            .split("\n")
                            .map((line) => line.trim())
                            .filter(Boolean),
                        })
                      }
                    />
                  </Field>
                )}

                <FieldRow columns={3}>
                  <Field
                    label="Attempts"
                    htmlFor={`check-${index}-attempts`}
                    hint="0–60. Default 10."
                  >
                    <Input
                      id={`check-${index}-attempts`}
                      type="number"
                      min={0}
                      max={60}
                      value={config.attempts ?? 10}
                      readOnly={disabled}
                      onChange={(event) =>
                        updateConfig(index, { attempts: Number(event.target.value) })
                      }
                    />
                  </Field>
                  <Field
                    label="Timeout (seconds)"
                    htmlFor={`check-${index}-timeout`}
                    hint="0–60. Default 3."
                  >
                    <Input
                      id={`check-${index}-timeout`}
                      type="number"
                      min={0}
                      max={60}
                      value={config.timeoutSeconds ?? 3}
                      readOnly={disabled}
                      onChange={(event) =>
                        updateConfig(index, { timeoutSeconds: Number(event.target.value) })
                      }
                    />
                  </Field>
                  {kind === "http" && (
                    <Field
                      label="Interval (seconds)"
                      htmlFor={`check-${index}-interval`}
                      hint="0–60. Time between attempts."
                    >
                      <Input
                        id={`check-${index}-interval`}
                        type="number"
                        min={0}
                        max={60}
                        value={config.intervalSeconds ?? 0}
                        readOnly={disabled}
                        onChange={(event) =>
                          updateConfig(index, { intervalSeconds: Number(event.target.value) })
                        }
                      />
                    </Field>
                  )}
                </FieldRow>

                <OptionRow
                  title="Required"
                  hint="A failing required check blocks activation; an optional one only warns."
                  checked={check.required}
                  onCheckedChange={(required) => update(index, { required })}
                  disabled={disabled}
                />
                {rowError?.(index) && (
                  <p role="alert" className="text-hint text-destructive">
                    {rowError(index)}
                  </p>
                )}
              </Group>
            )
          })}
        </div>
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
