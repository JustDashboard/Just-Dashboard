"use client"

import { useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { CheckCircle, CrossCircle, External, Inspect } from "@/components/icons"
import { get } from "@/lib/api"
import { duration, relativeTime, timestamp } from "@/lib/format"
import {
  parseScanQuery,
  parseScanTarget,
  scanSuggestions,
  targetHint,
  targetLabel,
  withRecent,
  type ScanTarget,
} from "@/lib/scan-target"
import {
  certificateLeft,
  diagnosisLinks,
  failureSteps,
  plainVerdict,
  termLeft,
  termText,
} from "@/lib/tls-report"
import { cn } from "@/lib/utils"
import { useViewState } from "@/lib/view-state"
import type { Certificate, HTTPScan, PreloadCheck, TLSScan, VHost } from "@/lib/types"
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
import { useNow } from "@/components/deploy/vocabulary"
import { expiryTone } from "@/components/proxy/expiry-status"
import { Button } from "@/components/ui/button"
import { Field } from "@/components/form"
import { InputGroup, InputGroupInput } from "@/components/ui/input-group"

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
  const admin = can("system.admin")
  const router = useRouter()
  const params = useSearchParams()
  // The address bar holds the question — ?domain= with a name, host:port or a
  // URL, and ?port= — read the way the field is, so a watched mail server's
  // link scans 993 and reload, a shared link and Back each ask it again. A
  // link is the question, and a page that then waited for a second click to
  // ask it would be a page that forgot why it was opened.
  const linked = params.get("domain")
  const linkedPort = params.get("port")
  const asked = linked === null ? undefined : parseScanQuery(linked, linkedPort)
  const target = asked?.target
  const targetKey = target ? targetLabel(target) : ""
  // Another spelling of the same target — ?port= beside the name, a pasted
  // URL — becomes the one every link uses, so a copied address reads the same.
  const respelled =
    target && (linked !== targetKey || linkedPort !== null) ? reportHref(params, target) : undefined
  useEffect(() => {
    if (respelled) router.replace(respelled, { scroll: false })
  }, [respelled, router])

  // The fields show the target the address asks for, and change with it on
  // Back or a link; what is typed over them is kept until then.
  const address = JSON.stringify([linked, linkedPort])
  const fromAddress: ScanFields = target
    ? targetFields(target)
    : { domain: linked ?? "", port: linkedPort ?? "" }
  const [fields, setFields] = useState(fromAddress)
  const [fieldError, setFieldError] = useState(asked?.error)
  const [shownFor, setShownFor] = useState(address)
  if (shownFor !== address) {
    setShownFor(address)
    setFields(fromAddress)
    setFieldError(asked?.error)
  }
  const typed = parseScanQuery(fields.domain, fields.port.trim())
  const edit = (next: Partial<ScanFields>) => {
    setFields((current) => ({ ...current, ...next }))
    setFieldError(undefined)
  }

  // Cancel stops waiting and aborts the request, which ends the scan on the
  // server too. It holds for this target until Scan is pressed again.
  const [cancelled, setCancelled] = useState<{ key: string; after: number; over: boolean }>()
  if (cancelled && cancelled.key !== targetKey) setCancelled(undefined)
  const report = usePoll(
    (signal) =>
      get<TLSScan>("/certificates/scan", { domain: target?.host, port: target?.port }, signal),
    0,
    [targetKey],
    { enabled: admin && target !== undefined && cancelled === undefined },
  )
  // usePoll reports loading only while there is nothing to show, so a scan
  // asked for again over a report already on screen is tracked here: busy
  // until an answer or an error replaces what it was asked over. It belongs
  // to its target and ends with its answer; kept past either, a scan asked
  // with nothing on screen would match the next page with nothing on screen
  // and draw a scan that is not running.
  const [rescanOver, setRescanOver] = useState<
    Pick<typeof report, "data" | "error"> & { key: string }
  >()
  if (
    rescanOver &&
    (rescanOver.key !== targetKey ||
      rescanOver.data !== report.data ||
      rescanOver.error !== report.error)
  )
    setRescanOver(undefined)
  const rescanning = rescanOver !== undefined
  const rescan = () => {
    setRescanOver({ key: targetKey, data: report.data, error: report.error })
    report.refresh()
  }
  // Asked again from the report itself, a cancelled scan is asked again too:
  // the cancel holds only until the next ask.
  const scanAgain = () => {
    setCancelled(undefined)
    rescan()
  }
  const cancel = (after: number) => {
    setCancelled({ key: targetKey, after, over: report.data !== undefined })
    setRescanOver(undefined)
  }
  const scan = report.data ?? null
  const busy = report.loading || rescanning
  const scanned = scan ? targetLabel({ host: scan.domain, port: scan.port }) : ""

  // The field offers what was scanned here before and the names this server
  // knows: its sites, its watched endpoints and its certificates. They are
  // fetched the first time the field is used, not on every visit.
  const [recent, setRecent] = useViewState<string[]>("proxy.tls.recent", [])
  useEffect(() => {
    if (scan?.reachable) {
      const label = targetLabel({ host: scan.domain, port: scan.port })
      setRecent((list) => withRecent(list, label))
    }
  }, [scan, setRecent])
  const [offering, setOffering] = useState(false)
  const offered = { enabled: admin && offering }
  const sites = usePoll(
    (signal) => get<VHost[]>("/proxy/vhosts", undefined, signal),
    0,
    [],
    offered,
  )
  const certificates = usePoll(
    (signal) => get<Certificate[]>("/certificates/", undefined, signal),
    0,
    [],
    offered,
  )
  // check=false reads the stored results: suggesting a name is no reason to
  // handshake with every watched endpoint.
  const watched = usePoll(
    (signal) =>
      get<{ domain: string; port: number }[]>("/certificates/watched", { check: false }, signal),
    0,
    [],
    offered,
  )
  const suggestions = useMemo(
    () =>
      admin
        ? scanSuggestions({
            recent,
            sites: sites.data,
            certificates: certificates.data,
            watched: watched.data,
          })
        : [],
    [admin, recent, sites.data, certificates.data, watched.data],
  )

  const run = () => {
    if (!typed.target) {
      setFieldError(typed.error)
      return
    }
    setFields(targetFields(typed.target))
    setFieldError(undefined)
    setCancelled(undefined)
    // A new target is a new address, so Back returns to the last report.
    if (targetLabel(typed.target) === targetKey) rescan()
    else router.push(reportHref(params, typed.target), { scroll: false })
  }

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Proxy" title="TLS report" />

      {scan?.reachable ? (
        <StatGrid columns={4} dense>
          <StatTile
            label={scanned}
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
                  : certificateLeft(scan.certificate, Date.parse(scan.checkedAt))
                : "none"
            }
            // Amber inside the renewal window, the Certificates page's own
            // rule: the backend judges both against the certificate's term.
            tone={scan.certificate ? expiryTone(scan.certificate) : "danger"}
            meter={
              scan.certificate ? termLeft(scan.certificate, Date.parse(scan.checkedAt)) : undefined
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
            tone={
              scan.legacyOnly ? "danger" : scan.negotiated === "TLS 1.3" ? "success" : "default"
            }
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
      ) : scan?.failure ? (
        // A failed scan is graded and read like any other: the letter, then
        // the three steps a connection takes, the one that failed in red and
        // the ones after it not reached.
        <StatGrid columns={4} dense>
          <StatTile label={scanned} value={scan.grade} tone="danger" hint={scan.summary} />
          {failureSteps(scan).map((step) => (
            <StatTile
              key={step.label}
              label={step.label}
              value={step.value}
              tone={step.tone}
              hint={step.hint}
            />
          ))}
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
            <h2 className="text-base font-semibold tracking-tight break-all">
              {scanned || targetKey || "Live TLS report"}
            </h2>
            {scan && <CheckedAgo at={scan.checkedAt} />}
          </div>
        </div>
        {/* As wide as the field's row: a long name in the hint or the error
            wraps under the field rather than widening the form off the
            header's row. */}
        <form
          onSubmit={(event) => {
            event.preventDefault()
            if (admin && !busy) run()
          }}
          className="w-full min-w-0 sm:w-min"
        >
          {/* The button sits in the field's row so an error line under the
              input does not pull it down with it. The hint says what the
              scan will reach, so a pasted URL or imaps:// shows its port
              before anything is sent. */}
          <Field
            label="Domain to scan"
            htmlFor="tls-domain"
            hint={
              <span className="wrap-anywhere">
                {typed.target ? `Scans ${targetHint(typed.target)}` : "A name, host:port or URL"}
              </span>
            }
            error={fieldError && <span className="wrap-anywhere">{fieldError}</span>}
          >
            <div className="flex min-w-0 items-center gap-2">
              <InputGroup className="w-full sm:w-96">
                <InputGroupInput
                  id="tls-domain"
                  list={admin ? "tls-targets" : undefined}
                  value={fields.domain}
                  onChange={(event) => edit({ domain: event.target.value })}
                  onFocus={() => setOffering(true)}
                  placeholder="app.example.com"
                  aria-invalid={fieldError ? true : undefined}
                  autoComplete="off"
                  spellCheck={false}
                  autoCapitalize="none"
                  autoCorrect="off"
                  inputMode="url"
                />
                {/* The port is part of the address, so it sits inside the
                    same edge; empty, it shows the port the address implies. */}
                <label
                  htmlFor="tls-port"
                  className="flex shrink-0 cursor-text items-center border-l border-hairline pl-2.5 text-hint text-muted-foreground select-none"
                >
                  port
                </label>
                <InputGroupInput
                  id="tls-port"
                  value={fields.port}
                  onChange={(event) => edit({ port: event.target.value })}
                  placeholder={String(parseScanTarget(fields.domain).target?.port ?? 443)}
                  aria-invalid={fieldError ? true : undefined}
                  autoComplete="off"
                  inputMode="numeric"
                  className="w-16 flex-none pl-1.5 font-mono sm:text-xs"
                />
              </InputGroup>
              <Button
                type="submit"
                size="sm"
                disabled={busy || !fields.domain.trim() || !admin}
                pending={busy}
              >
                Scan
              </Button>
            </div>
          </Field>
          {admin && (
            <datalist id="tls-targets">
              {suggestions.map((suggestion) => (
                <option key={suggestion.value} value={suggestion.value} label={suggestion.source} />
              ))}
            </datalist>
          )}
        </form>
      </div>
      <div>
        {!admin && (
          <Notice title="Scanning needs an administrator">
            The report reaches a domain of your choosing from this server, so it is held to the same
            account level as the network probes.
          </Notice>
        )}
        {admin && !scan && !busy && !report.error && !cancelled && (
          <EmptyState
            icon={Inspect}
            title="Nothing scanned yet"
            description="Enter a domain, host:port or URL, or pick one this server knows. The scan connects to it from this server and grades what it serves."
          />
        )}
        {busy && <ScanProgress key={targetKey} onCancel={cancel} />}
        {cancelled && (
          <p className="text-body text-muted-foreground">
            {cancelled.over
              ? `The new scan was cancelled after ${duration(cancelled.after)}. The report below is the one from before.`
              : `The scan was cancelled after ${duration(cancelled.after)}. Scan to run it again.`}
          </p>
        )}
        {report.error && !busy && <ErrorState error={report.error} onRetry={scanAgain} />}
        {/* Why no handshake completed is the report's one finding, with
            advice for the step that failed and the pages where it is fixed
            when the fault can be on this server. */}
        {scan &&
          !scan.reachable &&
          scan.findings.map((finding) => (
            <Notice key={finding.id} tone="danger" icon={CrossCircle} title={finding.title}>
              <p className="break-words">{finding.detail}</p>
              {finding.advice && <p className="break-words">{finding.advice}</p>}
              {admin && (
                <div className="flex flex-wrap gap-2 pt-2">
                  <Button
                    type="button"
                    variant="outline"
                    size="xs"
                    onClick={scanAgain}
                    disabled={busy}
                    pending={rescanning}
                  >
                    Scan again
                  </Button>
                  {diagnosisLinks(scan).map((link) => (
                    <Button key={link.href} variant="outline" size="xs" asChild>
                      <Link href={link.href}>{link.label}</Link>
                    </Button>
                  ))}
                </div>
              )}
            </Notice>
          ))}
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
                    // Headers are only vouched for where an HTTP answer was read.
                    emptyLabel={
                      scan.http?.service === "http"
                        ? "Trusted chain, current protocols, and the headers that matter are in place"
                        : "Trusted chain and current protocols"
                    }
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
                      server&rsquo;s rather than a negotiation. &ldquo;refused&rdquo; is the server
                      saying no. &ldquo;unknown&rdquo; is no answer to stand behind — the connection
                      failed, this dashboard&rsquo;s TLS library would not ask, or the
                      server&rsquo;s answer could mean either — and the row says which; reporting it
                      as absent would be a false reassurance.
                    </p>
                  </PanelBody>
                </Panel>
              </div>
              {scan.http?.service === "other" && (
                <Panel plain>
                  <PanelHeader title="HTTP behaviour" />
                  <PanelBody flush>
                    <RowList>
                      <ReportRow
                        title="HTTPS"
                        subtitle={
                          scan.http.serviceName
                            ? `Port ${scan.port} is registered to ${scan.http.serviceName}, so no web request was sent.`
                            : scan.http.banner
                              ? `Answered: ${scan.http.banner}`
                              : "It answered the web request in a protocol other than HTTP."
                        }
                        mono={!scan.http.serviceName && Boolean(scan.http.banner)}
                        trailing={<Status verdict="notice" label="not an HTTP service" />}
                        className="py-2"
                      />
                    </RowList>
                    <p className="pt-3 text-hint leading-relaxed text-muted-foreground">
                      Only TLS was checked. HSTS, the security headers and the plain-HTTP redirect
                      are for websites, and this service is something else.
                    </p>
                  </PanelBody>
                </Panel>
              )}
              {scan.http && scan.http.service !== "other" && (
                <Panel plain>
                  <PanelHeader title="HTTP behaviour" />
                  <PanelBody flush>
                    <RowList>
                      <ReportRow
                        title="HTTPS"
                        subtitle={
                          scan.http.httpsError ??
                          ([
                            scan.http.server && `Server: ${scan.http.server}`,
                            scan.http.location && `redirects to ${scan.http.location}`,
                          ]
                            .filter(Boolean)
                            .join(" · ") ||
                            undefined)
                        }
                        mono
                        trailing={
                          scan.http.httpsError ? (
                            <Status verdict="notice" label="no HTTP answer" />
                          ) : (
                            <Status
                              verdict={scan.http.statusCode >= 500 ? "warning" : "ok"}
                              label={`answers ${scan.http.statusCode}`}
                            />
                          )
                        }
                        className="py-2"
                      />
                      {!scan.http.httpsError && (
                        <>
                          <ReportRow
                            title="Plain HTTP"
                            subtitle={<PlainChain http={scan.http} />}
                            mono
                            trailing={<Status {...plainVerdict(scan.http)} />}
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
                        </>
                      )}
                    </RowList>
                    {scan.http.httpsError && (
                      <p className="pt-3 text-hint leading-relaxed text-muted-foreground">
                        Only TLS was checked. HSTS, the security headers and the plain-HTTP redirect
                        need an HTTP answer, which a mail server or another service that is not a
                        website does not give.
                      </p>
                    )}
                  </PanelBody>
                </Panel>
              )}
              {scan.preload && <PreloadPanel preload={scan.preload} domain={scan.domain} />}
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
                    <Detail label="Valid from">
                      {scan.certificate ? timestamp(scan.certificate.notBefore) : "—"}
                    </Detail>
                    <Detail label="Valid until">
                      {scan.certificate ? timestamp(scan.certificate.notAfter) : "—"}
                    </Detail>
                    <Detail label="Lifetime">{termText(scan) ?? "—"}</Detail>
                    <Detail label="Key">
                      {scan.keyType}
                      {scan.keyBits ? ` ${scan.keyBits} bits` : ""}
                    </Detail>
                    <Detail label="Signature">{scan.signatureAlgorithm ?? "—"}</Detail>
                    <Detail label="Serial" className="font-mono break-all">
                      {scan.serial ?? "—"}
                    </Detail>
                    <Detail label="OCSP stapled">
                      {scan.ocspStapled
                        ? "yes"
                        : scan.ocspServers?.length
                          ? "no"
                          : "not applicable, the certificate names no OCSP responder"}
                    </Detail>
                    {scan.ocspServers?.length ? (
                      <Detail label="OCSP" className="font-mono text-micro wrap-anywhere">
                        {scan.ocspServers.join(" ")}
                      </Detail>
                    ) : null}
                    {scan.crlUrls?.length ? (
                      <Detail label="CRL" className="font-mono text-micro wrap-anywhere">
                        {scan.crlUrls.join(" ")}
                      </Detail>
                    ) : null}
                    <Detail label="SHA-256" className="font-mono text-micro break-all">
                      {scan.fingerprint}
                    </Detail>
                    <Detail label="SPKI pin" className="font-mono text-micro break-all">
                      {scan.spkiPin ?? "—"}
                    </Detail>
                  </DetailList>
                </PanelBody>
              </Panel>
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
                          <span className="text-body font-medium wrap-anywhere">
                            {link.subject}
                          </span>
                          {link.isCa && <Tag>CA</Tag>}
                          {link.selfIssued && <Tag>self-issued</Tag>}
                        </div>
                        <p className="text-hint wrap-anywhere text-muted-foreground">
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

/**
 * The scan form's two fields: the address as typed, and a port for one that
 * does not say its own.
 */
type ScanFields = { domain: string; port: string }

/**
 * A target as the fields show it: the whole address in the first, as links
 * spell it, and the port field left empty. A port left in it would outvote the
 * next name typed or picked — imaps:// would scan the old port, and a picked
 * host:port would be refused for disagreeing with it.
 */
function targetFields(target: ScanTarget): ScanFields {
  return { domain: targetLabel(target), port: "" }
}

/**
 * This page's address for a target, in the spelling every link to it uses
 * (`tlsReportHref`), keeping whatever else the address says.
 */
function reportHref(params: { toString(): string }, target: ScanTarget) {
  const next = new URLSearchParams(params.toString())
  next.set("domain", targetLabel(target))
  next.delete("port")
  return `/proxy/tls?${next}`
}

/**
 * A scan in flight: how long it has run, and the way out of it. A scan is
 * allowed a minute, and a spinner with no clock and no way out of it is
 * indistinguishable from a page that has hung.
 */
function ScanProgress({ onCancel }: { onCancel: (seconds: number) => void }) {
  const [started] = useState(() => Date.now())
  const now = useNow(1000)
  const seconds = Math.max(0, Math.floor((now - started) / 1000))
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
      <p className="flex min-w-0 items-center gap-2 text-body text-muted-foreground">
        <Spinner className="size-3.5 shrink-0" />
        <span>
          Handshaking, probing each TLS version separately, and fetching the headers…{" "}
          <span className="numeric">{duration(seconds)}</span>
        </span>
      </p>
      <Button type="button" variant="outline" size="xs" onClick={() => onCancel(seconds)}>
        Cancel
      </Button>
    </div>
  )
}

/**
 * Findings and header values must wrap, especially on a phone or beside the
 * certificate rail: prose at its spaces, and only a word too long for the line
 * — a URL, a header value — anywhere in it.
 */
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
          <div className="text-body font-medium wrap-anywhere">{title}</div>
          {subtitle && (
            <div
              className={cn(
                "text-hint leading-relaxed wrap-anywhere text-muted-foreground",
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

/** Every plain-HTTP request in order, so a redirect through another host reads as one. */
function PlainChain({ http }: { http: HTTPScan }) {
  if (http.plainError) return <>{http.plainError}</>
  return (
    <ol className="space-y-0.5">
      {http.redirectChain.map((hop) => (
        <li key={hop.url}>
          {hop.url}{" "}
          {hop.internal
            ? "not requested, on this machine or its private network"
            : hop.error
              ? "did not answer"
              : `${hop.status}${hop.location ? ` → ${hop.location}` : ""}`}
        </li>
      ))}
    </ol>
  )
}

/** "Checked 3 minutes ago", kept true while the page stays open. */
function CheckedAgo({ at }: { at: string }) {
  useNow(30_000)
  return <p className="text-hint text-muted-foreground">Checked {relativeTime(at)}</p>
}

/**
 * The domain against hstspreload.org's rules, each with what the scan saw of
 * it. A subdomain is never submitted, so it gets the one rule that says so and
 * a way to the name that would be.
 */
function PreloadPanel({ preload, domain }: { preload: PreloadCheck; domain: string }) {
  const parent = preload.domain && preload.domain !== domain ? preload.domain : undefined
  return (
    <Panel plain>
      <PanelHeader
        title="HSTS preload"
        actions={
          <Status
            verdict={preload.eligible ? "ok" : "notice"}
            label={preload.eligible ? "eligible" : "not eligible"}
          />
        }
      />
      <PanelBody flush>
        <RowList aria-label="HSTS preload rules">
          {preload.rules.map((rule) => (
            <ReportRow
              key={rule.id}
              leading={
                rule.passed ? (
                  <CheckCircle className="size-3.5 text-success" />
                ) : (
                  <CrossCircle className="size-3.5 text-muted-foreground" />
                )
              }
              title={
                <>
                  <span className="sr-only">{rule.passed ? "Met: " : "Not met: "}</span>
                  {rule.title}
                </>
              }
              subtitle={rule.detail}
              className="py-2"
            />
          ))}
        </RowList>
        <div className="space-y-2 pt-3 text-hint leading-relaxed text-muted-foreground">
          <p>
            Preloading has browsers use HTTPS for a domain and every name under it before the first
            visit. Coming off the list takes months, so it suits a domain whose every subdomain
            already serves HTTPS.
          </p>
          {parent && (
            <Button variant="outline" size="xs" asChild>
              <Link href={`/proxy/tls?domain=${encodeURIComponent(parent)}`}>Scan {parent}</Link>
            </Button>
          )}
          {preload.eligible && (
            <Button variant="outline" size="xs" asChild>
              <a
                href={`https://hstspreload.org/?domain=${encodeURIComponent(preload.domain)}`}
                target="_blank"
                rel="noreferrer"
              >
                <External className="size-3.5" />
                Submit at hstspreload.org
              </a>
            </Button>
          )}
        </div>
      </PanelBody>
    </Panel>
  )
}

function gradeTone(grade: string): Tone {
  if (grade === "A+" || grade === "A") return "success"
  if (grade === "B" || grade === "C") return "warning"
  return "danger"
}

function isOldProtocol(name: string) {
  return name === "TLS 1.0" || name === "TLS 1.1"
}
