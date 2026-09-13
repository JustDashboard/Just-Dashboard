"use client"

import { useState } from "react"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

type SubnetInfo = {
  cidr: string
  mask: string
  wildcard: string
  network: string
  broadcast: string
  first: string
  last: string
  hosts: string
  note: string
}

function parseOctets(s: string): number[] | null {
  const parts = s.trim().split(".")
  if (parts.length !== 4) return null
  const nums = parts.map((p) => {
    if (!/^\d{1,3}$/.test(p.trim())) return -1
    return Number(p.trim())
  })
  if (nums.some((n) => n < 0 || n > 255)) return null
  return nums
}

function toDotted(n: number): string {
  return [(n >>> 24) & 255, (n >>> 16) & 255, (n >>> 8) & 255, n & 255].join(".")
}

function toNum(octets: number[]): number {
  return ((octets[0] << 24) | (octets[1] << 16) | (octets[2] << 8) | octets[3]) >>> 0
}

/**
 * The subnet calculator: pure arithmetic in the browser, no probe — a mask
 * is math, not traffic, so it answers instantly and works offline.
 */
export function calcSubnet(input: string): SubnetInfo {
  const m = input.trim().match(/^(.+?)\s*\/\s*(\d{1,2})$/)
  if (!m) throw new Error('Write it as an address with a prefix, like "192.168.1.20/24".')
  const octets = parseOctets(m[1])
  const prefix = Number(m[2])
  if (!octets || prefix > 32) throw new Error("That is not an IPv4 address with a /0–/32 prefix.")
  const addr = toNum(octets)
  const mask = prefix === 0 ? 0 : (0xffffffff << (32 - prefix)) >>> 0
  const network = (addr & mask) >>> 0
  const broadcast = (network | ~mask) >>> 0
  const size = 2 ** (32 - prefix)
  let first: string
  let last: string
  let hosts: string
  if (prefix === 32) {
    first = toDotted(network)
    last = first
    hosts = "1 (a single host route)"
  } else if (prefix === 31) {
    // RFC 3021: a point-to-point link has no broadcast, both are usable.
    first = toDotted(network)
    last = toDotted(broadcast)
    hosts = "2 (point-to-point, both usable)"
  } else {
    first = toDotted(network + 1)
    last = toDotted(broadcast - 1)
    hosts = `${(size - 2).toLocaleString("en-US")} usable`
  }
  const firstOctet = octets[0]
  const secondOctet = octets[1]
  let note = "Public address space."
  if (
    firstOctet === 10 ||
    (firstOctet === 172 && secondOctet >= 16 && secondOctet <= 31) ||
    (firstOctet === 192 && secondOctet === 168)
  ) {
    note = "Private (RFC 1918) — not routable on the internet."
  } else if (firstOctet === 127) {
    note = "Loopback — this machine only."
  } else if (firstOctet === 169 && secondOctet === 254) {
    note = "Link-local — one broadcast domain, no router."
  } else if (firstOctet >= 224 && firstOctet <= 239) {
    note = "Multicast — not a host address."
  }
  return {
    cidr: `${toDotted(network)}/${prefix}`,
    mask: toDotted(mask),
    wildcard: toDotted(~mask >>> 0),
    network: toDotted(network),
    broadcast: toDotted(broadcast),
    first,
    last,
    hosts,
    note,
  }
}

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
    <Panel>
      <PanelHeader title="Subnet calc" />
      <PanelBody className="space-y-2.5">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <Input
            id="tool-subnet-input"
            aria-label="Address with a prefix"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && input.trim() && run()}
            placeholder="192.168.1.20/24"
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

        {error && <p className="text-xs text-destructive">{error}</p>}

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
            <Detail label="Usable range">
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
