"use client"

import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import {
  DNS_SERVICE_BASE,
  type DNSConnection,
  type DNSNativeReading,
} from "@/lib/network-dns-services"
import {
  dnsFilterKindName,
  readDNSFilterView,
  type DNSFilterSection,
} from "@/lib/network-dns-filters"
import { ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"

function Reading({ label, value }: { label: string; value: DNSNativeReading }) {
  return (
    <div className="min-w-0 space-y-1 text-body">
      <p className="font-medium">
        {label} · {value.state}
      </p>
      <p className="text-hint break-words text-muted-foreground">Basis: {value.basis}</p>
      <p className="break-words text-muted-foreground">{value.summary}</p>
    </div>
  )
}

function FilterSection({ title, value }: { title: string; value: DNSFilterSection }) {
  const configured = value.evidence.state === "configured"
  return (
    <section className="min-w-0 space-y-4" aria-label={title}>
      <div className="border-panel-border flex min-w-0 items-baseline justify-between gap-3 border-b pb-3">
        <h4 className="text-title font-semibold">{title}</h4>
        {configured && (
          <span
            className="numeric text-2xl font-semibold"
            aria-label={`${value.entries.length} ${title.toLowerCase()} entries`}
          >
            {value.entries.length}
          </span>
        )}
      </div>
      <Reading label="Configured inventory" value={value.evidence} />
      <Reading label="Entry identity" value={value.identity} />
      {value.entries.length > 0 ? (
        <ul className="divide-panel-border min-w-0 divide-y">
          {value.entries.map((entry, index) => (
            <li key={`${index}-${entry.fingerprint}`} className="min-w-0 space-y-2 py-3 text-body">
              <p className="font-medium break-words">
                {dnsFilterKindName(entry.kind)}
                {entry.id !== undefined && ` · native ID ${entry.id}`}
                {entry.enabled !== undefined && (entry.enabled ? " · Enabled" : " · Disabled")}
              </p>
              {entry.origin && <p className="font-mono break-all">Origin: {entry.origin}</p>}
              {!entry.origin && (
                <p className="text-muted-foreground">Content or destination redacted.</p>
              )}
              {entry.groups !== undefined && (
                <p>Native group IDs: {entry.groups.join(", ") || "None assigned"}</p>
              )}
              {entry.ruleCount !== undefined && (
                <p className="numeric">Native rule count: {entry.ruleCount}</p>
              )}
              {entry.updatedAt !== undefined && (
                <p className="break-all">Native update time: {entry.updatedAt}</p>
              )}
              {entry.status !== undefined && <p>Native list status code: {entry.status}</p>}
              <p className="font-mono text-hint break-all text-muted-foreground">
                Entry fingerprint: {entry.fingerprint}
              </p>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-body text-muted-foreground">
          {configured
            ? "No entries in this configured native inventory."
            : "No readable entry inventory; this does not establish an empty policy."}
        </p>
      )}
      {value.fingerprint && (
        <p className="font-mono text-hint break-all text-muted-foreground">
          Inventory fingerprint: {value.fingerprint}
        </p>
      )}
    </section>
  )
}

/** Reading register: counts belong to their inventory headers; flat rows retain
 * redacted native identities and configured/runtime evidence beside the data. */
export function DNSServiceFilters({
  connection,
  ownerStale,
}: {
  connection: DNSConnection
  ownerStale: boolean
}) {
  const filter = usePoll(
    async (signal) => {
      const next = readDNSFilterView(
        await get(
          `${DNS_SERVICE_BASE}/${encodeURIComponent(connection.id)}/filters`,
          undefined,
          signal,
        ),
        connection,
      )
      if (!next.inventory)
        throw new Error(
          next.error ||
            `Native filter inventory is ${next.state}. No authenticated policy inventory is available.`,
        )
      return next
    },
    30000,
    [connection.id, connection.generation, connection.updatedAt],
    { enabled: !ownerStale },
  )
  const inventory = filter.data?.inventory
  return (
    <section className="min-w-0 space-y-6" aria-label="Native DNS filter inventory">
      <div className="border-panel-border flex min-w-0 flex-wrap items-baseline justify-between gap-3 border-b pb-3">
        <h3 className="text-base font-semibold">Native filter inventory</h3>
        <Button variant="outline" onClick={filter.refresh} disabled={ownerStale}>
          Read native filters
        </Button>
      </div>
      {filter.error && (
        <ErrorState error={filter.error} onRetry={ownerStale ? undefined : filter.refresh} />
      )}
      {(ownerStale || (filter.error && filter.data)) && (
        <div role="status">
          <Notice
            tone="warning"
            title={filter.data ? "Last filter reading retained" : "Filter reading unavailable"}
          >
            {filter.data && (
              <p>
                The current filter reading is unavailable. Retained entries keep their original
                observation time.
              </p>
            )}
            {ownerStale && <p>Read the current engine before retrying its filters.</p>}
          </Notice>
        </div>
      )}
      {!filter.data ? (
        !filter.error && !ownerStale && <LoadingPanel plain rows={2} />
      ) : !inventory ? (
        <div role="status">
          <Notice tone="warning" title={`Native filters · ${filter.data.state}`}>
            {filter.data.error || "No authenticated filter inventory is available for this owner."}
          </Notice>
        </div>
      ) : (
        <div className="min-w-0 space-y-6">
          <p className="text-hint text-muted-foreground">
            Native version {inventory.nativeVersion} · {filter.data.state} · observed{" "}
            <time dateTime={inventory.observedAt}>
              {new Date(inventory.observedAt).toLocaleString()}
            </time>
          </p>
          {filter.data.state === "partial" && (
            <div role="status">
              <Notice tone="warning" title="Filter inventory is partial">
                Some native policy could not be read. Empty unreadable sections do not establish an
                empty policy.
              </Notice>
            </div>
          )}
          <div className="grid min-w-0 gap-4 sm:grid-cols-2">
            <Reading label="Filter transport" value={inventory.transport} />
            <Reading label="Filter runtime" value={inventory.runtime} />
          </div>
          {(inventory.protection !== undefined || inventory.filtering !== undefined) && (
            <div className="space-y-1 text-body">
              {inventory.protection !== undefined && (
                <p>Native protection: {inventory.protection ? "Enabled" : "Disabled"}</p>
              )}
              {inventory.filtering !== undefined && (
                <p>Configured filtering: {inventory.filtering ? "Enabled" : "Disabled"}</p>
              )}
            </div>
          )}
          <FilterSection title="Filter subscriptions" value={inventory.sources} />
          <FilterSection title="Custom filter rules" value={inventory.rules} />
          <Reading label="Installed app rule content" value={inventory.appRules} />
          {inventory.limitations.length > 0 && (
            <ul className="space-y-1 text-body text-muted-foreground">
              {inventory.limitations.map((limit, index) => (
                <li key={index} className="break-words">
                  {limit}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </section>
  )
}
