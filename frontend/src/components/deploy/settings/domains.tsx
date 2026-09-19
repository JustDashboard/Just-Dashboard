"use client"

import { useState } from "react"
import { useSessionState } from "@/lib/view-state"
import Link from "next/link"
import { CheckCircle, LockClosed, Plus, Trash, Warning } from "@/components/icons"
import { ApiError, get, refusedIndex } from "@/lib/api"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import type { DeploymentDomainRoute, DeploymentEnvironmentConfiguration } from "@/lib/types"
import { Field, FormFact, FormFacts } from "@/components/form"
import { Group } from "@/components/panel"
import { EmptyNote, Notice } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { SidePanel } from "@/components/side-panel"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { IconAction } from "@/components/icon-action"
import { SettingCard } from "@/components/deploy/settings/setting-card"
import { PendingChanges } from "@/components/deploy/settings/pending-changes"
import {
  ConfigurationState,
  useConfiguration,
} from "@/components/deploy/settings/use-configuration"
import { useProject } from "@/components/deploy/project-context"

/**
 * Domains — the hostnames this environment answers on.
 *
 * A hostname only ever arrives through "Add domain", which is the one place
 * the certificate check happens; an existing row is never retyped, only
 * toggled, reassigned or removed, so the notices below always describe a name
 * the operator is about to commit to rather than one already half-edited.
 */

type DomainValue = DeploymentEnvironmentConfiguration["domains"][number]

const ROUTE_STATUS: Record<DeploymentDomainRoute["route"], { label: string; tone: DotTone }> = {
  served: { label: "Routed here", tone: "running" },
  missing: { label: "No route", tone: "warning" },
  foreign: { label: "Another site", tone: "danger" },
  conflict: { label: "Conflict", tone: "danger" },
  unavailable: { label: "Not observed", tone: "unknown" },
}

const CERTIFICATE_STATUS: Record<
  DeploymentDomainRoute["certificate"],
  { label: string; tone: DotTone }
> = {
  valid: { label: "Certificate valid", tone: "running" },
  expiring: { label: "Certificate expiring", tone: "warning" },
  expired: { label: "Certificate expired", tone: "danger" },
  missing: { label: "No certificate", tone: "warning" },
  "not requested": { label: "HTTP only", tone: "stopped" },
  unavailable: { label: "Certificate not observed", tone: "unknown" },
}

export function DomainsSettings({
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
        // No `key={configuration.revision}`: see the note in build.tsx.
        <DomainsForm configuration={configuration} save={state.save} />
      )}
    </ConfigurationState>
  )
}

function DomainsForm({
  configuration,
  save,
}: {
  configuration: DeploymentEnvironmentConfiguration
  save: ReturnType<typeof useConfiguration>["save"]
}) {
  const { can } = useAuth()
  const canAdmin = can("system.admin")
  const project = useProject()
  // Kept for the tab under the revision it was read from (see build.tsx).
  const [domains, setDomains] = useSessionState<DomainValue[]>(
    `deploy.${project.projectId}.settings.domains@${configuration.revision}`,
    configuration.domains,
  )
  const [saving, setSaving] = useState(false)
  const [adding, setAdding] = useSessionState(
    `deploy.${project.projectId}.settings.domains.adding`,
    false,
  )
  const [rowError, setRowError] = useState<{ index: number; message: string }>()
  const runtime = configuration.runtime
  const publicBind = runtime.bindAddress === "0.0.0.0" || runtime.bindAddress === "::"
  const routes = project.operations?.domains?.domains

  const routeFor = (hostname: string) =>
    routes?.find((route) => route.hostname.toLowerCase() === hostname.toLowerCase())

  const persist = async (next: DomainValue[]) => {
    await save({ domains: next })
    setDomains(next)
    notify.success("Domains saved")
  }

  const onSaveRows = async () => {
    setSaving(true)
    setRowError(undefined)
    try {
      await persist(domains)
    } catch (error) {
      const index = error instanceof ApiError ? refusedIndex(error.field, "domains") : undefined
      if (error instanceof ApiError && index !== undefined) {
        setRowError({ index, message: error.message })
      } else {
        notify.error("Could not save domains", error)
      }
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="space-y-6">
      <PendingChanges pending={configuration.pending} />
      <SettingCard
        title="Domains"
        actions={
          canAdmin && (
            <Button size="sm" variant="outline" onClick={() => setAdding(true)}>
              <Plus className="size-3.5" /> Add domain
            </Button>
          )
        }
        note="Applies on the next deployment."
        action={
          canAdmin && (
            <Button size="sm" onClick={onSaveRows} pending={saving}>
              Save
            </Button>
          )
        }
      >
        {domains.length === 0 ? (
          <EmptyNote>No public domains yet. Add one to route traffic through Proxy.</EmptyNote>
        ) : (
          <div className="space-y-3">
            {domains.map((domain, index) => {
              const route = routeFor(domain.hostname)
              const routeStatus = route ? ROUTE_STATUS[route.route] : undefined
              const certificateStatus = route ? CERTIFICATE_STATUS[route.certificate] : undefined
              return (
                <Group
                  key={`${domain.hostname}-${index}`}
                  className="grid min-w-0 gap-3 sm:grid-cols-2 lg:grid-cols-[minmax(0,1fr)_auto_auto_auto_auto_auto] lg:items-end"
                >
                  <div className="min-w-0 lg:col-span-1">
                    <p className="eyebrow">Hostname</p>
                    <p className="mt-1.5 min-h-9 truncate py-1.5 font-mono text-body">
                      {domain.hostname}
                    </p>
                  </div>
                  <Field label="HTTPS" htmlFor={`domain-https-${index}`}>
                    <Switch
                      id={`domain-https-${index}`}
                      checked={domain.https}
                      onCheckedChange={(https) =>
                        setDomains(
                          domains.map((item, i) => (i === index ? { ...item, https } : item)),
                        )
                      }
                      disabled={!canAdmin}
                    />
                  </Field>
                  <Field label="Password" htmlFor={`domain-protected-${index}`}>
                    <Switch
                      id={`domain-protected-${index}`}
                      checked={Boolean(domain.protection)}
                      onCheckedChange={(protectedRoute) =>
                        setDomains(
                          domains.map((item, i) =>
                            i === index
                              ? {
                                  ...item,
                                  protection: protectedRoute
                                    ? { username: "", password: "" }
                                    : undefined,
                                }
                              : item,
                          ),
                        )
                      }
                      disabled={!canAdmin}
                    />
                  </Field>
                  <Field label="Ownership" htmlFor={`domain-ownership-${index}`}>
                    <Select
                      value={domain.ownership}
                      onValueChange={(ownership: "managed" | "linked") =>
                        setDomains(
                          domains.map((item, i) => (i === index ? { ...item, ownership } : item)),
                        )
                      }
                      disabled={!canAdmin}
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
                  <div className="flex min-w-0 flex-col gap-1">
                    <Status
                      tone={routeStatus?.tone ?? "unknown"}
                      label={routeStatus?.label ?? "Not observed"}
                    />
                    <Status
                      tone={certificateStatus?.tone ?? "unknown"}
                      label={certificateStatus?.label ?? "Not observed"}
                    />
                  </div>
                  {canAdmin && (
                    <IconAction
                      label={`Remove ${domain.hostname}`}
                      onClick={() => setDomains(domains.filter((_, i) => i !== index))}
                      className="text-destructive"
                    >
                      <Trash />
                    </IconAction>
                  )}
                  {domain.protection && (
                    <div className="grid gap-3 sm:col-span-2 sm:grid-cols-2 lg:col-span-6">
                      <Field label="User name" htmlFor={`domain-user-${index}`}>
                        <Input
                          id={`domain-user-${index}`}
                          value={domain.protection.username}
                          readOnly={!canAdmin}
                          autoComplete="off"
                          className="font-mono"
                          onChange={(event) =>
                            setDomains(
                              domains.map((item, i) =>
                                i === index && item.protection
                                  ? {
                                      ...item,
                                      protection: {
                                        ...item.protection,
                                        username: event.target.value,
                                      },
                                    }
                                  : item,
                              ),
                            )
                          }
                        />
                      </Field>
                      <Field
                        label="Password"
                        htmlFor={`domain-password-${index}`}
                        hint={
                          domain.protection.hash
                            ? "Leave empty to keep the current password."
                            : "At least 8 characters. Visitors are asked for it before the site."
                        }
                      >
                        <Input
                          id={`domain-password-${index}`}
                          type="password"
                          autoComplete="new-password"
                          value={domain.protection.password ?? ""}
                          readOnly={!canAdmin}
                          className="font-mono"
                          placeholder={domain.protection.hash ? "Unchanged" : ""}
                          onChange={(event) =>
                            setDomains(
                              domains.map((item, i) =>
                                i === index && item.protection
                                  ? {
                                      ...item,
                                      protection: {
                                        ...item.protection,
                                        password: event.target.value,
                                      },
                                    }
                                  : item,
                              ),
                            )
                          }
                        />
                      </Field>
                    </div>
                  )}
                  {rowError?.index === index && (
                    <p
                      role="alert"
                      className="text-hint text-destructive sm:col-span-2 lg:col-span-6"
                    >
                      {rowError.message}
                    </p>
                  )}
                </Group>
              )
            })}
          </div>
        )}
      </SettingCard>

      <FormFacts>
        <FormFact label="Application port">{runtime.internalPort ?? "—"}</FormFact>
        <FormFact label="Bind address" mono>
          {runtime.bindAddress || "127.0.0.1"}
        </FormFact>
        <FormFact label="Host port">{runtime.hostPort ? runtime.hostPort : "Dynamic"}</FormFact>
        {runtime.ports && runtime.ports.length > 0 && (
          <FormFact label="Also published" mono>
            {runtime.ports
              .map((port) => `${port.hostPort}/${port.protocol ?? "tcp"} → ${port.containerPort}`)
              .join(", ")}
          </FormFact>
        )}
        <Link href="/proxy/sites" className="rounded-sm underline underline-offset-4 focus-ring">
          Proxy sites
        </Link>
        <Link
          href="/proxy/certificates"
          className="rounded-sm underline underline-offset-4 focus-ring"
        >
          Certificates
        </Link>
      </FormFacts>

      {publicBind && (
        <Notice tone="warning" title="This environment binds a public address" icon={Warning}>
          Traffic on {runtime.bindAddress} reaches the container directly, ahead of Proxy. Confirm
          the{" "}
          <Link href="/security/firewall" className="underline underline-offset-4">
            firewall
          </Link>{" "}
          allows only the traffic you expect before relying on it.
        </Notice>
      )}

      <AddDomainPanel
        open={adding}
        onOpenChange={setAdding}
        onAdd={async (domain) => {
          await persist([...domains, domain])
          setAdding(false)
        }}
      />
    </div>
  )
}

type HostnameSuggestion = {
  hostname: string
  covered: boolean
  certificateName?: string
  certificateMethod?: string
  certificateIssue?: string
  method: string
}

/**
 * The one place a hostname is typed and checked before it is committed — the
 * same certificate-coverage read the new-project flow does, ported here so an
 * existing environment gets the same answer before adding a second name.
 */
function AddDomainPanel({
  open,
  onOpenChange,
  onAdd,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onAdd: (domain: DomainValue) => Promise<void>
}) {
  const [hostname, setHostname] = useSessionState("deploy.settings.domains.add.hostname", "")
  const [https, setHttps] = useSessionState("deploy.settings.domains.add.https", true)
  const [checking, setChecking] = useState(false)
  const [saving, setSaving] = useState(false)
  const [suggestion, setSuggestion] = useState<HostnameSuggestion>()

  const reset = () => {
    setHostname("")
    setHttps(true)
    setSuggestion(undefined)
  }

  const check = async () => {
    const value = hostname.trim().toLowerCase()
    if (!value) return
    setChecking(true)
    try {
      setSuggestion(await get<HostnameSuggestion>("/deploy/hostname", { hostname: value }))
    } catch {
      // The saved answer stays on screen; the deploy itself is what settles this.
    } finally {
      setChecking(false)
    }
  }

  const matches = suggestion?.hostname.toLowerCase() === hostname.trim().toLowerCase()

  const add = async () => {
    const value = hostname.trim().toLowerCase()
    if (!value) return
    setSaving(true)
    try {
      await onAdd({ hostname: value, https, ownership: "managed" })
      reset()
    } catch (error) {
      notify.error("Could not save domains", error)
    } finally {
      setSaving(false)
    }
  }

  return (
    <SidePanel
      open={open}
      onOpenChange={(next) => {
        if (!next) reset()
        onOpenChange(next)
      }}
      title="Add domain"
      description="Point a record at this server and check it before adding it."
      width="sm"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
            Cancel
          </Button>
          <Button onClick={add} disabled={!hostname.trim()} pending={saving}>
            Save
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field label="Hostname" htmlFor="add-domain-hostname">
          <Input
            id="add-domain-hostname"
            value={hostname}
            onChange={(event) => setHostname(event.target.value)}
            onBlur={() => void check()}
            placeholder="app.example.com"
            className="font-mono"
          />
        </Field>
        <Field label="HTTPS" htmlFor="add-domain-https">
          <div className="flex min-h-9 items-center gap-2">
            <Switch id="add-domain-https" checked={https} onCheckedChange={setHttps} />
            <span className="text-body text-muted-foreground">{https ? "On" : "Off"}</span>
          </div>
        </Field>
        {checking && <p className="text-hint text-muted-foreground">Checking…</p>}
        {https && matches && suggestion?.covered && (
          <Notice tone="success" icon={CheckCircle} title="HTTPS is ready for this name">
            The <code className="font-mono">{suggestion.certificateName}</code> certificate already
            covers it, so the deploy reuses it rather than ordering another.
          </Notice>
        )}
        {https && matches && suggestion && !suggestion.covered && (
          <Notice
            tone={suggestion.certificateMethod ? "default" : "warning"}
            icon={suggestion.certificateMethod ? LockClosed : Warning}
            title={
              suggestion.certificateMethod
                ? "A certificate will be issued during the deploy"
                : "Automatic HTTPS needs attention"
            }
          >
            {suggestion.certificateMethod ? (
              <>
                The run orders one for <code className="font-mono">{hostname}</code> over the{" "}
                {suggestion.certificateMethod === "caddy"
                  ? "managed Caddy ingress"
                  : `${suggestion.certificateMethod} challenge`}{" "}
                before it starts anything.
              </>
            ) : (
              (suggestion.certificateIssue ?? "Certificate readiness could not be confirmed.")
            )}
          </Notice>
        )}
        {matches && suggestion?.method === "none" && (
          <Notice tone="warning" icon={Warning} title="This server has no public address">
            The deployment still runs; reach it through the port it publishes, or set a domain that
            resolves here.
          </Notice>
        )}
      </div>
    </SidePanel>
  )
}
