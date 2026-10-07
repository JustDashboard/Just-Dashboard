"use client"

import { useState } from "react"
import { post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { ForwardingFamily, NetworkForwarding } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Status } from "@/components/status-dot"
import { Switch } from "@/components/ui/switch"
import { useConfirm } from "@/components/confirm-dialog"

/**
 * Whether this server routes packets that are not its own, per family, as
 * the two switches they are — with what depends on each beside it, because
 * a switch that silently breaks Docker's networks is the one an operator
 * flips once. Turning one off is refused while something needs it, and the
 * refusal is drawn before it is asked: the switch stays, disabled, with the
 * guard's sentence.
 */
export function ForwardingSwitches({
  forwarding,
  onChanged,
}: {
  forwarding: NetworkForwarding
  onChanged: () => void
}) {
  return (
    <div className="grid min-w-0 gap-x-10 gap-y-6 md:grid-cols-2">
      <Family name="IPv4" family="ipv4" state={forwarding.ipv4} onChanged={onChanged} />
      <Family name="IPv6" family="ipv6" state={forwarding.ipv6} onChanged={onChanged} />
    </div>
  )
}

function Family({
  name,
  family,
  state,
  onChanged,
}: {
  name: string
  family: "ipv4" | "ipv6"
  state: ForwardingFamily
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState(false)
  const turn = async (on: boolean) => {
    if (!on) {
      confirm({
        title: `Stop forwarding ${name}`,
        confirmLabel: "Turn off",
        description: (
          <p>
            This server stops passing {name} traffic between its networks: every tunnel, NAT
            entry and container network that relies on it stops reaching anything beyond this
            machine.
          </p>
        ),
        action: async () => {
          await post(`/network/forwarding/${family}/off`)
          notify.success(`${name} forwarding is off`)
          onChanged()
        },
      })
      return
    }
    setBusy(true)
    try {
      await post(`/network/forwarding/${family}/on`)
      notify.success(`${name} forwarding is on`, { description: "It is set again at every boot." })
      onChanged()
    } catch (err) {
      notify.error(`${name} forwarding not changed`, err)
    } finally {
      setBusy(false)
    }
  }
  const locked = state.enabled && !!state.guard
  return (
    <div className="flex min-w-0 flex-col gap-2 border-t border-hairline pt-4">
      <div className="flex items-center justify-between gap-4">
        <div className="min-w-0">
          <p className="text-title font-medium">{name} forwarding</p>
          <Status
            tone={!state.available ? "unknown" : state.enabled ? "running" : "stopped"}
            label={
              !state.available
                ? "Not on this kernel"
                : state.enabled
                  ? state.persisted
                    ? "On · set at boot"
                    : "On"
                  : "Off"
            }
          />
        </div>
        <Switch
          checked={state.enabled}
          disabled={!state.available || busy || locked}
          onCheckedChange={(next) => void turn(next)}
          aria-label={`${name} forwarding`}
        />
      </div>
      <p className={cn("text-hint", state.neededBy.length ? "text-foreground/80" : "text-muted-foreground")}>
        {state.neededBy.length > 0
          ? `Needed by ${state.neededBy.join(", ")}.`
          : "Nothing here needs it."}
      </p>
      {locked && <p className="text-hint text-muted-foreground">{state.guard}</p>}
      {dialog}
    </div>
  )
}
