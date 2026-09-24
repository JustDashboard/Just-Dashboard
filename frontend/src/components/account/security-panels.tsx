"use client"

import { useState } from "react"
import Link from "next/link"
import { ArrowRight, Check, Copy, Key, ShieldCheck, ShieldOff } from "@/components/icons"
import { notify } from "@/lib/toast"
import { post } from "@/lib/api"
import { plural } from "@/lib/format"
import { parseAgent } from "@/lib/clients"
import { passwordChecks } from "@/lib/password"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useCopy } from "@/hooks/use-copy"
import { Field, FieldCheck, FieldRow, FormSection } from "@/components/form"
import { Well } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { TotpSecret } from "@/components/account/totp-secret"
import type { useSessions } from "@/components/account/sessions"

export function PasswordSection() {
  const { logout } = useAuth()
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")
  const [confirmPw, setConfirmPw] = useState("")
  const [busy, setBusy] = useState(false)

  const mismatch = confirmPw.length > 0 && next !== confirmPw
  const checks = passwordChecks(next)

  const change = async () => {
    if (next !== confirmPw) {
      notify.error("The new passwords do not match")
      return
    }
    setBusy(true)
    try {
      await post("/account/password", { currentPassword: current, newPassword: next })
      notify.success("Password changed", { description: "All sessions were signed out." })
      // The server drops every session on a password change, so the only
      // correct next step is back to the login screen.
      await logout()
    } catch (err) {
      notify.error("Could not change password", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <FormSection
      aside
      title="Password"
      hint="Changing it signs out every session, this one included."
    >
      <Field label="Current password" htmlFor="cur-pw" className="max-w-sm">
        <Input
          id="cur-pw"
          type="password"
          autoComplete="current-password"
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
        />
      </Field>
      <div className="space-y-2">
        <FieldRow>
          <Field label="New password" htmlFor="new-pw">
            <Input
              id="new-pw"
              type="password"
              autoComplete="new-password"
              value={next}
              onChange={(e) => setNext(e.target.value)}
            />
          </Field>
          <Field
            label="Confirm new password"
            htmlFor="conf-pw"
            error={mismatch ? "These do not match yet." : undefined}
          >
            <Input
              id="conf-pw"
              type="password"
              autoComplete="new-password"
              value={confirmPw}
              onChange={(e) => setConfirmPw(e.target.value)}
            />
          </Field>
        </FieldRow>
        <p className="flex flex-wrap gap-x-4 gap-y-1" aria-live="polite">
          <FieldCheck met={checks.long}>12 characters or more</FieldCheck>
          <FieldCheck met={checks.mixed}>
            three of upper case, lower case, digits, symbols
            {next && !checks.mixed && ` · ${checks.classes} so far`}
          </FieldCheck>
        </p>
      </div>
      <Button
        size="sm"
        onClick={change}
        disabled={busy || !current || !checks.ok || next !== confirmPw}
        pending={busy}
      >
        Change password
      </Button>
    </FormSection>
  )
}

/** The codes, shown once, in a grid that reads down and copies whole. */
function RecoveryCodes({ codes, onDone }: { codes: string[]; onDone: () => void }) {
  const { copy, copied } = useCopy()
  return (
    <>
      <Notice tone="warning" icon={Key} title="Save these recovery codes">
        Each one works once, in place of your authenticator. Any previous set no longer works, and
        this is the only time these are shown.
      </Notice>
      <Well className="grid max-w-md grid-cols-2 gap-x-4 gap-y-1.5">
        {codes.map((c) => (
          <span key={c} className="tracking-wider">
            {c}
          </span>
        ))}
      </Well>
      <div className="flex flex-wrap gap-2">
        <Button size="sm" variant="outline" onClick={() => void copy(codes.join("\n"))}>
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
          {copied ? "Copied" : "Copy all"}
        </Button>
        <Button size="sm" onClick={onDone}>
          I have saved them
        </Button>
      </div>
    </>
  )
}

/**
 * The state two-factor is in, as a mark and a sentence at the head of the
 * section: the one thing on this page a reader should find without reading.
 */
function Verdict({
  on,
  title,
  children,
}: {
  on: boolean
  title: string
  children: React.ReactNode
}) {
  const Mark = on ? ShieldCheck : ShieldOff
  return (
    <div className="flex min-w-0 items-start gap-3">
      <Mark
        aria-hidden
        className={cn("mt-0.5 size-5 shrink-0", on ? "text-success" : "text-warning")}
      />
      <div className="min-w-0 space-y-1">
        <p className="text-sm font-medium">{title}</p>
        <p className="max-w-prose text-hint leading-relaxed text-muted-foreground">{children}</p>
      </div>
    </div>
  )
}

/**
 * Two-factor, as something you turn on rather than something you are handed.
 *
 * It used to be neither: enrolment happened once, during a sign-in nobody
 * could get past without it, and this panel existed only to reissue recovery
 * codes. Now that an install can leave it optional, the account page is where
 * enrolling and un-enrolling actually belong — and the section has to make the
 * state obvious, because "am I protected by this" is the whole question.
 *
 * The three states are: not enrolled, enrolled, and enrolled on an install
 * that requires it — where the off switch is absent rather than disabled,
 * since a control that cannot be used is a question the operator has to answer
 * for themselves. The state is the section's rail head, and its verdict opens
 * the section in the colour of what it means.
 */
export function TwoFactorSection() {
  const { status, refresh } = useAuth()
  const [enrollment, setEnrollment] = useState<{ secret: string; otpauthUrl: string } | null>(null)
  const [code, setCode] = useState("")
  const [codes, setCodes] = useState<string[] | null>(null)
  const [password, setPassword] = useState("")
  const [disabling, setDisabling] = useState(false)
  const [busy, setBusy] = useState(false)

  const enrolled = Boolean(status?.user?.totpEnabled)
  const required = Boolean(status?.require2fa)

  const begin = async () => {
    setBusy(true)
    try {
      setEnrollment(await post<{ secret: string; otpauthUrl: string }>("/auth/2fa/setup"))
    } catch (err) {
      notify.error("Could not start enrolment", err)
    } finally {
      setBusy(false)
    }
  }

  const enable = async () => {
    setBusy(true)
    try {
      const res = await post<{ recoveryCodes: string[] }>("/auth/2fa/enable", { code })
      setCodes(res.recoveryCodes)
      setEnrollment(null)
      setCode("")
      await refresh().catch(() => undefined)
      notify.success("Two-factor enabled", {
        description: "You will be asked for a code the next time you sign in.",
      })
    } catch (err) {
      notify.error("Code rejected", err)
    } finally {
      setBusy(false)
    }
  }

  const disable = async () => {
    setBusy(true)
    try {
      await post("/account/2fa/disable", { password })
      setPassword("")
      setDisabling(false)
      setCodes(null)
      await refresh().catch(() => undefined)
      notify.success("Two-factor turned off", {
        description: "Your password is now the only thing between a session and this server.",
      })
    } catch (err) {
      notify.error("Could not turn it off", err)
    } finally {
      setBusy(false)
    }
  }

  const regenerate = async () => {
    setBusy(true)
    try {
      const res = await post<{ recoveryCodes: string[] }>("/account/recovery-codes")
      setCodes(res.recoveryCodes)
      notify.success("Recovery codes regenerated", {
        description: "The previous set no longer works.",
      })
    } catch (err) {
      notify.error("Could not regenerate", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <FormSection
      aside
      title="Two-factor"
      hint={
        <span className="flex flex-col items-start gap-1.5">
          <Status
            verdict={enrolled ? "ok" : required ? "warning" : "notice"}
            label={enrolled ? "on" : required ? "required — not yet enrolled" : "off"}
          />
          <span>{required ? "Required on this install." : "Optional on this install."}</span>
        </span>
      }
    >
      {codes ? (
        <RecoveryCodes codes={codes} onDone={() => setCodes(null)} />
      ) : enrollment ? (
        <>
          <TotpSecret secret={enrollment.secret} otpauthUrl={enrollment.otpauthUrl} />
          <Field
            label="Code from your app"
            htmlFor="enrol-code"
            hint="The seed is sealed with the dashboard's master key and shown only here."
          >
            <Input
              id="enrol-code"
              inputMode="numeric"
              maxLength={6}
              placeholder="000000"
              className="h-11 max-w-48 text-center font-mono text-lg tracking-[0.4em]"
              value={code}
              onChange={(e) => setCode(e.target.value)}
            />
          </Field>
          <div className="flex flex-wrap gap-2">
            <Button size="sm" disabled={busy || code.length < 6} onClick={enable} pending={busy}>
              Enable two-factor
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                setEnrollment(null)
                setCode("")
              }}
            >
              Cancel
            </Button>
          </div>
        </>
      ) : disabling ? (
        <>
          <Notice tone="warning" title="Your password becomes the only factor">
            This dashboard is root-equivalent. A session left open on an unlocked laptop is exactly
            what the second factor answers, which is why turning it off costs a password.
          </Notice>
          <Field label="Current password" htmlFor="disable-pw" className="max-w-sm">
            <Input
              id="disable-pw"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
          <div className="flex flex-wrap gap-2">
            <Button
              size="sm"
              variant="destructive"
              disabled={busy || !password}
              onClick={disable}
              pending={busy}
            >
              Turn two-factor off
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                setDisabling(false)
                setPassword("")
              }}
            >
              Cancel
            </Button>
          </div>
        </>
      ) : enrolled ? (
        <>
          <Verdict on title="A code is asked for at every sign-in">
            Recovery codes are the way back in if you lose the authenticator; regenerating issues a
            fresh set and ends the old one at once.
          </Verdict>
          <div className="flex flex-wrap gap-2">
            <Button size="sm" variant="outline" disabled={busy} onClick={regenerate}>
              Regenerate recovery codes
            </Button>
            {!required && (
              <Button size="sm" variant="ghost" onClick={() => setDisabling(true)}>
                Turn off
              </Button>
            )}
          </div>
        </>
      ) : (
        <>
          <Verdict on={false} title="Your password is the only factor">
            {required
              ? "This install requires an authenticator. Enrol one now — until you do, your session can reach nothing but this page."
              : "Not required on this install, and worth having anyway: a password alone is one stolen credential away from root on this server."}
          </Verdict>
          <div>
            <Button size="sm" onClick={begin} pending={busy}>
              Enable two-factor
            </Button>
          </div>
        </>
      )}
    </FormSection>
  )
}

/**
 * Where this account is signed in, drawn as the clients themselves, beside
 * the password that would sign every one of them out. The sessions page is
 * where they are read one by one.
 */
export function SessionsSection({ sessions }: { sessions: ReturnType<typeof useSessions> }) {
  // The sessions page says why they could not be read; here the section is
  // only a way there, and one stuck on "loading" would be a reading that lies.
  if (sessions.error && !sessions.data) return null
  const list = sessions.data ?? []
  const clients = [
    ...new Set(list.map((s) => parseAgent(s.userAgent).product).filter((id) => id !== undefined)),
  ]
  const bare = list.filter((s) => !s.twoFactorPassed).length
  return (
    <FormSection
      aside
      title="Sessions"
      hint={
        sessions.data
          ? `${plural(list.length, "session")} signed in${bare > 0 ? ` · ${bare} on a password alone` : ""}`
          : "loading"
      }
    >
      <div className="flex flex-wrap items-center gap-3">
        {clients.length > 0 && (
          <span aria-hidden className="flex items-center gap-1">
            {clients.map((id) => (
              <ProductLogo key={id} id={id} size="sm" />
            ))}
          </span>
        )}
        <Button size="sm" variant="outline" asChild>
          <Link href="/account/sessions">
            Review where you are signed in
            <ArrowRight className="size-3.5" />
          </Link>
        </Button>
      </div>
    </FormSection>
  )
}
