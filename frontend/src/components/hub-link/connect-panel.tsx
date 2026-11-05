"use client"

import { useEffect, useId, useState } from "react"
import { External, Warning } from "@/components/icons"
import { countdown, REQUEST_LIFETIME, scopeName } from "@/lib/account-link"
import { errorMessage } from "@/lib/api"
import { describeMachine, hubHost, openable, terminalCommand } from "@/lib/hub-link"
import { notify } from "@/lib/toast"
import type { HubLinkCheck, HubLinkMachine, HubLinkScope, HubLinkStatus } from "@/lib/types"
import { useNow } from "@/hooks/use-now"
import { CopyButton } from "@/components/account-link/link-panel"
import { ScopeCards } from "@/components/account-link/scope-cards"
import { ShellWords } from "@/components/deploy/run-evidence"
import { Field } from "@/components/form"
import { Meter } from "@/components/meter"
import { Section } from "@/components/page"
import { Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { BorderBeam } from "@/components/ui/border-beam"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

/** How long the address must stay unchanged before it is checked. */
const CHECK_AFTER = 400

/**
 * Connecting, before it is done: the hub's address and whose machine answers there, the scope,
 * and the button — and, once a request exists, the code to type into the hub, this server's
 * fingerprint, and the minutes left to approve it.
 *
 * Nothing here acts on its own. A link from the hub (`?connect=<hub host>`) only fills the
 * address in, says that it did, and leaves the scope for the person to choose: anyone on the
 * tailnet can send such a link with a look-alike name (hub-link-protocol §8.1). A server
 * administrator still presses Connect, and the backend still refuses any hub that is not one
 * machine on this server's own tailnet (§2.2). As the address is typed or filled, `check` asks
 * this server's tailscaled whose machine it is, so the person reads that before connecting.
 */
export function ConnectPanel({
  address,
  onAddress,
  invited,
  scope,
  onScope,
  onBegin,
  onCheck,
  className,
}: {
  address: string
  onAddress: (address: string) => void
  /** The hub that sent the person here, by host; the section asks about it by name. */
  invited?: string
  /** Unchosen until the person picks one, as after a link from the hub. */
  scope?: HubLinkScope
  onScope: (scope: HubLinkScope) => void
  onBegin: (url: string, scope: HubLinkScope) => Promise<void>
  onCheck: (url: string, signal?: AbortSignal) => Promise<HubLinkCheck>
  className?: string
}) {
  const field = useId()
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const url = address.trim()
  const checked = useMachineCheck(url, onCheck)

  const connect = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!url || !scope || busy) return
    setBusy(true)
    setRefusal(undefined)
    try {
      await onBegin(url, scope)
    } catch (err) {
      setRefusal(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const command = terminalCommand(url)

  return (
    <Section
      title={
        invited
          ? `Connect this server to Just Hub at ${invited}`
          : "Connect this server to Just Hub"
      }
      className={className}
    >
      {invited && (
        <Notice icon={Warning} title="This address came from a link">
          A link from the hub filled in {invited}. Anyone on your tailnet can send a link like this,
          so check below that it is the hub you meant, and choose what it may do yourself.
        </Notice>
      )}
      <form onSubmit={connect} className="flex min-w-0 flex-col gap-6">
        <Field
          label="Hub address"
          htmlFor={field}
          hint="The hub's name on this server's tailnet, ending in .ts.net. Nothing is sent to it until you press Connect."
          className="max-w-xl"
        >
          <Input
            id={field}
            value={address}
            onChange={(event) => onAddress(event.target.value)}
            placeholder="https://hub.your-tailnet.ts.net"
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            disabled={busy}
            className="font-mono"
          />
        </Field>
        {url && <MachineLine check={checked} />}

        <div className="flex min-w-0 flex-col gap-3">
          <p className="eyebrow">What the hub can do</p>
          <ScopeCards value={scope} onChange={onScope} disabled={busy} />
          <p className="text-xs text-muted-foreground">
            {scope ? "" : "Choose a scope to connect. "}
            No scope gives the hub a shell, files or accounts, and this server refuses any command
            aimed at its own containers. The scope can be lowered at any time.
          </p>
        </div>

        <div className="flex min-w-0 flex-wrap items-center gap-x-6 gap-y-4">
          <Button size="lg" type="submit" pending={busy} disabled={!url || !scope}>
            Connect
          </Button>
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
            <span className="text-xs text-muted-foreground">or from a terminal</span>
            <Well className="flex min-w-0 items-center gap-3 py-1.5 pr-1.5 pl-3">
              <span className="min-w-0 truncate">
                <span aria-hidden className="text-muted-foreground/60 select-none">
                  ${" "}
                </span>
                <ShellWords command={command} />
              </span>
              <CopyButton label="Copy command" text={command} />
            </Well>
          </div>
        </div>
      </form>
      {refusal && (
        <div role="alert">
          <Notice tone="danger" icon={Warning} title="Could not connect">
            {refusal}
          </Notice>
        </div>
      )}
    </Section>
  )
}

type MachineCheck =
  | { state: "checking" }
  | { state: "done"; result: HubLinkCheck }
  | { state: "refused"; message: string }

/**
 * `check` for the address as it stands, once it has stopped changing; an answer for an address
 * that was typed over is dropped.
 */
function useMachineCheck(
  url: string,
  onCheck: (url: string, signal?: AbortSignal) => Promise<HubLinkCheck>,
): MachineCheck & { url: string } {
  const [answer, setAnswer] = useState<MachineCheck & { url: string }>({
    url: "",
    state: "checking",
  })
  useEffect(() => {
    if (!url) return
    const controller = new AbortController()
    const timer = window.setTimeout(() => {
      onCheck(url, controller.signal).then(
        (result) => setAnswer({ url, state: "done", result }),
        (err) => {
          if (!controller.signal.aborted)
            setAnswer({ url, state: "refused", message: errorMessage(err) })
        },
      )
    }, CHECK_AFTER)
    return () => {
      window.clearTimeout(timer)
      controller.abort()
    }
  }, [url, onCheck])
  return answer.url === url ? answer : { url, state: "checking" }
}

/** Whose machine the address is, before anything is sent to it (§8.2). */
function MachineLine({ check }: { check: MachineCheck }) {
  return (
    <p
      data-slot="hub-link-machine"
      aria-live="polite"
      className="-mt-3 max-w-xl text-xs break-words text-muted-foreground"
    >
      {check.state === "checking" ? (
        "Checking whose machine this is…"
      ) : check.state === "refused" ? (
        <span className="text-destructive">{check.message}</span>
      ) : check.result.machine ? (
        <>
          The hub&apos;s machine:{" "}
          <span className="font-medium text-foreground">
            {describeMachine(check.result.machine)}
          </span>
        </>
      ) : check.result.testOrigin ? (
        "The test origin: no machine is checked."
      ) : (
        "Not checked: Tailscale on this server could not be asked whose machine this is."
      )}
    </p>
  )
}

/** The pinned machine, as the Settings page shows it beside the link (§2.4). */
export function MachineFact({ machine }: { machine?: HubLinkMachine }) {
  return (
    <p data-slot="hub-link-machine" className="text-xs break-words text-muted-foreground">
      The hub&apos;s machine:{" "}
      <span className="font-medium text-foreground">
        {machine ? describeMachine(machine) : "not checked"}
      </span>
    </p>
  )
}

/**
 * The request, while the hub owner decides: the code to type into the hub, which the hub never
 * shows (hub-link-protocol §4.1), this server's key fingerprint and the hub's machine, which its
 * approval screen leads with, the time left, and the way to the approval. Neither machine can
 * link the other alone: the code only reaches the hub through the person reading it here.
 */
export function WaitingPanel({
  request,
  fingerprint,
  onCancel,
  className,
}: {
  request: NonNullable<NonNullable<HubLinkStatus["admin"]>["request"]>
  fingerprint?: string
  onCancel: () => Promise<void>
  className?: string
}) {
  const now = useNow(1000)
  const left = countdown(request.expiresAt, now)
  const remaining = Math.max(0, Date.parse(request.expiresAt) - now)
  const [busy, setBusy] = useState(false)
  const host = hubHost(request.hubUrl)
  const approve = openable(request.approveUrl)

  const cancel = async () => {
    setBusy(true)
    try {
      await onCancel()
    } catch (err) {
      notify.error("Could not cancel the request", errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Section title={`Approve this server in Just Hub at ${host}`} className={className}>
      <div className="grid min-w-0 items-end gap-x-12 gap-y-8 lg:grid-cols-[minmax(0,22rem)_minmax(0,17rem)_minmax(0,18rem)]">
        <div className="min-w-0 space-y-2">
          <p className="eyebrow">Code</p>
          {/* The request is work in flight until the hub answers, so its frame carries the
              light that says so (§11). */}
          <Well plain className="relative flex items-center justify-between gap-4 px-4 py-3">
            {!left.expired && <BorderBeam duration={6} />}
            <span
              data-slot="hub-link-code"
              className="min-w-0 truncate font-mono text-2xl font-semibold tracking-widest select-all"
            >
              {request.userCode}
            </span>
            <CopyButton label="Copy code" text={request.userCode} />
          </Well>
        </div>
        <div className="min-w-0 space-y-2">
          <p className="eyebrow">This server&apos;s fingerprint</p>
          <p
            data-slot="hub-link-fingerprint"
            className="truncate py-3 font-mono text-lg font-semibold tracking-wide select-all"
          >
            {fingerprint ?? "—"}
          </p>
        </div>
        <div className="min-w-0 space-y-2">
          <div className="flex items-baseline justify-between gap-4">
            <p className="eyebrow">Expires in</p>
            <p className="text-xs text-muted-foreground">
              Scope <span className="font-medium text-foreground">{scopeName(request.scope)}</span>
            </p>
          </div>
          <p
            className={
              left.expired
                ? "numeric text-2xl font-semibold text-destructive"
                : "numeric text-2xl font-semibold"
            }
          >
            {left.label}
          </p>
          <Meter
            label="Time left to approve"
            value={(remaining / REQUEST_LIFETIME) * 100}
            tone={left.expired ? "danger" : remaining < 120_000 ? "warning" : "default"}
          />
        </div>
      </div>

      <MachineFact machine={request.hubMachine} />
      <p className="text-xs text-muted-foreground">
        Type this code into the hub when it asks for it; the hub never shows it. Its approval screen
        shows this server&apos;s fingerprint: approve only if it is the one above.
      </p>

      <div className="flex min-w-0 flex-wrap items-center gap-2 pt-2">
        {left.expired || !approve ? (
          <Button size="lg" disabled>
            Approve in Just Hub
          </Button>
        ) : (
          <Button size="lg" asChild>
            <a href={approve} target="_blank" rel="noopener noreferrer">
              Approve in Just Hub
              <External aria-hidden />
            </a>
          </Button>
        )}
        <Button size="lg" variant="outline" onClick={cancel} pending={busy}>
          Cancel
        </Button>
      </div>
    </Section>
  )
}
