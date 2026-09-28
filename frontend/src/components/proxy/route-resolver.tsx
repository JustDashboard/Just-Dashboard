"use client"

import { useState } from "react"
import { External, MagnifyingGlass, Warning } from "@/components/icons"
import { errorMessage, get } from "@/lib/api"
import type { RouteOutcome, RouteResolution } from "@/lib/proxy/types-route"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { RoutePath } from "@/components/proxy/route-path"
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
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<RouteResolution | null>(null)

  const resolve = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!draft.trim()) return
    setBusy(true)
    setError(null)
    try {
      setResult(await get<RouteResolution>("/proxy/resolve", { url: draft.trim() }))
    } catch (err) {
      setResult(null)
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
          <Button type="submit" pending={busy} disabled={!draft.trim()}>
            <MagnifyingGlass />
            Resolve
          </Button>
        </form>
        {error && <Notice tone="danger" icon={Warning} title={error} />}
        {result && <Resolution result={result} />}
      </PanelBody>
    </Panel>
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
