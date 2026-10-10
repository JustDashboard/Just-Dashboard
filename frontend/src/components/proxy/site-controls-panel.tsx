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
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  checkState,
  controlSetting,
  supportWord,
  validPath,
} from "@/components/proxy/site-controls"

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
            <span className="text-hint text-muted-foreground">{policy.data.engine}</span>
          )
        }
      />
      <PanelBody className="space-y-4">
        <ul className="divide-y divide-hairline">
          {policy.data.controls.map((control) => {
            const check = checks.get(control.id)
            const state = check && checkState(check)
            const lacks = supportWord(control)
            return (
              <li key={control.id} className="min-w-0 space-y-1 py-2.5">
                <div className="flex flex-wrap items-baseline justify-between gap-2">
                  <span className="text-body font-medium">{control.title}</span>
                  {state && <Status tone={state.tone} label={state.label} />}
                </div>
                <p className="flex flex-wrap items-center gap-2 font-mono text-hint break-all text-muted-foreground">
                  {controlSetting(control)}
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
                className="font-mono sm:flex-1"
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
