"use client"

import { useState } from "react"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

import { calcSubnet, type SubnetInfo } from "./subnet-math"

export function SubnetTool() {
  const [input, setInput] = useState("")
  const [info, setInfo] = useState<SubnetInfo | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

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
      } catch (err) {
        setInfo(null)
        setError(err instanceof Error ? err.message : "Could not parse that.")
      } finally {
        setBusy(false)
      }
    }, 0)
  }

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
      </PanelBody>
    </Panel>
  )
}
