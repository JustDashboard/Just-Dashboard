"use client"

import { useEffect, useState } from "react"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import type { Container } from "@/lib/types"
import { Group } from "@/components/panel"
import { Hint } from "@/components/docker/explain"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

export function AttachDialog({
  open,
  networkId,
  networkName,
  onOpenChange,
  onAttached,
  attached,
}: {
  open: boolean
  networkId: string | null
  networkName?: string
  onOpenChange: (open: boolean) => void
  onAttached: () => void
  attached: Set<string>
}) {
  const [containers, setContainers] = useState<Container[]>([])
  const [picked, setPicked] = useState("")
  const [alias, setAlias] = useState("")
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    get<Container[]>("/docker/containers/", undefined, controller.signal)
      .then(setContainers)
      .catch(() => undefined)
    return () => controller.abort()
  }, [open])

  const attach = async () => {
    setBusy(true)
    try {
      await post(`/docker/networks/${networkId}/connect`, {
        container: picked,
        aliases: alias.trim() ? [alias.trim()] : undefined,
      })
      notify.success(`Attached to ${networkName}`)
      onAttached()
      onOpenChange(false)
      setPicked("")
      setAlias("")
    } catch (err) {
      notify.error("Could not attach it", err)
    } finally {
      setBusy(false)
    }
  }

  const available = containers.filter((c) => !attached.has(c.id))

  return (
    <Modal
      open={open}
      onOpenChange={(o) => !busy && onOpenChange(o)}
      size="sm"
      title="Attach a container"
      description={
        <>
          It joins <b>{networkName}</b> immediately, without restarting, and can reach everything
          else on it by name.
        </>
      }
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={attach} disabled={busy || !picked} pending={busy}>
            Attach
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <div className="space-y-1.5">
          <Label htmlFor="attach-container" className="text-xs">
            Container
          </Label>
          <Select value={picked} onValueChange={setPicked}>
            <SelectTrigger id="attach-container" className="w-full">
              <SelectValue placeholder="Pick one" />
            </SelectTrigger>
            <SelectContent>
              {available.map((c) => (
                <SelectItem key={c.id} value={c.id}>
                  {c.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {available.length === 0 && <Hint>Every container is already on this network.</Hint>}
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="attach-alias" className="text-xs">
            Extra name (optional)
          </Label>
          <Input
            id="attach-alias"
            value={alias}
            spellCheck={false}
            className="font-mono text-xs"
            placeholder="db"
            onChange={(e) => setAlias(e.target.value)}
          />
          <Hint>
            An additional hostname the others can use. Useful when an application&apos;s config
            expects a name that is not the container&apos;s.
          </Hint>
        </div>
      </div>
    </Modal>
  )
}

export function NewNetworkDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: () => void
}) {
  const [name, setName] = useState("")
  const [internal, setInternal] = useState(false)
  const [subnet, setSubnet] = useState("")
  const [busy, setBusy] = useState(false)

  const create = async () => {
    setBusy(true)
    try {
      await post("/docker/networks/", { name, internal, subnet: subnet.trim() || undefined })
      notify.success(`${name} created`)
      onCreated()
      onOpenChange(false)
      setName("")
      setSubnet("")
      setInternal(false)
    } catch (err) {
      notify.error("Could not create the network", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(o) => !busy && onOpenChange(o)}
      size="sm"
      title="Create network"
      description="A private network for containers that need to reach each other. On it, a
            container's name is its hostname."
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={create} disabled={busy || !name.trim()} pending={busy}>
            Create
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <div className="space-y-1.5">
          <Label htmlFor="network-name" className="text-xs">
            Name
          </Label>
          <Input
            id="network-name"
            value={name}
            spellCheck={false}
            className="font-mono"
            placeholder="app-internal"
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <Group className="flex items-start gap-3">
          <Switch
            id="network-internal"
            checked={internal}
            onCheckedChange={setInternal}
            className="mt-0.5"
          />
          <label htmlFor="network-internal" className="cursor-pointer">
            <span className="block text-xs font-medium">Cut it off from the internet</span>
            <Hint>
              Containers on this network can reach each other and nothing else. The right choice for
              a database that only needs to talk to the application in front of it.
            </Hint>
          </label>
        </Group>
        <div className="space-y-1.5">
          <Label htmlFor="network-subnet" className="text-xs">
            Subnet (optional)
          </Label>
          <Input
            id="network-subnet"
            value={subnet}
            spellCheck={false}
            className="font-mono text-xs"
            placeholder="Docker picks one"
            onChange={(e) => setSubnet(e.target.value)}
          />
          <Hint>
            Only worth setting if it has to avoid a range already used on your own network — a VPN
            or an office LAN.
          </Hint>
        </div>
      </div>
    </Modal>
  )
}
