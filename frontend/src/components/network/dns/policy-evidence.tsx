"use client"

import { useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { del, get, post } from "@/lib/api"
import { dnsEvidenceLabel, type SavedDNSEvidence } from "@/lib/network-dns-evidence"
import { Field } from "@/components/form"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

const recordTypes = ["A", "AAAA", "CNAME", "MX", "TXT", "NS", "PTR", "SRV"]

/** A reading report: the query form obtains evidence, then flat rows separate
 * native measurements from configuration and unknown application scope. */
export function DNSPolicyEvidence() {
  const { can } = useAuth()
  const allowed = can("system.admin")
  const records = usePoll<SavedDNSEvidence[]>(
    (signal) => get("/network/dns/evidence", undefined, signal),
    15000,
    [allowed],
    { enabled: allowed },
  )
  const [name, setName] = useState("")
  const [type, setType] = useState("A")
  const [expectedInterface, setExpectedInterface] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [selected, setSelected] = useState<SavedDNSEvidence>()
  const launch = async () => {
    setBusy(true)
    setError(undefined)
    try {
      setSelected(
        await post<SavedDNSEvidence>("/network/dns/evidence", {
          name: name.trim(),
          type,
          ...(expectedInterface.trim() ? { expectedInterface: expectedInterface.trim() } : {}),
        }),
      )
      records.refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      records.refresh()
    } finally {
      setBusy(false)
    }
  }
  const open = async (id: string) => {
    setBusy(true)
    setError(undefined)
    try {
      setSelected(await get<SavedDNSEvidence>(`/network/dns/evidence/${id}`))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  const remove = async () => {
    if (!selected) return
    setBusy(true)
    setError(undefined)
    try {
      await del(`/network/dns/evidence/${selected.id}`)
      setSelected(undefined)
      records.refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  const report = selected?.result
  return (
    <div className="flex min-w-0 flex-col gap-6">
      <p className="text-body text-muted-foreground">
        Ask the identified native resolver for a fresh DNS record under its split-DNS, TLS and
        DNSSEC policy. Private names stay within that policy; failure does not try another resolver.
        Reports retain this host scope and timestamps for seven days, up to 128 finished reports.
      </p>
      {!allowed ? (
        <p className="text-body text-muted-foreground">
          Policy investigations and saved private answers require system administration access.
        </p>
      ) : (
        <>
          <form
            className="grid min-w-0 items-end gap-3 sm:grid-cols-[minmax(0,1fr)_8rem_minmax(0,1fr)_auto]"
            onSubmit={(event) => {
              event.preventDefault()
              if (name.trim() && !busy) void launch()
            }}
          >
            <Field label="Investigation name" htmlFor="dns-evidence-name">
              <Input
                id="dns-evidence-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder="secret.corp.example"
                spellCheck={false}
                autoComplete="off"
                className="font-mono"
              />
            </Field>
            <Field label="Record type" htmlFor="dns-evidence-type">
              <Select
                value={type}
                onValueChange={(value) => {
                  if (recordTypes.includes(value)) setType(value)
                }}
              >
                <SelectTrigger id="dns-evidence-type">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {recordTypes.map((value) => (
                    <SelectItem key={value} value={value}>
                      {value}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field label="Expected policy link (optional)" htmlFor="dns-evidence-link">
              <Input
                id="dns-evidence-link"
                value={expectedInterface}
                onChange={(event) => setExpectedInterface(event.target.value)}
                placeholder="Native automatic selection"
                spellCheck={false}
                autoComplete="off"
              />
            </Field>
            <Button type="submit" disabled={!name.trim() || busy} pending={busy}>
              Investigate policy
            </Button>
          </form>
          <p className="text-hint text-muted-foreground">
            An expected link must already be a best-match native policy scope. A/AAAA selects the
            answer family; it does not force the resolver upstream family or source address.
          </p>
          {error && (
            <div role="alert">
              <Notice tone="warning" title="Investigation unavailable">
                <p>{error}</p>
                <p>
                  {selected && "The last selected report remains below. "}No automatic retry was
                  requested.
                </p>
              </Notice>
            </div>
          )}
          <NetworkReadWarning
            error={records.error}
            refresh={records.refresh}
            lastSuccess={records.lastSuccess}
            reading="DNS investigation history"
          />
          <div className="flex flex-wrap gap-2" aria-label="Saved DNS investigations">
            {(records.data ?? []).map((record) => (
              <Button
                key={record.id}
                variant="ghost"
                disabled={busy}
                onClick={() => void open(record.id)}
              >
                {record.request.name} · {record.request.type} · {record.status}
              </Button>
            ))}
            {records.data?.length === 0 && (
              <p className="text-body text-muted-foreground">No retained policy investigations.</p>
            )}
          </div>
        </>
      )}
      {allowed && selected && (
        <div className="flex min-w-0 flex-col gap-4" aria-label="DNS policy evidence report">
          <div className="border-panel-border flex flex-wrap items-center justify-between gap-3 border-b pb-3">
            <div className="min-w-0">
              <p className="font-mono text-body break-all">
                {selected.request.name} · {selected.request.type}
              </p>
              <p className="text-hint text-muted-foreground">
                {selected.status} ·{" "}
                <time dateTime={selected.startedAt}>
                  {new Date(selected.startedAt).toLocaleString()}
                </time>
                {selected.endedAt && (
                  <>
                    {" "}
                    →{" "}
                    <time dateTime={selected.endedAt}>
                      {new Date(selected.endedAt).toLocaleString()}
                    </time>
                  </>
                )}
              </p>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" asChild>
                <a href={`/api/v1/network/dns/evidence/${selected.id}/export`} download>
                  Export evidence
                </a>
              </Button>
              <Button
                variant="ghost"
                disabled={busy || selected.status === "running" || !can("destructive")}
                onClick={() => void remove()}
              >
                Delete saved report
              </Button>
            </div>
          </div>
          {report ? (
            <>
              {report.error && (
                <div role="alert">
                  <Notice tone="warning" title="Native query unavailable">
                    {report.error}
                  </Notice>
                </div>
              )}
              <p className="text-body">
                {report.owner} · {report.ownerVersion ?? "Version unknown"} · {report.vantage} ·
                answer family {report.answerFamily} · upstream family {report.upstreamFamily}
              </p>
              <dl className="divide-panel-border divide-y">
                {(["route", "transport", "trust", "dnssec", "nss"] as const).map((key) => (
                  <div
                    key={key}
                    className="grid min-w-0 gap-1 py-3 sm:grid-cols-[10rem_minmax(0,1fr)]"
                  >
                    <dt className="text-body font-medium">
                      {
                        {
                          route: "DNS route",
                          transport: "Transport",
                          trust: "TLS trust",
                          dnssec: "DNSSEC",
                          nss: "Wire and NSS",
                        }[key]
                      }
                    </dt>
                    <dd className="min-w-0 text-body">
                      <p>
                        {dnsEvidenceLabel(report[key])}{" "}
                        <span className="text-hint text-muted-foreground">
                          · {report[key].basis}
                        </span>
                      </p>
                      <p className="text-muted-foreground">{report[key].summary}</p>
                    </dd>
                  </div>
                ))}
              </dl>
              <div className="min-w-0 text-body">
                <p className="font-medium">Answers</p>
                <p className="font-mono break-all">
                  {report.answers.join(" · ") || "No accepted answer"}
                </p>
                <p className="text-muted-foreground">
                  Answering link indexes: {report.answerInterfaces.join(", ") || "Unmeasured"}
                </p>
              </div>
              <div className="min-w-0 text-body">
                <p className="font-medium">Policy snapshot · {report.policyMatch}</p>
                {report.policy.map((scope) => (
                  <div key={scope.index} className="border-panel-border min-w-0 border-b py-3">
                    <p>
                      {scope.interface} · index {scope.index} · configured DoT{" "}
                      {scope.dnsOverTLS || "inherited/unknown"} · configured DNSSEC{" "}
                      {scope.dnssec || "inherited/unknown"}
                    </p>
                    <p className="text-muted-foreground">
                      Native DNS scope: {scope.activeDNS ? "active" : "unavailable"}
                    </p>
                    <p className="font-mono break-all">
                      {scope.domains.join(", ") || "Default route"} → {scope.servers.join(", ")}
                    </p>
                    <p className="break-all text-muted-foreground">
                      Current server observation: {scope.currentServer || "Unavailable"}; not a
                      per-query endpoint.
                    </p>
                    {scope.negativeTrustAnchors.length > 0 && (
                      <p className="break-all text-muted-foreground">
                        Configured negative trust anchors: {scope.negativeTrustAnchors.join(", ")}
                      </p>
                    )}
                  </div>
                ))}
              </div>
              {report.hops && report.hops.length > 0 && (
                <section className="min-w-0 text-body" aria-label="Native DNS question chain">
                  <h3 className="font-medium">Question chain</h3>
                  <p className="text-muted-foreground">
                    Each target receives a fresh policy check. Accepted-record trust includes every
                    alias; failed discovery questions have no transport measurement.
                  </p>
                  <ol className="divide-panel-border divide-y">
                    {report.hops.map((hop, index) => (
                      <li
                        key={`${index}-${hop.name}-${hop.type}`}
                        className="min-w-0 space-y-2 py-3"
                      >
                        <p className="font-mono break-all">
                          {index + 1}. {hop.name} · {hop.type}
                        </p>
                        <p className="text-hint break-all text-muted-foreground">
                          Native owner {hop.ownerIdentity} ·{" "}
                          {hop.policyMatch || "Policy unavailable"}
                        </p>
                        {hop.policy.map((scope) => (
                          <p key={scope.index} className="break-all text-muted-foreground">
                            {scope.interface} · {scope.domains.join(", ") || "Default route"} · DNS{" "}
                            {scope.activeDNS && scope.servers.length ? "active" : "unavailable"} ·
                            configured DoT {scope.dnsOverTLS || "unknown"} · configured DNSSEC{" "}
                            {scope.dnssec || "unknown"}
                          </p>
                        ))}
                        {hop.aliasTarget && (
                          <p className="font-mono break-all">CNAME target → {hop.aliasTarget}</p>
                        )}
                        <p className="font-mono break-all">
                          Accepted records: {hop.answers.join(" · ") || "None"}
                        </p>
                        <dl className="grid min-w-0 gap-2 sm:grid-cols-2">
                          {(["transport", "trust", "dnssec"] as const).map((key) => (
                            <div key={key} className="min-w-0">
                              <dt className="text-hint text-muted-foreground">
                                {
                                  { transport: "Transport", trust: "TLS trust", dnssec: "DNSSEC" }[
                                    key
                                  ]
                                }
                              </dt>
                              <dd>
                                {dnsEvidenceLabel(hop[key])}
                                <span className="text-hint text-muted-foreground">
                                  {" "}
                                  · {hop[key].basis}
                                </span>
                              </dd>
                            </div>
                          ))}
                        </dl>
                        <p className="text-hint text-muted-foreground">
                          Answering link indexes: {hop.answerInterfaces.join(", ") || "Unmeasured"}
                          {" · "}
                          {hop.policyStable
                            ? "Stable before/after native policy"
                            : "Policy changed or completion snapshot unavailable"}
                        </p>
                        {hop.error && <p className="break-all text-warning">{hop.error}</p>}
                      </li>
                    ))}
                  </ol>
                </section>
              )}
              <ul className="list-disc space-y-1 pl-5 text-body text-muted-foreground">
                {report.limitations.map((limit) => (
                  <li key={limit}>{limit}</li>
                ))}
              </ul>
            </>
          ) : (
            <p className="text-body text-muted-foreground">
              No retained native result. An interrupted query is not rerun and cannot establish
              DNSSEC, encryption or connectivity.
            </p>
          )}
        </div>
      )}
    </div>
  )
}
