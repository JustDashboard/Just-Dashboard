"use client"

import { useState } from "react"
import { post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { WGInterface } from "@/lib/types"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { Field, FieldRow } from "@/components/form"
import { ProductGlyph } from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"

/** What a client is told to resolve names with: a public resolver, or one that also blocks ads. */
const RESOLVERS = [
  {
    id: "cloudflare",
    name: "Cloudflare",
    product: "cloudflare",
    servers: ["1.1.1.1", "1.0.0.1"],
    hint: "fast, no filtering",
  },
  {
    id: "quad9",
    name: "Quad9",
    product: "quad9",
    servers: ["9.9.9.9", "149.112.112.112"],
    hint: "blocks known malware",
  },
  {
    id: "adguard",
    name: "AdGuard DNS",
    product: "adguard",
    servers: ["94.140.14.14", "94.140.15.15"],
    hint: "blocks ads and trackers",
  },
  {
    id: "mullvad",
    name: "Mullvad",
    product: "mullvad",
    servers: ["194.242.2.3"],
    hint: "blocks ads, no logs",
  },
]

/**
 * A WireGuard server in one step. Everything has a default the server
 * picks — the first free name and port, a /24 that collides with nothing
 * here, this server's public address as the endpoint — so the one decision
 * left is whether its clients' internet goes out through here, and which
 * resolver they are told to use. The server opens the port in the firewall
 * when the firewall would otherwise refuse it, and says so either way.
 */
export function WireGuardSetup({ onCreated }: { onCreated: (tunnel: WGInterface) => void }) {
  const [endpoint, setEndpoint] = useState("")
  const [port, setPort] = useState("")
  const [subnet, setSubnet] = useState("")
  const [resolver, setResolver] = useState("cloudflare")
  const [exitNode, setExitNode] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const made = await post<{
        interface: WGInterface
        warnings: string[]
        firewall: { opened: boolean; reason?: string }
      }>("/network/vpn/wireguard", {
        endpoint: endpoint.trim() || undefined,
        port: port ? Number(port) : undefined,
        subnet: subnet.trim() || undefined,
        dns: RESOLVERS.find((r) => r.id === resolver)?.servers,
        exitNode,
      })
      notify.success(`${made.interface.name} is up on udp ${made.interface.listenPort}`, {
        description: made.firewall.opened
          ? "The firewall now admits its port."
          : made.firewall.reason,
      })
      for (const w of made.warnings) notify.warning(w)
      onCreated(made.interface)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-w-0 flex-col gap-5">
      <ChoiceGrid columns={2}>
        <ChoiceCard
          verb="A VPN to browse through"
          selected={exitNode}
          onClick={() => setExitNode(true)}
          title="A VPN to browse through"
          description="Phones and laptops send all their traffic out through this server"
        />
        <ChoiceCard
          verb="A private network"
          selected={!exitNode}
          onClick={() => setExitNode(false)}
          title="A private network"
          description="Devices and sites reach this server and each other, nothing more"
        />
      </ChoiceGrid>
      <div>
        <p className="mb-2 text-body font-medium">Names are resolved with</p>
        <ChoiceGrid columns="compact">
          {RESOLVERS.map((r) => (
            <ChoiceCard
              key={r.id}
              verb={`Resolve with ${r.name}`}
              selected={resolver === r.id}
              onClick={() => setResolver(r.id)}
              logo={<ProductGlyph id={r.product} />}
              title={r.name}
              description={r.hint}
            />
          ))}
        </ChoiceGrid>
      </div>
      <FieldRow>
        <Field
          label="Endpoint"
          htmlFor="wg-endpoint"
          hint="Where clients dial; this server's public address when empty"
        >
          <Input
            id="wg-endpoint"
            value={endpoint}
            placeholder="vpn.example.com"
            onChange={(event) => setEndpoint(event.target.value)}
            className="font-mono"
          />
        </Field>
        <Field label="Port" htmlFor="wg-port" hint="UDP; the first free from 51820 when empty">
          <Input
            id="wg-port"
            inputMode="numeric"
            value={port}
            placeholder="51820"
            onChange={(event) => setPort(event.target.value)}
          />
        </Field>
      </FieldRow>
      <FieldRow>
        <Field label="Tunnel network" htmlFor="wg-subnet" hint="A /24 nothing here uses when empty">
          <Input
            id="wg-subnet"
            value={subnet}
            placeholder="10.8.0.0/24"
            onChange={(event) => setSubnet(event.target.value)}
            className="font-mono"
          />
        </Field>
        <Field label="Exit node" hint="Masquerade the tunnel's traffic out of the uplink">
          <label className="flex h-9 items-center gap-2 text-body">
            <Switch checked={exitNode} onCheckedChange={setExitNode} aria-label="Exit node" />
            {exitNode ? "On" : "Off"}
          </label>
        </Field>
      </FieldRow>
      {error && (
        <p role="alert" className="animate-rise text-body text-destructive">
          {error}
        </p>
      )}
      <div className="flex justify-end">
        <Button onClick={submit} pending={busy} disabled={busy}>
          Set up WireGuard
        </Button>
      </div>
    </div>
  )
}
