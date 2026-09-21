"use client"

import { useState } from "react"
import type { FormEvent } from "react"
import { useSessionState } from "@/lib/view-state"
import { ApiError, put, refusedIndex } from "@/lib/api"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import type {
  DeploymentConfiguration,
  DeploymentEnvironmentConfiguration,
  DeploymentRestartPolicy,
} from "@/lib/types"
import {
  Disclosure,
  Field,
  FieldRow,
  FormNote,
  FormSection,
  OptionList,
  OptionRow,
} from "@/components/form"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { ExplainIcon } from "@/components/docker/explain"
import {
  ConfigurationState,
  useConfiguration,
} from "@/components/deploy/settings/use-configuration"
import { PendingChanges } from "@/components/deploy/settings/pending-changes"
import { SettingCard } from "@/components/deploy/settings/setting-card"
import { HealthChecks, type Check } from "@/components/deploy/settings/health-checks"

/**
 * How the release runs once it exists: the image and command, where it
 * listens, what it may use, and the checks that decide when it is ready.
 */

type RuntimePlan = DeploymentConfiguration["runtime"]

const BIND_ADDRESSES: [string, string][] = [
  ["127.0.0.1", "127.0.0.1 · loopback"],
  ["::1", "::1 · loopback IPv6"],
  ["0.0.0.0", "0.0.0.0 · every interface"],
  ["::", ":: · every IPv6 interface"],
]

function linesOf(text: string) {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
}

/** What a restart policy does, for the tile that reads it back. */
const RESTART_PHRASE: Record<string, string> = {
  "unless-stopped": "restarts unless stopped",
  always: "always restarts",
  "on-failure": "restarts on failure",
  no: "never restarts",
}

/**
 * The four readings this form sets, above the form that sets them.
 *
 * §15 pass 2, applied where it had been skipped: this page had ten fields,
 * four selects and two switches on it and not one figure, so the design
 * system's three loud things — a 24px reading, a tone on it, a `Status` —
 * never fired and the screen had nothing a reader could find without reading.
 * §16 makes the same diagnosis about `/deploy/new` and reaches the same
 * answer: a page you configure is not exempt from having readings, and
 * `deploy/credentials-page.tsx` already runs a `PageHeader` straight into a
 * `StatGrid` on a page of exactly this kind.
 *
 * They read the *draft*, not the saved revision: this is what you are setting,
 * and the card's own footer is where "applies on your next deployment" is
 * said. A tile is `warning` where the absence of an answer is itself the
 * answer — an uncapped container and a port open on every interface are the
 * two facts an operator wants off this page without opening a fold.
 */
function RuntimeReadings({
  runtime,
  capabilities,
  devices,
}: {
  runtime: RuntimePlan
  capabilities: string[]
  devices: string[]
}) {
  const hostPort = runtime.hostPort ?? 0
  const bind = runtime.bindAddress || "127.0.0.1"
  const everywhere = bind === "0.0.0.0" || bind === "::"
  const memory = runtime.memoryMb ?? 0
  const cpus = runtime.cpus ?? 0
  const pids = runtime.pidsLimit ?? 0
  const capped = [cpus > 0 ? `${cpus} CPU` : null, pids > 0 ? `${pids} processes` : null].filter(
    Boolean,
  )

  const access = runtime.privileged
    ? { value: "Privileged", tone: "danger" as const }
    : runtime.hostNetwork
      ? { value: "Host network", tone: "warning" as const }
      : { value: "Unprivileged", tone: "default" as const }
  const accessHint =
    [
      // Named here only when the figure above is already spent on `privileged`.
      // With host networking alone the figure *is* "Host network", and a hint
      // repeating it is the fact written twice.
      runtime.privileged && runtime.hostNetwork && "host network",
      capabilities.length > 0 && `${capabilities.length} capabilities`,
      devices.length > 0 && `${devices.length} devices`,
    ]
      .filter(Boolean)
      .join(" · ") ||
    (runtime.hostNetwork ? "no added capabilities" : "own network, no added capabilities")

  return (
    <StatGrid columns={4}>
      <StatTile
        label="Listening on"
        value={`:${runtime.internalPort ?? 0}`}
        tone={hostPort > 0 && everywhere ? "warning" : "default"}
        hint={hostPort > 0 ? `on the host at :${hostPort} · ${bind}` : "private behind its route"}
      />
      <StatTile
        label="Resources"
        value={memory > 0 ? `${memory} MiB` : "No limit"}
        tone={memory > 0 ? "default" : "warning"}
        hint={capped.length > 0 ? capped.join(" · ") : "no CPU or process cap"}
      />
      <StatTile
        label="Releases"
        value={runtime.strategy === "blue_green" ? "Blue / green" : "Stop first"}
        hint={RESTART_PHRASE[runtime.restartPolicy ?? "unless-stopped"]}
      />
      <StatTile
        label="Container access"
        value={access.value}
        tone={access.tone}
        hint={accessHint}
      />
    </StatGrid>
  )
}

function clampPort(value: string) {
  return Math.min(65535, Math.max(0, Number(value) || 0))
}

/** The scalar `runtime.*` fields a validation refusal can name. */
const RUNTIME_FIELD_IDS: Record<string, string> = {
  "runtime.image": "runtime-image",
  "runtime.command": "runtime-command",
  "runtime.internalPort": "runtime-internal-port",
  "runtime.hostPort": "runtime-host-port",
  "runtime.bindAddress": "runtime-bind",
  "runtime.strategy": "runtime-strategy",
  "runtime.memoryMb": "runtime-memory",
  "runtime.cpus": "runtime-cpus",
  "runtime.pidsLimit": "runtime-pids",
  "runtime.restartPolicy": "runtime-restart",
  "runtime.capabilities": "runtime-capabilities",
  "runtime.devices": "runtime-devices",
}

export function RuntimeSettings({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const state = useConfiguration(projectId, environmentId)
  return (
    <ConfigurationState state={state}>
      {(configuration) => (
        // No `key={configuration.revision}`: see the note in build.tsx — a
        // Runtime save must not remount Health checks (and vice versa) and
        // discard whatever the operator was still editing there.
        <div className="space-y-6">
          <PendingChanges pending={configuration.pending} />
          <RuntimeForm
            projectId={projectId}
            environmentId={environmentId}
            configuration={configuration}
            onSaved={state.refresh}
          />
          <HealthChecksCard
            projectId={projectId}
            environmentId={environmentId}
            configuration={configuration}
            onSaved={state.refresh}
          />
        </div>
      )}
    </ConfigurationState>
  )
}

function RuntimeForm({
  projectId,
  environmentId,
  configuration,
  onSaved,
}: {
  projectId: number
  environmentId: number
  configuration: DeploymentEnvironmentConfiguration
  onSaved: () => void
}) {
  const { can } = useAuth()
  const canEdit = can("system.admin")
  // Kept for the tab under the revision they were read from (see build.tsx).
  const draft = `deploy.${projectId}.settings.runtime@${configuration.revision}`
  const [runtime, setRuntime] = useSessionState<RuntimePlan>(`${draft}.plan`, configuration.runtime)
  const [command, setCommand] = useSessionState(
    `${draft}.command`,
    (configuration.runtime.command ?? []).join("\n"),
  )
  const [capabilities, setCapabilities] = useSessionState(
    `${draft}.capabilities`,
    (configuration.runtime.capabilities ?? []).join("\n"),
  )
  const [devices, setDevices] = useSessionState(
    `${draft}.devices`,
    (configuration.runtime.devices ?? []).join("\n"),
  )
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [fieldError, setFieldError] = useState<{ id: string; message: string }>()
  const [accessOpen, setAccessOpen] = useState(false)
  const errorFor = (id: string) => (fieldError?.id === id ? fieldError.message : undefined)

  const save = async (event: FormEvent) => {
    event.preventDefault()
    setError(undefined)
    setFieldError(undefined)
    setBusy(true)
    try {
      await put(`/deploy/${projectId}/environments/${environmentId}/configuration`, {
        revision: configuration.revision,
        build: configuration.build,
        runtime: {
          ...runtime,
          command: linesOf(command),
          capabilities: linesOf(capabilities),
          devices: linesOf(devices),
        },
        dependencies: configuration.dependencies,
        checks: configuration.checks,
        domains: configuration.domains,
      })
      notify.success("Runtime settings saved", {
        description: "The desired revision changed; the live release was not touched.",
      })
      onSaved()
    } catch (caught) {
      if (caught instanceof ApiError && caught.field && RUNTIME_FIELD_IDS[caught.field]) {
        setFieldError({ id: RUNTIME_FIELD_IDS[caught.field], message: caught.message })
      } else {
        notify.error("Could not save runtime settings", caught)
      }
    } finally {
      setBusy(false)
    }
  }

  const capabilityList = linesOf(capabilities)
  const deviceList = linesOf(devices)
  // A refusal naming a field inside the fold has to open it, or the operator
  // is told the save failed and the reason is behind a closed `<details>`.
  // Forcing `open` fires the element's own `toggle`, which stores the state
  // here — so the fold latches open and does not snap shut again the moment
  // the next submit clears the error.
  const accessRefused = Boolean(errorFor("runtime-capabilities") || errorFor("runtime-devices"))
  const accessFacts =
    [
      runtime.privileged && "privileged",
      runtime.hostNetwork && "host network",
      capabilityList.length > 0 && `${capabilityList.length} capabilities`,
      deviceList.length > 0 && `${deviceList.length} devices`,
    ]
      .filter(Boolean)
      .join(" · ") || "Unprivileged, own network"

  return (
    // The readings sit on the page's own edge above the card rather than
    // inside it: §15 pass 2 puts the first column in line with the title, and
    // a run of figures takes no frame of its own.
    <form onSubmit={save} className="space-y-6">
      <RuntimeReadings runtime={runtime} capabilities={capabilityList} devices={deviceList} />
      <SettingCard
        title="Runtime"
        bodyClassName="space-y-6"
        note="Applies on your next deployment."
        action={
          canEdit && (
            <Button size="sm" type="submit" pending={busy}>
              Save
            </Button>
          )
        }
      >
        <FormSection title="Container">
          <Field label="Runtime image" htmlFor="runtime-image" error={errorFor("runtime-image")}>
            <Input
              id="runtime-image"
              value={runtime.image ?? ""}
              readOnly={!canEdit}
              aria-invalid={Boolean(errorFor("runtime-image"))}
              className="font-mono"
              onChange={(event) => setRuntime({ ...runtime, image: event.target.value })}
            />
          </Field>
          <Field
            label="Command argv"
            htmlFor="runtime-command"
            hint="One argument per line. Secret values belong in scoped variables, not argv."
            error={errorFor("runtime-command")}
          >
            <Textarea
              id="runtime-command"
              value={command}
              onChange={(event) => setCommand(event.target.value)}
              readOnly={!canEdit}
              aria-invalid={Boolean(errorFor("runtime-command"))}
              className="min-h-24 font-mono text-xs"
            />
          </Field>
        </FormSection>

        <FormSection title="Where it listens">
          <FieldRow columns={3}>
            <Field
              label="Application port"
              htmlFor="runtime-internal-port"
              error={errorFor("runtime-internal-port")}
            >
              <InputGroup>
                <InputGroupInput
                  id="runtime-internal-port"
                  type="number"
                  min={0}
                  max={65535}
                  value={runtime.internalPort ?? 0}
                  readOnly={!canEdit}
                  aria-invalid={Boolean(errorFor("runtime-internal-port"))}
                  onChange={(event) =>
                    setRuntime({ ...runtime, internalPort: clampPort(event.target.value) })
                  }
                />
                <InputGroupAddon align="inline-start">
                  <InputGroupText>:</InputGroupText>
                </InputGroupAddon>
              </InputGroup>
            </Field>
            <Field
              label="Fixed host port"
              htmlFor="runtime-host-port"
              hint="Zero lets an eligible proxied service lease a loopback candidate port."
              error={errorFor("runtime-host-port")}
            >
              <InputGroup>
                <InputGroupInput
                  id="runtime-host-port"
                  type="number"
                  min={0}
                  max={65535}
                  value={runtime.hostPort ?? 0}
                  readOnly={!canEdit}
                  aria-invalid={Boolean(errorFor("runtime-host-port"))}
                  onChange={(event) =>
                    setRuntime({ ...runtime, hostPort: clampPort(event.target.value) })
                  }
                />
                <InputGroupAddon align="inline-start">
                  <InputGroupText>:</InputGroupText>
                </InputGroupAddon>
              </InputGroup>
            </Field>
            <Field
              label="Bind address"
              htmlFor="runtime-bind"
              hint="Use 127.0.0.1 unless the container must be public without Proxy."
              error={errorFor("runtime-bind")}
            >
              <Select
                value={runtime.bindAddress ?? "127.0.0.1"}
                disabled={!canEdit}
                onValueChange={(bindAddress) => setRuntime({ ...runtime, bindAddress })}
              >
                <SelectTrigger id="runtime-bind" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {BIND_ADDRESSES.map(([value, label]) => (
                    <SelectItem key={value} value={value}>
                      {label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </FieldRow>
        </FormSection>

        <FormSection title="Resources & limits">
          <FieldRow columns={3}>
            <Field
              label="Memory limit"
              htmlFor="runtime-memory"
              hint="Zero means no limit. The kernel stops a container that exceeds its limit; the run's diagnostics say so."
              error={errorFor("runtime-memory")}
            >
              <InputGroup>
                <InputGroupInput
                  id="runtime-memory"
                  type="number"
                  min={0}
                  step={64}
                  value={runtime.memoryMb ?? 0}
                  readOnly={!canEdit}
                  aria-invalid={Boolean(errorFor("runtime-memory"))}
                  onChange={(event) =>
                    setRuntime({ ...runtime, memoryMb: Math.max(0, Number(event.target.value)) })
                  }
                />
                <InputGroupAddon align="inline-end">
                  <InputGroupText>MiB</InputGroupText>
                </InputGroupAddon>
              </InputGroup>
            </Field>
            <Field
              label="CPU limit"
              htmlFor="runtime-cpus"
              hint="Whole or fractional CPUs, for example 0.5 or 2. Zero means no limit."
              error={errorFor("runtime-cpus")}
            >
              <InputGroup>
                <InputGroupInput
                  id="runtime-cpus"
                  type="number"
                  min={0}
                  step={0.25}
                  value={runtime.cpus ?? 0}
                  readOnly={!canEdit}
                  aria-invalid={Boolean(errorFor("runtime-cpus"))}
                  onChange={(event) =>
                    setRuntime({ ...runtime, cpus: Math.max(0, Number(event.target.value)) })
                  }
                />
                <InputGroupAddon align="inline-end">
                  <InputGroupText>cores</InputGroupText>
                </InputGroupAddon>
              </InputGroup>
            </Field>
            <Field
              label="Process limit"
              htmlFor="runtime-pids"
              hint="Maximum processes and threads inside the container. Zero means no limit."
              error={errorFor("runtime-pids")}
            >
              <InputGroup>
                <InputGroupInput
                  id="runtime-pids"
                  type="number"
                  min={0}
                  step={16}
                  value={runtime.pidsLimit ?? 0}
                  readOnly={!canEdit}
                  aria-invalid={Boolean(errorFor("runtime-pids"))}
                  onChange={(event) =>
                    setRuntime({ ...runtime, pidsLimit: Math.max(0, Number(event.target.value)) })
                  }
                />
                <InputGroupAddon align="inline-end">
                  <InputGroupText>procs</InputGroupText>
                </InputGroupAddon>
              </InputGroup>
            </Field>
          </FieldRow>
        </FormSection>

        <FormSection title="Releases">
          <FieldRow columns={2}>
            <Field
              label="Release strategy"
              htmlFor="runtime-strategy"
              error={errorFor("runtime-strategy")}
            >
              <Select
                value={runtime.strategy}
                disabled={!canEdit}
                onValueChange={(strategy: RuntimePlan["strategy"]) =>
                  setRuntime({ ...runtime, strategy })
                }
              >
                <SelectTrigger id="runtime-strategy" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="blue_green">Blue / green</SelectItem>
                  <SelectItem value="stop_first">Stop first</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field
              label="Restart policy"
              htmlFor="runtime-restart"
              hint="How Docker treats a container that exits on its own. Deployments always stop and start their own releases."
              error={errorFor("runtime-restart")}
            >
              <Select
                value={runtime.restartPolicy ?? "unless-stopped"}
                disabled={!canEdit}
                onValueChange={(restartPolicy: DeploymentRestartPolicy) =>
                  setRuntime({ ...runtime, restartPolicy })
                }
              >
                <SelectTrigger id="runtime-restart" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="unless-stopped">Unless stopped (default)</SelectItem>
                  <SelectItem value="always">Always</SelectItem>
                  <SelectItem value="on-failure">On failure</SelectItem>
                  <SelectItem value="no">Never</SelectItem>
                </SelectContent>
              </Select>
            </Field>
          </FieldRow>
        </FormSection>

        {/* Folded, and the fold says what is inside it while it is shut: these
            four are the ones nobody sets on a first deployment, and the two
            that matter are stated again as a reading above the form (§7's
            `facts`, and the same pair `/deploy/new` folds under this name). */}
        <Disclosure
          summary="Container access"
          facts={accessFacts}
          open={accessOpen || accessRefused}
          onOpenChange={setAccessOpen}
        >
          <div className="space-y-4">
            <OptionList>
              <OptionRow
                title={
                  <span className="inline-flex items-center gap-1.5">
                    Privileged container <ExplainIcon name="privileged" />
                  </span>
                }
                hint="Removes almost every restriction separating the container from the server."
                tone={runtime.privileged ? "danger" : "default"}
                checked={runtime.privileged ?? false}
                onCheckedChange={(privileged) => setRuntime({ ...runtime, privileged })}
                disabled={!canEdit}
              />
              <OptionRow
                title={
                  <span className="inline-flex items-center gap-1.5">
                    Use the host network <ExplainIcon name="hostNetwork" />
                  </span>
                }
                hint="Every port it listens on is immediately open on the server."
                tone={runtime.hostNetwork ? "warning" : "default"}
                checked={runtime.hostNetwork ?? false}
                onCheckedChange={(hostNetwork) => setRuntime({ ...runtime, hostNetwork })}
                disabled={!canEdit}
              />
            </OptionList>
            <FieldRow columns={2}>
              <Field
                label="Linux capabilities"
                htmlFor="runtime-capabilities"
                hint="One uppercase capability per line."
                error={errorFor("runtime-capabilities")}
              >
                <Textarea
                  id="runtime-capabilities"
                  value={capabilities}
                  onChange={(event) => setCapabilities(event.target.value)}
                  readOnly={!canEdit}
                  aria-invalid={Boolean(errorFor("runtime-capabilities"))}
                  className="min-h-20 font-mono text-xs"
                />
              </Field>
              <Field
                label="Host devices"
                htmlFor="runtime-devices"
                hint="One absolute path per line."
                error={errorFor("runtime-devices")}
              >
                <Textarea
                  id="runtime-devices"
                  value={devices}
                  onChange={(event) => setDevices(event.target.value)}
                  readOnly={!canEdit}
                  aria-invalid={Boolean(errorFor("runtime-devices"))}
                  className="min-h-20 font-mono text-xs"
                />
              </Field>
            </FieldRow>
          </div>
        </Disclosure>

        {error && <FormNote tone="danger">{error}</FormNote>}
      </SettingCard>
    </form>
  )
}

function HealthChecksCard({
  projectId,
  environmentId,
  configuration,
  onSaved,
}: {
  projectId: number
  environmentId: number
  configuration: DeploymentEnvironmentConfiguration
  onSaved: () => void
}) {
  const { can } = useAuth()
  const canEdit = can("system.admin")
  const [checks, setChecks] = useSessionState<Check[]>(
    `deploy.${projectId}.settings.checks@${configuration.revision}`,
    configuration.checks,
  )
  const [busy, setBusy] = useState(false)
  const [rowError, setRowError] = useState<{ index: number; message: string }>()

  const save = async (event: FormEvent) => {
    event.preventDefault()
    setRowError(undefined)
    setBusy(true)
    try {
      await put(`/deploy/${projectId}/environments/${environmentId}/configuration`, {
        revision: configuration.revision,
        build: configuration.build,
        runtime: configuration.runtime,
        dependencies: configuration.dependencies,
        checks,
        domains: configuration.domains,
      })
      notify.success("Runtime settings saved", {
        description: "The desired revision changed; the live release was not touched.",
      })
      onSaved()
    } catch (caught) {
      const index = caught instanceof ApiError ? refusedIndex(caught.field, "checks") : undefined
      if (caught instanceof ApiError && index !== undefined) {
        setRowError({ index, message: caught.message })
      } else {
        notify.error("Could not save health checks", caught)
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={save}>
      <SettingCard
        title="Health checks"
        note="A candidate must pass its readiness checks before it takes traffic."
        action={
          canEdit && (
            <Button size="sm" type="submit" pending={busy}>
              Save
            </Button>
          )
        }
      >
        <HealthChecks
          checks={checks}
          disabled={!canEdit}
          onChange={setChecks}
          rowError={(index) => (rowError?.index === index ? rowError.message : undefined)}
        />
      </SettingCard>
    </form>
  )
}
