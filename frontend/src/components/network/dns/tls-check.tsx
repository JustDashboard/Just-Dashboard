"use client"

import { useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { post } from "@/lib/api"
import type { DNSTLSCheck, DNSTLSReport } from "@/lib/types"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"

const STATE: Record<
  DNSTLSCheck["state"],
  { label: string; tone: "running" | "danger" | "warning" }
> = {
  trusted: { label: "Trusted", tone: "running" },
  untrusted: { label: "Untrusted", tone: "danger" },
  "no-answer": { label: "No DNS answer", tone: "warning" },
  unreachable: { label: "Unreachable", tone: "warning" },
}

/**
 * The certificate each configured DNS-over-TLS server presents, checked by
 * the dashboard itself: a TLS session to the server's DoT port, verified
 * against this host's trust store for the name written after `#`, and one
 * question over it for the root's NS records, which carries nothing private.
 * The DNS over TLS setting says what resolved insists on; this says what the
 * servers would show it. A server without a name is listed with why it cannot
 * be checked.
 */
export function TLSCheck() {
  const { can } = useAuth()
  const [report, setReport] = useState<DNSTLSReport>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const check = async () => {
    setBusy(true)
    setError(undefined)
    try {
      setReport(await post<DNSTLSReport>("/network/dns/tls-check", {}))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <Button
          variant="outline"
          onClick={() => void check()}
          pending={busy}
          disabled={!can("read")}
        >
          Check certificates
        </Button>
        {report && (
          <span className="numeric text-hint text-muted-foreground">
            {report.checks.filter((c) => c.state === "trusted").length} of {report.checks.length}{" "}
            trusted · checked {new Date(report.checkedAt).toLocaleTimeString()}
          </span>
        )}
      </div>
      {error && (
        <p role="alert" className="text-body text-destructive">
          {error}
        </p>
      )}
      {report && (
        <section aria-label="Certificate checks" className="flex min-w-0 flex-col gap-3">
          {report.checks.length === 0 ? (
            <EmptyNote>No configured server names a TLS identity to check.</EmptyNote>
          ) : (
            <RowList>
              {report.checks.map((c) => (
                <Row
                  key={c.server}
                  title={
                    <>
                      <span className="font-mono">{c.server}</span>
                      <span className="font-normal text-muted-foreground"> · {c.scope}</span>
                    </>
                  }
                  subtitle={
                    c.error ??
                    [
                      c.version,
                      c.issuer && `issued by ${c.issuer}`,
                      c.notAfter && `valid until ${new Date(c.notAfter).toLocaleDateString()}`,
                      `${Math.round(c.latencyMs)} ms`,
                    ]
                      .filter(Boolean)
                      .join(" · ")
                  }
                  trailing={<Status tone={STATE[c.state].tone} label={STATE[c.state].label} />}
                >
                  {c.fingerprint && (
                    <span className="sr-only">SHA-256 fingerprint {c.fingerprint}</span>
                  )}
                </Row>
              ))}
            </RowList>
          )}
          {report.omitted.map((o) => (
            <p key={o.server} className="text-hint break-words text-muted-foreground">
              <span className="font-mono">{o.server}</span> ({o.label}): not checked — {o.reason}
            </p>
          ))}
          {report.limitations.map((line) => (
            <p key={line} className="text-hint text-muted-foreground">
              {line}
            </p>
          ))}
        </section>
      )}
    </div>
  )
}
