"use client"

import { useState } from "react"
import {
  CheckCircle,
  CrossCircle,
  Information,
  Inspect,
  ShieldOff,
  Warning,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { get } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { ScanFinding, TLSScan } from "@/lib/types"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

/**
 * What a visitor actually gets, graded.
 *
 * Everything else on this page reads files from disk, which answers a question
 * nobody has. A certificate renewed and never reloaded, a proxy still offering
 * TLS 1.0 because the config came from a 2015 blog post, a redirect to HTTPS
 * that quietly stopped working — none of those show up in a file. SSL Labs
 * answers this, takes two minutes and needs a public hostname; every panel in
 * this class leaves you to go there.
 *
 * The grade is coarse on purpose and every finding carries its reasoning: a
 * letter with no working is a number to optimise rather than a thing to fix.
 */
export function TLSReport() {
  const [domain, setDomain] = useState("")
  const [scan, setScan] = useState<TLSScan | null>(null)
  const [busy, setBusy] = useState(false)

  const run = async () => {
    setBusy(true)
    setScan(null)
    try {
      setScan(await get<TLSScan>("/certificates/scan", { domain: domain.trim() }))
    } catch (err) {
      notify.error("Could not scan", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <Panel>
        <PanelHeader title="Live TLS report" />
        <PanelToolbar>
          <Input
            value={domain}
            onChange={(e) => setDomain(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && domain.trim() && run()}
            placeholder="app.example.com"
            className="h-8 w-full text-body sm:w-72"
          />
          <Button size="sm" onClick={run} disabled={busy || !domain.trim()} pending={busy}>
            Scan
          </Button>
        </PanelToolbar>
        <PanelBody>
          {!scan && !busy && (
            <EmptyState
              icon={Inspect}
              title="Nothing scanned yet"
              description="Enter a domain this server should be serving. The scan reaches it the way a browser would, from the outside."
            />
          )}
          {busy && (
            <p className="text-body text-muted-foreground">
              Handshaking, probing each TLS version separately, and fetching the headers…
            </p>
          )}
          {scan && <ScanSummary scan={scan} />}
        </PanelBody>
      </Panel>

      {scan?.reachable && (
        <>
          <div className="grid gap-4 lg:grid-cols-2 [&>*]:min-w-0">
            <Panel>
              <PanelHeader title="Protocol versions" />
              <PanelBody className="space-y-1.5">
                {scan.protocols.map((protocol) => (
                  <div
                    key={protocol.name}
                    className="flex items-start justify-between gap-3 text-body"
                  >
                    <span className="font-mono text-xs">{protocol.name}</span>
                    <span className="flex min-w-0 flex-col items-end gap-0.5">
                      <Status
                        verdict={
                          protocol.status === "offered"
                            ? isOldProtocol(protocol.name)
                              ? "critical"
                              : "ok"
                            : "notice"
                        }
                        label={protocol.status}
                      />
                      {protocol.detail && (
                        <span className="max-w-56 text-right text-hint leading-tight text-muted-foreground">
                          {protocol.detail}
                        </span>
                      )}
                    </span>
                  </div>
                ))}
                <p className="pt-1 text-hint leading-relaxed text-muted-foreground">
                  Each version is asked for on a connection of its own, so the answer is the
                  server&rsquo;s rather than a negotiation. &ldquo;unknown&rdquo; means this
                  dashboard&rsquo;s own TLS library would not make the request — reporting that as
                  absent would be a false reassurance.
                </p>
              </PanelBody>
            </Panel>

            <Panel>
              <PanelHeader title="Certificate" />
              <PanelBody>
                <DetailList>
                  <Detail label="Subject">{scan.certificate?.name ?? "—"}</Detail>
                  <Detail label="Names">{scan.certificate?.domains.join(", ") || "—"}</Detail>
                  <Detail label="Issuer">{scan.certificate?.issuer ?? "—"}</Detail>
                  <Detail label="Valid until">
                    {scan.certificate ? timestamp(scan.certificate.notAfter) : "—"}
                    {scan.certificate && (
                      <span className="ml-2 text-muted-foreground">
                        {scan.certificate.daysLeft} days left
                      </span>
                    )}
                  </Detail>
                  <Detail label="Key">
                    {scan.keyType}
                    {scan.keyBits ? ` ${scan.keyBits} bits` : ""}
                  </Detail>
                  <Detail label="Signature">{scan.signatureAlgorithm ?? "—"}</Detail>
                  <Detail label="Negotiated">
                    {scan.negotiated} · {scan.cipherSuite}
                  </Detail>
                  <Detail label="OCSP stapled">{scan.ocspStapled ? "yes" : "no"}</Detail>
                  <Detail label="SHA-256" className="font-mono text-micro break-all">
                    {scan.fingerprint}
                  </Detail>
                </DetailList>
              </PanelBody>
            </Panel>
          </div>

          <Panel>
            <PanelHeader title="Chain as presented" />
            <PanelBody className="space-y-1.5">
              {scan.chain.map((link, i) => (
                <div
                  key={`${link.subject}-${i}`}
                  className="flex flex-wrap items-baseline gap-x-3 gap-y-1 rounded-lg border border-hairline bg-surface-sunken p-2.5"
                >
                  <span className="text-body font-medium">{link.subject}</span>
                  <span className="text-hint text-muted-foreground">issued by {link.issuer}</span>
                  <span className="ml-auto text-hint text-muted-foreground">
                    {link.keyType}
                    {link.keyBits ? ` ${link.keyBits}` : ""} · expires {relativeTime(link.notAfter)}
                  </span>
                  {link.isCa && <Tag>CA</Tag>}
                </div>
              ))}
            </PanelBody>
          </Panel>

          {scan.http && (
            <Panel>
              <PanelHeader title="HTTP behaviour" />
              <PanelBody className="space-y-2.5">
                <div className="flex flex-wrap items-center gap-2 text-body">
                  <span>Plain HTTP</span>
                  {scan.http.plainError ? (
                    <Status verdict="notice" label="refused connection" />
                  ) : scan.http.plainRedirects ? (
                    <Status verdict="ok" label="redirects to HTTPS" />
                  ) : (
                    <Status
                      verdict="critical"
                      label={`answers ${scan.http.plainStatus} without redirecting`}
                    />
                  )}
                  {scan.http.plainLocation && (
                    <code className="font-mono text-hint text-muted-foreground">
                      {scan.http.plainLocation}
                    </code>
                  )}
                </div>

                <div className="flex flex-wrap items-center gap-2 text-body">
                  <span>HSTS</span>
                  {scan.http.hsts ? (
                    <Status
                      verdict={scan.http.hsts.maxAge >= 15552000 ? "ok" : "warning"}
                      label={`max-age ${scan.http.hsts.maxAge}${
                        scan.http.hsts.includeSubDomains ? " · subdomains" : ""
                      }${scan.http.hsts.preload ? " · preload" : ""}`}
                    />
                  ) : (
                    <Status verdict="notice" label="not set" />
                  )}
                </div>

                <div className="space-y-1 pt-1">
                  {scan.http.headers.map((header) => (
                    <div key={header.name} className="flex items-start gap-2">
                      {header.present ? (
                        <CheckCircle className="mt-0.5 size-3.5 shrink-0 text-success" />
                      ) : (
                        <CrossCircle
                          className={cn(
                            "mt-0.5 size-3.5 shrink-0",
                            header.level === "important"
                              ? "text-warning"
                              : "text-muted-foreground/60",
                          )}
                        />
                      )}
                      <div className="min-w-0 flex-1">
                        <p className="font-mono text-hint">
                          {header.name}
                          {header.value && (
                            <span className="ml-2 text-muted-foreground">{header.value}</span>
                          )}
                        </p>
                        {!header.present && (
                          <p className="text-hint leading-relaxed text-muted-foreground">
                            {header.detail}
                          </p>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              </PanelBody>
            </Panel>
          )}
        </>
      )}
    </div>
  )
}

function ScanSummary({ scan }: { scan: TLSScan }) {
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <TlsGrade grade={scan.grade} />
        <div className="min-w-0">
          <p className="text-body font-medium">{scan.domain}</p>
          <p className="text-xs text-muted-foreground">{scan.summary}</p>
        </div>
        <span className="ml-auto text-hint text-muted-foreground">
          checked {relativeTime(scan.checkedAt)}
        </span>
      </div>

      {!scan.reachable && (
        <Notice tone="danger" icon={CrossCircle} title="Nothing answered">
          {scan.error}
        </Notice>
      )}

      {scan.findings.length > 0 && (
        <div className="space-y-2">
          {scan.findings.map((finding) => (
            <FindingRow key={finding.id} finding={finding} />
          ))}
        </div>
      )}
      {scan.reachable && scan.findings.length === 0 && (
        <Notice tone="success" icon={CheckCircle} title="Nothing to fix">
          Trusted chain, current protocols, and the headers that matter are in place.
        </Notice>
      )}
    </div>
  )
}

function FindingRow({ finding }: { finding: ScanFinding }) {
  return (
    <div
      className={cn(
        "flex min-w-0 gap-2.5 rounded-lg border p-2.5",
        finding.level === "critical"
          ? "border-rule-danger bg-wash-danger"
          : finding.level === "warning"
            ? "border-rule-warning bg-wash-warning"
            : "border-hairline bg-surface-sunken",
      )}
    >
      <LevelIcon
        level={finding.level}
        className={cn(
          "mt-0.5 size-4 shrink-0",
          finding.level === "critical"
            ? "text-destructive"
            : finding.level === "warning"
              ? "text-warning"
              : "text-muted-foreground",
        )}
      />
      <div className="min-w-0 flex-1 space-y-1">
        <p className="text-body font-medium">{finding.title}</p>
        <p className="text-hint leading-relaxed text-muted-foreground">{finding.detail}</p>
        {finding.advice && (
          <p className="text-hint leading-relaxed text-foreground/80">{finding.advice}</p>
        )}
      </div>
    </div>
  )
}

function LevelIcon({ level, className }: { level: string; className?: string }) {
  if (level === "critical") return <ShieldOff className={className} />
  if (level === "warning") return <Warning className={className} />
  return <Information className={className} />
}

/**
 * The overall verdict as a plate rather than a tag: it is the one thing on the
 * page that is meant to be seen from across the room, and it is a grade rather
 * than a property of a row.
 */
export function TlsGrade({ grade, className }: { grade: string; className?: string }) {
  const tone =
    grade === "A+" || grade === "A"
      ? "border-rule-success bg-wash-success text-success"
      : grade === "B"
        ? "border-rule-warning bg-wash-warning text-warning"
        : grade === "C"
          ? "border-rule-warning bg-plot-warning text-warning"
          : "border-rule-danger bg-wash-danger text-destructive"
  return (
    <span
      className={cn(
        "flex size-11 shrink-0 items-center justify-center rounded-xl border text-base font-semibold",
        tone,
        className,
      )}
    >
      {grade}
    </span>
  )
}

function isOldProtocol(name: string) {
  return name === "TLS 1.0" || name === "TLS 1.1"
}
