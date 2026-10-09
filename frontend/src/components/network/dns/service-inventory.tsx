"use client"

import { useEffect, useId, useRef, useState, type ReactNode } from "react"
import { useAuth } from "@/hooks/use-auth"
import { errorMessage, post } from "@/lib/api"
import {
  DNS_SERVICE_BASE,
  dnsEngineName,
  readDNSChange,
  readDNSChangeRequest,
  type DNSChangeRequest,
  type DNSNativeGroup,
  type DNSNativeReading,
  type DNSServiceChange,
  type DNSServiceView,
} from "@/lib/network-dns-services"
import { Field, OptionRow } from "@/components/form"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { dnsDraftLines, dnsStageProblem } from "./service-form"
import { DNSServicePolicyForm, type DNSPolicyAction } from "./service-policy-form"
import { DNSServiceFilters } from "./service-filters"

function InventorySection({
  title,
  count,
  children,
}: {
  title: string
  count?: number
  children: ReactNode
}) {
  return (
    <section className="min-w-0 space-y-4" aria-label={title}>
      <div className="border-panel-border flex min-w-0 items-baseline justify-between gap-3 border-b pb-3">
        <h3 className="text-base font-semibold">{title}</h3>
        {count !== undefined && (
          <span
            className="numeric text-2xl font-semibold"
            aria-label={`${count} ${title.toLowerCase()}`}
          >
            {count}
          </span>
        )}
      </div>
      {children}
    </section>
  )
}

function Reading({ label, value }: { label: string; value: DNSNativeReading }) {
  return (
    <div className="min-w-0 text-body">
      <p className="font-medium">
        {label} · {value.state}
      </p>
      <p className="text-hint break-words text-muted-foreground">Basis: {value.basis}</p>
      <p className="break-words text-muted-foreground">{value.summary}</p>
    </div>
  )
}

function Values({ values, empty }: { values: string[]; empty: string }) {
  return values.length ? (
    <ul className="min-w-0 space-y-1 text-body">
      {values.map((value, index) => (
        <li key={`${index}-${value}`} className="font-mono break-all">
          {value}
        </li>
      ))}
    </ul>
  ) : (
    <p className="text-body text-muted-foreground">{empty}</p>
  )
}

function Groups({ groups }: { groups: DNSNativeGroup[] }) {
  return groups.length ? (
    <ul className="divide-panel-border min-w-0 divide-y">
      {groups.map((group, index) => (
        <li key={`${index}-${group.id}-${group.name}`} className="min-w-0 space-y-1 py-3 text-body">
          <p className="font-medium break-words">
            {group.name}
            {group.id !== undefined && ` · ID ${group.id}`} ·{" "}
            {group.enabled === undefined
              ? "Native enabled state unspecified"
              : group.enabled
                ? "Enabled"
                : "Disabled"}
          </p>
          <p className="break-all">
            Client scopes: {group.clientScopes.join(", ") || "None declared"}
          </p>
          <p className="break-all">
            Listener scopes: {group.listenerScopes.join(", ") || "None declared"}
          </p>
          <p className="break-all">Domains: {group.domains.join(", ") || "None declared"}</p>
          {group.translations && (
            <dl className="min-w-0">
              {Object.entries(group.translations).map(([from, to]) => (
                <div key={from} className="grid min-w-0 gap-1 sm:grid-cols-2">
                  <dt className="font-mono break-all">{from}</dt>
                  <dd className="font-mono break-all">→ {to}</dd>
                </div>
              ))}
            </dl>
          )}
        </li>
      ))}
    </ul>
  ) : (
    <p className="text-body text-muted-foreground">No group rows in this native reading.</p>
  )
}

export function DNSServiceInventory(props: {
  view: DNSServiceView
  stale: boolean
  onReview: (change: DNSServiceChange) => void
  onRefresh: () => void
}) {
  return <Inventory key={props.view.connection.id} {...props} />
}

/** Reading register: counts head their own flat sections; native provenance
 * remains beside each reading, including empty, partial and unsupported data. */
function Inventory({
  view,
  stale,
  onReview,
  onRefresh,
}: {
  view: DNSServiceView
  stale: boolean
  onReview: (change: DNSServiceChange) => void
  onRefresh: () => void
}) {
  const { can } = useAuth()
  const id = useId()
  const snapshot = view.snapshot
  const [action, setAction] = useState<DNSChangeRequest["action"]>("protection")
  const [protection, setProtection] = useState(snapshot?.protection ?? false)
  const [upstreams, setUpstreams] = useState("")
  const [allowedClients, setAllowedClients] = useState(snapshot?.allowedClients.join("\n") ?? "")
  const [deniedClients, setDeniedClients] = useState(snapshot?.deniedClients.join("\n") ?? "")
  const [zone, setZone] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [fieldError, setFieldError] = useState<string>()
  const errorRef = useRef<HTMLDivElement>(null)
  const inFlight = useRef(false)
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  const admin = can("system.admin")
  const problem = dnsStageProblem(view, stale, admin)
  const stage = async (prepared?: DNSChangeRequest) => {
    if (inFlight.current || problem) return
    setError(undefined)
    setFieldError(undefined)
    let request: DNSChangeRequest
    try {
      if (prepared) {
        if (prepared.action !== action) throw new Error("The selected native action changed.")
        request = readDNSChangeRequest(prepared, view.connection.engine)
      } else {
        switch (action) {
          case "protection":
            request = { action, protection }
            break
          case "upstreams":
            request = { action, upstreams: dnsDraftLines(upstreams, 16, true) }
            break
          case "access":
            if (view.connection.engine !== "adguard")
              throw new Error("Allowed-client changes require AdGuard Home.")
            request = {
              action,
              allowedClients: dnsDraftLines(allowedClients, 128, true),
              deniedClients: dnsDraftLines(deniedClients, 128),
            }
            if (
              new Set([...request.allowedClients, ...request.deniedClients]).size !==
              request.allowedClients.length + request.deniedClients.length
            )
              throw new Error("Allowed and denied client scopes must be unique across both lists.")
            break
          case "zone_create":
            if (view.connection.engine !== "technitium")
              throw new Error("Primary-zone creation requires Technitium.")
            if (
              !zone.includes(".") ||
              zone.length > 253 ||
              !zone.split(".").every((part) => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(part))
            )
              throw new Error("Enter a lower-case primary-zone DNS name with complete labels.")
            request = { action, zone }
            break
          default:
            throw new Error("Complete the selected native record or client form before reviewing.")
        }
      }
    } catch (err) {
      setFieldError(errorMessage(err))
      requestAnimationFrame(() => errorRef.current?.focus())
      return
    }
    inFlight.current = true
    setBusy(true)
    try {
      const change = readDNSChange(
        await post<unknown>(
          `${DNS_SERVICE_BASE}/${encodeURIComponent(view.connection.id)}/changes`,
          request,
        ),
      )
      if (
        change.connectionId !== view.connection.id ||
        change.generation !== view.connection.generation ||
        change.before?.engine !== view.connection.engine ||
        change.state !== "planned"
      )
        throw new Error(
          "The retained review does not match this connection and native baseline. Refresh before continuing.",
        )
      if (JSON.stringify(change.request) !== JSON.stringify(request))
        throw new Error(
          "The retained review changed your requested intent. Refresh before continuing.",
        )
      if (mounted.current) onReview(change)
    } catch (err) {
      if (mounted.current) {
        setError(errorMessage(err))
        requestAnimationFrame(() => errorRef.current?.focus())
      }
    } finally {
      inFlight.current = false
      if (mounted.current) setBusy(false)
    }
  }
  const choices: { action: DNSChangeRequest["action"]; label: string }[] = [
    { action: "protection", label: "Protection" },
    { action: "upstreams", label: "Upstreams" },
    ...(view.connection.engine === "adguard"
      ? [{ action: "access" as const, label: "Client access" }]
      : []),
    ...(view.connection.engine === "technitium"
      ? [
          { action: "zone_create" as const, label: "Primary zone" },
          { action: "record_add" as const, label: "Add zone record" },
          { action: "record_remove" as const, label: "Remove zone record" },
        ]
      : [
          { action: "override_add" as const, label: "Add local override" },
          { action: "override_remove" as const, label: "Remove local override" },
        ]),
    ...(view.connection.engine === "pihole"
      ? [{ action: "client_groups" as const, label: "Client groups" }]
      : []),
  ]
  const policyAction = [
    "override_add",
    "override_remove",
    "record_add",
    "record_remove",
    "client_groups",
  ].includes(action)
  if (!admin)
    return (
      <p className="text-body text-muted-foreground">
        Native DNS inventory requires system administration access.
      </p>
    )
  return (
    <div className="flex min-w-0 flex-col gap-8" aria-label="Native DNS service inventory">
      <div className="border-panel-border flex min-w-0 flex-wrap items-start justify-between gap-3 border-b pb-3">
        <div className="min-w-0 space-y-1">
          <p className="text-base font-semibold">
            {view.connection.name} · {dnsEngineName(view.connection.engine)}
          </p>
          <p className="font-mono text-body break-all">{view.connection.endpoint}</p>
          <p className="text-hint text-muted-foreground">
            {view.state} ·{" "}
            {view.connection.management ? "Reviewed management allowed" : "Read-only connection"} ·{" "}
            {view.connection.ownership} · generation {view.connection.generation}
          </p>
          {snapshot && (
            <p className="text-hint text-muted-foreground">
              Native version {snapshot.version || "Unknown"} · observed{" "}
              <time dateTime={snapshot.observedAt}>
                {new Date(snapshot.observedAt).toLocaleString()}
              </time>
            </p>
          )}
        </div>
        <Button variant="outline" onClick={onRefresh} disabled={busy}>
          Refresh native reading
        </Button>
      </div>
      {(stale || view.state !== "available" || view.error) && (
        <div role="status">
          <Notice
            tone="warning"
            title={stale ? "Last native reading retained" : "Native service needs review"}
          >
            {view.error && <p>{view.error}</p>}
            <p>
              {stale
                ? "The refresh failed. The prior inventory remains visible; native changes are blocked."
                : `Native state: ${view.state}. A returned response does not establish verified native success.`}
            </p>
          </Notice>
        </div>
      )}
      {!snapshot ? (
        <p className="text-body text-muted-foreground">
          No authenticated native inventory is available. Refresh the connection to inspect its
          current owner.
        </p>
      ) : (
        <>
          <InventorySection title="Native owner and transport">
            <p className="text-body break-words">
              Roles: {snapshot.roles.join(" · ") || "No roles declared"}
            </p>
            <Reading label="Runtime" value={snapshot.runtime} />
            <Reading label="Transport" value={snapshot.transport} />
            {snapshot.process && (
              <p className="text-body break-words">
                Native PID {snapshot.process.pid} · startup interval{" "}
                {new Date(snapshot.process.startedAt).toLocaleString()} →{" "}
                {new Date(snapshot.process.startedBefore).toLocaleString()}
              </p>
            )}
          </InventorySection>
          <InventorySection title="Configured listeners" count={snapshot.listeners.length}>
            <ul className="divide-panel-border min-w-0 divide-y">
              {snapshot.listeners.map((listener, index) => (
                <li key={index} className="min-w-0 py-3 text-body">
                  <p className="font-mono break-all">
                    {listener.address.includes(":") && !listener.address.startsWith("[")
                      ? `[${listener.address}]`
                      : listener.address}
                    :{listener.port} · {listener.protocol}
                  </p>
                  <p className="break-words text-muted-foreground">Scope: {listener.scope}</p>
                </li>
              ))}
            </ul>
            {!snapshot.listeners.length && (
              <p className="text-body text-muted-foreground">
                No configured listener rows were returned. Runtime evidence remains separate.
              </p>
            )}
          </InventorySection>
          <InventorySection title="Native upstreams" count={snapshot.upstreams.length}>
            <p className="text-body">
              Native upstream protocol: {snapshot.upstreamProtocol || "Unspecified"}
            </p>
            <Values
              values={snapshot.upstreams}
              empty="No upstream entries in this native reading."
            />
            <p className="text-body">
              Native protection {snapshot.protection ? "enabled" : "disabled"}
              {snapshot.protectionTemporary && " · temporary setting"}
            </p>
          </InventorySection>
          <InventorySection
            title="Client access policy"
            count={snapshot.allowedClients.length + snapshot.deniedClients.length}
          >
            <Reading label="Native access" value={snapshot.access} />
            <div className="grid min-w-0 gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <p className="text-body font-medium">
                  Allowed clients · {snapshot.allowedClients.length}
                </p>
                <Values
                  values={snapshot.allowedClients}
                  empty="No explicit allowed-client entries; consult the native access reading."
                />
              </div>
              <div className="space-y-2">
                <p className="text-body font-medium">
                  Denied clients · {snapshot.deniedClients.length}
                </p>
                <Values
                  values={snapshot.deniedClients}
                  empty="No explicit denied-client entries."
                />
              </div>
            </div>
          </InventorySection>
          <InventorySection
            title="Native client settings"
            count={
              snapshot.clientEvidence.state === "configured" ? snapshot.clients.length : undefined
            }
          >
            <Reading label="Client evidence" value={snapshot.clientEvidence} />
            <ul className="divide-panel-border min-w-0 divide-y">
              {snapshot.clients.map((client, index) => (
                <li key={`${index}-${client.id}`} className="min-w-0 space-y-1 py-3 text-body">
                  <p className="font-medium break-words">
                    {client.name || client.id} ·{" "}
                    {client.inherited ? "Inherited settings" : "Client-specific settings"}
                  </p>
                  <p className="font-mono break-all">
                    ID {client.id} · {client.addresses.join(", ") || "No declared addresses"}
                  </p>
                  <p>
                    Filtering:{" "}
                    {client.filtering === undefined
                      ? "Unspecified"
                      : client.filtering
                        ? "Enabled"
                        : "Disabled"}
                    ; group IDs: {client.groups?.join(", ") || "None declared"}
                  </p>
                </li>
              ))}
            </ul>
          </InventorySection>
          <InventorySection title="Authoritative zones" count={snapshot.zones.length}>
            <Reading label="Zone evidence" value={snapshot.zoneEvidence} />
            <ul className="divide-panel-border min-w-0 divide-y">
              {snapshot.zones.map((zone, index) => (
                <li key={`${index}-${zone.name}`} className="min-w-0 py-3 text-body">
                  <p className="font-mono break-all">
                    {zone.name} · {zone.type} · {zone.disabled ? "Disabled" : "Enabled"}
                  </p>
                  <p className="text-muted-foreground">
                    Configured DNSSEC: {zone.dnssec || "Unspecified"}; no cryptographic verification
                    was measured here.
                  </p>
                </li>
              ))}
            </ul>
          </InventorySection>
          <InventorySection title="Local overrides" count={snapshot.localOverrides.length}>
            <Reading label="Override evidence" value={snapshot.overrideEvidence} />
            <ul className="divide-panel-border min-w-0 divide-y">
              {snapshot.localOverrides.map((override, index) => (
                <li
                  key={`${index}-${override.name}`}
                  className="min-w-0 py-3 font-mono text-body break-all"
                >
                  {override.name} · {override.type} → {override.value}
                  {override.enabled === undefined
                    ? " · Native enabled state unspecified"
                    : override.enabled
                      ? " · Enabled"
                      : " · Disabled"}
                </li>
              ))}
            </ul>
          </InventorySection>
          <InventorySection title="Native views" count={snapshot.viewGroups.length}>
            <Reading label="View evidence" value={snapshot.views} />
            <Groups groups={snapshot.viewGroups} />
          </InventorySection>
          <InventorySection
            title="Named networks"
            count={Object.keys(snapshot.namedNetworks).length}
          >
            <dl className="divide-panel-border min-w-0 divide-y">
              {Object.entries(snapshot.namedNetworks).map(([name, scopes]) => (
                <div
                  key={name}
                  className="grid min-w-0 gap-1 py-3 text-body sm:grid-cols-[12rem_minmax(0,1fr)]"
                >
                  <dt className="font-medium break-words">{name}</dt>
                  <dd className="font-mono break-all">
                    {scopes.join(", ") || "No declared scopes"}
                  </dd>
                </div>
              ))}
            </dl>
            <p className="text-body">
              Native translations:{" "}
              {snapshot.translationEnabled === undefined
                ? "Unspecified"
                : snapshot.translationEnabled
                  ? "Enabled"
                  : "Disabled"}
              . Mappings are listed with their native view groups.
            </p>
          </InventorySection>
          <InventorySection
            title="Native filtering groups"
            count={
              snapshot.appClientEvidence.state === "configured"
                ? snapshot.filterGroups.length
                : undefined
            }
          >
            <p className="text-body">
              Installed-app protection:{" "}
              {snapshot.appProtection === undefined
                ? "Unspecified"
                : snapshot.appProtection
                  ? "Enabled"
                  : "Disabled"}
            </p>
            <Reading label="App client evidence" value={snapshot.appClientEvidence} />
            <Groups groups={snapshot.filterGroups} />
          </InventorySection>
          <InventorySection title="Native query history" count={snapshot.queries.length}>
            <Reading label="Query evidence" value={snapshot.queryEvidence} />
            <ol className="divide-panel-border min-w-0 divide-y">
              {snapshot.queries.map((query, index) => (
                <li key={`${index}-${query.at}`} className="min-w-0 space-y-1 py-3 text-body">
                  <p className="font-mono break-all">
                    {query.name} · {query.type} · {query.status}
                  </p>
                  <p className="break-all text-muted-foreground">
                    {query.at} · client {query.client} · protocol {query.protocol || "Unspecified"}
                  </p>
                </li>
              ))}
            </ol>
            <p className="text-hint text-muted-foreground">
              These bounded rows are the native engine&apos;s query history; they do not establish
              an independent client reachability test.
            </p>
          </InventorySection>
          <InventorySection title="Native limitations" count={snapshot.limitations.length}>
            <ul className="min-w-0 list-inside list-disc space-y-2 text-body text-muted-foreground">
              {snapshot.limitations.map((limitation, index) => (
                <li key={index} className="break-words">
                  {limitation}
                </li>
              ))}
            </ul>
          </InventorySection>
        </>
      )}
      <DNSServiceFilters
        key={JSON.stringify(view.connection)}
        connection={view.connection}
        ownerStale={stale}
      />
      <InventorySection title="Review a native change">
        <ChoiceGrid columns={2} aria-label="Native DNS change kinds">
          {choices.map((choice) => (
            <ChoiceCard
              key={choice.action}
              selected={action === choice.action}
              title={choice.label}
              verb={`Review ${choice.label.toLowerCase()}`}
              disabled={busy}
              onClick={() => {
                setAction(choice.action)
                setFieldError(undefined)
              }}
            />
          ))}
        </ChoiceGrid>
        {policyAction ? (
          <DNSServicePolicyForm
            action={action as DNSPolicyAction}
            view={view}
            problem={problem}
            stale={stale}
            busy={busy}
            onStage={stage}
          />
        ) : (
          <form
            className="flex min-w-0 flex-col gap-4"
            aria-label="Review native DNS change"
            aria-busy={busy}
            onSubmit={(event) => {
              event.preventDefault()
              void stage()
            }}
          >
            {action === "protection" && (
              <OptionRow
                title="Native protection"
                checked={protection}
                disabled={Boolean(problem) || busy}
                onCheckedChange={setProtection}
                hint="The review sets persistent native protection; installed-app protection remains a separate reading."
              />
            )}
            {action === "upstreams" && (
              <Field
                label="Classic DNS upstream endpoints"
                htmlFor={`${id}-upstreams`}
                error={fieldError}
                hint="1–16 literal IP:port endpoints, one per line; bracket IPv6. This review replaces the native upstream list with classic DNS."
              >
                <Textarea
                  id={`${id}-upstreams`}
                  className="font-mono"
                  value={upstreams}
                  onChange={(event) => setUpstreams(event.target.value)}
                  disabled={Boolean(problem) || busy}
                  maxLength={8192}
                  spellCheck={false}
                  aria-invalid={Boolean(fieldError)}
                />
              </Field>
            )}
            {action === "access" && (
              <div className="grid min-w-0 gap-4 sm:grid-cols-2">
                <Field
                  label="Allowed client prefixes"
                  htmlFor={`${id}-allowed`}
                  error={fieldError}
                  hint="1–128 canonical IP prefixes, one per line. This is an explicit allow scope."
                >
                  <Textarea
                    id={`${id}-allowed`}
                    className="font-mono"
                    value={allowedClients}
                    onChange={(event) => setAllowedClients(event.target.value)}
                    disabled={Boolean(problem) || busy}
                    maxLength={8192}
                    spellCheck={false}
                    aria-invalid={Boolean(fieldError)}
                  />
                </Field>
                <Field
                  label="Denied client prefixes"
                  htmlFor={`${id}-denied`}
                  hint="Up to 128 canonical prefixes; none may overlap an identical allowed entry."
                >
                  <Textarea
                    id={`${id}-denied`}
                    className="font-mono"
                    value={deniedClients}
                    onChange={(event) => setDeniedClients(event.target.value)}
                    disabled={Boolean(problem) || busy}
                    maxLength={8192}
                    spellCheck={false}
                  />
                </Field>
              </div>
            )}
            {action === "zone_create" && (
              <Field
                label="Primary zone name"
                htmlFor={`${id}-zone`}
                error={fieldError}
                hint="A lower-case Technitium primary zone, separate from local overrides."
              >
                <Input
                  id={`${id}-zone`}
                  className="font-mono"
                  value={zone}
                  onChange={(event) => setZone(event.target.value)}
                  disabled={Boolean(problem) || busy}
                  maxLength={253}
                  spellCheck={false}
                  aria-invalid={Boolean(fieldError)}
                />
              </Field>
            )}
            {problem && <p className="text-body text-muted-foreground">{problem}</p>}
            <div>
              <Button type="submit" disabled={Boolean(problem) || busy} pending={busy}>
                Create retained review
              </Button>
            </div>
          </form>
        )}
        {(error || fieldError) && (
          <div ref={errorRef} role="alert" tabIndex={-1} className="rounded-sm focus-ring">
            <Notice tone="warning" title="Native review unavailable">
              <p>{error || fieldError}</p>
              <p>
                Your draft is retained. Refresh the native reading before retrying if its owner or
                baseline changed.
              </p>
            </Notice>
          </div>
        )}
      </InventorySection>
    </div>
  )
}
