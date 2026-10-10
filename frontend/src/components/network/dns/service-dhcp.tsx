"use client"

import { useState } from "react"
import { get } from "@/lib/api"
import { readDNSDHCPView, type DNSDHCPView } from "@/lib/network-dns-dhcp"
import { DNS_SERVICE_BASE, type DNSConnection } from "@/lib/network-dns-services"
import { plural } from "@/lib/format"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"

/**
 * The engine's DHCP server, read when asked: whether it is on, the ranges or
 * scopes it hands addresses from, and its own lease table — the names its
 * query log shows. Read-only connections read it too; nothing here changes a
 * DHCP server, and an unreadable table is said, not drawn as an empty one.
 */
export function DNSServiceDHCP({
  connection,
  ownerStale,
}: {
  connection: DNSConnection
  ownerStale: boolean
}) {
  const [view, setView] = useState<DNSDHCPView>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const read = async () => {
    setBusy(true)
    setError(undefined)
    try {
      setView(
        readDNSDHCPView(
          await get(`${DNS_SERVICE_BASE}/${encodeURIComponent(connection.id)}/dhcp`),
          connection,
        ),
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  const inventory = view?.inventory
  return (
    <section className="min-w-0 space-y-4" aria-label="Native DHCP server">
      <div className="border-panel-border flex min-w-0 flex-wrap items-baseline justify-between gap-3 border-b pb-3">
        <h3 className="text-base font-semibold">Native DHCP</h3>
        <Button variant="outline" onClick={() => void read()} pending={busy} disabled={ownerStale}>
          Read native DHCP
        </Button>
      </div>
      {error && (
        <p role="alert" className="text-body text-destructive">
          {error}
        </p>
      )}
      {view && !inventory && (
        <div role="status">
          <Notice tone="warning" title={`Native DHCP · ${view.state}`}>
            {view.error ?? "No authenticated DHCP inventory is available for this engine."}
          </Notice>
        </div>
      )}
      {inventory && (
        <div className="min-w-0 space-y-4">
          <div className="flex min-w-0 flex-wrap items-center gap-3">
            {inventory.enabled !== undefined && (
              <Status
                tone={inventory.enabled ? "running" : "stopped"}
                label={inventory.enabled ? "DHCP server enabled" : "DHCP server disabled"}
              />
            )}
            <span className="text-hint text-muted-foreground">
              Native version {inventory.nativeVersion} · {view.state} · observed{" "}
              <time dateTime={inventory.observedAt}>
                {new Date(inventory.observedAt).toLocaleString()}
              </time>
            </span>
          </div>
          <p className="text-body text-muted-foreground">{inventory.configuration.summary}</p>
          {inventory.interface && (
            <p className="text-body">
              Interface <span className="font-mono">{inventory.interface}</span>
            </p>
          )}
          {inventory.ranges.length > 0 && (
            <ul aria-label="DHCP ranges" className="space-y-1 text-body">
              {inventory.ranges.map((range, index) => (
                <li key={`${range.name ?? ""}:${index}`} className="font-mono break-all">
                  {range.name && <span className="font-sans">{range.name}: </span>}
                  {range.start || "unset"} – {range.end || "unset"}
                  {range.enabled !== undefined && (
                    <span className="font-sans text-muted-foreground">
                      {range.enabled ? " · enabled" : " · disabled"}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          )}
          <div className="space-y-2">
            <p className="text-body font-medium">
              {inventory.leaseEvidence.state === "reported"
                ? plural(inventory.leases.length, "lease")
                : "Lease table unavailable"}
            </p>
            <p className="text-hint text-muted-foreground">{inventory.leaseEvidence.summary}</p>
            {inventory.leases.length > 0 && (
              <ul aria-label="DHCP leases" className="divide-panel-border min-w-0 divide-y">
                {inventory.leases.map((lease) => (
                  <li
                    key={`${lease.address}:${lease.hardware ?? ""}`}
                    className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-3 gap-y-1 py-2 text-body"
                  >
                    <span className="min-w-0 font-mono break-all">
                      {lease.address}
                      {lease.hostname && <span className="font-sans"> · {lease.hostname}</span>}
                    </span>
                    <span className="flex items-center gap-2 text-hint text-muted-foreground">
                      {lease.hardware && <span className="font-mono">{lease.hardware}</span>}
                      {lease.static ? (
                        <Tag>static</Tag>
                      ) : lease.expires ? (
                        `until ${new Date(lease.expires).toLocaleString()}`
                      ) : (
                        "no expiry"
                      )}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>
          <ul className="space-y-1 text-hint text-muted-foreground">
            {inventory.limitations.map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
        </div>
      )}
    </section>
  )
}
