"use client"

import { useState } from "react"
import { ApiError, del, post, put } from "@/lib/api"
import { bytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { GatewayForward, NetworkLink } from "@/lib/types"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FieldRow } from "@/components/form"
import { SidePanel } from "@/components/side-panel"
import { Notice } from "@/components/state"
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
import { Segments } from "@/components/deploy/settings/segments"
import { Endpoint, Port, TargetLogo } from "@/components/network/gateway/marks"
import { ForwardingOff, forwardingFamily } from "@/components/network/gateway/notices"
import {
  compact,
  forwardFacts,
  forwardRequest,
  forwardTitle,
  hostPort,
  splitList,
  targetPortOf,
  type ForwardRequest,
} from "@/components/network/gateway/reading"
import { useAuth } from "@/hooks/use-auth"

const ANY = "__any"

/** The devices a forward may be tied to: real ones, not a container's veth or the kernel's own. */
export const arrivalDevices = (links: NetworkLink[]) =>
  links.filter((l) => l.role !== "container" && l.owner !== "kernel")

/**
 * Every forward as a lit card that opens its editor, with what it has carried
 * and the switch that puts it in force. A forward is *taken* — pressed to
 * change — so each is a `ChoiceRow` (§16); the switch inside it is a control
 * of its own and does not open the row.
 *
 * Switching one off is destructive — the port stops answering — so it asks
 * first; switching it on does not.
 */
export function ForwardList({
  forwards,
  onOpen,
  onChanged,
}: {
  forwards: GatewayForward[]
  onOpen: (forward: GatewayForward) => void
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState<number>()
  const setEnabled = async (f: GatewayForward, enabled: boolean) => {
    setBusy(f.id)
    try {
      await put(`/network/gateway/forwards/${f.id}`, forwardRequest(f, enabled))
      notify.success(enabled ? `${forwardTitle(f)} is on` : `${forwardTitle(f)} is off`)
      onChanged()
    } catch (err) {
      notify.error(`${forwardTitle(f)} was not changed`, err)
    } finally {
      setBusy(undefined)
    }
  }
  const toggle = (f: GatewayForward, enabled: boolean) => {
    if (enabled) return void setEnabled(f, true)
    confirm({
      title: `Switch off ${forwardTitle(f)}`,
      confirmLabel: "Switch off",
      description: (
        <p>
          Port {f.ports} stops being passed on to {hostPort(f.target, targetPortOf(f))} until it is
          switched on again. The forward is kept.
        </p>
      ),
      action: async () => {
        await put(`/network/gateway/forwards/${f.id}`, forwardRequest(f, false))
        notify.success(`${forwardTitle(f)} is off`)
        onChanged()
      },
    })
  }
  return (
    <>
      <ChoiceList>
        {forwards.map((f, index) => (
          <ChoiceRow
            key={f.id}
            index={index}
            leading={<TargetLogo ports={f.ports} targetPort={f.targetPort} />}
            title={
              <span className={f.enabled ? undefined : "text-muted-foreground"}>
                <Port port={f.ports} /> <span className="text-muted-foreground">→</span>{" "}
                <Endpoint address={f.target} port={targetPortOf(f)} />
              </span>
            }
            verb={`Edit ${forwardTitle(f)}`}
            description={forwardFacts(f).join(" · ")}
            trailing={
              <span className="flex items-center gap-4">
                <span className="numeric hidden min-w-[4.5rem] text-right font-mono text-micro leading-tight text-muted-foreground sm:grid">
                  <span>{compact(f.packets)} packets</span>
                  <span>{bytes(f.bytes)}</span>
                </span>
                <Switch
                  checked={f.enabled}
                  disabled={busy === f.id}
                  onCheckedChange={(next) => toggle(f, next)}
                  aria-label={`${forwardTitle(f)} in force`}
                />
              </span>
            }
            onSelect={() => onOpen(f)}
          />
        ))}
      </ChoiceList>
      {dialog}
    </>
  )
}

const SOURCE_NAT: {
  value: GatewayForward["sourceNat"]
  title: string
  hint: string
}[] = [
  {
    value: "auto",
    title: "Auto — recommended",
    hint: "Keeps the visitor's address wherever the replies can find their way back.",
  },
  {
    value: "always",
    title: "Always",
    hint: "The target sees this server, never the visitor.",
  },
  {
    value: "never",
    title: "Never",
    hint: "The target sees the visitor, and must answer through this server.",
  },
]

const PROTOCOLS = [
  { value: "both", label: "TCP and UDP" },
  { value: "tcp", label: "TCP" },
  { value: "udp", label: "UDP" },
] as const

/**
 * The editor for one forward, as a sheet: a new one, or the one pressed.
 *
 * It says before it is asked that a forward admits its own traffic — the
 * connections it translates are marked and accepted in the firewall's forward
 * and input chains (and Docker's), so the reader does not go and add a rule
 * for it, and the sources field is the only filter it has. A refusal is drawn
 * where the form is: the guard's sentence, and for "forwarding is off" the
 * switch that fixes it, after which the form sends itself again.
 */
export function ForwardSheet({
  forward,
  links,
  writable,
  onOpenChange,
  onSaved,
}: {
  /** The forward being edited; none is a new one. */
  forward?: GatewayForward
  links: NetworkLink[]
  writable: boolean
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [name, setName] = useState(forward?.name ?? "")
  const [protocol, setProtocol] = useState<GatewayForward["protocol"]>(forward?.protocol ?? "both")
  const [device, setDevice] = useState(forward?.interface || ANY)
  const [ports, setPorts] = useState(forward?.ports ?? "")
  const [target, setTarget] = useState(forward?.target ?? "")
  const [targetPort, setTargetPort] = useState(forward?.targetPort ?? "")
  const [sourceNat, setSourceNat] = useState<GatewayForward["sourceNat"]>(
    forward?.sourceNat ?? "auto",
  )
  const [sources, setSources] = useState(forward?.sources.join(", ") ?? "")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>()

  const body = (): ForwardRequest => ({
    name: name.trim(),
    protocol,
    interface: device === ANY ? "" : device,
    ports: ports.trim(),
    target: target.trim(),
    targetPort: targetPort.trim(),
    sourceNat,
    sources: splitList(sources),
  })
  const ready = name.trim() && ports.trim() && target.trim()
  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      if (forward) await put(`/network/gateway/forwards/${forward.id}`, body())
      else await post("/network/gateway/forwards", body())
      notify.success(forward ? "Forward saved" : "Forward made", {
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
    if (!forward) return
    confirm({
      title: `Remove ${forwardTitle(forward)}`,
      confirmLabel: "Remove",
      description: (
        <p>
          Port {forward.ports} stops being passed on to{" "}
          {hostPort(forward.target, targetPortOf(forward))} and the forward is forgotten.
        </p>
      ),
      action: async () => {
        await del(`/network/gateway/forwards/${forward.id}`)
        notify.success("Forward removed")
        onOpenChange(false)
        onSaved()
      },
    })
  }

  const family = forwardingFamily(error)
  const message = error instanceof ApiError || error instanceof Error ? error.message : undefined
  // The field a refusal is about, when the server named one.
  const refused = (field: string) =>
    error instanceof ApiError && error.field === field ? error.message : undefined
  const devices = arrivalDevices(links)
  return (
    <>
      <SidePanel
        open
        onOpenChange={onOpenChange}
        width="md"
        title={
          forward ? <span className="font-mono">{forwardTitle(forward)}</span> : "Forward a port"
        }
        description="Pass a public port on to an address inside or beyond this server"
        actions={
          forward && (
            <>
              <Status
                tone={forward.enabled ? "running" : "stopped"}
                label={forward.enabled ? "In force" : "Switched off"}
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
              form="forward-form"
              disabled={!ready || busy || !writable}
              pending={busy}
            >
              {forward ? "Save forward" : "Forward port"}
            </Button>
          </>
        }
      >
        <form
          id="forward-form"
          className="flex min-w-0 flex-col gap-5"
          onSubmit={(event) => {
            event.preventDefault()
            if (ready && !busy && writable) void submit()
          }}
        >
          <FieldRow>
            <Field
              label="Name"
              htmlFor="forward-name"
              hint="What it is for: Website, Minecraft"
              error={refused("name")}
            >
              <Input
                id="forward-name"
                value={name}
                placeholder="Website"
                onChange={(event) => setName(event.target.value)}
                autoComplete="off"
              />
            </Field>
            <Field label="Protocol">
              <Segments
                label="Protocol"
                value={protocol}
                options={[...PROTOCOLS]}
                onChange={setProtocol}
                fill
              />
            </Field>
          </FieldRow>
          <FieldRow>
            <Field
              label="Arrive on"
              htmlFor="forward-device"
              hint="The device the visitor reaches this server through"
              error={refused("interface")}
            >
              <Select value={device} onValueChange={setDevice}>
                <SelectTrigger id="forward-device" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={ANY}>Any device</SelectItem>
                  {devices.map((l) => (
                    <SelectItem key={l.name} value={l.name}>
                      <span className="font-mono">{l.name}</span>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field
              label="Public port"
              htmlFor="forward-ports"
              hint="One port, or a range like 8000-8010"
              error={refused("ports")}
            >
              <Input
                id="forward-ports"
                value={ports}
                inputMode="numeric"
                placeholder="8080"
                onChange={(event) => setPorts(event.target.value)}
                className="font-mono"
                autoComplete="off"
              />
            </Field>
          </FieldRow>
          <FieldRow>
            <Field
              label="Target address"
              htmlFor="forward-target"
              hint="The container or machine that answers"
              error={refused("target")}
            >
              <Input
                id="forward-target"
                value={target}
                placeholder="10.0.4.5"
                onChange={(event) => setTarget(event.target.value)}
                className="font-mono"
                autoComplete="off"
              />
            </Field>
            <Field
              label="Target port"
              htmlFor="forward-target-port"
              hint="Empty keeps the port it arrived on"
              error={refused("targetPort")}
            >
              <Input
                id="forward-target-port"
                value={targetPort}
                inputMode="numeric"
                placeholder="same"
                onChange={(event) => setTargetPort(event.target.value)}
                className="font-mono"
                autoComplete="off"
              />
            </Field>
          </FieldRow>

          <fieldset className="min-w-0 space-y-1.5">
            <legend className="mb-1.5 text-body font-medium">The visitor&rsquo;s address</legend>
            <ChoiceGrid className="sm:grid-cols-3">
              {SOURCE_NAT.map((option) => (
                <ChoiceCard
                  key={option.value}
                  selected={sourceNat === option.value}
                  onClick={() => setSourceNat(option.value)}
                >
                  <ChoiceCardTitle>{option.title}</ChoiceCardTitle>
                  <ChoiceCardHint>{option.hint}</ChoiceCardHint>
                </ChoiceCard>
              ))}
            </ChoiceGrid>
          </fieldset>

          <Field
            label="Allowed sources"
            htmlFor="forward-sources"
            hint="Optional, comma-separated networks. Empty lets anyone in."
            error={refused("sources")}
          >
            <Input
              id="forward-sources"
              value={sources}
              placeholder="203.0.113.0/24, 198.51.100.7"
              onChange={(event) => setSources(event.target.value)}
              className="font-mono"
              autoComplete="off"
            />
          </Field>

          <Notice title="A forward admits its own traffic">
            Its connections are marked and accepted in the firewall&rsquo;s forward and input
            chains, and in Docker&rsquo;s, so there is no rule to add there. The allowed sources
            above are the only filter it has.
          </Notice>

          {family && message ? (
            <ForwardingOff
              family={family}
              message={message}
              disabled={busy}
              onTurnedOn={() => void submit()}
            />
          ) : (
            message &&
            !refusedField(error) && (
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

/** Whether a refusal named a field that one of the form's own fields shows. */
function refusedField(error: unknown) {
  return (
    error instanceof ApiError &&
    ["name", "interface", "ports", "target", "targetPort", "sources"].includes(error.field ?? "")
  )
}
