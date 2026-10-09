"use client"

import { useState } from "react"
import Link from "next/link"
import { External, MagnifyingGlass, Warning } from "@/components/icons"
import { errorMessage, get } from "@/lib/api"
import type { AccessExplanation, RouteOutcome, RouteResolution } from "@/lib/proxy/types-route"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { RoutePath } from "@/components/proxy/route-path"
import { accessVerdict, layerVerdict, validSource } from "@/components/proxy/access-explain"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

const OUTCOME: Record<RouteOutcome, { label: string; tone: DotTone }> = {
  refused: { label: "Nothing listens", tone: "danger" },
  "tls-failed": { label: "TLS fails", tone: "danger" },
  "plain-to-tls": { label: "Answered 400", tone: "danger" },
  return: { label: "Returns", tone: "running" },
  redirect: { label: "Redirects", tone: "running" },
  proxy: { label: "Proxied", tone: "running" },
  handler: { label: "Built-in handler", tone: "running" },
  static: { label: "Files", tone: "running" },
}

const MATCH: Record<NonNullable<RouteResolution["server"]>["match"], string> = {
  exact: "exact name",
  wildcard: "wildcard",
  regex: "regex",
  default_server: "default server",
  first: "first block",
}

const at = (file?: string, line?: number) => (file ? `${file}:${line ?? 0}` : "")

/**
 * Which server block and location nginx picks for a URL, and why.
 *
 * With a dozen sites, a wildcard and a default_server on the same port, the
 * question "why does this address land on the wrong site" was answered by
 * reading every file and replaying nginx's precedence in your head. This asks
 * the backend to replay it from `nginx -T`, step by step, without sending
 * anything to the address itself.
 */
export function RouteResolver() {
  const [draft, setDraft] = useState("")
  const [source, setSource] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<RouteResolution | null>(null)
  const [access, setAccess] = useState<AccessExplanation | null>(null)
  const sourceBad = source.trim() !== "" && !validSource(source)

  // With an address, the same route is asked who may take it from there:
  // every layer of access in nginx's order, the host firewall included.
  const resolve = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!draft.trim() || sourceBad) return
    setBusy(true)
    setError(null)
    try {
      if (source.trim()) {
        const explained = await get<AccessExplanation>("/proxy/resolve/access", {
          url: draft.trim(),
          source: source.trim(),
        })
        setAccess(explained)
        setResult(explained.route)
      } else {
        setAccess(null)
        setResult(await get<RouteResolution>("/proxy/resolve", { url: draft.trim() }))
      }
    } catch (err) {
      setResult(null)
      setAccess(null)
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Panel plain data-testid="route-resolver">
      <PanelHeader title="Which site answers a URL" />
      <PanelBody className="space-y-4">
        <form onSubmit={resolve} className="flex flex-col gap-2 sm:flex-row">
          <Input
            aria-label="URL to resolve"
            placeholder="https://app.example.com/api/health"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            className="font-mono sm:flex-1"
            spellCheck={false}
            autoCapitalize="off"
          />
          <Input
            aria-label="From address"
            aria-invalid={sourceBad || undefined}
            placeholder="From address (optional)"
            value={source}
            onChange={(e) => setSource(e.target.value)}
            className="font-mono sm:w-56"
            spellCheck={false}
            autoCapitalize="off"
          />
          <Button type="submit" pending={busy} disabled={!draft.trim() || sourceBad}>
            <MagnifyingGlass />
            Resolve
          </Button>
        </form>
        {sourceBad && (
          <p className="text-hint text-destructive">
            One IPv4 or IPv6 address, as nginx sees the visitor.
          </p>
        )}
        {error && <Notice tone="danger" icon={Warning} title={error} />}
        {access && <AccessLayers access={access} />}
        {result && <Resolution result={result} />}
      </PanelBody>
    </Panel>
  )
}

/** Who may take the route from one address, layer by layer. */
function AccessLayers({ access }: { access: AccessExplanation }) {
  const verdict = accessVerdict(access)
  return (
    <section aria-label={`Access from ${access.source}`} className="space-y-3">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <Status tone={verdict.tone} label={verdict.label} />
        <span className="font-mono text-xs text-muted-foreground">from {access.source}</span>
      </div>
      <p className="text-body">{access.summary}</p>
      <ol className="divide-y divide-hairline">
        {access.layers.map((layer, index) => {
          const reading = layerVerdict(layer)
          return (
            <li key={`${layer.id}:${index}`} className="min-w-0 space-y-1 py-2.5">
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <span className="text-body font-medium">{layer.title}</span>
                <Status tone={reading.tone} label={reading.label} />
              </div>
              <p className="text-body break-words text-muted-foreground">{layer.detail}</p>
              {layer.rules?.map((rule) => (
                <p key={`${rule.file}:${rule.line}`} className="font-mono text-hint break-all">
                  {rule.text}
                  <span className="text-muted-foreground">
                    {"  # "}
                    {at(rule.file, rule.line)}
                  </span>
                </p>
              ))}
              {layer.ownerPath && (
                <Link href={layer.ownerPath} className="text-hint underline underline-offset-4">
                  {layer.owner}
                </Link>
              )}
            </li>
          )
        })}
      </ol>
    </section>
  )
}

function Resolution({ result }: { result: RouteResolution }) {
  const outcome = OUTCOME[result.outcome]
  const { server, location } = result
  const reaches = result.outcome !== "refused"
  return (
    <div className="space-y-4" data-testid="route-resolution">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <Status tone={result.certain ? outcome.tone : "warning"} label={outcome.label} />
        {!result.certain && <Tag tone="warning">Not certain</Tag>}
        <span className="min-w-0 font-mono text-xs break-all text-muted-foreground">
          {result.url}
        </span>
        {reaches && (
          <Button asChild variant="outline" size="xs" className="ml-auto">
            <a href={result.url} target="_blank" rel="noreferrer">
              <External />
              Open site
            </a>
          </Button>
        )}
      </div>

      {server && (
        <RoutePath
          sourceLabel={`Server block · ${MATCH[server.match]} · ${server.listen}`}
          source={
            <>
              {server.names.filter(Boolean).join(" ") || "(no server_name)"}
              <span className="block text-hint text-muted-foreground">
                {at(server.file, server.line)}
              </span>
            </>
          }
          destinationLabel="Location"
          destination={
            location ? (
              <>
                {[...location.parents, `location ${location.modifier} ${location.path}`]
                  .map((l) => l.replace(/\s+/g, " "))
                  .join(" › ")}
                <span className="block text-hint text-muted-foreground">
                  {at(location.file, location.line)}
                </span>
              </>
            ) : (
              "The server block itself"
            )
          }
        />
      )}

      {result.serves.length > 0 && (
        <Well>
          {result.serves.map((d) => (
            <div key={`${d.file}:${d.line}:${d.text}`} className="break-all">
              {d.text}
              <span className="text-muted-foreground">
                {"  # "}
                {at(d.file, d.line)}
                {d.inherited ? " (inherited)" : ""}
              </span>
            </div>
          ))}
        </Well>
      )}

      <ol className="space-y-2">
        {result.steps.map((step, i) => (
          <li key={i} className="flex min-w-0 gap-3 text-body">
            <span className="numeric w-5 shrink-0 text-right text-muted-foreground">{i + 1}</span>
            <div className="min-w-0 space-y-0.5">
              <p className="flex items-start gap-1.5">
                {step.caution && (
                  <Warning aria-label="Caution" className="mt-0.5 size-4 shrink-0 text-warning" />
                )}
                <span>{step.message}</span>
              </p>
              {step.file && (
                <p className="font-mono text-hint break-all text-muted-foreground">
                  {at(step.file, step.line)}
                </p>
              )}
            </div>
          </li>
        ))}
      </ol>
      <p className="text-hint text-muted-foreground">
        Worked out from the configuration nginx -T prints, including changes not yet reloaded.
        Nothing is sent to the address.
      </p>
    </div>
  )
}
