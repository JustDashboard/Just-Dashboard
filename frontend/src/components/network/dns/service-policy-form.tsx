"use client"

import { useEffect, useId, useRef, useState } from "react"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import {
  DNS_SERVICE_BASE,
  readDNSRecords,
  type DNSChangeRequest,
  type DNSServiceView,
} from "@/lib/network-dns-services"
import {
  dnsClientAddress,
  dnsRecordZoneEditable,
  prepareDNSClientGroups,
  prepareDNSRecordChange,
  type DNSRecordAction,
  type DNSRecordErrors,
} from "@/lib/network-dns-service-policy"
import { Field } from "@/components/form"
import { ErrorState, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

export type DNSPolicyAction = DNSRecordAction | "client_groups"
type Props = {
  action: DNSPolicyAction
  view: DNSServiceView
  problem?: string
  stale?: boolean
  busy: boolean
  onStage: (request: DNSChangeRequest) => Promise<void>
}

export function DNSServicePolicyForm(props: Props) {
  return props.action === "client_groups" ? (
    <ClientGroupsForm {...props} />
  ) : (
    <RecordForm {...props} action={props.action} />
  )
}

function FormErrors({
  errors,
  fields,
}: {
  errors: Record<string, string | undefined>
  fields: Record<string, { label: string; id: string }>
}) {
  const target = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (Object.values(errors).some(Boolean)) target.current?.focus()
  }, [errors])
  if (!Object.values(errors).some(Boolean)) return null
  return (
    <div ref={target} role="alert" tabIndex={-1} className="rounded-sm focus-ring">
      <Notice tone="warning" title="Check the native change fields">
        <ul className="space-y-1">
          {Object.entries(errors).map(([key, error]) =>
            error ? (
              <li key={key}>
                {fields[key] ? (
                  <a
                    href={`#${fields[key].id}`}
                    className="underline underline-offset-2"
                    onClick={() => document.getElementById(fields[key].id)?.focus()}
                  >
                    {fields[key].label}: {error}
                  </a>
                ) : (
                  error
                )}
              </li>
            ) : null,
          )}
        </ul>
      </Notice>
    </div>
  )
}

function RecordForm({
  action,
  view,
  problem,
  stale,
  busy,
  onStage,
}: Props & { action: DNSRecordAction }) {
  const id = useId()
  const [name, setName] = useState("")
  const [type, setType] = useState("A")
  const [value, setValue] = useState("")
  const [zone, setZone] = useState("")
  const [ttl, setTTL] = useState("300")
  const [errors, setErrors] = useState<DNSRecordErrors>({})
  const authoritative = action === "record_add" || action === "record_remove"
  const inventory = usePoll(
    async (signal) =>
      readDNSRecords(
        await get(
          `${DNS_SERVICE_BASE}/${view.connection.id}/zones/${encodeURIComponent(zone)}/records`,
          undefined,
          signal,
        ),
        zone,
      ),
    30000,
    [view.connection.id, view.connection.generation, zone],
    { enabled: authoritative && Boolean(zone) && view.state === "available" && !stale },
  )
  const zoneProblem = authoritative
    ? inventory.error
      ? "Refresh the selected zone before reviewing its records."
      : !inventory.data
        ? "Read an explicit native zone before reviewing records."
        : !dnsRecordZoneEditable(inventory.data)
          ? "Record changes require a supported enabled unsigned Primary zone."
          : undefined
    : undefined
  const held = problem ?? zoneProblem
  const fields = Object.fromEntries(
    [
      ["name", "DNS record name"],
      ["type", "Record type"],
      ["value", "Record value"],
      ...(authoritative
        ? [
            ["zone", "Authoritative zone"],
            ["ttl", "TTL (seconds)"],
          ]
        : []),
    ].map(([key, label]) => [key, { label, id: `${id}-${key}` }]),
  )
  return (
    <form
      className="flex min-w-0 flex-col gap-4"
      aria-label="Review native DNS record"
      aria-busy={busy}
      onSubmit={(event) => {
        event.preventDefault()
        if (held || busy) return
        const result = prepareDNSRecordChange(
          view.connection.engine,
          action,
          { name, type, value, ...(authoritative ? { zone, ttl } : {}) },
          authoritative ? inventory.data : undefined,
        )
        setErrors(result.errors)
        if (result.request) void onStage(result.request)
      }}
    >
      <FormErrors errors={errors} fields={fields} />
      {authoritative && (
        <>
          <Field
            label="Authoritative zone"
            htmlFor={`${id}-zone`}
            error={errors.zone && <span id={`${id}-zone-error`}>{errors.zone}</span>}
          >
            <Select value={zone} onValueChange={setZone} disabled={busy}>
              <SelectTrigger
                id={`${id}-zone`}
                aria-invalid={Boolean(errors.zone)}
                aria-describedby={errors.zone ? `${id}-zone-error` : undefined}
              >
                <SelectValue placeholder="Select a native zone" />
              </SelectTrigger>
              <SelectContent>
                {view.snapshot?.zones.map((item) => (
                  <SelectItem key={item.name} value={item.name}>
                    {item.name} · {item.type}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <div>
            <Button
              type="button"
              variant="outline"
              onClick={inventory.refresh}
              disabled={!zone || busy || Boolean(problem)}
            >
              Read zone records
            </Button>
          </div>
          {inventory.error && <ErrorState error={inventory.error} onRetry={inventory.refresh} />}
          {inventory.data && (
            <section className="min-w-0 space-y-3" aria-label="Selected zone records">
              <p className="text-body break-words">
                {inventory.data.zone} · {inventory.data.type} ·{" "}
                {inventory.data.disabled ? "Disabled" : "Enabled"} · {inventory.data.dnssec}
              </p>
              <p className="text-hint break-words text-muted-foreground">
                Native version {inventory.data.nativeVersion} ·{" "}
                {inventory.data.internal === null
                  ? "Internal category unreported"
                  : inventory.data.internal
                    ? "Internal zone"
                    : "External zone"}
              </p>
              <p className="text-hint break-words text-muted-foreground">
                {inventory.data.evidence.state} · {inventory.data.evidence.basis} ·{" "}
                {inventory.data.evidence.summary}
              </p>
              <ul className="divide-panel-border min-w-0 divide-y">
                {inventory.data.records.map((record, index) => (
                  <li
                    key={`${index}-${record.fingerprint}`}
                    className="min-w-0 space-y-2 py-3 text-body"
                  >
                    <p className="font-mono break-all">
                      {record.name} · {record.type} →{" "}
                      {record.value ?? "Native value outside this control"} · TTL {record.ttl}s ·{" "}
                      {record.disabled ? "Disabled" : "Enabled"}
                    </p>
                    {record.comments && (
                      <p className="break-words text-muted-foreground">{record.comments}</p>
                    )}
                    {record.editable && record.value ? (
                      <Button
                        type="button"
                        variant="outline"
                        disabled={Boolean(problem) || busy}
                        aria-label={`Use record ${record.name} ${record.type} ${record.value}`}
                        onClick={() => {
                          setName(record.name)
                          setType(record.type)
                          setValue(record.value!)
                          setTTL(String(record.ttl))
                          setErrors({})
                        }}
                      >
                        Use record
                      </Button>
                    ) : (
                      <p className="text-hint text-muted-foreground">Read-only native record.</p>
                    )}
                  </li>
                ))}
              </ul>
              {!inventory.data.records.length && (
                <p className="text-body text-muted-foreground">
                  No records in this native reading.
                </p>
              )}
            </section>
          )}
        </>
      )}
      <div className="grid min-w-0 gap-4 sm:grid-cols-2">
        <Field
          label="DNS record name"
          htmlFor={`${id}-name`}
          error={errors.name && <span id={`${id}-name-error`}>{errors.name}</span>}
          hint="A complete lower-case DNS name."
        >
          <Input
            id={`${id}-name`}
            value={name}
            onChange={(event) => setName(event.target.value)}
            disabled={Boolean(problem) || busy}
            maxLength={253}
            spellCheck={false}
            className="font-mono"
            aria-invalid={Boolean(errors.name)}
            aria-describedby={errors.name ? `${id}-name-error` : undefined}
          />
        </Field>
        <Field
          label="Record type"
          htmlFor={`${id}-type`}
          error={errors.type && <span id={`${id}-type-error`}>{errors.type}</span>}
        >
          <Select value={type} onValueChange={setType} disabled={Boolean(problem) || busy}>
            <SelectTrigger
              id={`${id}-type`}
              aria-invalid={Boolean(errors.type)}
              aria-describedby={errors.type ? `${id}-type-error` : undefined}
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="A">A</SelectItem>
              <SelectItem value="AAAA">AAAA</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        <Field
          label="Record value"
          htmlFor={`${id}-value`}
          error={errors.value && <span id={`${id}-value-error`}>{errors.value}</span>}
          hint="The exact canonical unicast IP for this record family."
        >
          <Input
            id={`${id}-value`}
            value={value}
            onChange={(event) => setValue(event.target.value)}
            disabled={Boolean(problem) || busy}
            maxLength={64}
            spellCheck={false}
            className="font-mono"
            aria-invalid={Boolean(errors.value)}
            aria-describedby={errors.value ? `${id}-value-error` : undefined}
          />
        </Field>
        {authoritative && (
          <Field
            label="TTL (seconds)"
            htmlFor={`${id}-ttl`}
            error={errors.ttl && <span id={`${id}-ttl-error`}>{errors.ttl}</span>}
            hint="An exact TTL from 1 to 86400 seconds."
          >
            <Input
              id={`${id}-ttl`}
              value={ttl}
              onChange={(event) => setTTL(event.target.value)}
              disabled={Boolean(problem) || busy}
              inputMode="numeric"
              maxLength={5}
              aria-invalid={Boolean(errors.ttl)}
              aria-describedby={errors.ttl ? `${id}-ttl-error` : undefined}
            />
          </Field>
        )}
      </div>
      {held && <p className="text-body text-muted-foreground">{held}</p>}
      <div>
        <Button type="submit" disabled={Boolean(held) || busy} pending={busy}>
          Create retained review
        </Button>
      </div>
    </form>
  )
}

function ClientGroupsForm({ view, problem, busy, onStage }: Props) {
  const id = useId()
  const [address, setAddress] = useState("")
  const [groups, setGroups] = useState<number[]>([])
  const [errors, setErrors] = useState<{ address?: string; groups?: string }>({})
  const clients =
    view.snapshot?.clients.flatMap((client) =>
      client.addresses.filter(dnsClientAddress).map((address) => ({ address, client })),
    ) ?? []
  const nativeGroups = view.snapshot?.filterGroups.filter((group) => group.id !== undefined) ?? []
  const fields = {
    address: { label: "Existing Pi-hole client", id: `${id}-address` },
    groups: { label: "Native groups", id: `${id}-groups` },
  }
  return (
    <form
      className="flex min-w-0 flex-col gap-4"
      aria-label="Review native client groups"
      aria-busy={busy}
      onSubmit={(event) => {
        event.preventDefault()
        if (problem || busy) return
        const result = prepareDNSClientGroups(view.connection.engine, address, groups)
        if (address && !clients.some((item) => item.address === address))
          result.errors.address = "Select a client still present in the current native reading."
        if (groups.some((id) => !nativeGroups.some((group) => group.id === id)))
          result.errors.groups = "Choose groups still present in the current native reading."
        setErrors(result.errors)
        if (result.request && !Object.values(result.errors).some(Boolean))
          void onStage(result.request)
      }}
    >
      <FormErrors errors={errors} fields={fields} />
      <Field
        label="Existing Pi-hole client"
        htmlFor={`${id}-address`}
        error={errors.address && <span id={`${id}-address-error`}>{errors.address}</span>}
      >
        <Select
          value={address}
          disabled={Boolean(problem) || busy}
          onValueChange={(next) => {
            setAddress(next)
            setGroups([...(clients.find((item) => item.address === next)?.client.groups ?? [])])
            setErrors({})
          }}
        >
          <SelectTrigger
            id={`${id}-address`}
            aria-invalid={Boolean(errors.address)}
            aria-describedby={errors.address ? `${id}-address-error` : undefined}
          >
            <SelectValue placeholder="Select an existing literal client" />
          </SelectTrigger>
          <SelectContent>
            {clients.map((item, index) => (
              <SelectItem key={`${index}-${item.address}`} value={item.address}>
                {item.address}
                {item.client.name ? ` · ${item.client.name}` : ""}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <fieldset
        id={`${id}-groups`}
        tabIndex={-1}
        className="min-w-0 space-y-2 focus-ring"
        aria-describedby={errors.groups ? `${id}-groups-error` : undefined}
      >
        <legend className="text-body font-medium">Native groups</legend>
        {nativeGroups.map((group) => (
          <label
            key={group.id}
            htmlFor={`${id}-group-${group.id}`}
            className="flex min-h-11 min-w-0 cursor-pointer items-center gap-3 rounded-md py-2 text-body"
          >
            <Checkbox
              id={`${id}-group-${group.id}`}
              checked={groups.includes(group.id!)}
              disabled={Boolean(problem) || busy || !address}
              onCheckedChange={(checked) =>
                setGroups((prior) =>
                  checked
                    ? prior.includes(group.id!)
                      ? prior
                      : [...prior, group.id!]
                    : prior.filter((value) => value !== group.id),
                )
              }
            />
            <span className="min-w-0 break-words">
              {group.name} · ID {group.id}
              <span className="block text-hint text-muted-foreground">
                {group.enabled === undefined
                  ? "Native enabled state unspecified"
                  : group.enabled
                    ? "Enabled"
                    : "Disabled"}
              </span>
            </span>
          </label>
        ))}
        {!nativeGroups.length && (
          <p className="text-body text-muted-foreground">
            No existing native groups in this reading.
          </p>
        )}
        {errors.groups && (
          <p id={`${id}-groups-error`} className="text-body text-destructive">
            {errors.groups}
          </p>
        )}
      </fieldset>
      <p className="text-hint text-muted-foreground">
        Selected group IDs: {groups.join(", ") || "None"}. An empty selection explicitly removes all
        group memberships for this existing client.
      </p>
      {problem && <p className="text-body text-muted-foreground">{problem}</p>}
      <div>
        <Button type="submit" disabled={Boolean(problem) || busy} pending={busy}>
          Create retained review
        </Button>
      </div>
    </form>
  )
}
