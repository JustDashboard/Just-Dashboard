"use client"

import { useState } from "react"
import { Check, Copy, External, Warning } from "@/components/icons"
import { countdown, REQUEST_LIFETIME, scopeName, siteHost } from "@/lib/account-link"
import { errorMessage } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { AccountLinkScope, AccountLinkStatus } from "@/lib/types"
import { useCopy } from "@/hooks/use-copy"
import { useNow } from "@/hooks/use-now"
import { ScopeCards } from "@/components/account-link/scope-cards"
import { ShellWords } from "@/components/deploy/run-evidence"
import { Meter } from "@/components/meter"
import { Section } from "@/components/page"
import { Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { BorderBeam } from "@/components/ui/border-beam"
import { Button } from "@/components/ui/button"

/** The same flow from a terminal; one code path on the backend, so the same approval below. */
const TERMINAL_COMMAND = "sudo ./scripts/manage.sh link"

/**
 * Linking, before it is done: the scope to link with and the button that starts it, then — once a
 * request exists — the code to approve it with and the minutes left to do so.
 *
 * The scope being chosen is the page's, because the picture over this and the list under it both
 * follow it.
 */
export function LinkPanel({
  status,
  scope,
  onScope,
  onBegin,
  onCancel,
  className,
}: {
  status: AccountLinkStatus
  scope: AccountLinkScope
  onScope: (scope: AccountLinkScope) => void
  onBegin: (scope: AccountLinkScope) => Promise<void>
  onCancel: () => Promise<void>
  className?: string
}) {
  if (status.state === "pending" && status.request) {
    return (
      <Waiting status={status} request={status.request} onCancel={onCancel} className={className} />
    )
  }
  return <Choose scope={scope} onScope={onScope} onBegin={onBegin} className={className} />
}

function Choose({
  scope,
  onScope,
  onBegin,
  className,
}: {
  scope: AccountLinkScope
  onScope: (scope: AccountLinkScope) => void
  onBegin: (scope: AccountLinkScope) => Promise<void>
  className?: string
}) {
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()

  const link = async () => {
    setBusy(true)
    setRefusal(undefined)
    try {
      await onBegin(scope)
    } catch (err) {
      setRefusal(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Section title="Choose what the site can do" className={className}>
      <ScopeCards value={scope} onChange={onScope} disabled={busy} />
      <div className="flex min-w-0 flex-wrap items-center gap-x-6 gap-y-4 pt-2">
        <Button size="lg" onClick={link} pending={busy}>
          Link this server
        </Button>
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
          <span className="text-xs text-muted-foreground">or from a terminal</span>
          <Well className="flex min-w-0 items-center gap-3 py-1.5 pr-1.5 pl-3">
            <span className="min-w-0 truncate">
              <span aria-hidden className="text-muted-foreground/60 select-none">
                ${" "}
              </span>
              <ShellWords command={TERMINAL_COMMAND} />
            </span>
            <CopyButton label="Copy command" text={TERMINAL_COMMAND} />
          </Well>
        </div>
      </div>
      {refusal && (
        <div role="alert">
          <Notice tone="danger" icon={Warning} title="Could not start linking">
            {refusal}
          </Notice>
        </div>
      )}
    </Section>
  )
}

function Waiting({
  status,
  request,
  onCancel,
  className,
}: {
  status: AccountLinkStatus
  request: NonNullable<AccountLinkStatus["request"]>
  onCancel: () => Promise<void>
  className?: string
}) {
  const now = useNow(1000)
  const left = countdown(request.expiresAt, now)
  const remaining = Math.max(0, Date.parse(request.expiresAt) - now)
  const [busy, setBusy] = useState(false)
  const host = siteHost(status.siteUrl)

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
    <Section title={`Approve this server on ${host}`} className={className}>
      <div className="grid min-w-0 items-end gap-x-12 gap-y-8 lg:grid-cols-[minmax(0,26rem)_minmax(0,22rem)]">
        <div className="min-w-0 space-y-2">
          <p className="eyebrow">Code</p>
          {/* The request is work in flight until the site answers, so its frame carries the
              light that says so (§11). */}
          <Well plain className="relative flex items-center justify-between gap-4 px-4 py-3">
            {!left.expired && <BorderBeam duration={6} />}
            <span className="min-w-0 truncate font-mono text-2xl font-semibold tracking-widest select-all">
              {request.userCode}
            </span>
            <CopyButton label="Copy code" text={request.userCode} />
          </Well>
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

      <div className="flex min-w-0 flex-wrap items-center gap-2 pt-2">
        {left.expired ? (
          <Button size="lg" disabled>
            Approve on {host}
          </Button>
        ) : (
          <Button size="lg" asChild>
            <a href={request.approveUrl} target="_blank" rel="noopener noreferrer">
              Approve on {host}
              <External aria-hidden />
            </a>
          </Button>
        )}
        <Button size="lg" variant="outline" onClick={cancel} pending={busy}>
          Cancel
        </Button>
      </div>

      {status.fingerprint && (
        <p className="text-xs text-muted-foreground">
          {host} shows this server&apos;s fingerprint,{" "}
          <span className="font-mono text-foreground">{status.fingerprint}</span>, before you
          approve. Check that it matches.
        </p>
      )}
    </Section>
  )
}

function CopyButton({ label, text }: { label: string; text: string }) {
  const { copy, copied } = useCopy()
  return (
    <Button size="xs" variant="outline" onClick={() => void copy(text)} aria-label={label}>
      {copied ? <Check /> : <Copy />}
      {copied ? "Copied" : "Copy"}
    </Button>
  )
}
