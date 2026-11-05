"use client"

import { useId, useState } from "react"
import { External, Warning } from "@/components/icons"
import { countdown, REQUEST_LIFETIME, scopeName } from "@/lib/account-link"
import { errorMessage } from "@/lib/api"
import { hubHost, openable, terminalCommand } from "@/lib/hub-link"
import { notify } from "@/lib/toast"
import type { HubLinkScope, HubLinkStatus } from "@/lib/types"
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

/**
 * Connecting, before it is done: the hub's address, the scope, and the button — and, once a
 * request exists, the code and this server's fingerprint to check in the hub, and the minutes
 * left to approve it.
 *
 * Nothing here acts on its own. A link from the hub (`?connect=<hub host>`) only fills the
 * address in; a server administrator still presses Connect, and the backend still refuses any
 * hub that is not one machine on this server's own tailnet (hub-link-protocol §2.2).
 */
export function ConnectPanel({
  address,
  onAddress,
  invited,
  scope,
  onScope,
  onBegin,
  className,
}: {
  address: string
  onAddress: (address: string) => void
  /** The hub that sent the person here, by host; the section asks about it by name. */
  invited?: string
  scope: HubLinkScope
  onScope: (scope: HubLinkScope) => void
  onBegin: (url: string, scope: HubLinkScope) => Promise<void>
  className?: string
}) {
  const field = useId()
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const url = address.trim()

  const connect = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!url || busy) return
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
      <form onSubmit={connect} className="flex min-w-0 flex-col gap-6">
        <Field
          label="Hub address"
          htmlFor={field}
          hint="The hub's name on this server's tailnet, ending in .ts.net. Nothing is sent until you press Connect."
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

        <div className="flex min-w-0 flex-col gap-3">
          <p className="eyebrow">What the hub can do</p>
          <ScopeCards value={scope} onChange={onScope} disabled={busy} />
          <p className="text-xs text-muted-foreground">
            No scope gives the hub a shell, files or accounts, and this server refuses any command
            aimed at its own containers. The scope can be lowered at any time.
          </p>
        </div>

        <div className="flex min-w-0 flex-wrap items-center gap-x-6 gap-y-4">
          <Button size="lg" type="submit" pending={busy} disabled={!url}>
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

/**
 * The request, while the hub owner decides: the code and this server's key fingerprint, which
 * the hub's approval screen shows too (hub-link-protocol §4.1), the time left, and the way to the
 * approval. Neither machine can link the other alone; the two matching is what the person checks.
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

      <p className="text-xs text-muted-foreground">
        The hub shows the same code and fingerprint on its approval screen. Approve only if both
        match.
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
