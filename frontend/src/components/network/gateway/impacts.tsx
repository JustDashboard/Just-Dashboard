"use client"

import { useEffect, useRef, useState } from "react"
import { post } from "@/lib/api"
import type { EntryFlow, GatewayImpact, GatewayPreview } from "@/lib/types"
import { Tag } from "@/components/tag"
import { flowWord, impactTone, layerTone } from "@/components/network/gateway/reading"

/**
 * What saving a forward or a NAT entry would do, asked of the server while the
 * form is edited: the same validation and host checks a save runs, with no
 * lock, journal or host change, and the consequences a save does not refuse —
 * an overlapping entry whose rule would win, a local service losing its port,
 * the limits and lists that also judge the traffic, the connections left on an
 * old translation, and the host's other chains as modeled for this flow.
 */
export function useGatewayPreview(body: unknown, enabled: boolean) {
  const [state, setState] = useState<{ preview?: GatewayPreview; pending: boolean }>({
    pending: false,
  })
  // The body object is rebuilt on every render; its content is what changes.
  const key = JSON.stringify(body ?? null)
  const latest = useRef(body)
  useEffect(() => {
    latest.current = body
  })
  useEffect(() => {
    if (!enabled || latest.current === undefined) return
    const controller = new AbortController()
    const timer = setTimeout(() => {
      setState((s) => ({ ...s, pending: true }))
      post<GatewayPreview>("/network/gateway/preview", latest.current, {
        signal: controller.signal,
      })
        .then((preview) => setState({ preview, pending: false }))
        .catch(() => {
          if (!controller.signal.aborted) setState({ pending: false })
        })
    }, 400)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [key, enabled])
  return state
}

const SEVERITY_WORD: Record<GatewayImpact["severity"], string> = {
  refused: "refused",
  warning: "check",
  info: "note",
}

/** The preview, as the section at the foot of the editor. */
export function ImpactList({ preview, pending }: { preview?: GatewayPreview; pending: boolean }) {
  if (!preview) {
    return pending ? (
      <p className="text-hint text-muted-foreground" aria-live="polite">
        Checking what saving would do…
      </p>
    ) : null
  }
  if (!preview.valid) return null
  const nothing =
    preview.impacts.length === 0 && !preview.connections?.tracked && preview.flows.length === 0
  return (
    <section
      aria-label="What saving does"
      className="min-w-0 space-y-3 border-t border-hairline pt-4"
    >
      <p className="flex items-center gap-2 text-body font-medium">
        What saving does
        {pending && <span className="text-hint font-normal text-muted-foreground">updating…</span>}
      </p>
      {nothing && (
        <p className="text-hint text-muted-foreground">
          Nothing else on this server overlaps it or judges its traffic.
        </p>
      )}
      {preview.impacts.length > 0 && (
        <ul className="space-y-2">
          {preview.impacts.map((impact, index) => (
            <li key={index} className="flex min-w-0 items-baseline gap-2 text-hint">
              <Tag tone={impactTone(impact)} className="shrink-0">
                {SEVERITY_WORD[impact.severity]}
              </Tag>
              <span className="min-w-0 text-muted-foreground">{impact.message}</span>
            </li>
          ))}
        </ul>
      )}
      {preview.connections && preview.connections.error && (
        <p className="text-hint text-muted-foreground">{preview.connections.error}</p>
      )}
      {preview.flows.length > 0 && <FlowSummary flows={preview.flows} />}
    </section>
  )
}

/** Each modeled flow's verdict, with its layers folded under it. */
export function FlowSummary({ flows }: { flows: EntryFlow[] }) {
  return (
    <ul className="space-y-2" aria-label="Modeled policy layers">
      {flows.map((flow) => (
        <li key={flow.entry} className="min-w-0 text-hint">
          <details>
            <summary className="flex min-w-0 cursor-pointer items-baseline gap-2 focus-ring">
              <Tag tone={layerTone(flow.verdict)} className="shrink-0">
                {flow.verdict === "clear" ? "clear" : flow.verdict}
              </Tag>
              <span className="min-w-0 text-muted-foreground">
                {flow.direction === "inbound" ? "Arriving" : "Leaving"}: {flowWord(flow)}
              </span>
            </summary>
            <ul className="mt-1.5 space-y-1 pl-4">
              {flow.layers.map((layer, index) => (
                <li key={index} className="min-w-0 text-muted-foreground">
                  <span className="font-mono text-foreground">
                    {layer.family} {layer.table} {layer.chain}
                  </span>{" "}
                  <span>
                    {layer.verdict}
                    {layer.rule ? ` at rule ${layer.rule}` : ""}
                    {layer.path ? ` (${layer.path})` : ""}
                  </span>
                  {" — "}
                  {layer.reason}
                </li>
              ))}
            </ul>
          </details>
        </li>
      ))}
    </ul>
  )
}
