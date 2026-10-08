"use client"

import { useAuth } from "@/hooks/use-auth"
import { useState } from "react"
import { Box, Layers, Plus, Trash } from "@/components/icons"
import { del, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { NetworkLink, NetworkNamespace } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Modal } from "@/components/modal"
import { Field, FieldRow } from "@/components/form"
import { Tag } from "@/components/tag"
import { ProductLogo, imageProduct } from "@/components/product-logo"
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
import { useConfirm } from "@/components/confirm-dialog"
import { Cidr } from "@/components/network/address"
import { LinkGlyph } from "@/components/network/marks"

const NONE = "__none"

/**
 * The machine's other network stacks: the named namespaces (`ip netns`) —
 * the dashboard's own, with their veth into a bridge, removable — and each
 * running container's, read and never changed, because Docker owns them.
 * A namespace is a separate network: its own devices, addresses, routes and
 * firewall. What each holds is listed under it, so "what does this container
 * see" is answered without a shell.
 */
export function Namespaces({
  namespaces,
  links,
  onChanged,
}: {
  namespaces: NetworkNamespace[] | undefined
  links: NetworkLink[]
  onChanged: () => void
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const { confirm, dialog } = useConfirm()
  const [creating, setCreating] = useState(false)
  const [showContainers, setShowContainers] = useState(false)
  if (!namespaces) return null
  const named = namespaces.filter((n) => n.kind === "named")
  const containers = namespaces.filter((n) => n.kind === "container")
  const remove = (ns: NetworkNamespace) =>
    confirm({
      title: `Delete namespace ${ns.name}`,
      confirmLabel: "Delete",
      description: (
        <p>
          The namespace and the devices the dashboard made into it go now, and are not made again at
          boot. Anything running inside it loses its network.
        </p>
      ),
      action: async () => {
        await del(`/network/namespaces/${encodeURIComponent(ns.name)}`)
        notify.success(`${ns.name} deleted`)
        onChanged()
      },
    })
  return (
    <Panel plain>
      <PanelHeader
        title="Namespaces"
        actions={
          <span className="flex items-center gap-3">
            <label className="flex items-center gap-2 text-hint text-muted-foreground">
              Containers&rsquo; too
              <Switch
                checked={showContainers}
                onCheckedChange={setShowContainers}
                aria-label="Show containers' namespaces"
              />
            </label>
            <Button size="xs" variant="outline" onClick={() => setCreating(true)} disabled={!admin}>
              <Plus aria-hidden />
              New namespace
            </Button>
          </span>
        }
      />
      <PanelBody>
        {named.length === 0 && !showContainers ? (
          <p className="text-body text-muted-foreground">
            No named namespace. One is a separate network on this machine — for a service that must
            reach only a bridge, or a test that must not touch the real one.
          </p>
        ) : (
          <ul className="grid min-w-0 gap-x-10 gap-y-5 lg:grid-cols-2">
            {[...named, ...(showContainers ? containers : [])].map((ns) => (
              <li key={`${ns.kind}:${ns.name}`} className="min-w-0">
                <div className="flex min-w-0 items-center gap-3">
                  <ProductLogo
                    id={ns.image ? imageProduct(ns.image) : undefined}
                    size="sm"
                    fallback={ns.kind === "named" ? Layers : Box}
                  />
                  <div className="min-w-0 flex-1">
                    <p className="flex items-center gap-2 text-body font-medium">
                      <span className="truncate font-mono">{ns.name}</span>
                      {ns.managed && <Tag>made here</Tag>}
                      {ns.kind === "container" && <Tag>container</Tag>}
                    </p>
                    <p className="truncate text-hint text-muted-foreground">
                      {ns.devices.length} device{ns.devices.length === 1 ? "" : "s"}
                      {ns.pid ? ` · pid ${ns.pid}` : ""}
                    </p>
                  </div>
                  {admin && can("destructive") && ns.managed && (
                    <Button
                      size="icon-xs"
                      variant="ghost"
                      aria-label={`Delete namespace ${ns.name}`}
                      onClick={() => remove(ns)}
                    >
                      <Trash aria-hidden />
                    </Button>
                  )}
                </div>
                <ul className="mt-2 flex flex-col gap-1 border-l border-hairline pl-4">
                  {ns.devices.map((d) => (
                    <li key={d.name} className="flex min-w-0 items-center gap-2 text-hint">
                      <LinkGlyph kind="veth" className="size-3 shrink-0 text-muted-foreground" />
                      <span className="font-mono">{d.name}</span>
                      <span className="flex min-w-0 gap-2 truncate">
                        {d.addresses.length ? (
                          d.addresses.map((a) => <Cidr key={a} cidr={a} />)
                        ) : (
                          <span className="text-muted-foreground">no address</span>
                        )}
                      </span>
                      <span className="ml-auto shrink-0 text-muted-foreground">{d.state}</span>
                    </li>
                  ))}
                </ul>
              </li>
            ))}
          </ul>
        )}
      </PanelBody>
      <CreateNamespace
        open={creating && admin}
        onOpenChange={setCreating}
        links={links}
        onCreated={onChanged}
      />
      {dialog}
    </Panel>
  )
}

/**
 * A namespace, and optionally the veth pair that joins it to this machine:
 * one end stays here (and may become a port of a bridge), the other goes
 * inside with an address of its own.
 */
function CreateNamespace({
  open,
  onOpenChange,
  links,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  links: NetworkLink[]
  onCreated: () => void
}) {
  const [name, setName] = useState("")
  const [joined, setJoined] = useState(true)
  const [bridge, setBridge] = useState(NONE)
  const [hostAddress, setHostAddress] = useState("")
  const [peerAddress, setPeerAddress] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const bridges = links.filter((l) => l.role === "bridge" && l.owner !== "docker")
  const stem = name.trim().slice(0, 10) || "ns"
  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await post("/network/namespaces", {
        name: name.trim(),
        veth: joined
          ? {
              hostName: `${stem}-h`,
              peerName: `${stem}-n`,
              bridge: bridge === NONE ? undefined : bridge,
              hostAddress: bridge === NONE && hostAddress.trim() ? hostAddress.trim() : undefined,
              peerAddress: peerAddress.trim() || undefined,
            }
          : undefined,
      })
      notify.success(`${name.trim()} created`, { description: "It is made again at every boot." })
      onOpenChange(false)
      onCreated()
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
      title="New namespace"
      description="A separate network stack on this machine"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!name.trim() || busy} pending={busy}>
            Create namespace
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        <Field label="Name" htmlFor="ns-name" hint="Letters, digits, - _ .">
          <Input
            id="ns-name"
            value={name}
            placeholder="lab"
            onChange={(event) => setName(event.target.value)}
            className="font-mono"
          />
        </Field>
        <Field
          label="Join it to this machine"
          hint={`A veth pair: ${stem}-h stays here, ${stem}-n goes inside`}
        >
          <label className="flex h-9 items-center gap-2 text-body">
            <Switch
              checked={joined}
              onCheckedChange={setJoined}
              aria-label="Join with a veth pair"
            />
            {joined ? "With a veth pair" : "Empty — loopback only"}
          </label>
        </Field>
        {joined && (
          <>
            <Field
              label="Port of a bridge"
              htmlFor="ns-bridge"
              hint="Or keep the host end on its own"
            >
              <Select value={bridge} onValueChange={setBridge}>
                <SelectTrigger id="ns-bridge" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NONE}>Its own address here</SelectItem>
                  {bridges.map((b) => (
                    <SelectItem key={b.name} value={b.name}>
                      {b.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <FieldRow>
              {bridge === NONE && (
                <Field label="Address here" htmlFor="ns-host" hint="This machine's end">
                  <Input
                    id="ns-host"
                    value={hostAddress}
                    placeholder="10.60.0.1/30"
                    onChange={(event) => setHostAddress(event.target.value)}
                    className="font-mono"
                  />
                </Field>
              )}
              <Field label="Address inside" htmlFor="ns-peer" hint="The namespace's end">
                <Input
                  id="ns-peer"
                  value={peerAddress}
                  placeholder={bridge === NONE ? "10.60.0.2/30" : "192.168.50.20/24"}
                  onChange={(event) => setPeerAddress(event.target.value)}
                  className="font-mono"
                />
              </Field>
            </FieldRow>
          </>
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
