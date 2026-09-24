"use client"

import { Plus, Trash } from "@/components/icons"
import { Field, FieldRow, FormSection, OptionList, OptionRow } from "@/components/form"
import { Group } from "@/components/panel"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { humanize } from "@/components/deploy/vocabulary"
import { MountRows } from "@/components/deploy/settings/mounts"
import { imageProduct } from "@/components/product-logo"
import { EmptyNote } from "@/components/state"
import type { DeploymentConfiguration, DeploymentRestartPolicy } from "@/lib/types"
import type { WizardErrors } from "@/components/deploy/deployment-defaults"

type Check = DeploymentConfiguration["checks"][number]
type Mount = NonNullable<DeploymentConfiguration["runtime"]["mounts"]>[number]
type BuildSecret = NonNullable<DeploymentConfiguration["build"]["secrets"]>[number]
type ReleaseTask = NonNullable<DeploymentConfiguration["build"]["releaseTasks"]>[number]
type Variable = DeploymentConfiguration["variables"][number]

function nonemptyLines(value: string) {
  return value
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
}

/**
 * Everything a plan can carry that most deployments never touch: fixed
 * resource limits, a non-default release strategy, storage, release tasks,
 * build secrets, container privileges, and the declarative variable
 * references a reviewed recipe or a release task reads by name.
 *
 * Structured rows rather than the old wizard's raw JSON textareas (mounts,
 * checks, dependencies): the plan schema is exact either way, but a row the
 * form validates as you type is a plan a first-time operator can actually
 * finish, and JSON asked for the normalized field names from memory.
 *
 * This was one `Advanced` fold holding seven sections at the foot of one
 * Configure screen — so "is my answer in there" cost a press and a wall of
 * twenty-five fields, and a preflight finding about a health check had to
 * open a fold programmatically to point at the control that fixed it. The
 * seven are exported one at a time now and each is drawn on the step that
 * owns it: the build extras on Project, the limits, the checks, the storage
 * and the container on Runtime, the references on Variables. A fold there
 * holds three fields and says which three while it is shut.
 */

type AdvancedProps = {
  configuration: DeploymentConfiguration
  onChange: (configuration: DeploymentConfiguration) => void
}

/**
 * What the container is allowed to use. Not the application port — that one
 * is the first thing the runtime step asks, in the open, because a plan with
 * no port is a plan with no address.
 */
export function RuntimeLimits({
  configuration,
  onChange,
  errors,
}: AdvancedProps & { errors: WizardErrors }) {
  const updateRuntime = (patch: Partial<DeploymentConfiguration["runtime"]>) =>
    onChange({ ...configuration, runtime: { ...configuration.runtime, ...patch } })
  return (
    <div className="space-y-4">
      <FieldRow columns={2}>
        <Field
          label="Host port"
          htmlFor="adv-host-port"
          hint="0 leaves the service private behind its managed route."
          error={errors.hostPort}
        >
          <Input
            id="adv-host-port"
            type="number"
            min={0}
            max={65535}
            value={configuration.runtime.hostPort ?? 0}
            onChange={(event) => updateRuntime({ hostPort: Number(event.target.value) || 0 })}
            className="font-mono"
          />
        </Field>
        <Field label="Bind address" htmlFor="adv-bind-address">
          <Select
            value={configuration.runtime.bindAddress || "127.0.0.1"}
            onValueChange={(bindAddress) => updateRuntime({ bindAddress })}
          >
            <SelectTrigger id="adv-bind-address" className="w-full font-mono">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="127.0.0.1">127.0.0.1 · loopback</SelectItem>
              <SelectItem value="::1">::1 · loopback IPv6</SelectItem>
              <SelectItem value="0.0.0.0">0.0.0.0 · every interface</SelectItem>
              <SelectItem value="::">:: · every IPv6 interface</SelectItem>
            </SelectContent>
          </Select>
        </Field>
      </FieldRow>
      <FieldRow columns={2}>
        <Field label="Release strategy" htmlFor="adv-strategy">
          <Select
            value={configuration.runtime.strategy}
            onValueChange={(strategy) =>
              updateRuntime({ strategy: strategy as "blue_green" | "stop_first" })
            }
          >
            <SelectTrigger id="adv-strategy" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="blue_green">Candidate first</SelectItem>
              <SelectItem value="stop_first">Stop first</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        <Field label="Restart policy" htmlFor="adv-restart-policy">
          <Select
            value={configuration.runtime.restartPolicy || "unless-stopped"}
            onValueChange={(restartPolicy) =>
              updateRuntime({ restartPolicy: restartPolicy as DeploymentRestartPolicy })
            }
          >
            <SelectTrigger id="adv-restart-policy" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="unless-stopped">Unless stopped</SelectItem>
              <SelectItem value="always">Always</SelectItem>
              <SelectItem value="on-failure">On failure</SelectItem>
              <SelectItem value="no">Never</SelectItem>
            </SelectContent>
          </Select>
        </Field>
      </FieldRow>
      <FieldRow columns={3}>
        <Field label="Memory limit (MB)" htmlFor="adv-memory" hint="0 for no limit.">
          <Input
            id="adv-memory"
            type="number"
            min={0}
            value={configuration.runtime.memoryMb ?? 0}
            onChange={(event) => updateRuntime({ memoryMb: Number(event.target.value) || 0 })}
            className="font-mono"
          />
        </Field>
        <Field label="CPU limit (cores)" htmlFor="adv-cpus" hint="0 for no limit.">
          <Input
            id="adv-cpus"
            type="number"
            min={0}
            step="0.1"
            value={configuration.runtime.cpus ?? 0}
            onChange={(event) => updateRuntime({ cpus: Number(event.target.value) || 0 })}
            className="font-mono"
          />
        </Field>
        <Field label="Process limit" htmlFor="adv-pids" hint="0 for no limit.">
          <Input
            id="adv-pids"
            type="number"
            min={0}
            value={configuration.runtime.pidsLimit ?? 0}
            onChange={(event) => updateRuntime({ pidsLimit: Number(event.target.value) || 0 })}
            className="font-mono"
          />
        </Field>
      </FieldRow>
    </div>
  )
}

/** What has to answer before a release is allowed to take traffic. */
export function HealthChecks({ configuration, onChange }: AdvancedProps) {
  const readiness = configuration.checks.find((check) => check.phase === "readiness")
  const smoke = configuration.checks.find((check) => check.phase === "smoke")
  const setCheck = (previous: Check | undefined, next: Check | undefined) =>
    onChange({
      ...configuration,
      checks: next
        ? previous
          ? configuration.checks.map((check) => (check === previous ? next : check))
          : [...configuration.checks, next]
        : configuration.checks.filter((check) => check !== previous),
    })
  return (
    <div className="space-y-4">
      <div className="space-y-3">
        {readiness ? (
          <CheckFields
            idPrefix="readiness"
            check={readiness}
            onChange={(next) => setCheck(readiness, next)}
            onRemove={() => setCheck(readiness, undefined)}
          />
        ) : (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() =>
              setCheck(undefined, {
                name: "Readiness",
                kind: "http",
                phase: "readiness",
                required: true,
                config: { path: "/", attempts: 20, timeoutSeconds: 5, intervalSeconds: 3 },
              })
            }
          >
            <Plus className="size-3.5" /> Add a readiness check
          </Button>
        )}
      </div>
      <div className="border-t border-hairline pt-3">
        <OptionRow
          title="Add a smoke check against the live route after activation"
          checked={Boolean(smoke)}
          onCheckedChange={(on) =>
            setCheck(
              smoke,
              on
                ? {
                    name: "Smoke test",
                    kind: "http",
                    phase: "smoke",
                    required: false,
                    config: { path: "/", attempts: 3, timeoutSeconds: 5 },
                  }
                : undefined,
            )
          }
        >
          {smoke && (
            <CheckFields
              idPrefix="smoke"
              check={smoke}
              onChange={(next) => setCheck(smoke, next)}
            />
          )}
        </OptionRow>
      </div>
    </div>
  )
}

/**
 * What survives the container it is written in.
 *
 * The rows are `MountRows`, the editor Storage settings draws, so a mount is
 * the same fields in the same order on the day the project is created and on
 * every day after: this had grown its own copy with a 12px "Read-only"
 * checkbox and no labels, which is two answers to one question.
 */
export function StorageMounts({ configuration, onChange }: AdvancedProps) {
  const mounts = configuration.runtime.mounts ?? []
  const setMounts = (next: Mount[]) =>
    onChange({ ...configuration, runtime: { ...configuration.runtime, mounts: next } })
  // A named volume is drawn as the product whose data it keeps, as Storage
  // settings draws it — Postgres's elephant, not a generic disk, for an image
  // or template that names one.
  const named = configuration.runtime.image ? imageProduct(configuration.runtime.image) : undefined
  return (
    <div className="space-y-3">
      {/* One line, not MountRows' own empty state: that is a dashed frame and
          a plate inside the step's framed surface, repeating an absence the
          fold's head has already stated. */}
      {mounts.length === 0 ? (
        <EmptyNote className="px-0 py-2 text-left">
          Nothing is mounted, so whatever the container writes is gone on its next release.
        </EmptyNote>
      ) : (
        <MountRows
          mounts={mounts}
          onChange={setMounts}
          idPrefix="adv-mount"
          product={named === "docker" ? undefined : named}
        />
      )}
      <Button
        type="button"
        variant="outline"
        size="sm"
        // Managed, not the settings page's linked: a mount named while the
        // project is being created is one the project makes, and owns.
        onClick={() => setMounts([...mounts, { source: "", target: "", ownership: "managed" }])}
      >
        <Plus className="size-3.5" /> Add mount
      </Button>
    </div>
  )
}

/**
 * What the container is allowed to reach on the host it runs on.
 *
 * The two binaries were 12px words beside a switch, each with a "?" holding
 * the sentence that said what turning it on costs. On `/deploy/new` an
 * option's title says the whole thing (§7), so the consequence is the title
 * and the explainer went with it. The title turns amber while the option is
 * on: that is a reading of what this container will be allowed to do, not a
 * warning painted on a switch nobody has touched.
 */
export function ContainerAccess({ configuration, onChange }: AdvancedProps) {
  const updateRuntime = (patch: Partial<DeploymentConfiguration["runtime"]>) =>
    onChange({ ...configuration, runtime: { ...configuration.runtime, ...patch } })
  const privileged = configuration.runtime.privileged ?? false
  const hostNetwork = configuration.runtime.hostNetwork ?? false
  return (
    <div className="space-y-4">
      <OptionList>
        <OptionRow
          title="Run privileged — every host device and kernel capability"
          tone={privileged ? "warning" : "default"}
          checked={privileged}
          onCheckedChange={(next) => updateRuntime({ privileged: next })}
        />
        <OptionRow
          title="Share the host's network — every port it opens is open on the host"
          tone={hostNetwork ? "warning" : "default"}
          checked={hostNetwork}
          onCheckedChange={(next) => updateRuntime({ hostNetwork: next })}
        />
      </OptionList>
      <FieldRow columns={2}>
        <Field label="Linux capabilities" htmlFor="adv-capabilities" hint="One per line.">
          <Textarea
            id="adv-capabilities"
            value={(configuration.runtime.capabilities ?? []).join("\n")}
            onChange={(event) => updateRuntime({ capabilities: nonemptyLines(event.target.value) })}
            rows={3}
            className="font-mono sm:text-xs"
          />
        </Field>
        <Field label="Host devices" htmlFor="adv-devices" hint="One absolute path per line.">
          <Textarea
            id="adv-devices"
            value={(configuration.runtime.devices ?? []).join("\n")}
            onChange={(event) => updateRuntime({ devices: nonemptyLines(event.target.value) })}
            rows={3}
            className="font-mono sm:text-xs"
          />
        </Field>
      </FieldRow>
    </div>
  )
}

/**
 * The parts of a build nobody sets on a first deployment: which platform it
 * targets, whether the cache is honoured, the gates that run before
 * activation, and the secrets BuildKit mounts for one named step.
 */
export function BuildExtras({
  configuration,
  onChange,
  errors,
}: AdvancedProps & { errors: WizardErrors }) {
  const updateBuild = (patch: Partial<DeploymentConfiguration["build"]>) =>
    onChange({ ...configuration, build: { ...configuration.build, ...patch } })
  return (
    <div className="space-y-6">
      <div className="space-y-3">
        <Field
          label="Target platform"
          htmlFor="adv-target-platform"
          hint="Optional OCI platform, for example linux/amd64."
        >
          <Input
            id="adv-target-platform"
            value={configuration.build.targetPlatform ?? ""}
            onChange={(event) => updateBuild({ targetPlatform: event.target.value })}
            placeholder="linux/amd64"
            className="font-mono"
          />
        </Field>
        <OptionRow
          title="Build without the cache — every layer from scratch"
          checked={configuration.build.noCache ?? false}
          onCheckedChange={(noCache) => updateBuild({ noCache })}
        />
      </div>

      <FormSection
        title="Release tasks"
        hint="Named, timed gates run after the artifact is recorded and before activation."
      >
        <ReleaseTaskEditor
          tasks={configuration.build.releaseTasks ?? []}
          variables={configuration.variables}
          error={errors.releaseTasks}
          onChange={(releaseTasks) => updateBuild({ releaseTasks })}
        />
      </FormSection>

      {configuration.build.method === "recipe" && (
        <FormSection
          title="Build secrets"
          hint="Reviewed recipes mount these only for the named BuildKit step; values never enter argv."
        >
          <BuildSecretEditor
            secrets={configuration.build.secrets ?? []}
            variables={configuration.variables}
            error={errors.buildSecrets}
            onChange={(secrets) => updateBuild({ secrets })}
          />
        </FormSection>
      )}
    </div>
  )
}

/**
 * Variables the *plan* declares, as against the values imported once the
 * project exists: a reference naming a stored or generated value, and the
 * scopes that decide which steps can read it.
 */
export function VariableReferences({
  configuration,
  onChange,
  overriddenNames,
}: AdvancedProps & { overriddenNames?: string[] }) {
  return (
    <VariableEditor
      variables={configuration.variables}
      overriddenNames={overriddenNames}
      onChange={(variables) => onChange({ ...configuration, variables })}
    />
  )
}

const CHECK_KINDS = [
  ["http", "HTTP request"],
  ["tcp", "TCP connection"],
  ["docker_health", "Container health check"],
  ["command", "Command"],
] as const

/** The fields one check needs, which vary by kind. */
function CheckFields({
  idPrefix,
  check,
  onChange,
  onRemove,
}: {
  idPrefix: string
  check: Check
  onChange: (check: Check) => void
  onRemove?: () => void
}) {
  const config = (check.config ?? {}) as Record<string, unknown>
  const updateConfig = (patch: Record<string, unknown>) =>
    onChange({ ...check, config: { ...config, ...patch } })
  return (
    <div className="space-y-3">
      <FieldRow columns={onRemove ? 3 : 2}>
        <Field label="Kind" htmlFor={`${idPrefix}-kind`}>
          <Select
            value={check.kind}
            onValueChange={(kind) => onChange({ ...check, kind, config: {} })}
          >
            <SelectTrigger id={`${idPrefix}-kind`} className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {CHECK_KINDS.map(([value, label]) => (
                <SelectItem key={value} value={value}>
                  {label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field label="Name" htmlFor={`${idPrefix}-name`}>
          <Input
            id={`${idPrefix}-name`}
            value={check.name}
            onChange={(event) => onChange({ ...check, name: event.target.value })}
          />
        </Field>
        {onRemove && (
          <div className="flex items-end">
            <Button type="button" variant="ghost" size="sm" onClick={onRemove}>
              <Trash className="size-3.5" /> Remove
            </Button>
          </div>
        )}
      </FieldRow>
      {check.kind === "http" && (
        <FieldRow columns={2}>
          <Field label="Path" htmlFor={`${idPrefix}-path`} hint="Starts with /.">
            <Input
              id={`${idPrefix}-path`}
              value={String(config.path ?? "/")}
              onChange={(event) => updateConfig({ path: event.target.value })}
              className="font-mono"
            />
          </Field>
          <Field
            label="Port"
            htmlFor={`${idPrefix}-port`}
            hint="Leave empty to use the published port."
          >
            <Input
              id={`${idPrefix}-port`}
              type="number"
              min={0}
              max={65535}
              value={config.port ? String(config.port) : ""}
              onChange={(event) =>
                updateConfig({ port: event.target.value ? Number(event.target.value) : undefined })
              }
              className="font-mono"
            />
          </Field>
        </FieldRow>
      )}
      {check.kind === "http" && (
        <OptionRow
          title="Any answer counts"
          hint="Anything below 500 except 400 and 421 passes, for an API with no page at this path. Off, only a 2xx passes."
          checked={Boolean(config.acceptAnyAnswer)}
          onCheckedChange={(acceptAnyAnswer) =>
            updateConfig(
              acceptAnyAnswer
                ? { acceptAnyAnswer: true, expectedStatus: undefined }
                : { acceptAnyAnswer: undefined },
            )
          }
        />
      )}
      {check.kind === "tcp" && (
        <FieldRow columns={2}>
          <Field
            label="Host"
            htmlFor={`${idPrefix}-host`}
            hint="Leave empty to use the candidate's own address."
          >
            <Input
              id={`${idPrefix}-host`}
              value={String(config.host ?? "")}
              onChange={(event) => updateConfig({ host: event.target.value })}
              className="font-mono"
            />
          </Field>
          <Field label="Port" htmlFor={`${idPrefix}-tcp-port`}>
            <Input
              id={`${idPrefix}-tcp-port`}
              type="number"
              min={0}
              max={65535}
              value={config.port ? String(config.port) : ""}
              onChange={(event) =>
                updateConfig({ port: event.target.value ? Number(event.target.value) : undefined })
              }
              className="font-mono"
            />
          </Field>
        </FieldRow>
      )}
      {check.kind === "command" && (
        <Field label="Command" htmlFor={`${idPrefix}-command`} hint="One argument per line.">
          <Textarea
            id={`${idPrefix}-command`}
            value={Array.isArray(config.command) ? (config.command as string[]).join("\n") : ""}
            onChange={(event) => updateConfig({ command: nonemptyLines(event.target.value) })}
            rows={3}
            className="font-mono sm:text-xs"
          />
        </Field>
      )}
      {check.kind !== "docker_health" && (
        <FieldRow columns={3}>
          <Field label="Attempts" htmlFor={`${idPrefix}-attempts`}>
            <Input
              id={`${idPrefix}-attempts`}
              type="number"
              min={0}
              max={60}
              value={config.attempts ? String(config.attempts) : ""}
              onChange={(event) => updateConfig({ attempts: Number(event.target.value) || 0 })}
              className="font-mono"
            />
          </Field>
          <Field label="Timeout (seconds)" htmlFor={`${idPrefix}-timeout`}>
            <Input
              id={`${idPrefix}-timeout`}
              type="number"
              min={0}
              max={60}
              value={config.timeoutSeconds ? String(config.timeoutSeconds) : ""}
              onChange={(event) =>
                updateConfig({ timeoutSeconds: Number(event.target.value) || 0 })
              }
              className="font-mono"
            />
          </Field>
          <Field label="Interval (seconds)" htmlFor={`${idPrefix}-interval`}>
            <Input
              id={`${idPrefix}-interval`}
              type="number"
              min={0}
              max={60}
              value={config.intervalSeconds ? String(config.intervalSeconds) : ""}
              onChange={(event) =>
                updateConfig({ intervalSeconds: Number(event.target.value) || 0 })
              }
              className="font-mono"
            />
          </Field>
        </FieldRow>
      )}
      <OptionRow
        title={
          idPrefix === "smoke"
            ? "Required — a failing smoke check blocks the release"
            : "Required — a failing readiness check blocks activation"
        }
        checked={check.required}
        onCheckedChange={(required) => onChange({ ...check, required })}
      />
    </div>
  )
}

/** A variable name that names a stored value, generated once and never re-typed. */
function VariableEditor({
  variables,
  onChange,
  overriddenNames = [],
}: {
  variables: Variable[]
  onChange: (variables: Variable[]) => void
  overriddenNames?: string[]
}) {
  // The add sits under the rows, where every other list on these screens puts
  // it (Add variable, Add mount, Add another hostname), and an empty list is
  // just that button rather than a sentence saying there is nothing above it.
  return (
    <div className="space-y-3">
      {variables.length > 0 && (
        <div className="space-y-2">
          {variables.map((variable, index) => (
            <Group
              key={index}
              className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_8rem_minmax(0,1.4fr)_auto]"
            >
              <Input
                aria-label={`Variable ${index + 1} name`}
                value={variable.name}
                onChange={(event) =>
                  onChange(
                    variables.map((item, i) =>
                      i === index ? { ...item, name: event.target.value.toUpperCase() } : item,
                    ),
                  )
                }
                placeholder="NPM_TOKEN"
                className="font-mono"
              />
              <Select
                value={variable.sensitivity}
                onValueChange={(sensitivity) =>
                  onChange(
                    variables.map((item, i) =>
                      i === index
                        ? {
                            ...item,
                            sensitivity: sensitivity as "plain" | "secret",
                            ...(sensitivity === "secret"
                              ? { value: undefined, domainTemplate: undefined }
                              : { generate: undefined }),
                          }
                        : item,
                    ),
                  )
                }
              >
                <SelectTrigger
                  aria-label={`Variable ${variable.name || index + 1} sensitivity`}
                  className="w-full"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="secret">Secret</SelectItem>
                  <SelectItem value="plain">Plain</SelectItem>
                </SelectContent>
              </Select>
              {variable.generate ? (
                <Input
                  aria-label={`Variable ${variable.name || index + 1} value`}
                  value={
                    overriddenNames.includes(variable.name)
                      ? `Supplied value overrides the ${variable.generate}-character default`
                      : `Generated on save (${variable.generate} characters)`
                  }
                  readOnly
                  className="font-mono text-muted-foreground"
                />
              ) : variable.value !== undefined && variable.sensitivity === "plain" ? (
                <Input
                  aria-label={`Variable ${variable.name || index + 1} value`}
                  value={variable.value}
                  onChange={(event) =>
                    onChange(
                      variables.map((item, i) =>
                        i === index ? { ...item, value: event.target.value } : item,
                      ),
                    )
                  }
                  placeholder="value"
                  className="font-mono"
                />
              ) : (
                <Input
                  aria-label={`Variable ${variable.name || index + 1} reference`}
                  value={variable.reference ?? ""}
                  onChange={(event) =>
                    onChange(
                      variables.map((item, i) =>
                        i === index
                          ? {
                              ...item,
                              reference: event.target.value,
                              value: undefined,
                              generate: undefined,
                              domainTemplate: undefined,
                            }
                          : item,
                      ),
                    )
                  }
                  placeholder="${{credential.name}}"
                  className="font-mono"
                />
              )}
              <IconAction
                label={`Remove variable reference ${variable.name || index + 1}`}
                onClick={() => onChange(variables.filter((_, i) => i !== index))}
              >
                <Trash />
              </IconAction>
              <div className="flex flex-wrap gap-x-4 gap-y-2 sm:col-span-4">
                {["build", "runtime", "release_task"].map((scope) => (
                  <Label key={scope} className="flex min-h-9 items-center gap-2 text-body">
                    <Checkbox
                      checked={variable.scopes.includes(scope)}
                      onCheckedChange={(checked) =>
                        onChange(
                          variables.map((item, i) =>
                            i === index
                              ? {
                                  ...item,
                                  scopes: checked
                                    ? [...new Set([...item.scopes, scope])]
                                    : item.scopes.filter((value) => value !== scope),
                                }
                              : item,
                          ),
                        )
                      }
                    />
                    {humanize(scope)}
                  </Label>
                ))}
                <Label className="flex min-h-9 items-center gap-2 text-body">
                  <Checkbox
                    checked={variable.required ?? false}
                    onCheckedChange={(checked) =>
                      onChange(
                        variables.map((item, i) =>
                          i === index ? { ...item, required: checked === true } : item,
                        ),
                      )
                    }
                  />
                  Required
                </Label>
              </div>
            </Group>
          ))}
        </div>
      )}
      <Button
        type="button"
        size="sm"
        variant="outline"
        onClick={() =>
          onChange([
            ...variables,
            {
              name: "",
              sensitivity: "secret",
              scopes: ["runtime"],
              required: false,
              reference: "",
            },
          ])
        }
      >
        <Plus className="size-3.5" />
        Add variable reference
      </Button>
    </div>
  )
}

function BuildSecretEditor({
  secrets,
  variables,
  error,
  onChange,
}: {
  secrets: BuildSecret[]
  variables: Variable[]
  error?: string
  onChange: (secrets: BuildSecret[]) => void
}) {
  const buildVariables = variables.filter((variable) => variable.scopes.includes("build"))
  return (
    <div className="space-y-3">
      {secrets.length > 0 && (
        <div className="space-y-2">
          {secrets.map((secret, index) => (
            <Group key={index} className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_10rem_auto]">
              <Input
                aria-label={`Build secret ${index + 1} variable`}
                value={secret.variable}
                onChange={(event) =>
                  onChange(
                    secrets.map((item, i) =>
                      i === index ? { ...item, variable: event.target.value.toUpperCase() } : item,
                    ),
                  )
                }
                list="build-variable-names"
                placeholder="NPM_TOKEN"
                className="font-mono"
                aria-invalid={Boolean(error)}
              />
              <Select
                value={secret.step}
                onValueChange={(step) =>
                  onChange(
                    secrets.map((item, i) =>
                      i === index ? { ...item, step: step as "install" | "build" } : item,
                    ),
                  )
                }
              >
                <SelectTrigger
                  aria-label={`Build secret ${secret.variable || index + 1} step`}
                  className="w-full"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="install">Install step</SelectItem>
                  <SelectItem value="build">Build step</SelectItem>
                </SelectContent>
              </Select>
              <IconAction
                label={`Remove build secret ${secret.variable || index + 1}`}
                onClick={() => onChange(secrets.filter((_, i) => i !== index))}
              >
                <Trash />
              </IconAction>
            </Group>
          ))}
        </div>
      )}
      {error && (
        <p role="alert" className="text-hint leading-relaxed text-destructive">
          {error}
        </p>
      )}
      <Button
        type="button"
        size="sm"
        variant="outline"
        onClick={() => onChange([...secrets, { variable: "", step: "install" }])}
      >
        <Plus className="size-3.5" />
        Add build secret
      </Button>
      <datalist id="build-variable-names">
        {buildVariables.map((variable) => (
          <option key={variable.name} value={variable.name} />
        ))}
      </datalist>
    </div>
  )
}

function ReleaseTaskEditor({
  tasks,
  variables,
  error,
  onChange,
}: {
  tasks: ReleaseTask[]
  variables: Variable[]
  error?: string
  onChange: (tasks: ReleaseTask[]) => void
}) {
  const releaseVariables = variables.filter((variable) => variable.scopes.includes("release_task"))
  const update = (index: number, patch: Partial<ReleaseTask>) =>
    onChange(tasks.map((task, i) => (i === index ? { ...task, ...patch } : task)))
  return (
    <div className="space-y-3">
      {tasks.length > 0 && (
        <div className="space-y-3">
          {tasks.map((task, index) => (
            <Group key={index} className="space-y-3">
              <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_9rem_auto]">
                <Input
                  aria-label={`Release task ${index + 1} name`}
                  value={task.name}
                  onChange={(event) => update(index, { name: event.target.value })}
                  placeholder="Database migration"
                />
                <Input
                  aria-label={`Release task ${index + 1} timeout seconds`}
                  type="number"
                  min={1}
                  max={3600}
                  value={task.timeoutSeconds}
                  onChange={(event) =>
                    update(index, { timeoutSeconds: Number(event.target.value) })
                  }
                  className="font-mono"
                />
                <IconAction
                  label={`Remove release task ${task.name || index + 1}`}
                  onClick={() => onChange(tasks.filter((_, i) => i !== index))}
                >
                  <Trash />
                </IconAction>
              </div>
              <Input
                aria-label={`Release task ${index + 1} working directory`}
                value={task.workingDirectory ?? ""}
                onChange={(event) => update(index, { workingDirectory: event.target.value })}
                placeholder="Working directory (source root by default)"
                className="font-mono"
              />
              <Textarea
                aria-label={`Release task ${index + 1} command`}
                value={task.command}
                onChange={(event) => update(index, { command: event.target.value })}
                placeholder="./bin/migrate"
                rows={3}
                className="font-mono sm:text-xs"
              />
              <div>
                <p className="text-hint text-muted-foreground">Release task environment</p>
                {releaseVariables.length === 0 ? (
                  <p className="mt-1 text-hint text-muted-foreground">
                    Add Release Task scope to a variable above to make it selectable here.
                  </p>
                ) : (
                  <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1">
                    {releaseVariables.map((variable) => (
                      <Label
                        key={variable.name}
                        className="flex min-h-9 items-center gap-2 text-body"
                      >
                        <Checkbox
                          checked={task.env.includes(variable.name)}
                          onCheckedChange={(checked) =>
                            update(index, {
                              env: checked
                                ? [...new Set([...task.env, variable.name])]
                                : task.env.filter((name) => name !== variable.name),
                            })
                          }
                        />
                        <span className="font-mono">{variable.name}</span>
                      </Label>
                    ))}
                  </div>
                )}
              </div>
            </Group>
          ))}
        </div>
      )}
      {error && (
        <p role="alert" className="text-hint leading-relaxed text-destructive">
          {error}
        </p>
      )}
      <Button
        type="button"
        size="sm"
        variant="outline"
        onClick={() =>
          onChange([
            ...tasks,
            { name: "", command: "", workingDirectory: "", timeoutSeconds: 300, env: [] },
          ])
        }
      >
        <Plus className="size-3.5" />
        Add release task
      </Button>
    </div>
  )
}
