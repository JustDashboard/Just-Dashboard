"use client"

import { useState } from "react"
import { errorMessage, get, put } from "@/lib/api"
import { duration } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DNSAddress, DNSReport, ResolverView } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { RowList, Row } from "@/components/row-list"
import { ErrorState, LoadingRows } from "@/components/state"
import { Status, type Verdict } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import type { Tone } from "@/components/tone"
import { Button } from "@/components/ui/button"
import { InputGroup, InputGroupInput } from "@/components/ui/input-group"
import { Disclosure, Field } from "@/components/form"

/**
 * The DNS behind a scan: each address with its TTL and whose it is, the alias
 * chain, the CAA verdict for Let's Encrypt, the ACME challenge records and
 * whether the public resolvers agree with this server's. It is asked for
 * only when opened — a lookup is a few dozen packets to four resolvers —
 * and a scan that never reached the host opens it, since the name is then
 * the first suspect.
 */
export function TLSDNSPanel({ domain, openAtFirst }: { domain: string; openAtFirst: boolean }) {
  const [open, setOpen] = useState(openAtFirst)
  const report = usePoll(
    (signal) => get<DNSReport>("/certificates/dns", { domain, deep: 1 }, signal),
    0,
    [domain],
    { enabled: open },
  )
  const dns = report.data

  return (
    <Disclosure
      summary="DNS, CAA and propagation"
      facts="Records with their TTLs, whether Let's Encrypt may issue, and what public resolvers answer"
      open={open}
      onOpenChange={setOpen}
    >
      {report.loading && !dns && <LoadingRows rows={4} />}
      {report.error && <ErrorState error={report.error} onRetry={report.refresh} />}
      {dns && (
        <div className="min-w-0 space-y-6">
          <div className="flex min-w-0 flex-wrap items-start justify-between gap-3">
            <div className="min-w-0 space-y-1">
              <Status {...summaryVerdict(dns)} />
              <p className="text-body break-words">{dns.error ?? dns.summary}</p>
              {dns.source && (
                <p className="text-hint break-words text-muted-foreground">
                  Records as {dns.source} gives them.
                </p>
              )}
            </div>
            <Button
              type="button"
              variant="outline"
              size="xs"
              onClick={report.refresh}
              disabled={report.loading}
              pending={report.loading}
            >
              Look up again
            </Button>
          </div>

          {dns.source && (
            <>
              <section className="space-y-2">
                <h3 className="text-body font-medium">Addresses</h3>
                {dns.addresses.length === 0 && dns.cnameChain.length === 0 ? (
                  <p className="text-hint text-muted-foreground">
                    No A or AAAA record
                    {dns.rcode && dns.rcode !== "NOERROR" ? ` (${dns.rcode})` : ""}.
                  </p>
                ) : (
                  <RowList>
                    {dns.cnameChain.map((alias) => (
                      <Row
                        key={`cname-${alias.name}`}
                        title={
                          <span className="font-mono text-xs wrap-anywhere">{alias.name}</span>
                        }
                        subtitle={`CNAME to ${alias.value} · TTL ${duration(alias.ttl)}`}
                        mono
                        className="py-2"
                      />
                    ))}
                    {dns.addresses.map((address) => (
                      <Row
                        key={`${address.type}-${address.address}`}
                        title={<span className="font-mono text-xs">{address.address}</span>}
                        subtitle={`${address.type} · TTL ${duration(address.ttl)}`}
                        trailing={
                          <Tag tone={OWNER[address.owner].tone}>{OWNER[address.owner].label}</Tag>
                        }
                        className="py-2"
                      />
                    ))}
                  </RowList>
                )}
              </section>

              <section className="space-y-2">
                <h3 className="text-body font-medium">CAA</h3>
                <Status
                  verdict={dns.caa.error ? "critical" : dns.caa.letsEncrypt ? "ok" : "critical"}
                  label={
                    dns.caa.error
                      ? "lookup failed"
                      : dns.caa.letsEncrypt
                        ? "Let's Encrypt may issue"
                        : dns.caa.issuers.length
                          ? `only ${dns.caa.issuers.join(", ")}`
                          : "no CA may issue"
                  }
                />
                <p className="text-hint break-words text-muted-foreground">{dns.caa.verdict}</p>
                {dns.caa.records.length > 0 && (
                  <RowList>
                    {dns.caa.records.map((record, index) => (
                      <Row
                        key={`${record.tag}-${record.value}-${index}`}
                        title={
                          <span className="font-mono text-xs wrap-anywhere">
                            {record.critical ? 128 : 0} {record.tag} &quot;{record.value}&quot;
                          </span>
                        }
                        subtitle={`${record.name} · TTL ${duration(record.ttl)}`}
                        trailing={record.critical ? <Tag tone="warning">critical</Tag> : undefined}
                        className="py-2"
                      />
                    ))}
                  </RowList>
                )}
                {dns.caa.checked.length > 0 && (
                  <p className="text-hint break-words text-muted-foreground">
                    Asked, from the name up: {dns.caa.checked.join(", ")}.
                  </p>
                )}
              </section>

              <section className="space-y-2">
                <h3 className="font-mono text-xs font-medium">_acme-challenge.{dns.domain}</h3>
                {dns.acmeError ? (
                  <p className="text-hint break-words text-muted-foreground">
                    The lookup failed: {dns.acmeError}
                  </p>
                ) : dns.acmeChallenge.length === 0 ? (
                  <p className="text-hint text-muted-foreground">
                    No record. That is normal: certbot adds a TXT record here only for the length of
                    a DNS challenge.
                  </p>
                ) : (
                  <RowList>
                    {dns.acmeChallenge.map((record, index) => (
                      <Row
                        key={`${record.type}-${record.value}-${index}`}
                        title={
                          <span className="font-mono text-xs wrap-anywhere">{record.value}</span>
                        }
                        subtitle={`${record.type} on ${record.name} · TTL ${duration(record.ttl)}`}
                        className="py-2"
                      />
                    ))}
                  </RowList>
                )}
              </section>
            </>
          )}

          <section className="space-y-2">
            <h3 className="text-body font-medium">Propagation</h3>
            <RowList>
              {dns.propagation.map((view) => (
                <Row
                  key={`${view.label}-${view.resolver}`}
                  title={view.label}
                  subtitle={
                    view.error
                      ? view.error
                      : [
                          view.resolver,
                          view.addresses.length
                            ? view.addresses.join(", ")
                            : `no address${view.rcode && view.rcode !== "NOERROR" ? ` (${view.rcode})` : ""}`,
                        ].join(" · ")
                  }
                  mono={!view.error}
                  trailing={<Status {...propagationVerdict(view)} />}
                  className="py-2"
                />
              ))}
            </RowList>
            <p className="text-hint text-muted-foreground">
              {dns.consistent
                ? "Every resolver that answered gives the same addresses."
                : "The resolvers disagree. A record changed recently is still cached somewhere; each resolver keeps the old answer until its TTL runs out."}
            </p>
            <ResolverSetting saved={dns.resolvers} onSaved={report.refresh} />
          </section>
        </div>
      )}
    </Disclosure>
  )
}

const OWNER: Record<DNSAddress["owner"], { label: string; tone: Tone }> = {
  "this-server": { label: "this server", tone: "success" },
  cloudflare: { label: "Cloudflare", tone: "default" },
  other: { label: "other", tone: "warning" },
  unknown: { label: "cannot tell", tone: "default" },
}

function summaryVerdict(dns: DNSReport): { verdict: Verdict; label: string } {
  if (!dns.source || dns.rcode === "NXDOMAIN") return { verdict: "critical", label: "not resolved" }
  if (dns.pointsHere) return { verdict: "ok", label: "points here" }
  if (dns.behindProxy) return { verdict: "notice", label: "behind Cloudflare" }
  if (dns.addresses.length === 0) return { verdict: "critical", label: "no address" }
  if (!dns.hostAddressesKnown) return { verdict: "notice", label: "cannot tell" }
  return { verdict: "warning", label: "points elsewhere" }
}

function propagationVerdict(view: ResolverView): { verdict: Verdict; label: string } {
  if (view.error) return { verdict: "notice", label: "no answer" }
  return view.agrees ? { verdict: "ok", label: "agrees" } : { verdict: "warning", label: "differs" }
}

/** The public resolvers the table compares with, kept as a setting. */
function ResolverSetting({ saved, onSaved }: { saved: string[]; onSaved: () => void }) {
  const [value, setValue] = useState(saved.join(", "))
  const [error, setError] = useState<string>()
  const [saving, setSaving] = useState(false)
  const save = async () => {
    setSaving(true)
    setError(undefined)
    try {
      const resolvers = value.split(/[\s,]+/).filter(Boolean)
      const result = await put<{ resolvers: string[] }>("/certificates/dns/resolvers", {
        resolvers,
      })
      setValue(result.resolvers.join(", "))
      notify.success("Resolvers saved")
      onSaved()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }
  return (
    <Disclosure quiet summary="Resolvers compared" facts={saved.join(", ")}>
      <form
        onSubmit={(event) => {
          event.preventDefault()
          if (!saving) void save()
        }}
      >
        <Field
          label="Resolver addresses"
          htmlFor="tls-dns-resolvers"
          hint="IP addresses, separated by commas. Leave it empty for 1.1.1.1, 8.8.8.8 and 9.9.9.9."
          error={error}
        >
          <div className="flex min-w-0 items-center gap-2">
            <InputGroup className="w-full sm:w-96">
              <InputGroupInput
                id="tls-dns-resolvers"
                value={value}
                onChange={(event) => {
                  setValue(event.target.value)
                  setError(undefined)
                }}
                aria-invalid={error ? true : undefined}
                autoComplete="off"
                spellCheck={false}
                className="font-mono sm:text-xs"
              />
            </InputGroup>
            <Button type="submit" size="sm" disabled={saving} pending={saving}>
              Save
            </Button>
          </div>
        </Field>
      </form>
    </Disclosure>
  )
}
