"use client"

import Link from "next/link"
import { useState } from "react"
import { Field } from "@/components/form"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ErrorState } from "@/components/state"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useAuth } from "@/hooks/use-auth"
import { post } from "@/lib/api"
import { previewReading, type IPAMPreview } from "@/lib/network-ipam"

import { calcSubnet, prefixRelation, type SubnetInfo } from "./subnet-math"

export function SubnetTool() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const [input, setInput] = useState("")
  const [info, setInfo] = useState<SubnetInfo | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [other, setOther] = useState("")
  const [preview, setPreview] = useState<IPAMPreview>()
  const [previewBusy, setPreviewBusy] = useState(false)
  const [previewError, setPreviewError] = useState<Error>()

  // Pure arithmetic: reading the other prefix never leaves the browser.
  let relation: { sentence?: string; problem?: string } = {}
  if (info && other.trim()) {
    try {
      relation = { sentence: prefixRelation(info.cidr, other).sentence }
    } catch (err) {
      relation = { problem: err instanceof Error ? err.message : "Could not parse that." }
    }
  }

  const run = () => {
    if (!input.trim() || busy) return
    setBusy(true)
    // Async so the spinner paints on the same frame the work lands — the math
    // itself is instant, but a button that never acknowledges the press reads
    // as broken next to nineteen cards that spin.
    setTimeout(() => {
      try {
        setInfo(calcSubnet(input))
        setError(null)
        setPreview(undefined)
        setPreviewError(undefined)
      } catch (err) {
        setInfo(null)
        setError(err instanceof Error ? err.message : "Could not parse that.")
      } finally {
        setBusy(false)
      }
    }, 0)
  }

  const checkIPAM = async () => {
    if (!info || previewBusy) return
    setPreviewBusy(true)
    setPreviewError(undefined)
    try {
      setPreview(await post<IPAMPreview>("/network/ipam/preview", { prefix: info.cidr }))
    } catch (err) {
      setPreviewError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setPreviewBusy(false)
    }
  }
  const reading = preview ? previewReading(preview) : undefined

  return (
    <Panel plain>
      <PanelHeader title="Subnet calc" />
      <PanelBody className="space-y-2.5">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <Input
            id="tool-subnet-input"
            aria-label="Address with a prefix"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && input.trim() && run()}
            placeholder="192.168.1.20/24 or 2001:db8::1/64"
            className="h-8 min-w-0 flex-1 font-mono text-xs"
          />
          <Button size="sm" onClick={run} disabled={busy || !input.trim()} pending={busy}>
            Run
          </Button>
          {info && (
            <Button size="sm" variant="ghost" onClick={() => setInfo(null)}>
              Clear
            </Button>
          )}
        </div>

        {error && (
          <p role="alert" className="text-xs text-destructive">
            {error}
          </p>
        )}

        {info && (
          <DetailList>
            <Detail label="Network">
              <span className="font-mono">{info.cidr}</span>
            </Detail>
            <Detail label="Netmask">
              <span className="font-mono">{info.mask}</span>
            </Detail>
            <Detail label="Wildcard">
              <span className="font-mono">{info.wildcard}</span>
            </Detail>
            <Detail label="Network address">
              <span className="font-mono">{info.network}</span>
            </Detail>
            <Detail label="Broadcast">
              <span className="font-mono">{info.broadcast}</span>
            </Detail>
            <Detail label="Address range">
              <span className="font-mono">
                {info.first} → {info.last}
              </span>
            </Detail>
            <Detail label="Hosts">
              <span className="font-mono">{info.hosts}</span>
            </Detail>
            <Detail label="Scope">{info.note}</Detail>
          </DetailList>
        )}

        {info && (
          <section aria-label="Overlap" className="space-y-3 border-t border-hairline pt-4">
            <h3 className="eyebrow">Overlap</h3>
            <Field
              label="Compare with another prefix"
              htmlFor="tool-subnet-other"
              error={relation.problem}
            >
              <Input
                id="tool-subnet-other"
                value={other}
                onChange={(event) => setOther(event.target.value)}
                placeholder="10.0.0.0/8"
                className="font-mono"
                aria-invalid={Boolean(relation.problem)}
              />
            </Field>
            {relation.sentence && (
              <p className="text-body" aria-live="polite">
                {relation.sentence}
              </p>
            )}
            {admin ? (
              <div className="space-y-2">
                <div className="flex flex-wrap gap-2">
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => void checkIPAM()}
                    disabled={previewBusy}
                    pending={previewBusy}
                  >
                    Check shared IPAM
                  </Button>
                  <Button asChild size="sm" variant="ghost">
                    <Link href="/network/ipam">Plan in IPAM</Link>
                  </Button>
                </div>
                {previewError && <ErrorState error={previewError} />}
                {preview && reading && (
                  <div className="space-y-1.5 text-body" aria-label="IPAM overlap">
                    <p className="font-medium">
                      {reading.label}: <span className="font-mono">{preview.prefix}</span>
                    </p>
                    <p className="text-muted-foreground">{reading.detail}</p>
                    {preview.conflicts.map((conflict, index) => (
                      <p key={index} className="font-mono text-hint break-all">
                        {conflict.prefix} · {conflict.owner} · {conflict.resource} ·{" "}
                        {conflict.basis}
                      </p>
                    ))}
                    <p className="text-hint text-muted-foreground">
                      Reserving it in IPAM rechecks overlap and holds the prefix for the resource
                      that will use it.
                    </p>
                  </div>
                )}
              </div>
            ) : (
              <p className="text-hint text-muted-foreground">
                Checking against shared address planning needs the admin capability; the comparison
                above stays in this browser.
              </p>
            )}
          </section>
        )}
      </PanelBody>
    </Panel>
  )
}
