"use client"

import { useState } from "react"
import { Key } from "@/components/icons"
import { notify } from "@/lib/toast"
import { post } from "@/lib/api"
import { useAuth } from "@/hooks/use-auth"
import { Field } from "@/components/form"
import { Panel, PanelBody, PanelFooter, PanelHeader, Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

export function PasswordPanel() {
  const { logout } = useAuth()
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")
  const [confirmPw, setConfirmPw] = useState("")
  const [busy, setBusy] = useState(false)

  const mismatch = confirmPw.length > 0 && next !== confirmPw

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
    <Panel plain>
      <PanelHeader title="Password" />
      <PanelBody className="space-y-4">
        <Field label="Current password" htmlFor="cur-pw">
          <Input
            id="cur-pw"
            type="password"
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
          />
        </Field>
        <Field
          label="New password"
          htmlFor="new-pw"
          hint="At least 12 characters, mixing three of: upper case, lower case, digits, symbols."
        >
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
        <p className="text-hint leading-relaxed text-muted-foreground">
          Changing it signs out every session, including this one.
        </p>
      </PanelBody>
      <PanelFooter>
        <Button
          size="sm"
          onClick={change}
          disabled={busy || !current || !next || mismatch}
          pending={busy}
        >
          Change password
        </Button>
      </PanelFooter>
    </Panel>
  )
}

/**
 * Two-factor, as something you turn on rather than something you are handed.
 *
 * It used to be neither: enrolment happened once, during a sign-in nobody
 * could get past without it, and this panel existed only to reissue recovery
 * codes. Now that an install can leave it optional, the account page is where
 * enrolling and un-enrolling actually belong — and the panel has to make the
 * state obvious, because "am I protected by this" is the whole question.
 *
 * The three states are: not enrolled, enrolled, and enrolled on an install
 * that requires it — where the off switch is absent rather than disabled,
 * since a control that cannot be used is a question the operator has to answer
 * for themselves.
 */
export function TwoFactorPanel() {
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

  return (
    <Panel plain>
      <PanelHeader
        title="Two-factor authentication"
        actions={
          <Status
            verdict={enrolled ? "ok" : required ? "warning" : "notice"}
            label={enrolled ? "on" : required ? "required — not yet enrolled" : "off"}
          />
        }
      />
      <PanelBody className="space-y-4">
        {codes ? (
          <>
            <Notice tone="warning" icon={Key} title="Recovery codes">
              Each one works once, in place of your authenticator. Any previous set no longer works,
              and this is the only time these are shown.
            </Notice>
            <Well className="grid grid-cols-2 gap-x-4 gap-y-1.5">
              {codes.map((c) => (
                <span key={c} className="tracking-wider">
                  {c}
                </span>
              ))}
            </Well>
          </>
        ) : enrolled ? (
          <p className="text-xs leading-relaxed text-muted-foreground">
            Your account asks for a code at every sign in. Recovery codes are the way back in if you
            lose the authenticator; regenerating issues a fresh set and invalidates the old one
            immediately.
          </p>
        ) : enrollment ? (
          <div className="space-y-4">
            <Field
              label="Secret"
              hint={
                <>
                  Add it to your authenticator, or{" "}
                  <a href={enrollment.otpauthUrl} className="underline underline-offset-4">
                    open it directly
                  </a>
                  . The seed is sealed with the dashboard&apos;s master key and shown only here.
                </>
              }
            >
              <Well className="font-mono text-body tracking-widest break-all">
                {enrollment.secret}
              </Well>
            </Field>
            <Field label="Code from your app" htmlFor="enrol-code">
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
          </div>
        ) : disabling ? (
          <div className="space-y-4">
            <Notice tone="warning" title="Your password becomes the only factor">
              This dashboard is root-equivalent. A session left open on an unlocked laptop is
              exactly what the second factor answers, which is why turning it off costs a password.
            </Notice>
            <Field label="Current password" htmlFor="disable-pw">
              <Input
                id="disable-pw"
                type="password"
                autoComplete="current-password"
                className="max-w-72"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </Field>
          </div>
        ) : (
          <p className="text-xs leading-relaxed text-muted-foreground">
            {required
              ? "This install requires an authenticator. Enrol one now — until you do, your session can reach nothing but this page."
              : "Not required on this install, and worth having anyway: a password alone is one stolen credential away from root on this server."}
          </p>
        )}
      </PanelBody>
      <PanelFooter className="gap-2">
        {enrolled && !codes && (
          <Button
            size="sm"
            variant="outline"
            disabled={busy}
            onClick={async () => {
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
            }}
          >
            Regenerate recovery codes
          </Button>
        )}

        {enrolled && !required && !disabling && (
          <Button size="sm" variant="ghost" onClick={() => setDisabling(true)}>
            Turn off
          </Button>
        )}

        {disabling && (
          <>
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
          </>
        )}

        {!enrolled &&
          (enrollment ? (
            <>
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
            </>
          ) : (
            <Button size="sm" onClick={begin} pending={busy}>
              Enable two-factor
            </Button>
          ))}

        {codes && (
          <Button size="sm" variant="outline" onClick={() => setCodes(null)}>
            I have saved them
          </Button>
        )}
      </PanelFooter>
    </Panel>
  )
}
