"use client"

import { useState } from "react"
import { Check, Copy, External, Warning } from "@/components/icons"
import { countdown, scopeName, siteHost } from "@/lib/account-link"
import { errorMessage } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { AccountLinkScope, AccountLinkStatus } from "@/lib/types"
import { useCopy } from "@/hooks/use-copy"
import { useNow } from "@/hooks/use-now"
import { ScopeCards } from "@/components/account-link/scope-cards"
import { SharedFields } from "@/components/account-link/shared-fields"
import { ShellWords } from "@/components/deploy/run-evidence"
import { Section } from "@/components/page"
import { Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Button } from "@/components/ui/button"

/** The same flow from a terminal; one code path on the backend, so the same approval below. */
const TERMINAL_COMMAND = "sudo ./scripts/manage.sh link"

/**
 * Linking, before it is done: the scope to link with and the button that starts it, then — once a
 * request exists — the code to approve it with and the minutes left to do so.
 */
export function LinkPanel({
  status,
  onBegin,
  onCancel,
}: {
  status: AccountLinkStatus
  onBegin: (scope: AccountLinkScope) => Promise<void>
  onCancel: () => Promise<void>
}) {
  if (status.state === "pending" && status.request) {
    return <Waiting status={status} request={status.request} onCancel={onCancel} />
  }
  return <Choose onBegin={onBegin} />
}

function Choose({ onBegin }: { onBegin: (scope: AccountLinkScope) => Promise<void> }) {
  // Least access first: the person who only presses Link has chosen the scope that cannot act.
  const [scope, setScope] = useState<AccountLinkScope>("observe")
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
    <>
      <Section title="Link this server to your account">
        <ScopeCards value={scope} onChange={setScope} disabled={busy} />
        <div className="flex min-w-0 flex-wrap items-center gap-3">
          <Button onClick={link} pending={busy}>
            Link
          </Button>
        </div>
        {refusal && (
          <div role="alert">
            <Notice tone="danger" icon={Warning} title="Could not start linking">
              {refusal}
            </Notice>
          </div>
        )}
        <div className="min-w-0 space-y-2">
          <p className="text-hint text-muted-foreground">
            Or in a terminal on this server, from the install directory
          </p>
          <Well className="flex max-w-xl items-center justify-between gap-3 px-3 py-2">
            <span className="min-w-0 truncate">
              <span aria-hidden className="text-muted-foreground/60 select-none">
                ${" "}
              </span>
              <ShellWords command={TERMINAL_COMMAND} />
            </span>
            <CopyButton label="Copy command" text={TERMINAL_COMMAND} />
          </Well>
        </div>
      </Section>
      <SharedFields scope={scope} />
    </>
  )
}

function Waiting({
  status,
  request,
  onCancel,
}: {
  status: AccountLinkStatus
  request: NonNullable<AccountLinkStatus["request"]>
  onCancel: () => Promise<void>
}) {
  const now = useNow(1000)
  const left = countdown(request.expiresAt, now)
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
    <>
      <Section title={`Approve this server on ${host}`}>
        <div className="grid min-w-0 items-center gap-x-12 gap-y-6 xl:grid-cols-[minmax(0,26rem)_minmax(0,1fr)]">
          <div className="min-w-0 space-y-2">
            <p className="eyebrow">Code</p>
            <Well plain className="flex items-center justify-between gap-4 px-4 py-3">
              <span className="min-w-0 truncate font-mono text-2xl font-semibold tracking-widest select-all">
                {request.userCode}
              </span>
              <CopyButton label="Copy code" text={request.userCode} />
            </Well>
          </div>
          <StatGrid columns={2}>
            <StatTile
              label="Expires in"
              value={left.label}
              tone={left.expired ? "danger" : "default"}
            />
            <StatTile label="Scope" value={scopeName(request.scope)} />
          </StatGrid>
        </div>

        <div className="flex min-w-0 flex-wrap items-center gap-2">
          {left.expired ? (
            <Button disabled>Approve on {host}</Button>
          ) : (
            <Button asChild>
              <a href={request.approveUrl} target="_blank" rel="noopener noreferrer">
                Approve on {host}
                <External aria-hidden />
              </a>
            </Button>
          )}
          <Button variant="outline" onClick={cancel} pending={busy}>
            Cancel
          </Button>
        </div>

        {status.fingerprint && (
          <p className="text-hint text-muted-foreground">
            {host} shows this server&apos;s fingerprint,{" "}
            <span className="font-mono text-foreground">{status.fingerprint}</span>, before you
            approve. Check that it matches.
          </p>
        )}
      </Section>
      <SharedFields scope={request.scope} />
    </>
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
