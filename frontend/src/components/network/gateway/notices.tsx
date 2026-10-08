"use client"

import { useAuth } from "@/hooks/use-auth"
import { useState } from "react"
import { ApiError, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { GatewayView } from "@/lib/types"
import { Warning } from "@/components/icons"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"

/**
 * What the gateway says before anything is pressed: that this host's firewall
 * will not take its writes, that its entries are written but not loaded into
 * the kernel, or that the rule which lets a translated connection through the
 * firewall is missing. Each is a fact the reader would otherwise find out
 * from a refusal, or from a forward that was saved and never worked.
 */
export function GatewayNotices({ view }: { view: GatewayView }) {
  const { capability, loaded, admission, forwards, nat } = view
  const entries = forwards.some((f) => f.enabled) || nat.some((n) => n.enabled)
  return (
    <>
      {!capability.writable && (
        <Notice tone="warning" icon={Warning} title="This firewall does not let the gateway write">
          <p>{capability.reason ?? `${capability.firewall} filters forwarded traffic itself.`}</p>
          {capability.blocker && (
            <p className="mt-1">
              The chain that refuses is{" "}
              <span className="font-mono">
                {capability.blocker.family} {capability.blocker.table} {capability.blocker.chain}
              </span>
              . Add this rule there, and forwards and NAT can be made here:{" "}
              <code className="font-mono text-foreground">{capability.blocker.rule}</code>
            </p>
          )}
        </Notice>
      )}
      {entries && !loaded && (
        <Notice tone="warning" icon={Warning} title="The gateway is not loaded into the kernel">
          Its entries are saved but not in force: the host was started without the unit that loads
          them, or something deleted the table. Saving any entry loads it again.
        </Notice>
      )}
      {admission.needed && !admission.present && (
        <Notice tone="warning" icon={Warning} title="Forwarded connections are not admitted">
          The firewall rule that lets a translated connection through is missing, so a forward can
          be refused by the firewall after it has been translated. It is put back when an entry is
          saved and at every boot.
        </Notice>
      )}
    </>
  )
}

/** The family a `forwarding_off` refusal names, from its sentence. */
export function forwardingFamily(err: unknown): "ipv4" | "ipv6" | undefined {
  if (!(err instanceof ApiError) || err.code !== "forwarding_off") return undefined
  return /IPv6/.test(err.message) ? "ipv6" : "ipv4"
}

/**
 * The refusal "forwarding is off", with the one thing that fixes it. The
 * server's sentence sends the reader to the Routing page; this is that page's
 * switch, here, and the form that was refused sends itself again once it is
 * on, so the reader does not have to find where they were.
 */
export function ForwardingOff({
  family,
  message,
  onTurnedOn,
  disabled,
}: {
  family: "ipv4" | "ipv6"
  message: string
  /** Called once forwarding is on; the form sends itself again. */
  onTurnedOn: () => void
  disabled?: boolean
}) {
  const { can } = useAuth()
  const [busy, setBusy] = useState(false)
  const name = family === "ipv4" ? "IPv4" : "IPv6"
  const turnOn = async () => {
    setBusy(true)
    try {
      await post(`/network/forwarding/${family}/on`)
      notify.success(`${name} forwarding is on`, { description: "It is set again at every boot." })
      onTurnedOn()
    } catch (err) {
      notify.error(`${name} forwarding was not turned on`, err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Notice tone="warning" icon={Warning} title={`${name} forwarding is off`}>
      <p>{message}</p>
      <Button
        type="button"
        size="sm"
        variant="outline"
        className="mt-2"
        onClick={() => void turnOn()}
        pending={busy}
        disabled={!can("system.admin") || busy || disabled}
      >
        Turn on {name} forwarding
      </Button>
    </Notice>
  )
}
