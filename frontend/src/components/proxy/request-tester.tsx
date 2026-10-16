"use client"

import { useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { Check, Copy, CrossCircle, PaperAirplane } from "@/components/icons"
import { get, post } from "@/lib/api"
import { bytes, timestamp } from "@/lib/format"
import type { RequestHeader, RequestHop, RequestResult, RequestTest, VHost } from "@/lib/types"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useCopy } from "@/hooks/use-copy"
import { Detail, DetailList, Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyState, ErrorState, Notice } from "@/components/state"
import { Status, type Verdict } from "@/components/status-dot"
import type { Tone } from "@/components/tone"
import { tabClasses } from "@/components/tabs"
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

  // ?url= fills the address fields; the method and headers typed are kept
  // when it changes, since the address bar does not carry them.
  const [fields, setFields] = useState<Fields>(() => ({ ...EMPTY, ...fromURL(linked) }))
  const [shownFor, setShownFor] = useState(linked)
  if (shownFor !== linked) {
    setShownFor(linked)
    setFields((current) => ({ ...current, ...fromURL(linked) }))
  }
  const edit = (next: Partial<Fields>) => setFields((current) => ({ ...current, ...next }))

  const [offering, setOffering] = useState(false)
  const sites = usePoll((signal) => get<VHost[]>("/proxy/vhosts", undefined, signal), 0, [], {
    enabled: admin && offering,
  })
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

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Proxy" title="Request tester" />
      <TLSTools current="request" />

      {!admin && (
        <Notice title="Sending requests needs an administrator">
          The tester sends requests from this server with a method of your choosing, so it is held
          to the same account level as the network probes.
        </Notice>
      )}

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
          <Field label="Connect to" hint="nginx on this machine; DNS is not asked">
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
            hint="A name one of this server's nginx sites serves"
          >
            <Input
              id="request-domain"
              list={admin ? "request-domains" : undefined}
              value={fields.domain}
              onChange={(event) => edit({ domain: event.target.value })}
              onFocus={() => setOffering(true)}
              placeholder="app.example.com"
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
              autoCapitalize="none"
              autoCorrect="off"
              inputMode="url"
            />
          </Field>
          <Field label="Port" htmlFor="request-port" hint="Blank for the scheme's own">
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
          hint="One per line, Name: value. Host comes from the domain."
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
          {url && (
            <span className="min-w-0 font-mono text-hint wrap-anywhere text-muted-foreground">
              {url}
            </span>
          )}
        </div>
      </form>

      {error && !busy && <ErrorState error={error} />}
      {admin && !result && !error && !busy && (
        <EmptyState
          icon={PaperAirplane}
          title="Nothing sent yet"
          description="Name one of this server's sites and send. The request goes to nginx here, with the name in SNI and Host, and shows what came back."
        />
      )}

      {result && hop && (
        <div className="flex min-w-0 animate-rise flex-col gap-6 md:gap-8">
          <HopFigures hop={hop} />
          {result.hops.length > 1 || result.stopped ? (
            <Panel plain>
              <PanelHeader title="Redirect chain" />
              <PanelBody flush>
                <RowList>
                  {result.hops.map((entry, index) => (
                    <Row
                      key={index}
                      onClick={() => setSelected(index)}
                      className={cn(index === selected && "bg-accent")}
                      title={<span className="font-mono">{entry.url}</span>}
                      subtitle={`${entry.method} · site ${entry.site}`}
                      trailing={<Status verdict={hopVerdict(entry)} label={hopLabel(entry)} />}
                    />
                  ))}
                </RowList>
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
            // Reloading nginx sends the request again only when doing so is
            // harmless; a POST is sent when the operator says so.
            onReloaded={fields.method === "GET" || fields.method === "HEAD" ? send : () => {}}
          />
        </div>
      )}
    </Page>
  )
}

function HopFigures({ hop }: { hop: RequestHop }) {
  return (
    <StatGrid columns={4} dense>
      <StatTile
        label="Status"
        value={hop.status ?? "failed"}
        tone={verdictTone(hopVerdict(hop))}
        hint={hop.location ? `to ${hop.location}` : hop.error}
      />
      <StatTile
        label="Protocol"
        value={hop.proto ?? "—"}
        hint={
          hop.tls ? `${hop.tls.version}${hop.tls.alpn ? ` · ALPN ${hop.tls.alpn}` : ""}` : "plain"
        }
      />
      <StatTile
        label="First byte"
        value={hop.timings.firstByte ? millis(hop.timings.firstByte) : "—"}
        hint={`connect ${millis(hop.timings.connect)}${hop.timings.tls ? ` · TLS ${millis(hop.timings.tls)}` : ""}`}
      />
      <StatTile
        label="Total"
        value={millis(hop.timings.total)}
        hint={hop.status ? `${bytes(hop.bodyBytes)} read` : undefined}
      />
    </StatGrid>
  )
}

function HopDetail({
  hop,
  connectTo,
  onReloaded,
}: {
  hop: RequestHop
  connectTo: string
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
            <PanelHeader title="Response headers" />
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
                hop.truncated ? (
                  <span className="text-hint text-muted-foreground">first 64 KiB</span>
                ) : undefined
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
                  href={`/proxy/sites?site=${encodeURIComponent(hop.site)}`}
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
        {hop.tls?.origin && <TLSServedBy origin={hop.tls.origin} onRescan={onReloaded} />}
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

function verdictTone(verdict: Verdict): Tone {
  switch (verdict) {
    case "ok":
      return "success"
    case "warning":
      return "warning"
    case "critical":
      return "danger"
    default:
      return "default"
  }
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
