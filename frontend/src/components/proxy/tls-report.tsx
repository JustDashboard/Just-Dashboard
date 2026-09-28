"use client"

import { useSessionState } from "@/lib/view-state"
import { useSearchParams } from "next/navigation"
import { CheckCircle, CrossCircle, Inspect } from "@/components/icons"
import { get } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { TLSScan } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Detail, DetailList, Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, ProductLogo, issuerProduct } from "@/components/product-logo"
import { RowList } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { FindingList } from "@/components/finding-list"
import { EmptyState, ErrorState, Notice, Spinner } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import type { Tone } from "@/components/tone"
import { Button } from "@/components/ui/button"
import { Field } from "@/components/form"
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
 * The four readings are tiles and the findings are a plain list, as they are
 * on every other verdict page; the grade used to be a tinted plate, which is
 * the one decoration this system does not draw.
 */
export function TLSReportPage() {
  const { can } = useAuth()
  const params = useSearchParams()
  const initial = params.get("domain") ?? ""
  const [domain, setDomain] = useSessionState("proxy.tls.domain", "", initial || undefined)
  // The domain being reported on. A ?domain= link from a site or a
  // certificate runs the report on arrival: the link is the question, and a
  // page that then waits for a second click to ask it is a page that forgot
  // why it was opened. The scan is a one-shot poll keyed on the target, so
  // arriving with one and pressing Scan are the same path.
  const [target, setTarget] = useSessionState("proxy.tls.target", "", initial.trim() || undefined)
  const admin = can("system.admin")
  const report = usePoll(
    (signal) => get<TLSScan>("/certificates/scan", { domain: target }, signal),
    0,
    [target],
    { enabled: admin && target !== "" },
  )
  const scan = report.data ?? null
  const busy = report.loading

  const run = () => {
    const name = domain.trim()
    if (!name) return
    if (name === target) report.refresh()
    else setTarget(name)
  }

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Proxy" title="TLS report" />

      {scan?.reachable ? (
        <StatGrid columns={4} dense>
          <StatTile
            label={scan.domain}
            value={scan.grade}
            tone={gradeTone(scan.grade)}
            hint={scan.summary}
          />
          <StatTile
            label="Certificate"
            value={
              scan.certificate
                ? scan.certificate.expired
                  ? "expired"
                  : `${scan.certificate.daysLeft}d`
                : "none"
            }
            tone={
              !scan.certificate || scan.certificate.expired
                ? "danger"
                : scan.certificate.daysLeft <= 14
                  ? "warning"
                  : "default"
            }
            hint={
              scan.certificate ? (
                <span className="inline-flex max-w-full items-center gap-1.5">
                  <span className="truncate">until {timestamp(scan.certificate.notAfter)}</span>
                  <IssuerFact issuer={scan.certificate.issuer} />
                </span>
              ) : (
                "the handshake completed without one"
              )
            }
          />
          <StatTile
            label="Negotiated"
            value={scan.negotiated ?? "—"}
            hint={scan.cipherSuite}
            tone={scan.negotiated === "TLS 1.3" ? "success" : "default"}
          />
          <StatTile
            label="Chain"
            value={scan.trusted ? "trusted" : "untrusted"}
            tone={scan.trusted ? (scan.chainComplete ? "success" : "warning") : "danger"}
            hint={
              scan.chainComplete
                ? `${scan.chain.length} certificate${scan.chain.length === 1 ? "" : "s"} presented`
                : "the intermediate was not sent"
            }
          />
        </StatGrid>
      ) : (
        <StatGrid columns={4} dense>
          <StatTile label="Grade" value="—" hint="Run a live handshake" />
          <StatTile label="Certificate" value="—" hint="Validity at the endpoint" />
          <StatTile label="Negotiated" value="—" hint="Protocol and cipher" />
          <StatTile label="Chain" value="—" hint="Trust and intermediates" />
        </StatGrid>
      )}

      <div className="flex min-w-0 flex-wrap items-end justify-between gap-4 border-b border-hairline pb-5">
        <div className="flex min-w-0 items-center gap-3">
          <ProductLogo
            id={scan?.certificate ? issuerProduct(scan.certificate.issuer) : undefined}
            size="md"
            fallback={Inspect}
          />
          <div className="min-w-0">
            <h2 className="text-section font-semibold break-all">
              {scan?.domain || "Live TLS report"}
            </h2>
            {scan && (
              <p className="text-hint text-muted-foreground">
                Checked {relativeTime(scan.checkedAt)}
              </p>
            )}
          </div>
        </div>
        <form
          onSubmit={(event) => {
            event.preventDefault()
            if (admin && !busy) run()
          }}
          className="flex w-full items-end gap-2 sm:w-auto"
        >
          <Field label="Domain to scan" htmlFor="tls-domain" className="min-w-0 flex-1">
            <Input
              id="tls-domain"
              value={domain}
              onChange={(event) => setDomain(event.target.value)}
              placeholder="app.example.com"
              className="w-full sm:w-72"
            />
          </Field>
          <Button
            type="submit"
            size="sm"
            disabled={busy || !domain.trim() || !admin}
            pending={busy}
          >
            Scan
          </Button>
        </form>
      </div>
      <div>
        {!admin && (
          <Notice title="Scanning needs an administrator">
            The report reaches a domain of your choosing from this server, so it is held to the same
            account level as the network probes.
          </Notice>
        )}
        {admin && !scan && !busy && (
          <EmptyState
            icon={Inspect}
            title="Nothing scanned yet"
            description="Enter a domain this server should be serving. The scan reaches it the way a browser would, from the outside."
          />
        )}
        {busy && (
          <p className="flex items-center gap-2 text-body text-muted-foreground">
            <Spinner className="size-3.5" />
            Handshaking, probing each TLS version separately, and fetching the headers…
          </p>
        )}
        {report.error && !busy && <ErrorState error={report.error} />}
        {scan && !scan.reachable && (
          <Notice tone="danger" icon={CrossCircle} title={`Nothing answered at ${scan.domain}`}>
            {scan.error}
          </Notice>
        )}
      </div>

      {scan?.reachable && (
        <div className="flex min-w-0 animate-rise flex-col gap-6 md:gap-8">
          <div className="grid items-start gap-8 xl:grid-cols-[minmax(0,1fr)_24rem] [&>*]:min-w-0">
            <div className="min-w-0 space-y-8">
              <Panel plain>
                <PanelHeader title="Findings" />
                <PanelBody>
                  <FindingList
                    findings={scan.findings}
                    emptyLabel="Trusted chain, current protocols, and the headers that matter are in place"
                  />
                </PanelBody>
              </Panel>
              <div>
                <Panel plain>
                  <PanelHeader title="Protocol versions" />
                  <PanelBody flush>
                    <RowList>
                      {scan.protocols.map((protocol) => (
                        <ReportRow
                          key={protocol.name}
                          title={<span className="font-mono text-xs">{protocol.name}</span>}
                          subtitle={protocol.detail}
                          trailing={
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
                          }
                          className="py-2"
                        />
                      ))}
                    </RowList>
                    <p className="pt-3 text-hint leading-relaxed text-muted-foreground">
                      Each version is asked for on a connection of its own, so the answer is the
                      server&rsquo;s rather than a negotiation. &ldquo;unknown&rdquo; means this
                      dashboard&rsquo;s own TLS library would not make the request — reporting that
                      as absent would be a false reassurance.
                    </p>
                  </PanelBody>
                </Panel>
              </div>
              {scan.http && (
                <Panel plain>
                  <PanelHeader title="HTTP behaviour" />
                  <PanelBody flush>
                    <RowList>
                      <ReportRow
                        title="Plain HTTP"
                        subtitle={scan.http.plainLocation || "port 80, followed nowhere"}
                        mono
                        trailing={
                          scan.http.plainError ? (
                            <Status verdict="notice" label="refused connection" />
                          ) : scan.http.plainRedirects ? (
                            <Status verdict="ok" label="redirects to HTTPS" />
                          ) : (
                            <Status
                              verdict="critical"
                              label={`answers ${scan.http.plainStatus} without redirecting`}
                            />
                          )
                        }
                        className="py-2"
                      />
                      <ReportRow
                        title="HSTS"
                        subtitle={scan.http.hsts?.raw ?? "no Strict-Transport-Security header"}
                        mono
                        trailing={
                          scan.http.hsts ? (
                            <Status
                              verdict={scan.http.hsts.maxAge >= 15552000 ? "ok" : "warning"}
                              label={`max-age ${scan.http.hsts.maxAge}${
                                scan.http.hsts.includeSubDomains ? " · subdomains" : ""
                              }${scan.http.hsts.preload ? " · preload" : ""}`}
                            />
                          ) : (
                            <Status verdict="notice" label="not set" />
                          )
                        }
                        className="py-2"
                      />
                      {scan.http.headers.map((header) => (
                        <ReportRow
                          key={header.name}
                          leading={
                            header.present ? (
                              <CheckCircle className="size-3.5 text-success" />
                            ) : (
                              <CrossCircle
                                className={cn(
                                  "size-3.5",
                                  header.level === "important"
                                    ? "text-warning"
                                    : "text-muted-foreground/60",
                                )}
                              />
                            )
                          }
                          title={<span className="font-mono text-xs">{header.name}</span>}
                          subtitle={header.present ? header.value : header.detail}
                          mono={header.present}
                          trailing={
                            !header.present && header.level === "important" ? (
                              <Tag tone="warning">missing</Tag>
                            ) : !header.present ? (
                              <Tag>optional</Tag>
                            ) : undefined
                          }
                          className="py-2"
                        />
                      ))}
                    </RowList>
                  </PanelBody>
                </Panel>
              )}{" "}
            </div>
            <div className="min-w-0 space-y-8">
              <Panel plain>
                <PanelHeader title="Live certificate" />
                <PanelBody>
                  <DetailList>
                    <Detail label="Subject">{scan.certificate?.name ?? "—"}</Detail>
                    <Detail label="Names">{scan.certificate?.domains.join(", ") || "—"}</Detail>
                    <Detail label="Issuer">
                      {scan.certificate ? <IssuerFact issuer={scan.certificate.issuer} /> : "—"}
                    </Detail>
                    <Detail label="Valid until">
                      {scan.certificate ? timestamp(scan.certificate.notAfter) : "—"}
                    </Detail>
                    <Detail label="Key">
                      {scan.keyType}
                      {scan.keyBits ? ` ${scan.keyBits} bits` : ""}
                    </Detail>
                    <Detail label="Signature">{scan.signatureAlgorithm ?? "—"}</Detail>
                    <Detail label="Serial" className="font-mono">
                      {scan.serial ?? "—"}
                    </Detail>
                    <Detail label="OCSP stapled">{scan.ocspStapled ? "yes" : "no"}</Detail>
                    <Detail label="SHA-256" className="font-mono text-micro break-all">
                      {scan.fingerprint}
                    </Detail>
                  </DetailList>
                </PanelBody>
              </Panel>{" "}
              <Panel plain>
                <PanelHeader title="Chain as presented" />
                <PanelBody flush>
                  <ol className="ml-4 border-l border-hairline pl-6">
                    {scan.chain.map((link, index) => (
                      <li
                        key={`${link.subject}-${index}`}
                        className="relative space-y-2 py-4 first:pt-1"
                      >
                        <span className="absolute top-4 -left-10 flex size-8 items-center justify-center bg-background">
                          <ProductLogo
                            id={issuerProduct(link.issuer)}
                            size="sm"
                            fallback={Inspect}
                          />
                        </span>
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="text-body font-medium break-all">{link.subject}</span>
                          {link.isCa && <Tag>CA</Tag>}
                          {link.selfIssued && <Tag>self-issued</Tag>}
                        </div>
                        <p className="text-hint break-all text-muted-foreground">
                          Issued by {link.issuer}
                        </p>
                        <p className="text-hint text-muted-foreground">
                          {link.keyType} {link.keyBits || ""} · expires{" "}
                          {relativeTime(link.notAfter)}
                        </p>
                      </li>
                    ))}
                  </ol>
                </PanelBody>
              </Panel>
            </div>
          </div>
        </div>
      )}
    </Page>
  )
}

/** Findings and header values must wrap, especially on a phone or beside the certificate rail. */
function ReportRow({
  title,
  subtitle,
  trailing,
  leading,
  mono,
  className,
}: {
  title: React.ReactNode
  subtitle?: React.ReactNode
  trailing?: React.ReactNode
  leading?: React.ReactNode
  mono?: boolean
  className?: string
}) {
  return (
    <li
      className={cn(
        "flex min-w-0 flex-col gap-2 py-3 sm:flex-row sm:items-start sm:justify-between sm:gap-4",
        className,
      )}
    >
      <div className="flex min-w-0 items-start gap-2">
        {leading && <span className="mt-0.5 shrink-0">{leading}</span>}
        <div className="min-w-0 space-y-1">
          <div className="text-body font-medium break-all">{title}</div>
          {subtitle && (
            <div
              className={cn(
                "text-hint leading-relaxed break-all text-muted-foreground",
                mono && "font-mono",
              )}
            >
              {subtitle}
            </div>
          )}
        </div>
      </div>
      {trailing && (
        <div className="max-w-full shrink-0 text-hint sm:max-w-[45%] [&_*]:whitespace-normal">
          {trailing}
        </div>
      )}
    </li>
  )
}

/** Who signed it, with the authority drawn as itself where it is one this product knows. */
function IssuerFact({ issuer }: { issuer: string }) {
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5">
      <IssuerGlyph issuer={issuer} />
      <span className="truncate">{issuer || "—"}</span>
    </span>
  )
}

function IssuerGlyph({ issuer }: { issuer: string }) {
  const product = issuerProduct(issuer)
  return product ? <ProductGlyph id={product} /> : null
}

function gradeTone(grade: string): Tone {
  if (grade === "A+" || grade === "A") return "success"
  if (grade === "B" || grade === "C") return "warning"
  return "danger"
}

function isOldProtocol(name: string) {
  return name === "TLS 1.0" || name === "TLS 1.1"
}
