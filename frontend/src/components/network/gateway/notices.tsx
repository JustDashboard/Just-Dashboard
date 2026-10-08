"use client"

import { useAuth } from "@/hooks/use-auth"
import { useState } from "react"
import { ApiError, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { GatewayView } from "@/lib/types"
import { Warning } from "@/components/icons"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { useConfirm } from "@/components/confirm-dialog"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Tag } from "@/components/tag"

/**
 * What the gateway says before anything is pressed: that this host's firewall
 * will not take its writes, that its entries are written but not loaded into
 * the kernel, or that the rule which lets a translated connection through the
 * firewall is missing. Each is a fact the reader would otherwise find out
 * from a refusal, or from a forward that was saved and never worked.
 */
export function GatewayNotices({ view, onRefresh }: { view: GatewayView; onRefresh?: () => void }) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
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
        <Notice tone="warning" icon={Warning} title="Owned admission rules need attention">
          <p>
            A required rule is missing, unsupported or unreadable. A saved forward may be refused
            after translation. Inspect each chain below; the presence of these rules does not prove
            the complete connection works.
          </p>
          {can("system.admin") &&
            capability.writable &&
            admission.chains?.some((chain) => chain.needed && chain.status === "absent") && (
              <Button
                type="button"
                size="sm"
                variant="outline"
                className="mt-2"
                onClick={() =>
                  confirm({
                    title: "Repair owned admission rules",
                    description: (
                      <p>
                        Restore the dashboard&rsquo;s marked admission rules for its active translations.
                        This may admit traffic previously refused by the missing rules.
                      </p>
                    ),
                    confirmLabel: "Repair rules",
                    action: async () => {
                      await post("/network/gateway/admission/repair")
                      onRefresh?.()
                    },
                  })
                }
              >
                Repair owned rules
              </Button>
            )}
        </Notice>
      )}
      {(admission.chains || capability.layers) && (
        <Panel plain>
          <PanelHeader title="Policy evidence" />
          <PanelBody className="space-y-3">
            <DetailList>
              {admission.chains
                ?.filter((chain) => chain.needed)
                .map((chain) => (
                  <Detail
                    key={`${chain.family}:${chain.chain}`}
                    label={`${chain.family === "inet6" ? "IPv6" : "IPv4"} ${chain.chain}`}
                  >
                    <span className="space-x-2">
                      <Tag tone={chain.status === "present" ? "success" : "warning"}>
                        {chain.status === "present" ? "Owned rule present" : chain.status}
                      </Tag>
                      {chain.reason && (
                        <span className="text-muted-foreground">{chain.reason}</span>
                      )}
                    </span>
                  </Detail>
                ))}
            </DetailList>
            {Boolean(capability.layers?.length) && (
              <details className="text-hint text-muted-foreground">
                <summary className="cursor-pointer focus-ring">Checked policy layers</summary>
                <ul className="mt-2 space-y-1">
                  {capability.layers?.map((layer, index) => (
                    <li key={index}>
                      <span className="font-mono">
                        {layer.family} {layer.table} {layer.chain}
                      </span>
                      : {layer.status}
                      {layer.reason ? ` — ${layer.reason}` : ""}
                    </li>
                  ))}
                </ul>
              </details>
            )}
            <p className="text-hint text-muted-foreground">
              {capability.unknownLayers?.length
                ? `Unverified: ${capability.unknownLayers.join("; ")}.`
                : "Provider policy and end-to-end reachability remain unverified."}
            </p>
            {admission.checkedAt && (
              <p className="text-hint text-muted-foreground">
                Checked <time dateTime={admission.checkedAt}>{admission.checkedAt}</time>
              </p>
            )}
          </PanelBody>
        </Panel>
      )}
      {dialog}
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
