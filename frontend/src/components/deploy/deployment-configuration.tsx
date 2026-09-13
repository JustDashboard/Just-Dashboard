"use client"

import { useState } from "react"
import Link from "next/link"
import {
  Archive,
  ArrowRight,
  Database,
  Eye,
  EyeOff,
  Globe,
  Key,
  Plus,
  RefreshClockwise,
  Sparkles,
  Trash,
} from "@/components/icons"
import { del, errorMessage, get, post, put } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentBackupGateEvidence,
  DeploymentConfiguration,
  DeploymentEnvironmentConfiguration,
  DeploymentRemovalExecution,
  DeploymentRemovalPlan,
  DeploymentRemovalTarget,
  DeploymentRunSnapshot,
  DeploymentVariable,
} from "@/lib/types"
import { humanize } from "@/components/deploy/deployment-ui"
import { useConfirm } from "@/components/confirm-dialog"
import { Group, Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
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

type ConfigurationProps = {
  projectID: number
  environmentID: number
}

function useConfiguration({ projectID, environmentID }: ConfigurationProps) {
  return usePoll(
    (signal) =>
      get<DeploymentEnvironmentConfiguration>(
        `/deploy/${projectID}/environments/${environmentID}/configuration`,
        undefined,
        signal,
      ),
    0,
    [projectID, environmentID],
    { enabled: projectID > 0 && environmentID > 0 },
  )
}

function configurationBody(
  configuration: DeploymentEnvironmentConfiguration,
  changes: Partial<
    Pick<DeploymentConfiguration, "build" | "runtime" | "dependencies" | "checks" | "domains">
  > = {},
) {
  return {
    revision: configuration.revision,
    build: changes.build ?? configuration.build,
    runtime: changes.runtime ?? configuration.runtime,
    dependencies: changes.dependencies ?? configuration.dependencies,
    checks: changes.checks ?? configuration.checks,
    domains: changes.domains ?? configuration.domains,
  }
}

function ConfigurationLoad({
  state,
  children,
}: {
  state: ReturnType<typeof useConfiguration>
  children: (configuration: DeploymentEnvironmentConfiguration) => React.ReactNode
}) {
  if (state.loading && !state.data) return <LoadingPanel rows={5} />
  if (state.error && !state.data) return <ErrorState error={state.error} />
  if (!state.data) return null
  return children(state.data)
}

export function NormalizedConfigurationTab({
  projectID,
  environmentID,
  onArchived,
}: ConfigurationProps & { onArchived: () => void }) {
  const state = useConfiguration({ projectID, environmentID })
  return (
    <div className="space-y-4">
      <ConfigurationLoad state={state}>
        {(configuration) => (
          <>
            <PendingPanel pending={configuration.pending} />
            <RuntimeConfigurationForm
              key={configuration.revision}
              projectID={projectID}
              environmentID={environmentID}
              configuration={configuration}
              onSaved={state.refresh}
            />
          </>
        )}
      </ConfigurationLoad>
      <LifecyclePanel projectID={projectID} onArchived={onArchived} />
    </div>
  )
}

function PendingPanel({ pending }: { pending: DeploymentEnvironmentConfiguration["pending"] }) {
  return (
    <Panel>
      <PanelHeader
        title={pending.pending ? "Pending deployment" : "Desired plan is live"}
        actions={
          <Status
            verdict={pending.pending ? "warning" : "ok"}
            label={pending.pending ? `${pending.changes.length} changes` : "Live"}
          />
        }
      />
      {pending.changes.length > 0 && (
        <PanelBody flush>
          <ul className="divide-y divide-hairline">
            {pending.changes.map((change, index) => (
              <li
                key={`${change.kind}-${change.name}-${index}`}
                className="flex min-w-0 flex-wrap items-center justify-between gap-2 px-4 py-2.5 text-xs"
              >
                <span className="min-w-0 font-medium break-words">{change.name}</span>
                <span className="text-muted-foreground">
                  {humanize(change.kind)} · {humanize(change.change)}
                </span>
              </li>
            ))}
          </ul>
        </PanelBody>
      )}
    </Panel>
  )
}

function RuntimeConfigurationForm({
  projectID,
  environmentID,
  configuration,
  onSaved,
}: ConfigurationProps & {
  configuration: DeploymentEnvironmentConfiguration
  onSaved: () => void
}) {
  const { can } = useAuth()
  const [runtime, setRuntime] = useState(configuration.runtime)
  const [command, setCommand] = useState((configuration.runtime.command ?? []).join("\n"))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")

  const save = async () => {
    setBusy(true)
    setError("")
    try {
      await put(
        `/deploy/${projectID}/environments/${environmentID}/configuration`,
        configurationBody(configuration, {
          runtime: {
            ...runtime,
            command: command
              .split("\n")
              .map((part) => part.trim())
              .filter(Boolean),
          },
        }),
      )
      notify.success("Configuration saved", {
        description: "The desired revision changed; the live release was not touched.",
      })
      onSaved()
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Panel>
      <PanelHeader title="Runtime configuration" />
      <PanelBody>
        <div className="grid min-w-0 gap-4 sm:grid-cols-2">
          <Field label="Release strategy" htmlFor="runtime-strategy">
            <Select
              value={runtime.strategy}
              onValueChange={(strategy: "blue_green" | "stop_first") =>
                setRuntime({ ...runtime, strategy })
              }
              disabled={!can("system.admin")}
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
            label="Bind address"
            htmlFor="runtime-bind"
            hint="Use 127.0.0.1 unless the container must be public without Proxy."
          >
            <Input
              id="runtime-bind"
              value={runtime.bindAddress ?? ""}
              onChange={(event) => setRuntime({ ...runtime, bindAddress: event.target.value })}
              readOnly={!can("system.admin")}
              className="font-mono"
            />
          </Field>
          <Field label="Application port" htmlFor="runtime-internal-port">
            <Input
              id="runtime-internal-port"
              type="number"
              min={0}
              max={65535}
              value={runtime.internalPort ?? 0}
              onChange={(event) =>
                setRuntime({ ...runtime, internalPort: Number(event.target.value) })
              }
              readOnly={!can("system.admin")}
            />
          </Field>
          <Field
            label="Fixed host port"
            htmlFor="runtime-host-port"
            hint="Zero lets an eligible proxied service lease a loopback candidate port."
          >
            <Input
              id="runtime-host-port"
              type="number"
              min={0}
              max={65535}
              value={runtime.hostPort ?? 0}
              onChange={(event) => setRuntime({ ...runtime, hostPort: Number(event.target.value) })}
              readOnly={!can("system.admin")}
            />
          </Field>
          <Field label="Runtime image" htmlFor="runtime-image" className="sm:col-span-2">
            <Input
              id="runtime-image"
              value={runtime.image ?? ""}
              onChange={(event) => setRuntime({ ...runtime, image: event.target.value })}
              readOnly={!can("system.admin")}
              className="font-mono"
            />
          </Field>
          <Field
            label="Command argv"
            htmlFor="runtime-command"
            hint="One argument per line. Secret values belong in scoped variables, not argv."
            className="sm:col-span-2"
          >
            <Textarea
              id="runtime-command"
              value={command}
              onChange={(event) => setCommand(event.target.value)}
              readOnly={!can("system.admin")}
              className="min-h-28 font-mono"
            />
          </Field>
        </div>
        {error && (
          <p role="alert" className="mt-4 text-sm text-destructive">
            {error}
          </p>
        )}
        {can("system.admin") && (
          <div className="mt-4 flex justify-end">
            <Button onClick={save} pending={busy}>
              Save configuration
            </Button>
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}

export function NormalizedVariablesTab({ projectID, environmentID }: ConfigurationProps) {
  const state = useConfiguration({ projectID, environmentID })
  return (
    <ConfigurationLoad state={state}>
      {(configuration) => (
        <VariableWorkspace
          projectID={projectID}
          environmentID={environmentID}
          configuration={configuration}
          onChanged={state.refresh}
        />
      )}
    </ConfigurationLoad>
  )
}

function VariableWorkspace({
  projectID,
  environmentID,
  configuration,
  onChanged,
}: ConfigurationProps & {
  configuration: DeploymentEnvironmentConfiguration
  onChanged: () => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [name, setName] = useState("")
  const [value, setValue] = useState("")
  const [reference, setReference] = useState(false)
  const [sensitivity, setSensitivity] = useState<"plain" | "secret">("secret")
  const [scopes, setScopes] = useState<("build" | "runtime" | "release_task")[]>(["runtime"])
  const [dotenv, setDotenv] = useState("")
  const [showImport, setShowImport] = useState(false)
  const [busy, setBusy] = useState("")
  const [error, setError] = useState("")
  const [revealed, setRevealed] = useState<Record<string, string>>({})
  const [generated, setGenerated] = useState<{ name: string; value: string }>()

  const toggleScope = (scope: "build" | "runtime" | "release_task", checked: boolean) =>
    setScopes((current) =>
      checked ? [...new Set([...current, scope])] : current.filter((item) => item !== scope),
    )

  const mutate = async (label: string, action: () => Promise<void>) => {
    setBusy(label)
    setError("")
    try {
      await action()
      onChanged()
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy("")
    }
  }

  const save = () => {
    if (!name.trim() || scopes.length === 0) {
      setError("Enter a variable name and choose at least one scope.")
      return
    }
    void mutate("save", async () => {
      await put(
        `/deploy/${projectID}/environments/${environmentID}/variables/${encodeURIComponent(name.trim())}`,
        {
          revision: configuration.revision,
          ...(reference ? { reference: value.trim() } : { value }),
          sensitivity,
          scopes,
        },
      )
      setName("")
      setValue("")
      notify.success("Variable saved", { description: "It is pending until the next deployment." })
    })
  }

  const generate = () => {
    if (!name.trim() || scopes.length === 0) {
      setError("Enter a variable name and choose at least one scope.")
      return
    }
    void mutate("generate", async () => {
      const result = await post<{ generatedValue: string }>(
        `/deploy/${projectID}/environments/${environmentID}/variables/${encodeURIComponent(name.trim())}/generate`,
        { revision: configuration.revision, scopes },
      )
      setGenerated({ name: name.trim(), value: result.generatedValue })
      setName("")
      setValue("")
    })
  }

  const importDotenv = () => {
    if (!dotenv.trim() || scopes.length === 0) {
      setError("Paste dotenv values and choose at least one scope.")
      return
    }
    void mutate("import", async () => {
      await post(`/deploy/${projectID}/environments/${environmentID}/variables/import`, {
        revision: configuration.revision,
        dotenv,
        sensitivity,
        scopes,
      })
      setDotenv("")
      setShowImport(false)
      notify.success("Dotenv imported", {
        description: "Values remain masked in the variable list.",
      })
    })
  }

  const reveal = (variable: DeploymentVariable) =>
    void mutate(`reveal-${variable.name}`, async () => {
      const result = await get<{ value: string }>(
        `/deploy/${projectID}/environments/${environmentID}/variables/${encodeURIComponent(variable.name)}/reveal`,
      )
      setRevealed((current) => ({ ...current, [variable.name]: result.value }))
    })

  const rotate = (variable: DeploymentVariable) =>
    void mutate(`rotate-${variable.name}`, async () => {
      const result = await post<{ generatedValue: string }>(
        `/deploy/${projectID}/environments/${environmentID}/variables/${encodeURIComponent(variable.name)}/rotate`,
        { revision: configuration.revision },
      )
      setGenerated({ name: variable.name, value: result.generatedValue })
    })

  return (
    <>
      <div className="space-y-4">
        <PendingPanel pending={configuration.pending} />
        {generated && (
          <Notice icon={Sparkles} tone="warning" title={`${generated.name} was generated`}>
            <p>This value is shown once. Store it now; the list will keep only a fixed mask.</p>
            <Well className="mt-3 font-mono break-all select-all">{generated.value}</Well>
            <Button
              size="sm"
              variant="outline"
              className="mt-3"
              onClick={() => setGenerated(undefined)}
            >
              Hide value
            </Button>
          </Notice>
        )}
        {can("system.admin") && (
          <Panel>
            <PanelHeader title="Add or update a variable" />
            <PanelBody className="space-y-4">
              <div className="grid min-w-0 gap-4 sm:grid-cols-2">
                <Field label="Variable name" htmlFor="variable-name">
                  <Input
                    id="variable-name"
                    value={name}
                    onChange={(event) => setName(event.target.value.toUpperCase())}
                    autoCapitalize="characters"
                    autoComplete="off"
                    spellCheck={false}
                    className="font-mono"
                  />
                </Field>
                <Field label={reference ? "Typed reference" : "Value"} htmlFor="variable-value">
                  <Input
                    id="variable-value"
                    type={reference ? "text" : sensitivity === "secret" ? "password" : "text"}
                    value={value}
                    onChange={(event) => setValue(event.target.value)}
                    autoComplete="off"
                    spellCheck={false}
                    className="font-mono"
                  />
                </Field>
              </div>
              <div className="flex flex-wrap gap-x-5 gap-y-3">
                <CheckField
                  id="variable-reference"
                  checked={reference}
                  onChange={setReference}
                  label="Typed reference"
                />
                <CheckField
                  id="variable-secret"
                  checked={sensitivity === "secret"}
                  onChange={(checked) => setSensitivity(checked ? "secret" : "plain")}
                  label="Secret"
                />
                {(["build", "runtime", "release_task"] as const).map((scope) => (
                  <CheckField
                    key={scope}
                    id={`variable-scope-${scope}`}
                    checked={scopes.includes(scope)}
                    onChange={(checked) => toggleScope(scope, checked)}
                    label={humanize(scope)}
                  />
                ))}
              </div>
              {error && (
                <p role="alert" className="text-sm text-destructive">
                  {error}
                </p>
              )}
              <div className="flex flex-wrap gap-2">
                <Button onClick={save} disabled={Boolean(busy)} pending={busy === "save"}>
                  Save variable
                </Button>
                <Button
                  variant="outline"
                  onClick={generate}
                  disabled={Boolean(busy)}
                  pending={busy === "generate"}
                >
                  Generate secret
                </Button>
                <Button variant="ghost" onClick={() => setShowImport((open) => !open)}>
                  Import dotenv
                </Button>
              </div>
              {showImport && (
                <Field
                  label="Dotenv values"
                  htmlFor="dotenv-values"
                  hint="Comments, empty values, and quoted multiline values are accepted. Duplicate names are rejected."
                >
                  <Textarea
                    id="dotenv-values"
                    value={dotenv}
                    onChange={(event) => setDotenv(event.target.value)}
                    className="min-h-36 font-mono"
                    placeholder={'API_URL=https://api.example.test\nTOKEN="multiline\\nvalue"'}
                  />
                  <Button
                    className="mt-3"
                    onClick={importDotenv}
                    disabled={Boolean(busy)}
                    pending={busy === "import"}
                  >
                    Import variables
                  </Button>
                </Field>
              )}
            </PanelBody>
          </Panel>
        )}
        <Panel>
          <PanelHeader title="Scoped variables" />
          <PanelBody flush>
            {configuration.variables.length === 0 ? (
              <EmptyState
                icon={Key}
                title="No scoped variables"
                description="Add a runtime, build, or release-task value above."
                className="m-4"
              />
            ) : (
              <ul className="divide-y divide-hairline">
                {configuration.variables.map((variable) => (
                  <li key={variable.name} className="min-w-0 px-4 py-3">
                    <div className="flex min-w-0 flex-wrap items-start justify-between gap-3">
                      <div className="min-w-0 flex-1">
                        <div className="flex min-w-0 flex-wrap items-center gap-2">
                          <span className="font-mono text-xs font-medium break-all">
                            {variable.name}
                          </span>
                          {variable.pending && <Tag tone="warning">Pending</Tag>}
                          <Tag>{variable.sensitivity}</Tag>
                        </div>
                        <p className="mt-1 font-mono text-xs break-all text-muted-foreground">
                          {revealed[variable.name] ??
                            (variable.reference
                              ? `${variable.reference.kind}.${variable.reference.target}`
                              : variable.masked)}
                        </p>
                        <p className="mt-1 text-hint text-muted-foreground">
                          {variable.scopes.map(humanize).join(" · ")} · revision {variable.revision}{" "}
                          · {relativeTime(variable.createdAt)}
                        </p>
                      </div>
                      {can("system.admin") && (
                        <div className="flex flex-wrap gap-1">
                          {revealed[variable.name] ? (
                            <Button
                              size="sm"
                              variant="ghost"
                              aria-label={`Hide ${variable.name}`}
                              onClick={() =>
                                setRevealed((current) => {
                                  const next = { ...current }
                                  delete next[variable.name]
                                  return next
                                })
                              }
                            >
                              <EyeOff className="size-3.5" /> Hide
                            </Button>
                          ) : (
                            <Button
                              size="sm"
                              variant="ghost"
                              onClick={() => reveal(variable)}
                              disabled={Boolean(busy)}
                            >
                              <Eye className="size-3.5" /> Reveal
                            </Button>
                          )}
                          {variable.sensitivity === "secret" && (
                            <Button
                              size="sm"
                              variant="ghost"
                              onClick={() => rotate(variable)}
                              disabled={Boolean(busy)}
                            >
                              <RefreshClockwise className="size-3.5" /> Rotate
                            </Button>
                          )}
                          <Button
                            size="sm"
                            variant="ghost"
                            className="text-destructive"
                            onClick={() =>
                              confirm({
                                title: `Remove ${variable.name}`,
                                confirmLabel: "Remove variable",
                                description:
                                  "The desired plan will stop including this variable. The live release remains unchanged until deployment.",
                                action: async () => {
                                  await del(
                                    `/deploy/${projectID}/environments/${environmentID}/variables/${encodeURIComponent(variable.name)}`,
                                    { body: { revision: configuration.revision } },
                                  )
                                  onChanged()
                                },
                              })
                            }
                          >
                            <Trash className="size-3.5" /> Remove
                          </Button>
                        </div>
                      )}
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </PanelBody>
        </Panel>
      </div>
      {dialog}
    </>
  )
}

export function NormalizedNetworkTab({ projectID, environmentID }: ConfigurationProps) {
  const state = useConfiguration({ projectID, environmentID })
  return (
    <ConfigurationLoad state={state}>
      {(configuration) => (
        <NetworkForm
          key={configuration.revision}
          projectID={projectID}
          environmentID={environmentID}
          configuration={configuration}
          onSaved={state.refresh}
        />
      )}
    </ConfigurationLoad>
  )
}

function NetworkForm({
  projectID,
  environmentID,
  configuration,
  onSaved,
}: ConfigurationProps & {
  configuration: DeploymentEnvironmentConfiguration
  onSaved: () => void
}) {
  const { can } = useAuth()
  const [domains, setDomains] = useState(configuration.domains)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const runtime = configuration.runtime
  const publicBind = runtime.bindAddress === "0.0.0.0" || runtime.bindAddress === "::"

  const save = async () => {
    setBusy(true)
    setError("")
    try {
      await put(
        `/deploy/${projectID}/environments/${environmentID}/configuration`,
        configurationBody(configuration, { domains }),
      )
      notify.success("Network plan saved", {
        description:
          "DNS, certificate, port, and firewall evidence will be gated before activation.",
      })
      onSaved()
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-4">
      <PendingPanel pending={configuration.pending} />
      <div className="grid min-w-0 gap-4 lg:grid-cols-3">
        <PostureCard
          title="Proxy & DNS"
          value={`${domains.length} ${domains.length === 1 ? "domain" : "domains"}`}
          detail="Conflicts and DNS failures are separate preflight findings."
          href="/proxy/sites"
          label="Open Proxy"
        />
        <PostureCard
          title="TLS"
          value={domains.some((domain) => domain.https) ? "Certificate required" : "HTTP only"}
          detail="A configured HTTPS route cannot activate without matching certificate files."
          href="/proxy/certificates"
          label="Open Certificates"
        />
        <PostureCard
          title="Port & firewall"
          value={
            runtime.hostPort
              ? `${runtime.bindAddress || "127.0.0.1"}:${runtime.hostPort}`
              : "Dynamic candidate port"
          }
          detail={
            publicBind
              ? "Public bind: matching firewall policy is required."
              : "Loopback bind is not exposed directly."
          }
          href="/security"
          label="Open Security"
          warning={publicBind}
        />
      </div>
      <Panel>
        <PanelHeader
          title="Domains"
          actions={
            can("system.admin") && (
              <Button
                size="sm"
                variant="outline"
                onClick={() =>
                  setDomains([...domains, { hostname: "", https: true, ownership: "managed" }])
                }
              >
                <Plus className="size-3.5" /> Add domain
              </Button>
            )
          }
        />
        <PanelBody className="space-y-3">
          {domains.length === 0 && (
            <EmptyState
              icon={Globe}
              title="No public domains"
              description="Add one to route traffic through Proxy."
              className="border-0 py-5"
            />
          )}
          {domains.map((domain, index) => (
            <Group
              key={index}
              className="grid min-w-0 gap-3 sm:grid-cols-[minmax(0,1fr)_9rem_auto_auto] sm:items-end"
            >
              <Field label={`Domain ${index + 1}`} htmlFor={`domain-${index}`}>
                <Input
                  id={`domain-${index}`}
                  value={domain.hostname}
                  onChange={(event) =>
                    setDomains(
                      domains.map((item, itemIndex) =>
                        itemIndex === index
                          ? { ...item, hostname: event.target.value.toLowerCase() }
                          : item,
                      ),
                    )
                  }
                  readOnly={!can("system.admin")}
                  className="font-mono"
                />
              </Field>
              <Field label="Ownership" htmlFor={`domain-ownership-${index}`}>
                <Select
                  value={domain.ownership}
                  onValueChange={(ownership: "managed" | "linked") =>
                    setDomains(
                      domains.map((item, itemIndex) =>
                        itemIndex === index ? { ...item, ownership } : item,
                      ),
                    )
                  }
                  disabled={!can("system.admin")}
                >
                  <SelectTrigger id={`domain-ownership-${index}`} className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="managed">Managed</SelectItem>
                    <SelectItem value="linked">Linked</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              <CheckField
                id={`domain-https-${index}`}
                checked={domain.https}
                onChange={(https) =>
                  setDomains(
                    domains.map((item, itemIndex) =>
                      itemIndex === index ? { ...item, https } : item,
                    ),
                  )
                }
                label="HTTPS"
                disabled={!can("system.admin")}
              />
              {can("system.admin") && (
                <Button
                  size="sm"
                  variant="ghost"
                  className="text-destructive"
                  onClick={() => setDomains(domains.filter((_, itemIndex) => itemIndex !== index))}
                >
                  <Trash className="size-3.5" /> Remove
                </Button>
              )}
            </Group>
          ))}
          {error && (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}
          {can("system.admin") && (
            <div className="flex justify-end">
              <Button onClick={save} pending={busy}>
                Save network plan
              </Button>
            </div>
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}

export function NormalizedStorageTab({
  projectID,
  environmentID,
  latestRunID,
}: ConfigurationProps & { latestRunID?: number }) {
  const state = useConfiguration({ projectID, environmentID })
  return (
    <ConfigurationLoad state={state}>
      {(configuration) => (
        <StorageForm
          key={configuration.revision}
          projectID={projectID}
          environmentID={environmentID}
          latestRunID={latestRunID}
          configuration={configuration}
          onSaved={state.refresh}
        />
      )}
    </ConfigurationLoad>
  )
}

function StorageForm({
  projectID,
  environmentID,
  latestRunID,
  configuration,
  onSaved,
}: ConfigurationProps & {
  latestRunID?: number
  configuration: DeploymentEnvironmentConfiguration
  onSaved: () => void
}) {
  const { can } = useAuth()
  const [mounts, setMounts] = useState(configuration.runtime.mounts ?? [])
  const [dependencies, setDependencies] = useState(configuration.dependencies)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const evidence = usePoll(
    (signal) =>
      get<DeploymentRunSnapshot>(`/deploy/${projectID}/runs/${latestRunID}`, undefined, signal),
    0,
    [projectID, latestRunID],
    { enabled: Boolean(latestRunID) },
  )

  const addDependency = (kind: "backup" | "database" | "storage") => {
    const resourceKind =
      kind === "backup"
        ? "backup_job"
        : kind === "database"
          ? "database_connection"
          : "docker_volume"
    setDependencies([
      ...dependencies,
      {
        kind,
        ownership: "linked",
        resourceKind,
        resourceId: "",
        config:
          kind === "backup"
            ? { requiredBeforeDeploy: true, maxAgeSeconds: 86400, requireRestoreTest: false }
            : {},
      },
    ])
  }

  const save = async () => {
    setBusy(true)
    setError("")
    try {
      await put(
        `/deploy/${projectID}/environments/${environmentID}/configuration`,
        configurationBody(configuration, {
          runtime: { ...configuration.runtime, mounts },
          dependencies,
        }),
      )
      notify.success("Storage and dependency plan saved", {
        description: "Ownership and backup gates will be evaluated before the next activation.",
      })
      onSaved()
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(false)
    }
  }

  const backupEvidence = backupGateEvidence(evidence.data)
  return (
    <div className="space-y-4">
      <PendingPanel pending={configuration.pending} />
      <Panel>
        <PanelHeader
          title="Persistent mounts"
          actions={
            can("system.admin") && (
              <Button
                size="sm"
                variant="outline"
                onClick={() =>
                  setMounts([...mounts, { source: "", target: "", ownership: "linked" }])
                }
              >
                <Plus className="size-3.5" /> Add mount
              </Button>
            )
          }
        />
        <PanelBody className="space-y-3">
          {mounts.length === 0 && (
            <EmptyState
              icon={Database}
              title="No persistent mounts"
              description="The runtime is currently stateless."
              className="border-0 py-5"
            />
          )}
          {mounts.map((mount, index) => (
            <Group
              key={index}
              className="grid min-w-0 gap-3 sm:grid-cols-2 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_8rem_auto_auto] lg:items-end"
            >
              <Field label={`Source ${index + 1}`} htmlFor={`mount-source-${index}`}>
                <Input
                  id={`mount-source-${index}`}
                  value={mount.source}
                  readOnly={!can("system.admin")}
                  onChange={(event) =>
                    setMounts(
                      mounts.map((item, itemIndex) =>
                        itemIndex === index ? { ...item, source: event.target.value } : item,
                      ),
                    )
                  }
                  className="font-mono"
                />
              </Field>
              <Field label="Container path" htmlFor={`mount-target-${index}`}>
                <Input
                  id={`mount-target-${index}`}
                  value={mount.target}
                  readOnly={!can("system.admin")}
                  onChange={(event) =>
                    setMounts(
                      mounts.map((item, itemIndex) =>
                        itemIndex === index ? { ...item, target: event.target.value } : item,
                      ),
                    )
                  }
                  className="font-mono"
                />
              </Field>
              <Field label="Ownership" htmlFor={`mount-ownership-${index}`}>
                <Select
                  value={mount.ownership}
                  onValueChange={(ownership: "managed" | "linked" | "observed") =>
                    setMounts(
                      mounts.map((item, itemIndex) =>
                        itemIndex === index ? { ...item, ownership } : item,
                      ),
                    )
                  }
                  disabled={!can("system.admin")}
                >
                  <SelectTrigger id={`mount-ownership-${index}`} className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="managed">Managed</SelectItem>
                    <SelectItem value="linked">Linked</SelectItem>
                    <SelectItem value="observed">Observed</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              <CheckField
                id={`mount-readonly-${index}`}
                checked={Boolean(mount.readOnly)}
                onChange={(readOnly) =>
                  setMounts(
                    mounts.map((item, itemIndex) =>
                      itemIndex === index ? { ...item, readOnly } : item,
                    ),
                  )
                }
                label="Read only"
                disabled={!can("system.admin")}
              />
              {can("system.admin") && (
                <Button
                  size="sm"
                  variant="ghost"
                  className="text-destructive"
                  onClick={() => setMounts(mounts.filter((_, itemIndex) => itemIndex !== index))}
                >
                  <Trash className="size-3.5" /> Remove
                </Button>
              )}
            </Group>
          ))}
        </PanelBody>
      </Panel>
      <Panel>
        <PanelHeader
          title="Backups & dependencies"
          actions={
            can("system.admin") && (
              <div className="flex flex-wrap gap-1">
                <Button size="sm" variant="outline" onClick={() => addDependency("backup")}>
                  Link backup
                </Button>
                <Button size="sm" variant="outline" onClick={() => addDependency("database")}>
                  Link database
                </Button>
                <Button size="sm" variant="outline" onClick={() => addDependency("storage")}>
                  Link volume
                </Button>
              </div>
            )
          }
        />
        <PanelBody className="space-y-3">
          {dependencies.length === 0 && (
            <p className="text-xs text-muted-foreground">
              No backup, storage, or database dependency is linked.
            </p>
          )}
          {dependencies.map((dependency, index) => (
            <DependencyRow
              key={`${dependency.kind}-${index}`}
              dependency={dependency}
              index={index}
              readOnly={!can("system.admin")}
              onChange={(next) =>
                setDependencies(
                  dependencies.map((item, itemIndex) => (itemIndex === index ? next : item)),
                )
              }
              onRemove={() =>
                setDependencies(dependencies.filter((_, itemIndex) => itemIndex !== index))
              }
            />
          ))}
          {error && (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}
          {can("system.admin") && (
            <div className="flex justify-end">
              <Button onClick={save} pending={busy}>
                Save storage plan
              </Button>
            </div>
          )}
        </PanelBody>
      </Panel>
      <BackupEvidence
        evidence={backupEvidence}
        loading={evidence.loading}
        unavailable={!latestRunID}
      />
    </div>
  )
}

function DependencyRow({
  dependency,
  index,
  readOnly,
  onChange,
  onRemove,
}: {
  dependency: DeploymentConfiguration["dependencies"][number]
  index: number
  readOnly: boolean
  onChange: (dependency: DeploymentConfiguration["dependencies"][number]) => void
  onRemove: () => void
}) {
  const backup = dependency.kind === "backup"
  const config = dependency.config ?? {}
  const link =
    dependency.kind === "backup"
      ? "/backups"
      : dependency.kind === "database"
        ? "/databases"
        : "/docker?tab=volumes"
  return (
    <Group className="space-y-3">
      <div className="grid min-w-0 gap-3 sm:grid-cols-[8rem_minmax(0,1fr)_8rem_auto] sm:items-end">
        <Field label="Kind" htmlFor={`dependency-kind-${index}`}>
          <Input id={`dependency-kind-${index}`} value={humanize(dependency.kind)} readOnly />
        </Field>
        <Field label="Resource id" htmlFor={`dependency-resource-${index}`}>
          <Input
            id={`dependency-resource-${index}`}
            value={dependency.resourceId ?? ""}
            readOnly={readOnly}
            onChange={(event) => onChange({ ...dependency, resourceId: event.target.value })}
            className="font-mono"
          />
        </Field>
        <Field label="Ownership" htmlFor={`dependency-ownership-${index}`}>
          <Select
            value={dependency.ownership}
            onValueChange={(ownership: "managed" | "linked" | "observed") =>
              onChange({ ...dependency, ownership })
            }
            disabled={readOnly}
          >
            <SelectTrigger id={`dependency-ownership-${index}`} className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="managed">Managed</SelectItem>
              <SelectItem value="linked">Linked</SelectItem>
              <SelectItem value="observed">Observed</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        {!readOnly && (
          <Button size="sm" variant="ghost" className="text-destructive" onClick={onRemove}>
            <Trash className="size-3.5" /> Remove
          </Button>
        )}
      </div>
      {backup && (
        <div className="flex flex-wrap gap-x-5 gap-y-3">
          <CheckField
            id={`backup-required-${index}`}
            checked={Boolean(config.requiredBeforeDeploy)}
            onChange={(requiredBeforeDeploy) =>
              onChange({ ...dependency, config: { ...config, requiredBeforeDeploy } })
            }
            label="Backup before deploy"
            disabled={readOnly}
          />
          <CheckField
            id={`backup-restore-${index}`}
            checked={Boolean(config.requireRestoreTest)}
            onChange={(requireRestoreTest) =>
              onChange({ ...dependency, config: { ...config, requireRestoreTest } })
            }
            label="Require restore evidence"
            disabled={readOnly}
          />
          <Field label="Maximum age (hours)" htmlFor={`backup-age-${index}`}>
            <Input
              id={`backup-age-${index}`}
              type="number"
              min={0}
              value={Math.round(Number(config.maxAgeSeconds ?? 0) / 3600)}
              readOnly={readOnly}
              onChange={(event) =>
                onChange({
                  ...dependency,
                  config: { ...config, maxAgeSeconds: Number(event.target.value) * 3600 },
                })
              }
              className="w-32"
            />
          </Field>
        </div>
      )}
      <Button size="sm" variant="ghost" asChild>
        <Link href={link}>
          Open {humanize(dependency.kind)} owner <ArrowRight className="size-3.5" />
        </Link>
      </Button>
    </Group>
  )
}

function backupGateEvidence(snapshot?: DeploymentRunSnapshot): DeploymentBackupGateEvidence[] {
  const step = snapshot?.steps.find((item) => item.key === "backup_gate")
  const backups = step?.evidence?.backups
  return Array.isArray(backups) ? (backups as DeploymentBackupGateEvidence[]) : []
}

function BackupEvidence({
  evidence,
  loading,
  unavailable,
}: {
  evidence: DeploymentBackupGateEvidence[]
  loading: boolean
  unavailable: boolean
}) {
  return (
    <Panel>
      <PanelHeader title="Latest backup gate evidence" />
      <PanelBody>
        {loading ? (
          <p className="text-xs text-muted-foreground">Loading latest run evidence…</p>
        ) : unavailable ? (
          <p className="text-xs text-muted-foreground">
            No deployment run has produced backup evidence yet.
          </p>
        ) : evidence.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            The latest run did not execute a backup policy.
          </p>
        ) : (
          <div className="space-y-2">
            {evidence.map((item) => (
              <Group
                key={`${item.jobId}-${item.runId ?? 0}`}
                className="grid min-w-0 gap-2 text-xs sm:grid-cols-4"
              >
                <EvidenceValue label="Backup job" value={`#${item.jobId}`} />
                <EvidenceValue label="Run" value={item.runId ? `#${item.runId}` : "Unavailable"} />
                <EvidenceValue label="Fresh" value={item.fresh ? "Yes" : "No"} />
                <EvidenceValue
                  label="Restore tested"
                  value={item.restoreTested ? "Yes" : "No evidence"}
                />
                {item.endedAt && (
                  <p className="text-muted-foreground sm:col-span-4">
                    Completed {relativeTime(item.endedAt)} · {humanize(item.status)}
                    {item.detail ? ` · ${item.detail}` : ""}
                  </p>
                )}
              </Group>
            ))}
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}

function LifecyclePanel({ projectID, onArchived }: { projectID: number; onArchived: () => void }) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [plan, setPlan] = useState<DeploymentRemovalPlan>()
  const [busy, setBusy] = useState("")
  const [error, setError] = useState("")

  const loadPlan = async () => {
    setBusy("preview")
    setError("")
    try {
      setPlan(await post<DeploymentRemovalPlan>(`/deploy/${projectID}/removal-plan`, {}))
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy("")
    }
  }

  const remove = (target: DeploymentRemovalTarget) => {
    if (!plan) return
    confirm({
      title: `Remove ${target.displayName}`,
      confirmLabel: "Remove managed resource",
      phrase: target.confirmationType === "typed" ? target.confirmationPhrase : undefined,
      description: (
        <div className="space-y-2">
          <p>Only this exact managed {humanize(target.kind)} target will be removed.</p>
          <Well className="font-mono break-all">{target.resourceId}</Well>
          {target.data && (
            <p className="font-medium text-destructive">This target contains persistent data.</p>
          )}
        </div>
      ),
      action: async (confirmation) => {
        await post<DeploymentRemovalExecution>(
          `/deploy/${projectID}/remove-managed`,
          { planDigest: plan.digest, targetIds: [target.id] },
          { confirm: confirmation },
        )
        await loadPlan()
      },
    })
  }

  return (
    <>
      <Panel className="border-rule-danger">
        <PanelHeader
          title="Archive & managed resources"
          actions={
            can("system.admin") && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => void loadPlan()}
                disabled={Boolean(busy)}
                pending={busy === "preview"}
              >
                Preview managed targets
              </Button>
            )
          }
        />
        <PanelBody className="space-y-3">
          {plan && (
            <div className="space-y-2">
              {plan.targets.length === 0 ? (
                <p className="text-xs text-muted-foreground">
                  No managed resource is eligible for removal. Linked and observed resources are
                  intentionally absent.
                </p>
              ) : (
                <ul className="space-y-2">
                  {plan.targets.map((target) => (
                    <li
                      key={target.id}
                      className="flex min-w-0 flex-wrap items-center justify-between gap-3 rounded-lg border border-hairline p-3"
                    >
                      <div className="min-w-0 flex-1">
                        <p className="text-xs font-medium break-all">{target.displayName}</p>
                        <p className="text-hint text-muted-foreground">
                          {humanize(target.kind)} · {target.owner} · {target.confirmationType}{" "}
                          confirmation{target.data ? " · persistent data" : ""}
                        </p>
                      </div>
                      {plan.archived && can("destructive") && (
                        <Button size="sm" variant="destructive" onClick={() => remove(target)}>
                          <Trash className="size-3.5" /> Remove
                        </Button>
                      )}
                    </li>
                  ))}
                </ul>
              )}
              {!plan.archived && can("destructive") && (
                <Button
                  variant="destructive"
                  className="mt-2"
                  onClick={() =>
                    confirm({
                      title: "Archive deployment",
                      confirmLabel: "Archive deployment",
                      description:
                        "Triggers will be disabled. Runtime, routes, releases, linked resources, and persistent data remain in place.",
                      action: async () => {
                        await post(`/deploy/${projectID}/archive`, {})
                        onArchived()
                        await loadPlan()
                      },
                    })
                  }
                >
                  <Archive className="size-3.5" /> Archive deployment
                </Button>
              )}
              {plan.archived && (
                <Notice icon={Archive} title="Deployment archived">
                  Select managed resources one at a time if they should also be removed. Observed
                  and linked targets remain untouched.
                </Notice>
              )}
            </div>
          )}
          {error && (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}
        </PanelBody>
      </Panel>
      {dialog}
    </>
  )
}

function PostureCard({
  title,
  value,
  detail,
  href,
  label,
  warning = false,
}: {
  title: string
  value: string
  detail: string
  href: string
  label: string
  warning?: boolean
}) {
  return (
    <Panel>
      <PanelHeader
        title={title}
        actions={warning ? <Status verdict="warning" label="Review" /> : undefined}
      />
      <PanelBody className="space-y-2">
        <p className="text-sm font-medium">{value}</p>
        <p className="text-xs leading-relaxed text-muted-foreground">{detail}</p>
        <Button size="sm" variant="ghost" asChild>
          <Link href={href}>
            {label} <ArrowRight className="size-3.5" />
          </Link>
        </Button>
      </PanelBody>
    </Panel>
  )
}

function EvidenceValue({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className="text-muted-foreground">{label}</p>
      <p className="mt-0.5 font-medium">{value}</p>
    </div>
  )
}

function Field({
  label,
  htmlFor,
  hint,
  className = "",
  children,
}: {
  label: string
  htmlFor: string
  hint?: string
  className?: string
  children: React.ReactNode
}) {
  return (
    <div className={`min-w-0 space-y-1.5 ${className}`}>
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
      {hint && <p className="text-xs leading-relaxed text-muted-foreground">{hint}</p>}
    </div>
  )
}

function CheckField({
  id,
  checked,
  onChange,
  label,
  disabled = false,
}: {
  id: string
  checked: boolean
  onChange: (checked: boolean) => void
  label: string
  disabled?: boolean
}) {
  return (
    <Label htmlFor={id} className="min-h-11 gap-2 rounded-md px-1 text-xs">
      <Checkbox
        id={id}
        checked={checked}
        disabled={disabled}
        onCheckedChange={(next) => onChange(next === true)}
      />
      {label}
    </Label>
  )
}
