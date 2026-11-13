"use client"

import { useAuth } from "@/hooks/use-auth"
import { useState } from "react"
import { Box, Layers, Plus, Trash } from "@/components/icons"
import { del, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { NetworkLink, NetworkNamespace } from "@/lib/types"
import { plural } from "@/lib/format"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Modal } from "@/components/modal"
import { Field, FieldRow } from "@/components/form"
import { EmptyState } from "@/components/state"
import { Tag } from "@/components/tag"
import { VerbMenu } from "@/components/verbs"
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

const NONE = "__none"

/**
 * The machine's other network stacks: the named namespaces (`ip netns`) —
 * the dashboard's own, with their veth into a bridge, removable — and each
 * running container's, read and never changed, because Docker owns them.
 * A namespace is a separate network: its own devices, addresses, routes and
 * firewall. Each is one card with how many devices it holds, and opens the
 * sheet that reads it, so "what does this container see" is answered without
 * a shell.
 */
export function Namespaces({
  namespaces,
  links,
  onChanged,
  onOpen,
}: {
  namespaces: NetworkNamespace[] | undefined
  links: NetworkLink[]
  onChanged: () => void
  /** Opens a namespace's own reading: its routes, resolver and listeners. */
  onOpen: (ns: NetworkNamespace) => void
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
  const shown = [...named, ...(showContainers ? containers : [])]
  return (
    <Panel plain>
      <PanelHeader
        title="Namespaces"
        actions={
          <span className="flex items-center gap-3">
            <span className="numeric text-hint text-muted-foreground">{shown.length}</span>
            <label className="flex items-center gap-2 text-hint text-muted-foreground">
              Containers&rsquo; too
              <Switch
                checked={showContainers}
                onCheckedChange={setShowContainers}
                aria-label="Show containers' namespaces"
              />
            </label>
            {admin && (
              <Button size="xs" variant="outline" onClick={() => setCreating(true)}>
                <Plus aria-hidden />
                New namespace
              </Button>
            )}
          </span>
        }
      />
      <PanelBody>
        {shown.length === 0 ? (
          <EmptyState
            icon={Layers}
            title="No named namespace"
            description="A separate network on this machine, for a service that must reach only a bridge or a test that must not touch the real one."
            action={
              admin ? (
                <Button size="sm" variant="outline" onClick={() => setCreating(true)}>
                  <Plus aria-hidden />
                  New namespace
                </Button>
              ) : undefined
            }
          />
        ) : (
          <ChoiceList aria-label="Namespaces">
            {shown.map((ns) => (
              <NamespaceRow
                key={`${ns.kind}:${ns.name}`}
                namespace={ns}
                onOpen={() => onOpen(ns)}
                onDelete={admin && can("destructive") && ns.managed ? () => remove(ns) : undefined}
              />
            ))}
          </ChoiceList>
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
 * One namespace as a card of the list's one height: what runs in it, its
 * name, the devices inside with where the first answers, and how many there
 * are. Its routes, resolver and listeners are its sheet's; a namespace whose
 * devices could not be read says so on the line rather than reading as empty.
 */
function NamespaceRow({
  namespace: ns,
  onOpen,
  onDelete,
}: {
  namespace: NetworkNamespace
  onOpen: () => void
  onDelete?: () => void
}) {
  const first = ns.devices[0]
  return (
    <ChoiceRow
      leading={
        <ProductLogo
          id={ns.image ? imageProduct(ns.image) : undefined}
          size="sm"
          fallback={ns.kind === "named" ? Layers : Box}
        />
      }
      title={
        <span className="inline-flex min-w-0 items-center gap-2">
          <span className="truncate font-mono">{ns.name}</span>
          {ns.managed && <Tag>made here</Tag>}
          {ns.kind === "container" && <Tag>container</Tag>}
        </span>
      }
      verb={`Inspect ${ns.name}`}
      description={
        ns.readError ? (
          <span className="text-warning" title={ns.readError}>
            devices unknown
          </span>
        ) : first ? (
          <span className="inline-flex min-w-0 items-center gap-2">
            <span className="font-mono">{first.name}</span>
            {first.addresses[0] ? <Cidr cidr={first.addresses[0]} /> : <span>no address</span>}
            {ns.pid ? <span className="truncate">· pid {ns.pid}</span> : null}
          </span>
        ) : (
          <span>loopback only{ns.pid ? ` · pid ${ns.pid}` : ""}</span>
        )
      }
      trailing={
        <span
          data-slot="namespace-members"
          className="numeric w-20 text-right text-hint text-muted-foreground"
        >
          {ns.readError ? "unread" : plural(ns.devices.length, "device")}
        </span>
      }
      actions={
        onDelete ? (
          <VerbMenu
            label={`More for ${ns.name}`}
            verbs={[{ key: "delete", label: "Delete", icon: Trash, run: onDelete, danger: true }]}
          />
        ) : (
          // The menu's place, so every card's count stands in one column.
          <span aria-hidden className="size-8" />
        )
      }
      onSelect={onOpen}
    />
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
