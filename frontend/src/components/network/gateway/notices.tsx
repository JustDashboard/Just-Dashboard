"use client"

import { useAuth } from "@/hooks/use-auth"
import { useState } from "react"
import { ApiError, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { EntryFlow, EntryReadiness, GatewayView } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Information, Warning } from "@/components/icons"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { useConfirm } from "@/components/confirm-dialog"
import {
  admissionRepairable,
  admissionState,
  entryState,
} from "@/components/network/gateway/evidence"
import { FlowSummary } from "@/components/network/gateway/impacts"

/**
 * What the gateway says before anything is pressed: that this host's firewall
 * will not take its writes, or that its entries are written but not loaded
 * into the kernel. Each is a fact the reader would otherwise find out from a
 * refusal, or from a forward that was saved and never worked.
 */
export function GatewayNotices({ view }: { view: GatewayView }) {
  const { capability, loaded, forwards, nat } = view
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
    </>
  )
}

/**
 * Puts back the dashboard's own admission rules, behind the confirmation it
 * always had. The route is one of the server's destructive ones, so it is an
 * administrator's with that capability, on a firewall the gateway may write,
 * and only where an owned rule is absent.
 */
export function useAdmissionRepair(view: GatewayView | undefined, onRefresh: () => void) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const offered =
    Boolean(view) &&
    can("system.admin") &&
    can("destructive") &&
    view!.capability.writable &&
    admissionRepairable(view!.admission)
  const repair = () =>
    confirm({
      title: "Repair owned admission rules",
      description:
        "Restore the dashboard's marked admission rules for its active translations. This may admit traffic previously refused by the missing rules.",
      confirmLabel: "Repair rules",
      action: async () => {
        await post("/network/gateway/admission/repair")
        onRefresh()
      },
    })
  return { offered, repair, dialog }
}

/**
 * Whether the firewall lets a translated connection through, as a dot and a
 * word in the identity line, with everything the word rests on behind it:
 * each needed admission chain and its state, every enabled entry's flow as
 * modelled through the host's other chains, the policy layers that were
 * checked, what stays unverified, and when it was read.
 *
 * This was the block of sentences the page opened on. An entry's own part of
 * it is the state word on its row (`EntryEvidence`).
 */
export function PolicyEvidence({
  view,
  onRepair,
}: {
  view: GatewayView
  /** Offered where an owned rule is absent and the reader may put it back. */
  onRepair?: () => void
}) {
  const { capability, admission } = view
  const state = admissionState(admission)
  const chains = admission.chains?.filter((chain) => chain.needed) ?? []
  const [open, setOpen] = useState(false)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label="Policy evidence"
          className="-mx-1 inline-flex min-w-0 items-center gap-1.5 rounded-sm px-1 focus-ring transition-colors hover:bg-row-hover"
        >
          <Status tone={state.tone} label={state.label} />
          <Information aria-hidden className="size-3.5 shrink-0 text-faint" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[26rem] space-y-4">
        <div className="flex items-center justify-between gap-3">
          <p className="text-sm font-medium">Policy evidence</p>
          {onRepair && (
            <Button
              type="button"
              size="xs"
              variant="outline"
              onClick={() => {
                // The confirmation is a dialog of its own: it takes the page.
                setOpen(false)
                onRepair()
              }}
            >
              Repair owned rules
            </Button>
          )}
        </div>
        {chains.length > 0 && (
          <dl
            aria-label="Admission chains"
            className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-hint"
          >
            {chains.map((chain) => (
              <div key={`${chain.family}:${chain.chain}`} className="contents">
                <dt className="font-mono text-muted-foreground">
                  {chain.family === "inet6" ? "IPv6" : "IPv4"} {chain.chain}
                </dt>
                <dd className="min-w-0">
                  <Status
                    tone={chain.status === "present" ? "running" : "warning"}
                    label={chain.status === "present" ? "Owned rule present" : chain.status}
                  />
                  {chain.reason && (
                    <span className="block text-muted-foreground">{chain.reason}</span>
                  )}
                </dd>
              </div>
            ))}
          </dl>
        )}
        {Boolean(view.flows?.length) && (
          <div className="space-y-1.5">
            <p className="text-hint font-medium">Each entry&rsquo;s flow, as modeled</p>
            <FlowSummary flows={view.flows ?? []} named />
            <p className="text-hint text-muted-foreground">
              Each enabled entry&rsquo;s flow is walked through the host&rsquo;s other chains in
              their supported rule forms, following jumps. A drop that only names some sources
              restricts it rather than blocking it. This is a model, not a packet trace.
            </p>
          </div>
        )}
        {Boolean(capability.layers?.length) && (
          <details className="text-hint text-muted-foreground">
            <summary className="cursor-pointer focus-ring">Checked policy layers</summary>
            <ul className="mt-2 max-h-48 space-y-1 overflow-y-auto">
              {capability.layers?.map((layer, index) => (
                <li key={index}>
                  <span className="font-mono">
                    {layer.family} {layer.table} {layer.chain}
                  </span>
                  : {layer.status}
                  {layer.type === "nat" ? " (nat)" : ""}
                  {layer.reason ? ` — ${layer.reason}` : ""}
                  {layer.uncertain && layer.uncertain.length > 0 && (
                    <span className="block pl-4">Not read: {layer.uncertain.join("; ")}</span>
                  )}
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
      </PopoverContent>
    </Popover>
  )
}

/**
 * One entry's state as the word on its row, in the column every row keeps for
 * it, with what the word rests on behind a press: what is installed and
 * admitted, and the entry's flow as modelled through the host's other chains,
 * layer by layer.
 *
 * It is a control of its own inside a row that is itself a press, so neither
 * the word nor anything in its popover opens the row's editor.
 */
export function EntryEvidence({
  name,
  enabled,
  readiness,
  flow,
  className,
}: {
  /** The entry, for the control's name: ":8080 → 10.0.4.5:80". */
  name: string
  enabled: boolean
  readiness?: EntryReadiness
  flow?: EntryFlow
  className?: string
}) {
  const state = entryState(enabled, readiness, flow)
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          data-slot="entry-state"
          aria-label={`${state.label}: evidence for ${name}`}
          className={cn(
            "-mx-1 inline-flex min-w-0 items-center rounded-sm px-1 py-0.5 text-left focus-ring transition-colors hover:bg-row-hover",
            className,
          )}
        >
          <Status
            tone={state.tone}
            label={state.label}
            className="min-w-0 [&>span:last-child]:truncate"
          />
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        className="w-96 space-y-3"
        // A press in here is not a press on the row the word sits in.
        onClick={(event) => event.stopPropagation()}
      >
        <p className="font-mono text-xs font-medium break-all">{name}</p>
        {readiness ? (
          <div className="space-y-1 text-hint">
            <p className="text-muted-foreground">{readiness.reason}</p>
            <p className="numeric flex flex-wrap gap-x-3 text-muted-foreground">
              <span>
                rules{" "}
                <span className="text-foreground">
                  {readiness.rules} of {readiness.expected}
                </span>
              </span>
              <span>
                admission{" "}
                <span className="text-foreground">{readiness.admission.replace("_", " ")}</span>
              </span>
              <span>
                forwarding{" "}
                <span className="text-foreground">{readiness.forwarding ? "on" : "off"}</span>
              </span>
            </p>
          </div>
        ) : (
          <p className="text-hint text-muted-foreground">Nothing was read about its rules.</p>
        )}
        {flow ? (
          <FlowSummary flows={[flow]} />
        ) : (
          enabled && <p className="text-hint text-muted-foreground">Its flow was not modelled.</p>
        )}
      </PopoverContent>
    </Popover>
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
