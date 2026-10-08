"use client"

import { useCallback, useMemo, useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { Modal } from "@/components/modal"
import { Group } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { Hint } from "./explain"
import { EMPTY_NETWORK, networkCreation, type NetworkDraft } from "./network-create-reading"
import { IPAMReservationPicker } from "@/components/network/ipam-reservation-picker"
import type { IPAMReservation } from "@/lib/network-ipam"

export function NewNetworkDialog({
  open,
  onOpenChange,
  onCreated,
  networks,
  inventoryError,
  refreshInventory,
  initialReservationId,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: () => void
  networks: { name: string; subnets: string[] }[]
  inventoryError?: Error
  refreshInventory: () => void
  initialReservationId?: string
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const [draft, setDraft] = useState<NetworkDraft>(EMPTY_NETWORK)
  const [advanced, setAdvanced] = useState(false)
  const [busy, setBusy] = useState(false)
  const [reservations, setReservations] = useState<IPAMReservation[]>([])
  const [ipamUnavailable, setIPAMUnavailable] = useState(Boolean(initialReservationId))
  const [ipamRefreshKey, setIPAMRefreshKey] = useState(0)
  const reading = useMemo(() => {
    try {
      return { spec: networkCreation(draft, networks), error: "" }
    } catch (error) {
      return { spec: undefined, error: (error as Error).message }
    }
  }, [draft, networks])
  const update = <K extends keyof NetworkDraft>(key: K, value: NetworkDraft[K]) => {
    if (key === "name") setReservations([])
    if (key === "subnet") setReservations((rows) => rows.filter((row) => row.family !== "inet"))
    if (key === "ipv6Subnet" || (key === "ipv6" && !value))
      setReservations((rows) => rows.filter((row) => row.family !== "inet6"))
    setDraft((previous) => ({ ...previous, [key]: value }))
  }
  const selectReservation = useCallback(
    (row: IPAMReservation | undefined, family: "inet" | "inet6") => {
      setReservations((rows) => [
        ...rows.filter(
          (existing) => existing.family !== family && (!row || existing.resource === row.resource),
        ),
        ...(row ? [row] : []),
      ])
      if (!row) return
      setDraft((previous) => ({
        ...previous,
        ...(previous.name !== row.resource
          ? {
              subnet: "",
              gateway: "",
              ipRange: "",
              ipv6: false,
              ipv6Subnet: "",
              ipv6Gateway: "",
              ipv6Range: "",
            }
          : {}),
        name: row.resource,
        ...(family === "inet"
          ? { subnet: row.prefix, gateway: "", ipRange: "" }
          : { ipv6: true, ipv6Subnet: row.prefix, ipv6Gateway: "", ipv6Range: "" }),
      }))
      if (family === "inet6") setAdvanced(true)
    },
    [],
  )

  const create = async () => {
    if (!reading.spec || busy || inventoryError || ipamUnavailable || !can("service.control"))
      return
    setBusy(true)
    try {
      const created = await post<{ ipamWarning?: string }>("/docker/networks/", {
        ...reading.spec,
        ...(reservations.length ? { ipamReservationIds: reservations.map((row) => row.id) } : {}),
      })
      notify.success(`${reading.spec.name} created`)
      if (created.ipamWarning)
        notify.error("Review the planning handoff", new Error(created.ipamWarning))
      onCreated()
      onOpenChange(false)
      setDraft(EMPTY_NETWORK)
      setAdvanced(false)
      setReservations([])
    } catch (error) {
      if (reservations.length) setIPAMRefreshKey((value) => value + 1)
      notify.error("Could not create the network", error)
    } finally {
      setBusy(false)
    }
  }
  const field = (key: keyof NetworkDraft, label: string, placeholder = "") => (
    <div className="space-y-1.5">
      <Label htmlFor={`network-${key}`} className="text-xs">
        {label}
      </Label>
      <Input
        id={`network-${key}`}
        value={String(draft[key])}
        spellCheck={false}
        className="font-mono text-xs"
        placeholder={placeholder}
        disabled={busy}
        onChange={(event) => update(key, event.target.value)}
      />
    </div>
  )
  const toggle = (key: "internal" | "attachable" | "ipv6", label: string, hint: string) => (
    <Group className="flex items-start gap-3">
      <Switch
        id={`network-${key}`}
        checked={draft[key]}
        onCheckedChange={(checked) => update(key, checked)}
        disabled={busy}
        className="mt-0.5"
      />
      <label htmlFor={`network-${key}`} className="cursor-pointer">
        <span className="block text-xs font-medium">{label}</span>
        <Hint>{hint}</Hint>
      </label>
    </Group>
  )

  return (
    <Modal
      open={open}
      onOpenChange={(value) => !busy && onOpenChange(value)}
      size="md"
      title="Create network"
      description="Choose how these containers connect. Docker owns the network and checks driver availability and address allocation when creating it."
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button
            onClick={create}
            disabled={
              busy ||
              !reading.spec ||
              Boolean(inventoryError) ||
              ipamUnavailable ||
              !can("service.control")
            }
            pending={busy}
          >
            Create
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <IPAMReservationPicker
          open={open}
          owner="docker_network"
          selected={reservations}
          onSelect={selectReservation}
          onUnavailable={setIPAMUnavailable}
          initialId={initialReservationId}
          disabled={busy}
          refreshKey={ipamRefreshKey}
        />
        {field("name", "Name", "app-internal")}
        {toggle(
          "internal",
          "Cut it off from the internet",
          "Containers on an internal network can reach its other members. Containers attached to additional networks may still use those paths.",
        )}
        {field("subnet", "Subnet (optional)", "Docker picks an IPv4 range")}
        <Hint>
          Choose an unused range. The preview checks the listed Docker networks; host, VPN and
          provider ranges also need to be considered.
        </Hint>
        <Button
          variant="ghost"
          size="sm"
          aria-expanded={advanced}
          aria-controls="network-advanced"
          onClick={() => setAdvanced((shown) => !shown)}
        >
          {advanced ? "Hide advanced settings" : "Advanced settings"}
        </Button>
        {advanced && (
          <div id="network-advanced" className="space-y-4">
            {admin ? (
              <>
                {field("driver", "Network driver", "bridge")}
                <Hint>
                  Bridge connects containers on this server. Macvlan and IPvlan use a configured
                  parent; overlay needs an active Swarm. An installed plugin can use its registered
                  driver name.
                </Hint>
              </>
            ) : (
              <Hint>
                This account creates bridge networks. Other drivers and driver options need
                administrator permission.
              </Hint>
            )}
            <div className="grid gap-3 sm:grid-cols-2">
              {field("gateway", "IPv4 gateway (optional)", "192.0.2.1")}
              {field("ipRange", "IPv4 allocation range (optional)", "192.0.2.128/25")}
            </div>
            {toggle(
              "attachable",
              "Allow standalone containers to join",
              "Overlay networks need this to accept ordinary containers. Bridge networks already allow attachment.",
            )}
            {toggle(
              "ipv6",
              "Enable IPv6",
              "Set an explicit IPv6 pool or let Docker allocate one if the daemon supports automatic IPv6 pools.",
            )}
            {draft.ipv6 && (
              <>
                {field("ipv6Subnet", "IPv6 subnet (optional)", "fd00:1::/64")}
                <div className="grid gap-3 sm:grid-cols-2">
                  {field("ipv6Gateway", "IPv6 gateway (optional)", "fd00:1::1")}
                  {field("ipv6Range", "IPv6 allocation range (optional)", "fd00:1::1000/116")}
                </div>
              </>
            )}
            {(["labels", ...(admin ? ["options"] : [])] as const).map((key) => (
              <div className="space-y-1.5" key={key}>
                <Label htmlFor={`network-${key}`} className="text-xs">
                  {key === "labels" ? "Labels" : "Driver options"}
                </Label>
                <Textarea
                  id={`network-${key}`}
                  value={draft[key as "labels" | "options"]}
                  disabled={busy}
                  spellCheck={false}
                  className="font-mono text-xs"
                  placeholder={
                    key === "labels" ? "purpose=application" : "com.docker.network.driver.mtu=1400"
                  }
                  onChange={(event) => update(key as "labels" | "options", event.target.value)}
                />
                <Hint>
                  One key=value per line.{" "}
                  {key === "labels"
                    ? "Compose and dashboard ownership labels are reserved."
                    : "Use options supported by the selected driver."}
                </Hint>
              </div>
            ))}
          </div>
        )}
        {draft.name.trim() && reading.error && (
          <p role="alert" className="text-xs text-destructive">
            {reading.error}
          </p>
        )}
        {inventoryError && (
          <div role="alert" className="space-y-2 text-xs text-muted-foreground">
            <p>
              The network inventory could not be refreshed. Your draft is retained. Refresh before
              creating to check the known allocations.
            </p>
            <Button size="xs" variant="outline" onClick={refreshInventory}>
              Refresh network inventory
            </Button>
          </div>
        )}
      </div>
    </Modal>
  )
}
