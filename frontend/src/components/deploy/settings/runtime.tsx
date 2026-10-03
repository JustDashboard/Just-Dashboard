"use client"

import { useRef, useState } from "react"
import Link from "next/link"
import { ArrowLeftRight, Box, Globe, Pause, Servers } from "@/components/icons"
import { ApiError, get, refusedIndex } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  ContainerHistory,
  DeploymentBuildMethod,
  DeploymentConfiguration,
  DeploymentEnvironmentConfiguration,
  DeploymentRestartPolicy,
  WorkloadProfile,
} from "@/lib/types"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { Disclosure, Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { Meter, utilisationTone } from "@/components/meter"
import { ProductGlyph, imageProduct } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
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
import {
  DEFAULT_REQUEST_BODY_LIMIT,
  MAX_REQUEST_BODY_MB,
} from "@/components/deploy/deployment-defaults"
import { useProject } from "@/components/deploy/project-context"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { useConfiguration, useSettingDraft } from "@/components/deploy/settings/use-configuration"
import {
  SettingForm,
  SettingSection,
  SettingsPage,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import {
  HealthChecks,
  hasReadiness,
  savedChecks,
  type Check,
} from "@/components/deploy/settings/health-checks"

/**
 * How the release runs once it exists: the image and command, where it
 * listens, what it may use, how one release replaces the next, and what it
 * can reach on the server — then the checks that decide when it is ready.
 *
 * There is no opening row of figures: each section's head says what it
 * currently is. Two sections read the running container rather than only the
 * form: each limit is drawn against what the live release actually peaked at
 * in the last hour, and the release strategy is checked against the rule the
 * executor applies at the next deployment — a writable mount, a fixed host
 * port or the host network cannot run two releases side by side, and
 * blue/green on such a plan used to be offered here and then refused at start.
 *
 * Two forms, two saves: Runtime (five sections, one PUT) and Health
 * checks. Each keeps its own draft keyed on its own saved value, so saving
 * one no longer restarts the other.
 *
 * A head says only what the fields under it do not: the command as one line,
 * a count, a state. What a field needs while it is typed stays under it, a
 * list's format is shown by its placeholder, and the reasoning behind a field
 * is behind its ⓘ, so the page reads as heads, fields and switches.
 */

type RuntimePlan = DeploymentConfiguration["runtime"]

/**
 * The Runtime form's draft: the plan, with its three lists held as the text
 * the operator types — one entry per line — so a half-typed line survives a
 * visit to another page.
 */
type RuntimeDraft = RuntimePlan & {
  commandText: string
  capabilitiesText: string
  devicesText: string
}

function linesOf(text: string) {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
}

function runtimeDraftOf(runtime: RuntimePlan): RuntimeDraft {
  return {
    ...runtime,
    commandText: (runtime.command ?? []).join("\n"),
    capabilitiesText: (runtime.capabilities ?? []).join("\n"),
    devicesText: (runtime.devices ?? []).join("\n"),
  }
}

function runtimePlanOf(draft: RuntimeDraft): RuntimePlan {
  const plan: RuntimePlan & Partial<RuntimeDraft> = {
    ...draft,
    command: linesOf(draft.commandText),
    capabilities: linesOf(draft.capabilitiesText),
    devices: linesOf(draft.devicesText),
  }
  delete plan.commandText
  delete plan.capabilitiesText
  delete plan.devicesText
  return plan
}

function useRuntimeDraft(projectId: number, configuration: DeploymentEnvironmentConfiguration) {
  return useSettingDraft(
    `deploy.${projectId}.settings.runtime`,
    runtimeDraftOf(configuration.runtime),
  )
}

function useChecksDraft(projectId: number, configuration: DeploymentEnvironmentConfiguration) {
  return useSettingDraft<Check[]>(`deploy.${projectId}.settings.checks`, configuration.checks)
}

const BIND_ADDRESSES: [string, string][] = [
  ["127.0.0.1", "127.0.0.1 · this server only"],
  ["::1", "::1 · this server only (IPv6)"],
  ["0.0.0.0", "0.0.0.0 · every interface"],
  ["::", ":: · every IPv6 interface"],
]

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
  "runtime.maxRequestBodyMb": "runtime-max-body",
  "runtime.capabilities": "runtime-capabilities",
  "runtime.devices": "runtime-devices",
}

/** Which section head a refused field belongs to, so that head says "Not saved". */
const FIELD_SECTION: Record<string, string> = {
  "runtime-image": "runtime",
  "runtime-command": "runtime",
  "runtime-internal-port": "listen",
  "runtime-host-port": "listen",
  "runtime-bind": "listen",
  "runtime-max-body": "listen",
  "runtime-memory": "resources",
  "runtime-cpus": "resources",
  "runtime-pids": "resources",
  "runtime-strategy": "releases",
  "runtime-restart": "releases",
  "runtime-capabilities": "access",
  "runtime-devices": "access",
}

function clampPort(value: string) {
  return Math.min(65535, Math.max(0, Number(value) || 0))
}

function publicBind(bind: string | undefined) {
  return bind === "0.0.0.0" || bind === "::"
}

const MIB = 1024 * 1024

/**
 * Why this plan cannot run two releases side by side, or nothing when it
 * can: the executor's own rule (`validateRuntimeActivationStrategy`, with
 * preflight's profile check in front of it), in the words the reader needs
 * to fix it. A plan that trips it fails at the next deployment's start, not
 * at save, which is why the page has to say so itself.
 */
function blueGreenRefusal(
  runtime: RuntimePlan,
  profile: WorkloadProfile,
  method: DeploymentBuildMethod,
): string | undefined {
  if (profile !== "web" && profile !== "static")
    return "only a web app or static site has traffic to move between two releases"
  if (method === "compose" || method === "legacy_compose")
    return "a Compose stack is replaced stop first"
  if (runtime.hostNetwork) return "on the host network two releases would claim the same ports"
  if ((runtime.hostPort ?? 0) > 0)
    return `host port ${runtime.hostPort} can be held by one release at a time`
  if (runtime.ports?.length) return "a published port can be held by one release at a time"
  const writable = runtime.mounts?.find((mount) => !mount.readOnly)
  if (writable) return `${writable.target} is writable — two releases cannot share it`
  return undefined
}

/** The live release's container over the last hour: its peaks, for the limits drawn against them. */
type Usage =
  | { state: "none" | "loading" }
  | { state: "ready"; memory: number; cpus: number; processes: number }

/**
 * One hour of the live release's history, polled each minute for the whole
 * page: the same recorded history the Overview's usage tiles read, reduced to
 * the three peaks the limits are set against. A poll that fails after one
 * landed keeps the peaks it has, rather than dropping every meter on the page
 * and raising them again a minute later.
 */
function useLiveUsage(): Usage {
  const { runtime } = useProject().detail
  const services = runtime?.status === "available" ? runtime.services : []
  const live = services.find((service) => service.liveRelease) ?? services[0]
  const containerId = live?.containerId
  const history = usePoll(
    (signal) =>
      get<ContainerHistory>(
        `/docker/containers/${encodeURIComponent(containerId ?? "")}/stats/history`,
        { points: 60 },
        signal,
      ),
    60000,
    [containerId],
    { enabled: Boolean(containerId) },
  )
  if (!containerId || (history.error && !history.data)) return { state: "none" }
  if (!history.data) return { state: "loading" }
  const points = history.data.points
  if (points.length === 0) return { state: "none" }
  return {
    state: "ready",
    memory: Math.max(...points.map((point) => point.memBytesPeak)),
    // Docker's CPU percentage counts one core as 100.
    cpus: Math.max(...points.map((point) => point.cpuPeak)) / 100,
    processes: Math.max(...points.map((point) => point.pids)),
  }
}

export function RuntimeSettings({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const state = useConfiguration(projectId, environmentId)
  const usage = useLiveUsage()
  return (
    <SettingsPage state={state} pageKinds={["runtime", "check"]}>
      {(configuration) => (
        <>
          <RuntimeForm
            projectId={projectId}
            configuration={configuration}
            usage={usage}
            save={state.save}
          />
          <HealthChecksForm projectId={projectId} configuration={configuration} save={state.save} />
        </>
      )}
    </SettingsPage>
  )
}

type Save = ReturnType<typeof useConfiguration>["save"]

/**
 * One or two states for a section head, side by side, or nothing when there
 * are none. Stacked, "Unsaved changes" arriving under "Public" made a head
 * with no state line a line taller, and pushed the field being typed in down.
 */
function statuses(...items: React.ReactNode[]) {
  const shown = items.filter(Boolean)
  if (shown.length === 0) return undefined
  return <span className="flex flex-wrap items-center gap-x-3 gap-y-1">{shown}</span>
}

/**
 * The Runtime form: five section heads — the image and command, where it
 * listens, what it may use, how releases replace each other, what it can
 * reach — and one save, because one PUT writes all of it.
 */
function RuntimeForm({
  projectId,
  configuration,
  usage,
  save,
}: {
  projectId: number
  configuration: DeploymentEnvironmentConfiguration
  usage: Usage
  save: Save
}) {
  const { can } = useAuth()
  const canEdit = can("system.admin")
  const { deployment } = useProject().detail
  const draft = useRuntimeDraft(projectId, configuration)
  const runtime = draft.value
  const patch = (fields: Partial<RuntimeDraft>) => draft.set((prev) => ({ ...prev, ...fields }))
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const [fieldError, setFieldError] = useState<{ id: string; message: string }>()
  const [accessOpen, setAccessOpen] = useState(false)
  // The switch is on while there is a host port, and stays on while the
  // operator clears the field to type another: bound to the value alone, an
  // emptied field turned the switch off and unmounted the input being typed in.
  const [hostPortCleared, setHostPortCleared] = useState(false)
  const errorFor = (id: string) => (fieldError?.id === id ? fieldError.message : undefined)
  const refusedIn = (section: string) =>
    fieldError !== undefined && FIELD_SECTION[fieldError.id] === section

  const method = configuration.build.method
  const built = method === "recipe" || method === "dockerfile" || method === "static"
  const argv = linesOf(runtime.commandText)
  const capabilities = linesOf(runtime.capabilitiesText)
  const devices = linesOf(runtime.devicesText)
  const port = runtime.internalPort ?? 0
  const hostPort = runtime.hostPort ?? 0
  const fixedPort = hostPort > 0 || hostPortCleared
  const bind = runtime.bindAddress || "127.0.0.1"
  const exposed = hostPort > 0 && publicBind(bind)
  const memory = runtime.memoryMb ?? 0
  const cpus = runtime.cpus ?? 0
  const pids = runtime.pidsLimit ?? 0
  const refusal = blueGreenRefusal(runtime, deployment.profile, method)
  const blueGreen = runtime.strategy === "blue_green"
  const failsNext = blueGreen && Boolean(refusal)

  const submit = async () => {
    setError(undefined)
    setFieldError(undefined)
    if (fixedPort && hostPort === 0) {
      setFieldError({ id: "runtime-host-port", message: "Use a port from 1 to 65535." })
      return false
    }
    setSaving(true)
    try {
      await save({ runtime: runtimePlanOf(runtime) })
      // The lists go to the server one entry per line, trimmed; the draft
      // takes the same shape, or a stray blank line would read as an edit
      // the save did not make.
      draft.set((prev) => ({
        ...prev,
        commandText: linesOf(prev.commandText).join("\n"),
        capabilitiesText: linesOf(prev.capabilitiesText).join("\n"),
        devicesText: linesOf(prev.devicesText).join("\n"),
      }))
      return true
    } catch (caught) {
      if (caught instanceof ApiError && caught.field && RUNTIME_FIELD_IDS[caught.field]) {
        setFieldError({ id: RUNTIME_FIELD_IDS[caught.field], message: caught.message })
      } else if (caught instanceof ApiError && (caught.status === 422 || caught.status === 400)) {
        // The plan's own refusals — a credential in argv, a path outside the
        // source — name no field, so they are the form's sentence, not a toast.
        setError(caught.message)
      } else {
        notify.error("Could not save runtime settings", caught)
      }
      return false
    } finally {
      setSaving(false)
    }
  }

  // A refusal naming a field inside the fold has to open it, or the operator
  // is told the save failed and the reason is behind a closed `<details>`.
  // Forcing `open` fires the element's own `toggle`, which stores the state
  // here — so the fold latches open and does not snap shut again the moment
  // the next submit clears the error.
  const accessRefused = Boolean(errorFor("runtime-capabilities") || errorFor("runtime-devices"))

  const image = runtime.image?.trim()

  return (
    <SettingForm
      name="Runtime"
      onSave={submit}
      dirty={draft.dirty}
      changes={draft.changes}
      saving={saving}
      canEdit={canEdit}
      onDiscard={() => {
        setError(undefined)
        setFieldError(undefined)
        setHostPortCleared(false)
        draft.discard()
      }}
      applies="next-deployment"
      error={error}
    >
      {/* The head is the command as one line, which the field under it, one
          argument per line, does not show; the image was the field under it
          again, glyph and all. The line rises when it swaps between the
          image's own command and one typed here, not on every keystroke. */}
      <SettingSection
        id="runtime"
        title="Runtime"
        state={
          <span
            key={argv.length > 0 ? "argv" : "own"}
            className="block animate-rise truncate"
            title={argv.length > 0 ? argv.join(" ") : undefined}
          >
            {argv.length > 0 ? (
              <>
                runs <span className="font-mono text-foreground">{argv.join(" ")}</span>
              </>
            ) : (
              "runs the image's own command"
            )}
          </span>
        }
        status={settingStatus({
          dirty: draft.changed(["image", "commandText"]),
          refused: refusedIn("runtime") || Boolean(error),
        })}
      >
        <Field label="Runtime image" htmlFor="runtime-image" error={errorFor("runtime-image")}>
          <InputGroup>
            {image && (
              <InputGroupAddon align="inline-start">
                <ProductGlyph id={imageProduct(image)} />
              </InputGroupAddon>
            )}
            <InputGroupInput
              id="runtime-image"
              value={runtime.image ?? ""}
              readOnly={!canEdit}
              aria-invalid={Boolean(errorFor("runtime-image"))}
              className="font-mono"
              placeholder={built ? "The image Build produces" : "ghcr.io/owner/image:tag"}
              autoComplete="off"
              spellCheck={false}
              onChange={(event) => patch({ image: event.target.value })}
            />
          </InputGroup>
        </Field>
        {/* The placeholder is an argv written one argument per line, so it
            says the format; what empty means is the head's line while it is. */}
        <Field
          label="Command argv"
          htmlFor="runtime-command"
          info="One argument per line; empty runs the image's own command. Secret values belong in scoped variables, not argv: the command line is visible to anything on the server that can list processes."
          error={errorFor("runtime-command")}
        >
          <Textarea
            id="runtime-command"
            value={runtime.commandText}
            onChange={(event) => patch({ commandText: event.target.value })}
            readOnly={!canEdit}
            aria-invalid={Boolean(errorFor("runtime-command"))}
            placeholder={"node\ndist/server.js\n--port\n3000"}
            className="min-h-24 font-mono sm:text-xs"
          />
        </Field>
      </SettingSection>

      <SettingSection
        id="listen"
        title="Where it listens"
        status={statuses(
          exposed && <Status key="public" tone="warning" label="Public" className="animate-rise" />,
          settingStatus({
            dirty: draft.changed(["internalPort", "hostPort", "bindAddress", "maxRequestBodyMb"]),
            refused: refusedIn("listen"),
          }),
        )}
      >
        <ListenPicture domains={configuration.domains} runtime={runtime} projectId={projectId} />
        {/* Both are the proxy's way in to the application — where it forwards
            and how much it lets through — so they read as one row. */}
        <FieldRow>
          <Field
            label="Application port"
            htmlFor="runtime-internal-port"
            error={errorFor("runtime-internal-port")}
          >
            <InputGroup>
              <InputGroupAddon align="inline-start">
                <InputGroupText className="font-mono">container :</InputGroupText>
              </InputGroupAddon>
              <InputGroupInput
                id="runtime-internal-port"
                type="number"
                min={0}
                max={65535}
                value={runtime.internalPort ?? 0}
                readOnly={!canEdit}
                aria-invalid={Boolean(errorFor("runtime-internal-port"))}
                className="font-mono"
                onChange={(event) => patch({ internalPort: clampPort(event.target.value) })}
              />
            </InputGroup>
          </Field>
          <Field
            label="Largest upload"
            htmlFor="runtime-max-body"
            info="The proxy answers a bigger request with 413 before the application sees it. Zero keeps the proxy's default."
            hint={
              runtime.maxRequestBodyMb ? undefined : (
                <span className="block animate-rise">
                  Proxy default: {DEFAULT_REQUEST_BODY_LIMIT}
                </span>
              )
            }
            error={errorFor("runtime-max-body")}
          >
            <InputGroup>
              <InputGroupInput
                id="runtime-max-body"
                type="number"
                min={0}
                max={MAX_REQUEST_BODY_MB}
                value={runtime.maxRequestBodyMb ?? 0}
                readOnly={!canEdit}
                aria-invalid={Boolean(errorFor("runtime-max-body"))}
                className="font-mono"
                onChange={(event) =>
                  patch({
                    maxRequestBodyMb:
                      Math.min(MAX_REQUEST_BODY_MB, Math.max(0, Number(event.target.value) || 0)) ||
                      undefined,
                  })
                }
              />
              <InputGroupAddon align="inline-end">
                <InputGroupText>MB</InputGroupText>
              </InputGroupAddon>
            </InputGroup>
          </Field>
        </FieldRow>
        <OptionList>
          {/* The consequence is the hint, so the title is the switch's name
              rather than a sentence with a clause hung off it. */}
          <OptionRow
            title="Publish on a fixed host port"
            hint="One release at a time can hold the port, so blue / green is unavailable."
            tone={exposed ? "warning" : "default"}
            checked={fixedPort}
            onCheckedChange={(on) => {
              setHostPortCleared(false)
              patch({ hostPort: on ? port || 8080 : undefined })
            }}
            disabled={!canEdit}
          >
            <div className="space-y-3">
              <FieldRow>
                <Field
                  label="Host port"
                  htmlFor="runtime-host-port"
                  error={errorFor("runtime-host-port")}
                >
                  <InputGroup>
                    <InputGroupAddon align="inline-start">
                      <InputGroupText className="max-w-28 font-mono">
                        <span className="truncate">{bind}</span> :
                      </InputGroupText>
                    </InputGroupAddon>
                    <InputGroupInput
                      id="runtime-host-port"
                      type="number"
                      min={1}
                      max={65535}
                      value={hostPort || ""}
                      readOnly={!canEdit}
                      aria-invalid={Boolean(errorFor("runtime-host-port"))}
                      className="font-mono"
                      onChange={(event) => {
                        const next = clampPort(event.target.value)
                        setHostPortCleared(next === 0)
                        patch({ hostPort: next || undefined })
                      }}
                    />
                  </InputGroup>
                </Field>
                <Field label="Bind address" htmlFor="runtime-bind" error={errorFor("runtime-bind")}>
                  <Select
                    value={bind}
                    disabled={!canEdit}
                    onValueChange={(bindAddress) => patch({ bindAddress })}
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
              {exposed && (
                <FormNote tone="warning" className="animate-rise">
                  Open on every interface — reachable without the proxy. Close it at the{" "}
                  <Link
                    href="/security/firewall"
                    className="rounded-sm underline underline-offset-2 focus-ring hover:text-foreground"
                  >
                    firewall
                  </Link>{" "}
                  if only the route should answer.
                </FormNote>
              )}
            </div>
          </OptionRow>
        </OptionList>
        {/* The fields a refusal can name are behind the switch while it is off. */}
        {!fixedPort && (errorFor("runtime-host-port") || errorFor("runtime-bind")) && (
          <FormNote tone="danger" role="alert" className="animate-rise">
            {errorFor("runtime-host-port") || errorFor("runtime-bind")}
          </FormNote>
        )}
      </SettingSection>

      <SettingSection
        id="resources"
        title="Resources"
        status={statuses(
          memory === 0 && cpus === 0 && pids === 0 && (
            <Status key="uncapped" tone="warning" label="No limits" className="animate-rise" />
          ),
          settingStatus({
            dirty: draft.changed(["memoryMb", "cpus", "pidsLimit"]),
            refused: refusedIn("resources"),
          }),
        )}
      >
        <FieldRow className="xl:grid-cols-3">
          <LimitField
            id="runtime-memory"
            label="Memory limit"
            unit="MiB"
            step={64}
            value={memory}
            peak={usage.state === "ready" ? usage.memory / MIB : undefined}
            peakLabel={(value) => `${Math.round(value)} MiB`}
            info="Zero means no limit. The kernel stops a container that goes past its limit, and the run's diagnostics say so."
            error={errorFor("runtime-memory")}
            disabled={!canEdit}
            onChange={(memoryMb) => patch({ memoryMb })}
          />
          <LimitField
            id="runtime-cpus"
            label="CPU limit"
            unit="cores"
            step={0.25}
            value={cpus}
            peak={usage.state === "ready" ? usage.cpus : undefined}
            peakLabel={(value) => `${value.toFixed(2)} cores`}
            info="Whole or fractional CPUs, for example 0.5 or 2. Zero means no limit."
            error={errorFor("runtime-cpus")}
            disabled={!canEdit}
            onChange={(next) => patch({ cpus: next })}
          />
          <LimitField
            id="runtime-pids"
            label="Process limit"
            unit="procs"
            step={16}
            value={pids}
            peak={usage.state === "ready" ? usage.processes : undefined}
            peakLabel={(value) => `${value} processes`}
            info="The most processes and threads the container may run at once. Zero means no limit."
            error={errorFor("runtime-pids")}
            disabled={!canEdit}
            onChange={(pidsLimit) => patch({ pidsLimit })}
          />
        </FieldRow>
      </SettingSection>

      {/* No state line: it said the restart policy back ("restarts unless
          stopped") over the select that already says it. */}
      <SettingSection
        id="releases"
        title="Releases"
        status={statuses(
          failsNext && (
            <Status
              key="fails"
              tone="warning"
              label="Will fail on the next deployment"
              className="animate-rise"
            />
          ),
          settingStatus({
            dirty: draft.changed(["strategy", "restartPolicy"]),
            refused: refusedIn("releases"),
          }),
        )}
      >
        <Field label="Release strategy" error={errorFor("runtime-strategy")}>
          <ChoiceGrid id="runtime-strategy" columns={2} role="group" aria-label="Release strategy">
            <ChoiceCard
              verb="Release blue / green"
              title="Blue / green"
              mark={ArrowLeftRight}
              description={
                <>
                  {/* An option that cannot be taken carries no control, so
                      nothing else would tell a screen reader it is the one
                      saved. */}
                  {failsNext && <span className="sr-only">Current strategy, unavailable. </span>}
                  Starts the new release beside the live one and moves traffic once its checks pass
                  — no downtime.
                </>
              }
              selected={blueGreen}
              disabled={!canEdit || Boolean(refusal)}
              onClick={() => patch({ strategy: "blue_green" })}
            />
            <ChoiceCard
              verb="Release stop first"
              title="Stop first"
              mark={Pause}
              index={1}
              description="Stops the live release, then starts the new one — a moment of downtime."
              selected={runtime.strategy === "stop_first"}
              disabled={!canEdit}
              onClick={() => patch({ strategy: "stop_first" })}
            />
          </ChoiceGrid>
          {/* Said beside the choice it blocks, at full ink: on the card it was
              a hint inside a faded option, the one sentence on the page the
              operator needed and the hardest to read. */}
          {refusal && (
            <FormNote tone={failsNext ? "warning" : "default"} className="animate-rise">
              Blue / green is unavailable: {refusal}.
            </FormNote>
          )}
        </Field>
        <Field
          label="Restart policy"
          htmlFor="runtime-restart"
          info="What Docker does when the container exits or the server restarts. Deployments stop and start their own releases either way."
          error={errorFor("runtime-restart")}
        >
          <Select
            value={runtime.restartPolicy ?? "unless-stopped"}
            disabled={!canEdit}
            onValueChange={(restartPolicy: DeploymentRestartPolicy) => patch({ restartPolicy })}
          >
            <SelectTrigger id="runtime-restart" className="w-full sm:w-72">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="unless-stopped" hint="default">
                Unless stopped
              </SelectItem>
              <SelectItem value="always">Always</SelectItem>
              <SelectItem value="on-failure">On failure</SelectItem>
              <SelectItem value="no">Never</SelectItem>
            </SelectContent>
          </Select>
        </Field>
      </SettingSection>

      <SettingSection
        id="access"
        title="Container access"
        status={statuses(
          runtime.privileged ? (
            <Status key="privileged" tone="danger" label="Privileged" className="animate-rise" />
          ) : (
            runtime.hostNetwork && (
              <Status key="host" tone="warning" label="Host network" className="animate-rise" />
            )
          ),
          settingStatus({
            dirty: draft.changed(["privileged", "hostNetwork", "capabilitiesText", "devicesText"]),
            refused: refusedIn("access"),
          }),
        )}
      >
        <OptionList>
          {/* The glossary's sentence is the hint rather than a ⓘ in the title:
              the title sits inside the row's <label>, and a button there was
              what the label named — a press on the words opened the tip and
              left the switch alone. */}
          <OptionRow
            title="Privileged container"
            hint="Removes almost every restriction separating the container from the server — anything that gets into it has the machine."
            tone={runtime.privileged ? "danger" : "default"}
            checked={runtime.privileged ?? false}
            onCheckedChange={(privileged) => patch({ privileged: privileged || undefined })}
            disabled={!canEdit}
          />
          <OptionRow
            title="Use the host network"
            hint="Every port it listens on is immediately open on the server, and it can reach anything bound to 127.0.0.1."
            tone={runtime.hostNetwork ? "warning" : "default"}
            checked={runtime.hostNetwork ?? false}
            onCheckedChange={(hostNetwork) => patch({ hostNetwork: hostNetwork || undefined })}
            disabled={!canEdit}
          />
        </OptionList>
        <Disclosure
          quiet
          summary="Capabilities and devices"
          facts={`${plural(capabilities.length, "capability", "capabilities")} · ${plural(devices.length, "device")}`}
          open={accessOpen || accessRefused}
          onOpenChange={setAccessOpen}
        >
          {/* The format is the placeholders', which hold two lines each; the
              rule the server checks it against is behind ⓘ. */}
          <FieldRow>
            <Field
              label="Linux capabilities"
              htmlFor="runtime-capabilities"
              info="Kernel capabilities added to the container, one per line, in capitals."
              error={errorFor("runtime-capabilities")}
            >
              <Textarea
                id="runtime-capabilities"
                value={runtime.capabilitiesText}
                onChange={(event) => patch({ capabilitiesText: event.target.value })}
                readOnly={!canEdit}
                aria-invalid={Boolean(errorFor("runtime-capabilities"))}
                placeholder={"NET_ADMIN\nSYS_TIME"}
                className="min-h-20 font-mono sm:text-xs"
              />
            </Field>
            <Field
              label="Host devices"
              htmlFor="runtime-devices"
              info="Devices on this server passed into the container, one absolute path per line."
              error={errorFor("runtime-devices")}
            >
              <Textarea
                id="runtime-devices"
                value={runtime.devicesText}
                onChange={(event) => patch({ devicesText: event.target.value })}
                readOnly={!canEdit}
                aria-invalid={Boolean(errorFor("runtime-devices"))}
                placeholder={"/dev/dri\n/dev/net/tun"}
                className="min-h-20 font-mono sm:text-xs"
              />
            </Field>
          </FieldRow>
        </Disclosure>
      </SettingSection>
    </SettingForm>
  )
}

/**
 * A limit, and — while the live release has an hour of history — how close
 * that release came to it: a meter of its peak against the limit being typed,
 * so a limit set below what the application already uses is seen before it
 * is saved rather than found in an out-of-memory kill.
 */
function LimitField({
  id,
  label,
  unit,
  step,
  value,
  peak,
  peakLabel,
  info,
  error,
  disabled,
  onChange,
}: {
  id: string
  label: string
  unit: string
  step: number
  value: number
  peak?: number
  peakLabel: (peak: number) => string
  info: string
  error?: string
  disabled: boolean
  onChange: (value: number) => void
}) {
  const pct = peak !== undefined && value > 0 ? (peak / value) * 100 : undefined
  // Keyed on which line it is rather than on its text, so "no limit" rises as
  // the field is cleared and a peak rises when its hour lands, but a figure
  // that only moves is not re-announced.
  const hint =
    peak !== undefined ? (
      <span key="peak" className="block animate-rise">
        peak {peakLabel(peak)} in the last hour
      </span>
    ) : value === 0 ? (
      <span key="unlimited" className="block animate-rise">
        no limit
      </span>
    ) : undefined
  return (
    <Field label={label} htmlFor={id} info={info} hint={hint} error={error}>
      <InputGroup>
        <InputGroupInput
          id={id}
          type="number"
          min={0}
          step={step}
          value={value}
          readOnly={disabled}
          aria-invalid={Boolean(error)}
          className="font-mono"
          onChange={(event) => onChange(Math.max(0, Number(event.target.value)))}
        />
        <InputGroupAddon align="inline-end">
          <InputGroupText>{unit}</InputGroupText>
        </InputGroupAddon>
      </InputGroup>
      {pct !== undefined && (
        <Meter
          value={pct}
          tone={utilisationTone(pct)}
          size="thin"
          // Named for the peak, not the limit, so the field keeps the label to itself.
          label={`${label.replace(/ limit$/, "")} peak in the last hour`}
          className="animate-rise"
        />
      )}
    </Field>
  )
}

/**
 * How a request reaches the container, drawn from the draft: the domain it
 * is asked at, the address on this server the proxy forwards to, and the
 * port the application listens on inside its container.
 *
 * Nothing pulses — a draft carries no traffic — and a hop that does not exist
 * is dashed. A fixed port on every interface draws a second way in, from
 * anywhere straight to the server, in amber: that is the route that skips
 * the proxy, and it is the one fact on this section worth seeing before
 * reading. On the host network the server and the container are one address,
 * so they are one mark.
 */
function ListenPicture({
  domains,
  runtime,
  projectId,
}: {
  domains: DeploymentEnvironmentConfiguration["domains"]
  runtime: RuntimeDraft
  projectId: number
}) {
  const container = useRef<HTMLDivElement>(null)
  const domainMark = useRef<HTMLDivElement>(null)
  const anywhereMark = useRef<HTMLDivElement>(null)
  const hostMark = useRef<HTMLDivElement>(null)
  const appMark = useRef<HTMLDivElement>(null)
  const [domain] = domains
  const port = runtime.internalPort ?? 0
  const hostPort = runtime.hostPort ?? 0
  const bind = runtime.bindAddress || "127.0.0.1"
  const exposed = hostPort > 0 && publicBind(bind)
  const hostNetwork = Boolean(runtime.hostNetwork)

  const domainNode = (
    <WireNode
      nodeRef={domainMark}
      align="end"
      mark={
        domain ? (
          <WireMark size="md">
            <Globe />
          </WireMark>
        ) : (
          <WirePlaceholder size="md">
            <Globe />
          </WirePlaceholder>
        )
      }
      eyebrow="Domain"
      title={
        domain ? (
          <span className="block truncate">{domain.hostname}</span>
        ) : (
          <Link
            href={`/deploy/${projectId}/settings/domains`}
            className="rounded-sm text-muted-foreground focus-ring hover:text-foreground"
          >
            No domain
          </Link>
        )
      }
      hint={
        domain
          ? `${domain.https ? "HTTPS" : "HTTP"}${domains.length > 1 ? ` · +${domains.length - 1}` : ""}`
          : "add one on Domains"
      }
    />
  )
  // The second way in rises as the switch opens it — its mark and its words,
  // never the node: the line to it is measured once as it appears, and a node
  // still four pixels into its rise would leave the line ending under its mark.
  const anywhereNode = exposed && (
    <WireNode
      key="anywhere"
      nodeRef={anywhereMark}
      align="end"
      mark={
        <WireMark size="md" tone="warning" className="animate-rise">
          <Globe />
        </WireMark>
      }
      eyebrow={<span className="inline-block animate-rise">Anywhere</span>}
      title={<span className="block animate-rise">Any address</span>}
      hint={<span className="block animate-rise text-warning">bypasses the proxy</span>}
    />
  )
  // What the server's address is, as a kind rather than a value: its words
  // rise when the kind changes — a fixed port turned on, the host network —
  // and not with every digit typed into a port.
  const hostKind = hostNetwork ? "host" : port === 0 ? "none" : hostPort > 0 ? "fixed" : "leased"
  const hostNode = (
    <WireNode
      nodeRef={hostMark}
      align={hostNetwork ? "start" : "center"}
      mark={
        <WireMark size="md" tone={hostNetwork ? "warning" : "neutral"}>
          <Servers />
        </WireMark>
      }
      eyebrow="This server"
      title={
        <span key={hostKind} className="block animate-rise">
          {hostNetwork ? (
            <span className="font-mono">:{port || "any"}</span>
          ) : port === 0 ? (
            // No application port leases no host port (activation_executor.go):
            // there is nothing for the proxy to forward to.
            "No port"
          ) : (
            <span className="font-mono">
              {hostPort > 0 ? `${bind}:${hostPort}` : "127.0.0.1:leased"}
            </span>
          )}
        </span>
      }
      hint={
        hostNetwork
          ? "host network — the container is the server"
          : port === 0
            ? "nothing to forward"
            : "the proxy forwards here"
      }
    />
  )
  const appNode = (
    <WireNode
      nodeRef={appMark}
      align="start"
      mark={
        port > 0 ? (
          <WireMark key="listens" size="md" tone="brand" className="animate-rise">
            <Box />
          </WireMark>
        ) : (
          <WirePlaceholder key="none" size="md" className="animate-rise">
            <Box />
          </WirePlaceholder>
        )
      }
      eyebrow="Container"
      title={<span className="font-mono">{port > 0 ? `:${port}` : "No port"}</span>}
      hint="the application listens"
    />
  )

  return (
    <SettingPicture
      label="How a request reaches the container"
      containerRef={container}
      lines={
        <>
          <AnimatedBeam
            containerRef={container}
            fromRef={domainMark}
            toRef={hostMark}
            still
            dashed={!domain || port === 0}
          />
          {exposed && (
            <AnimatedBeam
              containerRef={container}
              fromRef={anywhereMark}
              toRef={hostMark}
              still
              tone="warning"
            />
          )}
          {!hostNetwork && (
            <AnimatedBeam
              containerRef={container}
              fromRef={hostMark}
              toRef={appMark}
              still
              dashed={port === 0}
            />
          )}
        </>
      }
      start={anywhereNode ? [domainNode, anywhereNode] : [domainNode]}
      middle={hostNetwork ? undefined : hostNode}
      end={hostNetwork ? hostNode : appNode}
    />
  )
}

/**
 * The checks that decide a release is ready, as their own form and save. It
 * reads the Runtime draft's port, so a check that follows the application's
 * port shows the port it will actually probe.
 */
function HealthChecksForm({
  projectId,
  configuration,
  save,
}: {
  projectId: number
  configuration: DeploymentEnvironmentConfiguration
  save: Save
}) {
  const { can } = useAuth()
  const canEdit = can("system.admin")
  const { deployment } = useProject().detail
  const draft = useChecksDraft(projectId, configuration)
  const checks = draft.value
  const port = useRuntimeDraft(projectId, configuration).value.internalPort
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const [rowError, setRowError] = useState<{ index: number; message: string }>()

  const submit = async () => {
    setError(undefined)
    setRowError(undefined)
    setSaving(true)
    const next = savedChecks(checks)
    try {
      await save({ checks: next })
      // What was sent, not what was typed: a command's blank lines stay out
      // of the draft too, or they would read as an edit the save did not make.
      draft.set(next)
      return true
    } catch (caught) {
      const index = caught instanceof ApiError ? refusedIndex(caught.field, "checks") : undefined
      if (caught instanceof ApiError && index !== undefined) {
        setRowError({ index, message: caught.message })
      } else if (caught instanceof ApiError && (caught.status === 422 || caught.status === 400)) {
        setError(caught.message)
      } else {
        notify.error("Could not save health checks", caught)
      }
      return false
    } finally {
      setSaving(false)
    }
  }

  const required = checks.filter((check) => check.required).length
  const web = deployment.profile === "web" || deployment.profile === "static"

  return (
    <SettingForm
      name="Health checks"
      onSave={submit}
      dirty={draft.dirty}
      changes={draft.changes}
      saving={saving}
      canEdit={canEdit}
      onDiscard={() => {
        setError(undefined)
        setRowError(undefined)
        draft.discard()
      }}
      applies="next-deployment"
      error={error}
    >
      {/* The head counts only what nothing below it does: each phase's rule
          carries its own count and each check its own summary. The absence of
          a readiness check is the head's to say — once, as a state. */}
      <SettingSection
        id="health-checks"
        title="Health checks"
        state={
          checks.length > 0 ? (
            // Rises as a switch or a new check changes it.
            <span key={required} className="inline-block animate-rise">
              <span className="numeric">{required}</span> required
            </span>
          ) : undefined
        }
        status={statuses(
          web && !hasReadiness(checks) && (
            <Status
              key="unverified"
              tone="warning"
              label="No readiness check"
              className="animate-rise"
            />
          ),
          settingStatus({
            dirty: draft.dirty,
            refused: rowError !== undefined || Boolean(error),
            notLive: configuration.pending.changes.some((change) => change.kind === "check"),
          }),
        )}
      >
        <HealthChecks
          checks={checks}
          disabled={!canEdit}
          onChange={(next) => draft.set(next)}
          rowError={(index) => (rowError?.index === index ? rowError.message : undefined)}
          port={port}
        />
      </SettingSection>
    </SettingForm>
  )
}
