"use client"

import { useState } from "react"
import { ApiError, del, post, put } from "@/lib/api"
import { bytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { GatewayNAT, NetworkLink } from "@/lib/types"
import { Linked } from "@/components/icons"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FieldRow } from "@/components/form"
import { ProductLogo } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Cidr } from "@/components/network/address"
import { ForwardingOff, forwardingFamily } from "@/components/network/gateway/notices"
import { arrivalDevices } from "@/components/network/gateway/forwards"
import { ImpactList, useGatewayPreview } from "@/components/network/gateway/impacts"
import {
  compact,
  natMode,
  natRequest,
  natWord,
  ownerOf,
  readinessWord,
  splitList,
  type NATMode,
  type NATRequest,
} from "@/components/network/gateway/reading"
import { Tag } from "@/components/tag"
import { useAuth } from "@/hooks/use-auth"

/**
 * Every NAT entry as a lit card: the network, where it leaves, what it
 * carried, and a switch. One the VPN page made is the VPN page's to change —
 * the server answers `not_managed` for it — so its row names the product that
 * keeps it, leads to the VPN page and has no switch to offer.
 */
export function NATList({
  entries,
  writable = true,
  onOpen,
  onChanged,
}: {
  entries: GatewayNAT[]
  writable?: boolean
  onOpen: (entry: GatewayNAT) => void
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const { can } = useAuth()
  const [busy, setBusy] = useState<number>()
  const label = (n: GatewayNAT) => `${n.source} out through ${n.interface}`
  const toggle = (n: GatewayNAT, enabled: boolean) => {
    if (!enabled) {
      confirm({
        title: `Switch off ${n.name}`,
        confirmLabel: "Switch off",
        description: (
          <p>
            Traffic from {n.source} stops being translated on its way out through {n.interface}, so
            it no longer reaches the internet through this server. The entry is kept.
          </p>
        ),
        action: async () => {
          await put(`/network/gateway/nat/${n.id}`, natRequest(n, false))
          notify.success(`${n.name} is off`)
          onChanged()
        },
      })
      return
    }
    setBusy(n.id)
    put(`/network/gateway/nat/${n.id}`, natRequest(n, true))
      .then(() => {
        notify.success(`${n.name} is on`)
        onChanged()
      })
      .catch((err) => notify.error(`${n.name} was not changed`, err))
      .finally(() => setBusy(undefined))
  }
  return (
    <>
      <ChoiceList>
        {entries.map((n, index) => {
          const owner = n.owner ? ownerOf(n.owner) : undefined
          return (
            <ChoiceRow
              key={n.id}
              index={index}
              leading={
                owner?.product === "wireguard" || owner?.product === "tailscale" ? (
                  <ProductLogo id={owner.product} size="sm" />
                ) : (
                  <ProductLogo size="sm" fallback={Linked} />
                )
              }
              title={
                <span className="inline-flex min-w-0 flex-wrap items-center gap-2">
                  <span className={n.enabled ? undefined : "text-muted-foreground"}>
                    <Cidr cidr={n.source} /> <span className="text-muted-foreground">→</span>{" "}
                    <span className="font-mono">{n.interface}</span>
                  </span>
                  {n.enabled && n.readiness && readinessWord(n.readiness).tone !== "default" && (
                    <Tag tone={readinessWord(n.readiness).tone}>
                      {readinessWord(n.readiness).label}
                    </Tag>
                  )}
                </span>
              }
              verb={owner ? `Open ${owner.name} on the VPN page` : `Edit ${label(n)}`}
              href={owner ? "/network/vpn" : undefined}
              description={
                owner
                  ? `${n.name} · made by ${owner.name}${owner.device ? ` ${owner.device}` : ""} · changed on the VPN page`
                  : `${n.name} · ${natWord(n)}`
              }
              trailing={
                <span className="flex items-center gap-4">
                  <span className="numeric hidden min-w-[4.5rem] text-right font-mono text-micro leading-tight text-muted-foreground sm:grid">
                    <span>{compact(n.packets + (n.inPackets ?? 0))} packets</span>
                    <span>{bytes(n.bytes + (n.inBytes ?? 0))}</span>
                  </span>
                  {owner ? (
                    <Status
                      tone={n.enabled ? "running" : "stopped"}
                      label={n.enabled ? "In force" : "Off"}
                      className="hidden sm:inline-flex"
                    />
                  ) : (
                    <Switch
                      checked={n.enabled}
                      disabled={
                        !writable ||
                        busy === n.id ||
                        !can("system.admin") ||
                        (n.enabled && !can("destructive"))
                      }
                      onCheckedChange={(next) => toggle(n, next)}
                      aria-label={`${label(n)} in force`}
                    />
                  )}
                </span>
              }
              onSelect={owner ? undefined : () => onOpen(n)}
            />
          )
        })}
      </ChoiceList>
      {dialog}
    </>
  )
}

const MODES: { value: NATMode; title: string; hint: string }[] = [
  {
    value: "masquerade",
    title: "The device's own address",
    hint: "Masquerade: follows the address if the provider changes it.",
  },
  {
    value: "snat",
    title: "A fixed address",
    hint: "One of the device's addresses, always the same.",
  },
  {
    value: "one-to-one",
    title: "One-to-one",
    hint: "A public address of its own, both ways: arrivals for it reach the private host.",
  },
  {
    value: "nptv6",
    title: "IPv6 prefix",
    hint: "A private IPv6 network mapped to a public one of the same length, both ways.",
  },
]

/**
 * The editor for one NAT entry: a network, the device its traffic leaves
 * through, and whether it leaves as that device's own address (masquerade,
 * which follows the address when a provider changes it) or as a fixed one.
 * Like a forward's, a refusal is drawn in the form, and "forwarding is off"
 * carries its own fix.
 */
export function NATSheet({
  entry,
  links,
  writable,
  onOpenChange,
  onSaved,
}: {
  entry?: GatewayNAT
  links: NetworkLink[]
  writable: boolean
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const uplink = links.find((l) => l.uplink)?.name
  const devices = arrivalDevices(links)
  const [name, setName] = useState(entry?.name ?? "")
  const [source, setSource] = useState(entry?.source ?? "")
  const [device, setDevice] = useState(entry?.interface ?? uplink ?? "")
  const [mode, setMode] = useState<NATMode>(entry ? natMode(entry) : "masquerade")
  const [toAddress, setToAddress] = useState(entry?.toAddress ?? "")
  const [translated, setTranslated] = useState(entry?.translated ?? "")
  const [destinations, setDestinations] = useState(entry?.destinations?.join(", ") ?? "")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>()

  const mapped = mode === "one-to-one" || mode === "nptv6"
  const body = (): NATRequest => ({
    name: name.trim(),
    source: source.trim(),
    interface: device,
    toAddress: mode === "snat" ? toAddress.trim() : "",
    mode,
    translated: mapped ? translated.trim() : "",
    destinations: mapped ? [] : splitList(destinations),
  })
  const ready =
    name.trim() &&
    source.trim() &&
    device &&
    (mode === "snat" ? toAddress.trim() : mapped ? translated.trim() : true)
  const { preview, pending } = useGatewayPreview(
    ready ? { kind: "nat", id: entry?.id ?? 0, nat: body() } : undefined,
    Boolean(ready && writable),
  )
  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      if (entry) await put(`/network/gateway/nat/${entry.id}`, body())
      else await post("/network/gateway/nat", body())
      notify.success(entry ? "NAT entry saved" : "NAT entry made", {
        description: "It is made again at every boot.",
      })
      onOpenChange(false)
      onSaved()
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }
  const remove = () => {
    if (!entry) return
    confirm({
      title: `Remove ${entry.name}`,
      confirmLabel: "Remove",
      description: (
        <p>
          Traffic from {entry.source} stops being translated on its way out through{" "}
          {entry.interface} and the entry is forgotten.
        </p>
      ),
      action: async () => {
        await del(`/network/gateway/nat/${entry.id}`)
        notify.success("NAT entry removed")
        onOpenChange(false)
        onSaved()
      },
    })
  }

  const family = forwardingFamily(error)
  const message = error instanceof Error ? error.message : undefined
  const refused = (field: string) =>
    error instanceof ApiError && error.field === field ? error.message : undefined
  return (
    <>
      <SidePanel
        open
        onOpenChange={onOpenChange}
        width="sm"
        title={entry ? entry.name : "Share a network out"}
        description="Let a private network reach beyond this server through one of its devices"
        actions={
          entry && (
            <>
              <Status
                tone={entry.enabled ? "running" : "stopped"}
                label={entry.enabled ? "In force" : "Switched off"}
              />
              {can("destructive") && (
                <Button size="sm" variant="outline" className="ml-auto" onClick={remove}>
                  Remove
                </Button>
              )}
            </>
          )
        }
        footer={
          <>
            <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
              Cancel
            </Button>
            <Button
              type="submit"
              form="nat-form"
              disabled={!ready || busy || !writable}
              pending={busy}
            >
              {entry ? "Save entry" : "Share network"}
            </Button>
          </>
        }
      >
        <form
          id="nat-form"
          className="flex min-w-0 flex-col gap-5"
          onSubmit={(event) => {
            event.preventDefault()
            if (ready && !busy && writable) void submit()
          }}
        >
          <Field
            label="Name"
            htmlFor="nat-name"
            hint="What it is for: Lab network, Office VLAN"
            error={refused("name")}
          >
            <Input
              id="nat-name"
              value={name}
              placeholder="Lab network"
              onChange={(event) => setName(event.target.value)}
              autoComplete="off"
            />
          </Field>
          <FieldRow>
            <Field
              label="Network"
              htmlFor="nat-source"
              hint="The private network, in CIDR form"
              error={refused("source")}
            >
              <Input
                id="nat-source"
                value={source}
                placeholder="10.20.0.0/24"
                onChange={(event) => setSource(event.target.value)}
                className="font-mono"
                autoComplete="off"
              />
            </Field>
            <Field label="Leaves through" htmlFor="nat-device" error={refused("interface")}>
              <Select value={device} onValueChange={setDevice}>
                <SelectTrigger id="nat-device" className="w-full">
                  <SelectValue placeholder="Choose a device" />
                </SelectTrigger>
                <SelectContent>
                  {devices.map((l) => (
                    <SelectItem key={l.name} value={l.name}>
                      <span className="font-mono">{l.name}</span>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </FieldRow>

          <fieldset className="min-w-0 space-y-1.5">
            <legend className="mb-1.5 text-body font-medium">It goes out as</legend>
            <ChoiceGrid columns={2}>
              {MODES.map((m) => (
                <ChoiceCard
                  key={m.value}
                  selected={mode === m.value}
                  onClick={() => setMode(m.value)}
                >
                  <ChoiceCardTitle>{m.title}</ChoiceCardTitle>
                  <ChoiceCardHint>{m.hint}</ChoiceCardHint>
                </ChoiceCard>
              ))}
            </ChoiceGrid>
          </fieldset>
          {mode === "snat" && (
            <Field
              label="Address"
              htmlFor="nat-to"
              hint="An address this server owns on that device"
              error={refused("toAddress")}
            >
              <Input
                id="nat-to"
                value={toAddress}
                placeholder="203.0.113.20"
                onChange={(event) => setToAddress(event.target.value)}
                className="font-mono"
                autoComplete="off"
              />
            </Field>
          )}
          {mapped && (
            <Field
              label={mode === "nptv6" ? "Public IPv6 network" : "Public address or network"}
              htmlFor="nat-translated"
              hint={
                mode === "nptv6"
                  ? "The same length as the private network, routed to this server by the provider"
                  : "The same size as the network above; one address must be on that device"
              }
              error={refused("translated")}
            >
              <Input
                id="nat-translated"
                value={translated}
                placeholder={mode === "nptv6" ? "2001:db8:3::/48" : "203.0.113.25"}
                onChange={(event) => setTranslated(event.target.value)}
                className="font-mono"
                autoComplete="off"
              />
            </Field>
          )}
          {!mapped && (
            <Field
              label="Only to these destinations"
              htmlFor="nat-destinations"
              hint="Optional, comma-separated. Empty translates traffic to everywhere."
              error={refused("destinations")}
            >
              <Input
                id="nat-destinations"
                value={destinations}
                placeholder="192.0.2.0/24"
                onChange={(event) => setDestinations(event.target.value)}
                className="font-mono"
                autoComplete="off"
              />
            </Field>
          )}

          <ImpactList preview={preview} pending={pending} />

          {family && message ? (
            <ForwardingOff
              family={family}
              message={message}
              disabled={busy}
              onTurnedOn={() => void submit()}
            />
          ) : (
            message &&
            !(error instanceof ApiError && error.field) && (
              <p role="alert" className="animate-rise text-body text-destructive">
                {message}
              </p>
            )
          )}
        </form>
      </SidePanel>
      {dialog}
    </>
  )
}
