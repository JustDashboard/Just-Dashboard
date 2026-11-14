"use client"

import { useState } from "react"
import Link from "next/link"
import { ArrowRight, Check, Copy, Key } from "@/components/icons"
import { notify } from "@/lib/toast"
import { post } from "@/lib/api"
import { plural } from "@/lib/format"
import { describeClient } from "@/lib/clients"
import { passwordChecks } from "@/lib/password"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useCopy } from "@/hooks/use-copy"
import { Field, FieldCheck, FieldRow, InfoTip } from "@/components/form"
import { useSaveBarEntry } from "@/components/deploy/settings/save-bar"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import { Well } from "@/components/panel"
import { ClientMark, NetworkFact } from "@/components/client-mark"
import { Row, RowList } from "@/components/row-list"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { TotpSecret } from "@/components/account/totp-secret"
import type { useSessions } from "@/components/account/sessions"
import { sessionGuard } from "@/components/account/access"
import { GUARD_WORDS } from "@/components/account/session-map"

/**
 * The password, as a section of the account's settings column.
 *
 * Changing it is the page's one write that is typed rather than pressed, so it
 * is the floating bar's: the bar comes up once a new password is being typed,
 * its command is *Change password*, and its line says what is still missing or
 * that every session is signed out. The current password alone does not raise
 * it — a password manager fills that field in on its own, and a bar on every
 * visit would be about nothing the reader did. Nothing here is prefilled or
 * shown: the three fields start empty, are `type="password"`, and what is
 * typed leaves in the one request the button always sent.
 */
export function PasswordSection() {
  const { logout } = useAuth()
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")
  const [confirmPw, setConfirmPw] = useState("")
  const [busy, setBusy] = useState(false)

  const mismatch = confirmPw.length > 0 && next !== confirmPw
  const checks = passwordChecks(next)
  const dirty = next.length > 0 || confirmPw.length > 0
  const missing = !current
    ? "Type your current password"
    : !checks.ok
      ? "The new password does not meet the rule yet"
      : next !== confirmPw
        ? "The new passwords do not match yet"
        : undefined

  const change = async () => {
    setBusy(true)
    try {
      await post("/account/password", { currentPassword: current, newPassword: next })
      notify.success("Password changed", { description: "All sessions were signed out." })
      // The server drops every session on a password change, so the only
      // correct next step is back to the login screen.
      await logout()
      return true
    } catch (err) {
      notify.error("Could not change password", err)
      return false
    } finally {
      setBusy(false)
    }
  }

  const saveAll = useSaveBarEntry(
    true,
    {
      name: "Password",
      dirty,
      changes: 1,
      saving: busy,
      invalid: Boolean(missing),
      applies: "immediately",
      verb: "Change password",
      note: missing ?? "Signs out every session, this one included",
    },
    {
      save: change,
      discard: () => {
        setCurrent("")
        setNext("")
        setConfirmPw("")
      },
    },
  )

  return (
    <SettingSection title="Password" state="changing it signs out every session, this one included">
      {/* A form, so Enter in a field is the bar's command. */}
      <form
        aria-label="Password"
        className="space-y-5"
        onSubmit={(event) => {
          event.preventDefault()
          void saveAll?.()
        }}
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
        {/* Enter submits a form only where it has a submit control. This
            one is never drawn or reached: the floating bar is the control. */}
        <button type="submit" hidden tabIndex={-1} aria-label="Save" />
      </form>
    </SettingSection>
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
 * for themselves. The state is the head's one line of facts, in the colour of
 * what it means — on and asked at every sign-in, or off with the password the
 * only factor, and whether this install requires it — and under it are the
 * verbs, with why behind the ⓘ beside them. A verb opens its own short form in
 * the section; each still asks for what it asked for.
 */
export function TwoFactorSection() {
  const { status, refresh } = useAuth()
  const [enrollment, setEnrollment] = useState<{ secret: string; otpauthUrl: string } | null>(null)
  const [code, setCode] = useState("")
  const [codes, setCodes] = useState<string[] | null>(null)
  const [password, setPassword] = useState("")
  const [disabling, setDisabling] = useState(false)
  const [regenerating, setRegenerating] = useState(false)
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
      const res = await post<{ recoveryCodes: string[] }>("/account/recovery-codes", { password })
      setCodes(res.recoveryCodes)
      setPassword("")
      setRegenerating(false)
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
    <SettingSection
      title="Two-factor"
      state={
        <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
          <Status
            verdict={enrolled ? "ok" : required ? "warning" : "notice"}
            label={enrolled ? "on" : required ? "required — not yet enrolled" : "off"}
          />
          <span>
            {enrolled ? "asked at every sign-in" : "your password is the only factor"}
            {" · "}
            {required ? "required on this install" : "optional on this install"}
          </span>
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
            info="The seed is sealed with the dashboard's master key and shown only here."
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
      ) : regenerating ? (
        <>
          <Notice title="The old set stops working at once">
            A recovery code passes for your authenticator, so a new set costs your password. An open
            session is not enough.
          </Notice>
          <Field label="Current password" htmlFor="regenerate-pw" className="max-w-sm">
            <Input
              id="regenerate-pw"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
          <div className="flex flex-wrap gap-2">
            <Button size="sm" disabled={busy || !password} onClick={regenerate} pending={busy}>
              Regenerate recovery codes
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                setRegenerating(false)
                setPassword("")
              }}
            >
              Cancel
            </Button>
          </div>
        </>
      ) : enrolled ? (
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" variant="outline" disabled={busy} onClick={() => setRegenerating(true)}>
            Regenerate recovery codes
          </Button>
          {!required && (
            <Button size="sm" variant="ghost" onClick={() => setDisabling(true)}>
              Turn off
            </Button>
          )}
          <InfoTip label="What recovery codes are for">
            Recovery codes are the way back in if you lose the authenticator; regenerating issues a
            fresh set and ends the old one at once.
          </InfoTip>
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" onClick={begin} pending={busy}>
            Enable two-factor
          </Button>
          <InfoTip label="Why enable two-factor">
            {required
              ? "This install requires an authenticator. Enrol one now — until you do, your session can reach nothing but this page."
              : "Not required on this install, and worth having anyway: a password alone is one stolen credential away from root on this server."}
          </InfoTip>
        </div>
      )}
    </SettingSection>
  )
}

/**
 * Where this account is signed in, each session drawn as the client it is
 * with the network it came in on and what let it in, under the password
 * that would sign every one of them out. The sessions page is where they are
 * read in full and signed out one by one, and the way there is the head's.
 */
export function SessionsSection({ sessions }: { sessions: ReturnType<typeof useSessions> }) {
  // The sessions page says why they could not be read; here the section is
  // only a way there, and one stuck on "loading" would be a reading that lies.
  if (sessions.error && !sessions.data) return null
  const list = [...(sessions.data ?? [])].sort(
    (a, b) => Number(b.current) - Number(a.current) || b.lastSeenAt.localeCompare(a.lastSeenAt),
  )
  const bare = list.filter((s) => s.authMethod === "password" && s.twoFactorPassed).length
  const pending = list.filter((s) => !s.twoFactorPassed).length
  const unknown = list.filter((s) => !s.authMethod || s.authMethod === "unknown").length
  const shown = list.slice(0, 4)
  return (
    <SettingSection
      title="Sessions"
      state={
        sessions.data
          ? `${plural(list.length, "session")} signed in${bare > 0 ? ` · ${bare} on a password alone` : ""}${pending > 0 ? ` · ${pending} awaiting a second factor` : ""}${unknown > 0 ? ` · ${unknown} authentication method not recorded` : ""}`
          : "loading"
      }
      actions={
        <Button size="sm" variant="outline" asChild>
          <Link href="/account/sessions" aria-label="Review where you are signed in">
            Review
            <ArrowRight className="size-3.5" />
          </Link>
        </Button>
      }
    >
      {shown.length > 0 && (
        <RowList>
          {shown.map((session) => {
            const guard = sessionGuard(session)
            return (
              <Row
                key={session.id}
                className="px-0"
                leading={<ClientMark userAgent={session.userAgent} />}
                title={describeClient(session.userAgent)}
                subtitle={<NetworkFact ip={session.ip} />}
                trailing={
                  <span
                    className={cn(
                      "text-hint",
                      guard === "password" || guard === "pending"
                        ? "text-warning"
                        : "text-muted-foreground",
                    )}
                  >
                    {session.current ? "this device" : GUARD_WORDS[guard]}
                  </span>
                }
              />
            )
          })}
        </RowList>
      )}
    </SettingSection>
  )
}
