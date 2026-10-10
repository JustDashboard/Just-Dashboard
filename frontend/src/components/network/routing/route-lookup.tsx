"use client"

import { useState } from "react"
import { get } from "@/lib/api"
import type { NetworkPath } from "@/lib/types"
import { Detail, DetailList } from "@/components/page"
import { Field } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { calcSubnet } from "@/components/network/tools/subnet-math"
import { addressFamily, type RouteFamily } from "./decision-reading"

export type RouteLookupResult = NetworkPath & { family: RouteFamily; table?: number }

function literal(address: string) {
  const value = address.trim()
  if (!value || value.includes("/")) return false
  try {
    calcSubnet(`${value}/${addressFamily(value) === "inet6" ? 128 : 32}`)
    return true
  } catch {
    return false
  }
}

/** Ask the kernel directly rather than treating the diagram as a policy evaluator. */
export function RouteLookup() {
  const [target, setTarget] = useState("")
  const [source, setSource] = useState("")
  const [mark, setMark] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Error>()
  const [result, setResult] = useState<RouteLookupResult>()
  const valid = literal(target) && (!source.trim() || literal(source))
  const run = async () => {
    if (!valid || busy) return
    setBusy(true)
    setError(undefined)
    setResult(undefined)
    try {
      setResult(
        await get<RouteLookupResult>("/network/routing/lookup", {
          target: target.trim(),
          ...(source.trim() ? { source: source.trim() } : {}),
          ...(mark.trim() ? { mark: mark.trim() } : {}),
        }),
      )
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section
      aria-label="Kernel route lookup"
      className="mt-6 space-y-3 border-t border-hairline pt-4"
    >
      <div>
        <p className="text-title font-medium">Ask the kernel</p>
        <p className="text-hint text-muted-foreground">
          The current route for an IPv4 or IPv6 target, with optional source and packet mark. This
          lookup sends no traffic and does not prove a connection succeeds.
        </p>
      </div>
      <form
        onSubmit={(event) => {
          event.preventDefault()
          void run()
        }}
        className="flex flex-wrap items-end gap-3"
      >
        <Field
          label="Target address"
          htmlFor="routing-lookup-target"
          className="w-full min-w-0 sm:w-auto sm:flex-1"
        >
          <Input
            id="routing-lookup-target"
            value={target}
            onChange={(event) => setTarget(event.target.value)}
            placeholder="2001:db8::1"
            className="font-mono text-xs"
          />
        </Field>
        <Field
          label="Source address (optional)"
          htmlFor="routing-lookup-source"
          className="w-full min-w-0 sm:w-auto sm:flex-1"
        >
          <Input
            id="routing-lookup-source"
            value={source}
            onChange={(event) => setSource(event.target.value)}
            className="font-mono text-xs"
          />
        </Field>
        <Field
          label="Packet mark (optional)"
          htmlFor="routing-lookup-mark"
          className="w-full min-w-0 sm:w-auto sm:flex-1"
        >
          <Input
            id="routing-lookup-mark"
            value={mark}
            onChange={(event) => setMark(event.target.value)}
            placeholder="0x80000"
            className="font-mono text-xs"
          />
        </Field>
        <Button type="submit" variant="outline" disabled={!valid || busy} pending={busy}>
          Look up route
        </Button>
      </form>
      {error && (
        <div role="alert" className="space-y-2 text-body text-destructive">
          <p>{error.message}</p>
          <Button size="xs" variant="outline" onClick={() => void run()} disabled={!valid || busy}>
            Retry lookup
          </Button>
        </div>
      )}
      {result && (
        <div aria-live="polite">
          <DetailList>
            <Detail label="Target">
              <span className="font-mono">{result.address}</span>
            </Detail>
            <Detail label="Family">{result.family === "inet6" ? "IPv6" : "IPv4"}</Detail>
            <Detail label="Interface">
              <span className="font-mono">{result.device ?? "not reported"}</span>
            </Detail>
            <Detail label="Source">
              <span className="font-mono">{result.source ?? "not reported"}</span>
            </Detail>
            <Detail label="Gateway">
              <span className="font-mono">
                {result.gateway ?? (result.local ? "local to this host" : "direct or not reported")}
              </span>
            </Detail>
            <Detail label="Table">{result.table ?? "not reported by the kernel"}</Detail>
          </DetailList>
        </div>
      )}
    </section>
  )
}
