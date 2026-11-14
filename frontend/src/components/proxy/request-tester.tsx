"use client"

import { useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { Check, Copy, CrossCircle, PaperAirplane } from "@/components/icons"
import { post } from "@/lib/api"
import { bytes, timestamp } from "@/lib/format"
import type { RequestHeader, RequestHop, RequestResult, RequestTest, VHost } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useCopy } from "@/hooks/use-copy"
import { Detail, DetailList, Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { ErrorState, Notice, Spinner } from "@/components/state"
import { Status, type Verdict } from "@/components/status-dot"
import { tabClasses } from "@/components/tabs"
import { EmptyMark, OverviewEmpty } from "@/components/proxy/overview-empty"
import { useProxy, useProxyRead } from "@/components/proxy/proxy-context"
import { sitePath } from "@/components/proxy/site-verbs"
import { TLSServedBy } from "@/components/proxy/tls-served-by"
import { Segments } from "@/components/deploy/settings/segments"
import { Field, FieldRow } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

const METHODS = ["GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE"]

type Scheme = "https" | "http"
type ConnectTo = NonNullable<RequestTest["connectTo"]>

type Fields = {
  scheme: Scheme
  domain: string
  port: string
  path: string
  method: string
  headers: string
  connectTo: ConnectTo
}

const EMPTY: Fields = {
  scheme: "https",
  domain: "",
  port: "",
  path: "/",
  method: "GET",
  headers: "",
  connectTo: "127.0.0.1",
}

/**
 * The views of /proxy/tls: the graded scan of whatever a name reaches, every
 * name this server serves with its latest report (?view=fleet), and a request
 * to this machine's own nginx (?tool=request). They answer the same question
 * from different ends — what a visitor gets — so they share the page.
 */
export function TLSTools({ current }: { current: "report" | "fleet" | "request" }) {
  const params = useSearchParams()
  const report = new URLSearchParams(params.toString())
  report.delete("tool")
  report.delete("url")
  report.delete("view")
  const request = new URLSearchParams(params.toString())
  request.delete("view")
  request.set("tool", "request")
  const href = (query: URLSearchParams) => (query.size ? `/proxy/tls?${query}` : "/proxy/tls")
  return (
    <nav aria-label="TLS tools" className="flex min-w-0 gap-1 border-b border-hairline">
      <Link
        href={href(report)}
        aria-current={current === "report" ? "page" : undefined}
        className={tabClasses(current === "report", "h-10")}
      >
        Live report
      </Link>
      <Link
        href="/proxy/tls?view=fleet"
        aria-current={current === "fleet" ? "page" : undefined}
        className={tabClasses(current === "fleet", "h-10")}
      >
        Every site
      </Link>
      <Link
        href={href(request)}
        aria-current={current === "request" ? "page" : undefined}
        className={tabClasses(current === "request", "h-10")}
      >
        Request tester
      </Link>
    </nav>
  )
}

/**
 * One request to this machine's nginx for one of its sites, as a visitor would
 * send it but without asking DNS: the name goes in SNI and Host, the
 * connection goes to loopback. That separates "nginx serves this wrongly" from
 * "DNS points somewhere else", which a request from a browser cannot.
 *
 * The request is sent only when asked, never on opening a link: the method is
 * the operator's, and a POST reaches the site's application like any other.
 */
export function RequestTester() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const router = useRouter()
  const params = useSearchParams()
  const linked = params.get("url")
  const { status, hasNginx } = useProxy()

  // ?url= fills the address fields; the method and headers typed are kept
  // when it changes, since the address bar does not carry them.
  const [fields, setFields] = useState<Fields>(() => ({ ...EMPTY, ...fromURL(linked) }))
  const [shownFor, setShownFor] = useState(linked)
  if (shownFor !== linked) {
    setShownFor(linked)
    setFields((current) => ({ ...current, ...fromURL(linked) }))
  }
  const edit = (next: Partial<Fields>) => setFields((current) => ({ ...current, ...next }))

  // The section's own read of the sites, which the layout polls for every page.
  const sites = useProxyRead("vhosts")
  const names = siteNames(sites.data)

  const url = composeURL(fields)
  const headers = parseHeaders(fields.headers)
  const request: RequestTest | undefined =
    url && !headers.error
      ? { url, method: fields.method, headers: headers.list, connectTo: fields.connectTo }
      : undefined

  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<RequestResult>()
  const [error, setError] = useState<Error>()
  const [selected, setSelected] = useState(0)
  const send = async () => {
    if (!request) return
    setBusy(true)
    setError(undefined)
    if (request.url !== linked) {
      router.replace(`/proxy/tls?tool=request&url=${encodeURIComponent(request.url)}`, {
        scroll: false,
      })
    }
    try {
      const answer = await post<RequestResult>("/proxy/tools/request", request)
      setResult(answer)
      setSelected(answer.hops.length - 1)
    } catch (err) {
      setResult(undefined)
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setBusy(false)
    }
  }

  const { copy, copied } = useCopy()
  const hop = result?.hops[selected]
  // The tester reaches nginx on this machine and nothing else, so a host
  // without it gets the section's own screen for that rather than a form
  // whose every send fails with a dial error.
  const missing = status !== undefined && !hasNginx

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Proxy" title="Request tester" />
      <HostIdentity
        mark="nginx"
        fallback={PaperAirplane}
        title={
          <span
            role="heading"
            aria-level={2}
            className={cn("break-all", (hop || url) && "font-mono text-[0.92em]")}
          >
            {hop?.url ?? (url || "Request tester")}
          </span>
        }
        facts={
          <TesterFacts
            hop={hop}
            busy={busy}
            failed={error !== undefined}
            connectTo={result?.connectTo ?? fields.connectTo}
            admin={admin}
            missing={missing}
          />
        }
      />
      <TLSTools current="request" />

      {missing ? (
        <OverviewEmpty
          mark={<EmptyMark product="nginx" corner={PaperAirplane} />}
          title="nginx is not installed here"
          action={
            admin ? (
              <Button variant="outline" size="xs" asChild>
                <Link href="/proxy">Install</Link>
              </Button>
            ) : undefined
          }
        >
          The tester sends a request to nginx on this machine.
        </OverviewEmpty>
      ) : (
        <form
          onSubmit={(event) => {
            event.preventDefault()
            if (admin && !busy) void send()
          }}
          className="flex min-w-0 flex-col gap-4"
        >
          <FieldRow columns={3}>
            <Field label="Method" htmlFor="request-method">
              <Select value={fields.method} onValueChange={(method) => edit({ method })}>
                <SelectTrigger id="request-method" className="w-full font-mono">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {METHODS.map((method) => (
                    <SelectItem key={method} value={method} className="font-mono">
                      {method}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field label="Scheme">
              <Segments
                label="Scheme"
                value={fields.scheme}
                onChange={(scheme) => edit({ scheme })}
                options={[
                  { value: "https", label: "https", mono: true },
                  { value: "http", label: "http", mono: true },
                ]}
                fill
              />
            </Field>
            <Field label="Connect to" info="nginx on this machine; DNS is not asked.">
              <Segments
                label="Connect to"
                value={fields.connectTo}
                onChange={(connectTo) => edit({ connectTo })}
                options={[
                  { value: "127.0.0.1", label: "127.0.0.1", mono: true },
                  { value: "::1", label: "::1", mono: true },
                ]}
                fill
              />
            </Field>
          </FieldRow>
          <FieldRow columns={3}>
            <Field
              label="Domain"
              htmlFor="request-domain"
              info="A name one of this server's nginx sites serves."
            >
              <Input
                id="request-domain"
                list={admin ? "request-domains" : undefined}
                value={fields.domain}
                onChange={(event) => edit({ domain: event.target.value })}
                placeholder="app.example.com"
                className="font-mono"
                autoComplete="off"
                spellCheck={false}
                autoCapitalize="none"
                autoCorrect="off"
                inputMode="url"
              />
            </Field>
            <Field label="Port" htmlFor="request-port" info="Blank for the scheme's own.">
              <Input
                id="request-port"
                value={fields.port}
                onChange={(event) => edit({ port: event.target.value })}
                placeholder={fields.scheme === "https" ? "443" : "80"}
                className="font-mono"
                autoComplete="off"
                inputMode="numeric"
              />
            </Field>
            <Field label="Path" htmlFor="request-path">
              <Input
                id="request-path"
                value={fields.path}
                onChange={(event) => edit({ path: event.target.value })}
                placeholder="/"
                className="font-mono"
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
          </FieldRow>
          <Field
            label="Headers"
            htmlFor="request-headers"
            info="One per line, Name: value. Host comes from the domain."
            error={headers.error}
          >
            <Textarea
              id="request-headers"
              value={fields.headers}
              onChange={(event) => edit({ headers: event.target.value })}
              placeholder={"Accept: text/html\nCookie: session=…"}
              rows={3}
              className="font-mono text-xs"
              spellCheck={false}
              aria-invalid={headers.error ? true : undefined}
            />
          </Field>
          {admin && (
            <datalist id="request-domains">
              {names.map((name) => (
                <option key={name} value={name} />
              ))}
            </datalist>
          )}
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <Button type="submit" size="sm" disabled={!admin || !request || busy} pending={busy}>
              <PaperAirplane />
              Send
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={!request}
              onClick={() => request && void copy(curlCommand(request), "curl command")}
            >
              {copied ? <Check /> : <Copy />}
              Copy as curl
            </Button>
          </div>
        </form>
      )}

      {error && !busy && <ErrorState error={error} />}
      {admin && !missing && !result && !error && !busy && (
        <p className="text-body text-muted-foreground">
          <span className="font-medium text-foreground">Nothing sent yet.</span> Name one of this
          server&rsquo;s sites and send.
        </p>
      )}

      {result && hop && (
        <div className="flex min-w-0 animate-rise flex-col gap-6 md:gap-8">
          {result.hops.length > 1 || result.stopped ? (
            <Panel plain>
              <PanelHeader
                title="Redirect chain"
                actions={
                  <span className="numeric text-hint text-muted-foreground">
                    {result.hops.length} {result.hops.length === 1 ? "request" : "requests"}
                  </span>
                }
              />
              <PanelBody>
                <ChoiceList aria-label="Redirect chain">
                  {result.hops.map((entry, index) => (
                    <ChoiceRow
                      key={index}
                      verb={`Show ${entry.method} ${entry.url}`}
                      onSelect={() => setSelected(index)}
                      className={cn(index === selected && "ring-2 ring-ring")}
                      leading={
                        <span className="numeric flex size-8 items-center justify-center text-hint text-muted-foreground">
                          {index + 1}
                        </span>
                      }
                      title={<span className="font-mono text-[0.92em]">{entry.url}</span>}
                      description={`${entry.method} · site ${entry.site}`}
                      trailing={<Status verdict={hopVerdict(entry)} label={hopLabel(entry)} />}
                    />
                  ))}
                </ChoiceList>
                {result.stopped && (
                  <p className="pt-3 text-body break-words text-muted-foreground">
                    {stoppedText(result)}
                  </p>
                )}
              </PanelBody>
            </Panel>
          ) : null}
          <HopDetail
            hop={hop}
            connectTo={result.connectTo}
            admin={admin}
            // Reloading nginx sends the request again only when doing so is
            // harmless; a POST is sent when the operator says so.
            onReloaded={fields.method === "GET" || fields.method === "HEAD" ? send : () => {}}
          />
        </div>
      )}
    </Page>
  )
}

/**
 * The answer on the identity line: its status in its colour, the protocol it
 * came over, how long it took and how much was read. Before a send it says
 * where the request will go.
 */
function TesterFacts({
  hop,
  busy,
  failed,
  connectTo,
  admin,
  missing,
}: {
  hop?: RequestHop
  busy: boolean
  failed: boolean
  connectTo: string
  admin: boolean
  missing: boolean
}) {
  const facts: React.ReactNode[] = []
  if (busy) {
    facts.push(
      <span key="busy" className="inline-flex items-center gap-1.5">
        <Spinner className="size-3" /> sending
      </span>,
    )
  } else if (hop) {
    facts.push(
      <Status
        key="status"
        verdict={hopVerdict(hop)}
        label={hop.status === undefined ? "failed" : `answered ${hop.status}`}
        className="font-medium"
      />,
    )
    if (hop.proto) facts.push(<span key="proto">{hop.proto}</span>)
    if (hop.tls)
      facts.push(
        <span key="tls">
          {hop.tls.version}
          {hop.tls.alpn ? ` · ALPN ${hop.tls.alpn}` : ""}
        </span>,
      )
    if (hop.timings.firstByte)
      facts.push(<span key="first">first byte {millis(hop.timings.firstByte)}</span>)
    facts.push(<span key="total">total {millis(hop.timings.total)}</span>)
    if (hop.status !== undefined) facts.push(<span key="read">{bytes(hop.bodyBytes)} read</span>)
  } else if (missing) {
    facts.push(<span key="missing">no nginx on this host</span>)
  } else {
    facts.push(
      <HostFact key="to" product="nginx">
        nginx on <span className="font-mono">{connectTo}</span>
      </HostFact>,
      <span key="dns">DNS not asked</span>,
    )
    if (failed)
      facts.push(
        <span key="failed" className="text-destructive">
          not answered
        </span>,
      )
  }
  if (!admin)
    facts.push(
      <span key="admin" className="font-medium text-warning">
        sending requests needs an administrator
      </span>,
    )
  return facts.flatMap((fact, index) =>
    index === 0 ? [fact] : [<FactDot key={`dot-${index}`} />, fact],
  )
}

function HopDetail({
  hop,
  connectTo,
  admin,
  onReloaded,
}: {
  hop: RequestHop
  connectTo: string
  admin: boolean
  onReloaded: () => void
}) {
  const port = new URL(hop.url).port || (hop.url.startsWith("https:") ? "443" : "80")
  const cert = hop.tls?.certificate
  return (
    <div className="grid items-start gap-8 xl:grid-cols-[minmax(0,1fr)_24rem] [&>*]:min-w-0">
      <div className="min-w-0 space-y-8">
        {hop.error && (
          <Notice tone="danger" icon={CrossCircle} title="The request did not complete">
            <p className="break-words">{hop.error}</p>
          </Notice>
        )}
        {hop.status !== undefined && (
          <Panel plain>
            <PanelHeader
              title="Response headers"
              actions={
                <span className="numeric text-hint text-muted-foreground">
                  {hop.headers.length}
                </span>
              }
            />
            <PanelBody>
              <Well className="max-h-96 wrap-anywhere whitespace-pre-wrap">
                {hop.headers.length
                  ? hop.headers.map((h) => `${h.name}: ${h.value}`).join("\n")
                  : "No headers."}
              </Well>
            </PanelBody>
          </Panel>
        )}
        {hop.status !== undefined && (
          <Panel plain>
            <PanelHeader
              title="Body"
              actions={
                <span className="text-hint text-muted-foreground">
                  {hop.truncated ? "first 64 KiB" : bytes(hop.bodyBytes)}
                </span>
              }
            />
            <PanelBody>
              {hop.binary ? (
                <p className="text-body text-muted-foreground">
                  {bytes(hop.bodyBytes)} that are not text, not shown.
                </p>
              ) : hop.bodyBytes === 0 ? (
                <p className="text-body text-muted-foreground">No body.</p>
              ) : (
                <Well className="max-h-[32rem] wrap-anywhere whitespace-pre-wrap">{hop.body}</Well>
              )}
            </PanelBody>
          </Panel>
        )}
      </div>
      <div className="min-w-0 space-y-8">
        <Panel plain>
          <PanelHeader title="Request" />
          <PanelBody>
            <DetailList>
              <Detail label="Site">
                <Link
                  href={sitePath(hop.site)}
                  className="font-mono wrap-anywhere underline-offset-4 hover:underline"
                >
                  {hop.site}
                </Link>
              </Detail>
              <Detail label="Connected to">
                <span className="font-mono">
                  {connectTo.includes(":") ? `[${connectTo}]` : connectTo}:{port}
                </span>
              </Detail>
              <Detail label="Timings">
                <span className="numeric">
                  connect {millis(hop.timings.connect)}
                  {hop.timings.tls ? ` · TLS ${millis(hop.timings.tls)}` : ""}
                  {hop.timings.firstByte ? ` · first byte ${millis(hop.timings.firstByte)}` : ""}
                  {` · total ${millis(hop.timings.total)}`}
                </span>
              </Detail>
              {hop.location && (
                <Detail label="Redirects to">
                  <span className="font-mono wrap-anywhere">{hop.location}</span>
                </Detail>
              )}
              {hop.tls && (
                <Detail label="Cipher">
                  <span className="font-mono wrap-anywhere">{hop.tls.cipherSuite}</span>
                </Detail>
              )}
              {cert && (
                <Detail label="Certificate">
                  <span className="block wrap-anywhere">{cert.domains.join(", ")}</span>
                  <span className="block text-muted-foreground">
                    {cert.issuer} · {cert.expired ? "expired" : "expires"}{" "}
                    {timestamp(cert.notAfter)}
                  </span>
                  <span className="block">
                    <Status
                      verdict={cert.error ? "critical" : "ok"}
                      label={cert.error ? "not trusted for this name" : "trusted for this name"}
                    />
                  </span>
                  {cert.error && (
                    <span className="block text-hint wrap-anywhere text-muted-foreground">
                      {cert.error}
                    </span>
                  )}
                </Detail>
              )}
            </DetailList>
          </PanelBody>
        </Panel>
        {hop.tls?.origin && (
          <TLSServedBy origin={hop.tls.origin} onRescan={onReloaded} admin={admin} />
        )}
      </div>
    </div>
  )
}

function fromURL(raw: string | null): Partial<Fields> {
  if (!raw) return {}
  try {
    const u = new URL(raw)
    if (u.protocol !== "https:" && u.protocol !== "http:") return {}
    return {
      scheme: u.protocol === "https:" ? "https" : "http",
      domain: u.hostname,
      port: u.port,
      path: `${u.pathname}${u.search}`,
    }
  } catch {
    return {}
  }
}

function composeURL(fields: Fields) {
  const domain = fields.domain.trim()
  if (!domain) return ""
  const port = fields.port.trim()
  const path = fields.path.trim()
  return `${fields.scheme}://${domain}${port ? `:${port}` : ""}${path.startsWith("/") ? path : `/${path}`}`
}

function parseHeaders(text: string): { list: RequestHeader[]; error?: string } {
  const list: RequestHeader[] = []
  for (const line of text.split("\n")) {
    if (!line.trim()) continue
    const colon = line.indexOf(":")
    if (colon <= 0) return { list, error: `"${line.trim()}" is not Name: value` }
    list.push({ name: line.slice(0, colon).trim(), value: line.slice(colon + 1).trim() })
  }
  return { list }
}

/** The names the tester can reach: literal server_names of enabled nginx sites. */
function siteNames(vhosts: VHost[] | undefined) {
  const names = new Set<string>()
  for (const site of vhosts ?? []) {
    if (site.kind !== "nginx" || !site.enabled) continue
    for (const name of site.serverNames) {
      if (name && name !== "_" && !name.startsWith("~") && !name.includes("*")) names.add(name)
    }
  }
  return [...names].sort()
}

function shellQuote(value: string) {
  return `'${value.replaceAll("'", `'\\''`)}'`
}

/**
 * The same first request from a shell: --resolve pins the name to loopback as
 * the tester does. It does not follow redirects, since --resolve covers only
 * the first host, and it verifies the certificate as a visitor's client would.
 */
function curlCommand(request: RequestTest) {
  const u = new URL(request.url)
  const port = u.port || (u.protocol === "https:" ? "443" : "80")
  const address = request.connectTo === "::1" ? "[::1]" : (request.connectTo ?? "127.0.0.1")
  const parts = ["curl", "-sS", request.method === "HEAD" ? "-I" : "-i"]
  if (request.method !== "GET" && request.method !== "HEAD") parts.push("-X", request.method)
  parts.push("--resolve", `${u.hostname}:${port}:${address}`)
  for (const header of request.headers)
    parts.push("-H", shellQuote(`${header.name}: ${header.value}`))
  parts.push(shellQuote(request.url))
  return parts.join(" ")
}

function hopVerdict(hop: RequestHop): Verdict {
  if (hop.status === undefined) return "critical"
  if (hop.status >= 500) return "critical"
  if (hop.status >= 400) return "warning"
  if (hop.status >= 300) return "notice"
  return "ok"
}

function hopLabel(hop: RequestHop) {
  if (hop.status === undefined) return "failed"
  return hop.proto ? `${hop.status} · ${hop.proto}` : String(hop.status)
}

function stoppedText(result: RequestResult) {
  const last = result.hops[result.hops.length - 1]
  switch (result.stopped) {
    case "limit":
      return "Stopped after five redirects."
    case "loop":
      return `Stopped: ${last.location} points back to a URL already requested.`
    default:
      return `Not followed: ${last.location} is not a site on this server's nginx, and the tester reaches no one else.`
  }
}

function millis(ms: number) {
  return `${ms < 10 ? ms.toFixed(1) : Math.round(ms)} ms`
}
