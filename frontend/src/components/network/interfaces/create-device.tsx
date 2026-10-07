"use client"

import { useState } from "react"
import { Bridge, Hash, Linked, Servers, type Icon } from "@/components/icons"
import { post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { NetworkLink } from "@/lib/types"
import { Modal } from "@/components/modal"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { Field, FieldRow } from "@/components/form"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

type Kind = "bridge" | "vlan" | "vxlan" | "gre" | "gretap" | "dummy" | "macvlan"

const KINDS: { kind: Kind; title: string; mark: Icon; hint: string }[] = [
  {
    kind: "bridge",
    title: "Bridge",
    mark: Bridge,
    hint: "A private network of its own, for VMs, namespaces or a VLAN",
  },
  {
    kind: "vlan",
    title: "VLAN",
    mark: Hash,
    hint: "A tagged network on a card that carries several",
  },
  {
    kind: "vxlan",
    title: "VXLAN",
    mark: Linked,
    hint: "A layer-2 network stretched to another server over UDP",
  },
  {
    kind: "gre",
    title: "GRE tunnel",
    mark: Linked,
    hint: "A routed tunnel to another server, with no encryption",
  },
  {
    kind: "gretap",
    title: "GRE tap",
    mark: Linked,
    hint: "GRE carrying Ethernet frames, to bridge two sites",
  },
  {
    kind: "dummy",
    title: "Dummy",
    mark: Servers,
    hint: "An address that is always up, for a service to bind to",
  },
  {
    kind: "macvlan",
    title: "Macvlan",
    mark: Hash,
    hint: "A second MAC address on a card, seen by the LAN as its own machine",
  },
]

/** What the create route takes: a device as the spec keeps it. */
type LinkRequest = {
  name: string
  kind: Kind
  parent?: string
  vlanId?: number
  vni?: number
  local?: string
  remote?: string
  port?: number
  ttl?: number
  key?: number
  mode?: string
  mtu?: number
  stp?: boolean
  addresses?: string[]
  up: boolean
}

/**
 * A new device, of one of the kinds the kernel makes without any software
 * beyond iproute2. The kind is picked from cards and the form below them
 * asks only what that kind needs; the name is suggested and checked against
 * the kernel's rule (fifteen characters) before it is sent. What is made is
 * written into the spec and made again at every boot.
 */
export function CreateDevice({
  open,
  onOpenChange,
  links,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  links: NetworkLink[]
  onCreated: (name: string) => void
}) {
  const [kind, setKind] = useState<Kind>("bridge")
  const [name, setName] = useState("")
  const [parent, setParent] = useState("")
  const [vlanId, setVlanId] = useState("")
  const [vni, setVni] = useState("")
  const [local, setLocal] = useState("")
  const [remote, setRemote] = useState("")
  const [port, setPort] = useState("4789")
  const [mode, setMode] = useState("bridge")
  const [address, setAddress] = useState("")
  const [mtu, setMtu] = useState("")
  const [stp, setStp] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  // Cards that carry traffic out: what a VLAN, a macvlan or a VXLAN can ride on.
  const parents = links.filter(
    (l) => l.role === "uplink" || l.role === "physical" || l.kind === "bond",
  )
  const suggested = suggestName(kind, links, vlanId, vni)
  const finalName = name.trim() || suggested
  const nameError =
    finalName.length > 15
      ? "Fifteen characters at most, the kernel's limit"
      : links.some((l) => l.name === finalName)
        ? "A device already has this name"
        : undefined
  const needsParent = kind === "vlan" || kind === "macvlan"
  const ready =
    !nameError &&
    (!needsParent || parent) &&
    (kind !== "vlan" || vlanId) &&
    (kind !== "vxlan" || (vni && (remote || parent))) &&
    (!kind.startsWith("gre") || remote)

  const submit = async () => {
    const body: LinkRequest = { name: finalName, kind, up: true }
    if (needsParent || (kind === "vxlan" && parent)) body.parent = parent
    if (kind === "vlan") body.vlanId = Number(vlanId)
    if (kind === "vxlan") {
      body.vni = Number(vni)
      body.port = Number(port) || 4789
    }
    if (kind === "vxlan" || kind.startsWith("gre")) {
      if (local.trim()) body.local = local.trim()
      if (remote.trim()) body.remote = remote.trim()
    }
    if (kind === "macvlan") body.mode = mode
    if (kind === "bridge") body.stp = stp
    if (mtu.trim()) body.mtu = Number(mtu)
    if (address.trim()) body.addresses = [address.trim()]
    setBusy(true)
    setError(undefined)
    try {
      await post("/network/links", body)
      notify.success(`${finalName} created`, { description: "It is made again at every boot." })
      onOpenChange(false)
      onCreated(finalName)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="lg"
      title="New device"
      description="Create a bridge, VLAN, tunnel or virtual device on this server"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!ready || busy} pending={busy}>
            Create {finalName}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        <ChoiceGrid columns={3}>
          {KINDS.map((k, index) => (
            <ChoiceCard
              key={k.kind}
              index={index}
              verb={`Make a ${k.title}`}
              selected={kind === k.kind}
              onClick={() => setKind(k.kind)}
              mark={k.mark}
              title={k.title}
              description={k.hint}
            />
          ))}
        </ChoiceGrid>

        <FieldRow>
          <Field
            label="Name"
            htmlFor="device-name"
            error={nameError}
            hint="Up to fifteen characters"
          >
            <Input
              id="device-name"
              value={name}
              placeholder={suggested}
              onChange={(event) => setName(event.target.value)}
              className="font-mono"
            />
          </Field>
          {needsParent || kind === "vxlan" ? (
            <Field
              label={kind === "vxlan" ? "Send from" : "Rides on"}
              htmlFor="device-parent"
              hint={kind === "vxlan" ? "Optional: the card the tunnel leaves through" : undefined}
            >
              <Select value={parent} onValueChange={setParent}>
                <SelectTrigger id="device-parent" className="w-full">
                  <SelectValue placeholder="Choose a network card" />
                </SelectTrigger>
                <SelectContent>
                  {parents.map((l) => (
                    <SelectItem key={l.name} value={l.name}>
                      {l.name}
                      {l.uplink ? " · uplink" : ""}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          ) : (
            <Field label="MTU" htmlFor="device-mtu" hint="Leave empty for the kernel's default">
              <Input
                id="device-mtu"
                inputMode="numeric"
                value={mtu}
                placeholder={kind.startsWith("gre") ? "1476" : "1500"}
                onChange={(event) => setMtu(event.target.value)}
              />
            </Field>
          )}
        </FieldRow>

        {kind === "vlan" && (
          <Field label="VLAN id" htmlFor="device-vlan" hint="1 to 4094, as the switch tags it">
            <Input
              id="device-vlan"
              inputMode="numeric"
              value={vlanId}
              placeholder="30"
              onChange={(event) => setVlanId(event.target.value)}
            />
          </Field>
        )}
        {kind === "vxlan" && (
          <FieldRow>
            <Field
              label="VNI"
              htmlFor="device-vni"
              hint="The network's number, the same at both ends"
            >
              <Input
                id="device-vni"
                inputMode="numeric"
                value={vni}
                placeholder="42"
                onChange={(event) => setVni(event.target.value)}
              />
            </Field>
            <Field label="UDP port" htmlFor="device-port">
              <Input
                id="device-port"
                inputMode="numeric"
                value={port}
                onChange={(event) => setPort(event.target.value)}
              />
            </Field>
          </FieldRow>
        )}
        {(kind === "vxlan" || kind.startsWith("gre")) && (
          <FieldRow>
            <Field label="Remote end" htmlFor="device-remote" hint="The other server's address">
              <Input
                id="device-remote"
                value={remote}
                placeholder="198.51.100.7"
                onChange={(event) => setRemote(event.target.value)}
                className="font-mono"
              />
            </Field>
            <Field
              label="Local end"
              htmlFor="device-local"
              hint="Optional: this server's address to send from"
            >
              <Input
                id="device-local"
                value={local}
                placeholder="any"
                onChange={(event) => setLocal(event.target.value)}
                className="font-mono"
              />
            </Field>
          </FieldRow>
        )}
        {kind === "macvlan" && (
          <Field label="Mode" htmlFor="device-mode">
            <Select value={mode} onValueChange={setMode}>
              <SelectTrigger id="device-mode" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="bridge">Bridge — talk to each other directly</SelectItem>
                <SelectItem value="private">Private — never to each other</SelectItem>
                <SelectItem value="vepa">VEPA — through the switch</SelectItem>
                <SelectItem value="passthru">Passthru — one device takes the card</SelectItem>
              </SelectContent>
            </Select>
          </Field>
        )}
        <FieldRow>
          <Field
            label="Address"
            htmlFor="device-address"
            hint="Optional: this server's address on the new network, in CIDR form"
          >
            <Input
              id="device-address"
              value={address}
              placeholder={kind === "bridge" ? "192.168.50.1/24" : "10.20.0.1/24"}
              onChange={(event) => setAddress(event.target.value)}
              className="font-mono"
            />
          </Field>
          {kind === "bridge" && (
            <Field
              label="Spanning tree"
              hint="On where the bridge joins two switches that also reach each other"
            >
              <label className="flex h-9 items-center gap-2 text-body">
                <Switch checked={stp} onCheckedChange={setStp} aria-label="Spanning tree" />
                {stp ? "On" : "Off"}
              </label>
            </Field>
          )}
        </FieldRow>
        {kind.startsWith("gre") && (
          <Notice title="Not encrypted">
            GRE carries the packets as they are. Across the internet, put it inside WireGuard or use
            WireGuard instead.
          </Notice>
        )}
        {error && (
          <p role="alert" className="animate-rise text-body text-destructive">
            {error}
          </p>
        )}
      </div>
    </Modal>
  )
}

/** The next free name of the shape people give a kind: lan0, vlan30, vx42, gre0. */
function suggestName(kind: Kind, links: NetworkLink[], vlanId: string, vni: string) {
  const taken = new Set(links.map((l) => l.name))
  if (kind === "vlan" && vlanId) return `vlan${vlanId}`
  if (kind === "vxlan" && vni) return `vx${vni}`
  const stem = {
    bridge: "lan",
    vlan: "vlan",
    vxlan: "vx",
    gre: "gre-",
    gretap: "gtap",
    dummy: "dummy",
    macvlan: "mac",
  }[kind]
  for (let i = kind === "gre" ? 1 : 0; i < 100; i++) {
    const candidate = `${stem}${i}`
    if (!taken.has(candidate)) return candidate
  }
  return `${stem}x`
}
