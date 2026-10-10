"use client"

import { useState } from "react"
import { errorMessage, get, post } from "@/lib/api"
import { timestamp } from "@/lib/format"
import type { ControlsVerification, ServicePolicy } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Warning } from "@/components/icons"
import { ProductGlyph } from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"
import {
  checkState,
  controlSetting,
  supportWord,
  validPath,
} from "@/components/proxy/site-controls"
import { controlHue, controlState } from "@/components/proxy/site-overview"

/**
 * A site's request limits, caching and HTTP versions — its service policy —
 * as its file sets them, and, for an administrator, what a bounded set of
 * requests through this nginx found each one doing.
 */
export function SiteControls({ name, admin }: { name: string; admin: boolean }) {
  const policy = usePoll(
    (signal) =>
      get<ServicePolicy>(`/proxy/sites/${encodeURIComponent(name)}/policy`, undefined, signal),
    60_000,
    [name],
  )
  const [path, setPath] = useState("/")
  const [asset, setAsset] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [measured, setMeasured] = useState<ControlsVerification | null>(null)
  const pathBad = !validPath(path) || !validPath(asset)

  if (!policy.data) return null
  const checks = new Map((measured?.checks ?? []).map((check) => [check.id, check]))

  const measure = async (event: React.FormEvent) => {
    event.preventDefault()
    if (pathBad) return
    setBusy(true)
    setError(null)
    try {
      setMeasured(
        await post<ControlsVerification>(
          `/proxy/sites/${encodeURIComponent(name)}/controls/verify`,
          { path: path.trim() || "/", asset: asset.trim() },
        ),
      )
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Panel plain aria-label="Controls">
      <PanelHeader
        title="Controls"
        actions={
          policy.data.engine && (
            <span className="inline-flex items-center gap-1.5 font-mono text-hint text-muted-foreground">
              <ProductGlyph id="nginx-static" className="size-3" />
              {policy.data.engine}
            </span>
          )
        }
      />
      <PanelBody className="space-y-5">
        {/* Three to a row from xl, as the readings a page opens on are: six
            controls read down one column were a screen of grey for six
            words. Each row's hairline is the rule, so a column has none. */}
        <ul className="grid min-w-0 gap-x-8 sm:grid-cols-2 xl:grid-cols-3">
          {policy.data.controls.map((control) => {
            const check = checks.get(control.id)
            const state = check ? checkState(check) : controlState(control)
            const lacks = supportWord(control)
            return (
              <li key={control.id} className="min-w-0 space-y-1.5 border-t border-hairline py-3.5">
                <div className="flex flex-wrap items-baseline justify-between gap-2">
                  <span className="text-body font-medium">{control.title}</span>
                  <Status tone={state.tone} label={state.label} />
                </div>
                <p className="flex flex-wrap items-center gap-2 font-mono text-hint break-all">
                  <span
                    className={cn(!control.configured && "text-muted-foreground")}
                    style={control.configured ? { color: controlHue(control.id) } : undefined}
                  >
                    {controlSetting(control)}
                  </span>
                  {control.configured && lacks && <Tag tone="warning">{lacks}</Tag>}
                </p>
                {control.paths?.map((p) => (
                  <p key={p} className="font-mono text-hint break-all text-muted-foreground">
                    {p}
                  </p>
                ))}
                {check && (
                  <div className="space-y-0.5">
                    <p className="text-body break-words">{check.detail}</p>
                    {check.evidence?.map((line) => (
                      <p key={line} className="font-mono text-hint break-all text-muted-foreground">
                        {line}
                      </p>
                    ))}
                  </div>
                )}
              </li>
            )
          })}
        </ul>
        {admin && (
          <form onSubmit={measure} className="space-y-2">
            <div className="flex flex-col gap-2 sm:flex-row">
              <Input
                aria-label="Path to measure"
                value={path}
                onChange={(e) => setPath(e.target.value)}
                className="font-mono sm:w-48"
                spellCheck={false}
                autoCapitalize="off"
              />
              <Input
                aria-label="Asset path"
                placeholder="Asset path, such as /app.css (optional)"
                value={asset}
                onChange={(e) => setAsset(e.target.value)}
                className="font-mono sm:flex-1"
                spellCheck={false}
                autoCapitalize="off"
              />
              <Button type="submit" variant="outline" pending={busy} disabled={pathBad}>
                Measure
              </Button>
            </div>
            <p className="text-hint text-muted-foreground">
              Sends a bounded set of requests to this nginx on loopback, as a visitor to the site
              would — at most forty, past the request limit&rsquo;s burst — and they reach the
              application.
            </p>
            {pathBad && (
              <p className="text-hint text-destructive">
                A path starts with / and holds no spaces.
              </p>
            )}
          </form>
        )}
        {error && <Notice tone="danger" icon={Warning} title={error} />}
        {measured && (
          <p className="text-hint text-muted-foreground">
            Measured {timestamp(measured.checkedAt)} at {measured.url} with {measured.requests}{" "}
            requests.
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}
