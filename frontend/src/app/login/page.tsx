"use client"

import { useEffect, useRef, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import {
  ArrowRight,
  Check,
  Copy,
  Eye,
  EyeOff,
  Fingerprint,
  Key,
  ShieldCheck,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError, post } from "@/lib/api"
import { passwordChecks } from "@/lib/password"
import { conditionalSignInAvailable, usePasskeyPlace } from "@/lib/passkeys"
import { cn } from "@/lib/utils"
import type { AuthStatus } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Notice } from "@/components/state"
import { Logo } from "@/components/logo"
import { useCopy } from "@/hooks/use-copy"
import { Well } from "@/components/panel"
import { FieldCheck } from "@/components/form"
import { FlowPanel, FlowSteps } from "@/components/flow"
import { TotpSecret } from "@/components/account/totp-secret"
import { ProductOrbit } from "@/components/onboarding/product-orbit"

type Step = "credentials" | "totp" | "enroll" | "password"

const HEADINGS: Record<Step, string> = {
  credentials: "Sign in",
  totp: "Two-factor code",
  enroll: "Set up two-factor",
  password: "Change your password",
}

/**
 * Signing in.
 *
 * One card: the form, and beside it on a wide screen the picture of what is
 * on the other side of it — this server wired to the products the dashboard
 * drives, on the arrival ground's colour. It was a lone form on a grid of
 * hairlines, which read as graph paper behind a login box; the picture is the
 * same drawing the first-run question opens on, so the two screens a new
 * account sees first are one piece. On a phone the picture is a strip across
 * the top of the card, and the form keeps the width.
 *
 * Every step after the first is only as long as the account needs: the spine
 * names the steps this account actually has — a code where it has an
 * authenticator, a new password where it owes one — and is not drawn at all
 * before the server has said which those are. It used to promise "Verify" to
 * every account, two-factor or not.
 */
export default function LoginPage() {
  const router = useRouter()
  const endedInUrl = useSearchParams().get("reason") === "session"
  const { status, loading, sessionEnded, login, loginWithPasskey, verifyTotp, refresh, logout } =
    useAuth()
  // "auto" defers to whatever the session says; the explicit values are set
  // only once the user has moved the flow forward themselves.
  const [chosenStep, setChosenStep] = useState<Step | "auto">("auto")
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [newPassword, setNewPassword] = useState("")
  const [repeatPassword, setRepeatPassword] = useState("")
  const [showPassword, setShowPassword] = useState(false)
  const [forgot, setForgot] = useState(false)
  const [code, setCode] = useState("")
  const [recoveryMode, setRecoveryMode] = useState(false)
  const [busy, setBusy] = useState(false)
  const [enrollment, setEnrollment] = useState<{ secret: string; otpauthUrl: string } | null>(null)
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null)
  // Enrolled on this page: the session is complete, so no code step follows.
  const [enrolled, setEnrolled] = useState(false)

  const step: Step =
    chosenStep !== "auto"
      ? chosenStep
      : status?.needsTotp
        ? "totp"
        : status?.needsEnrollment
          ? "enroll"
          : status?.needsPasswordChange
            ? "password"
            : "credentials"
  const setStep = setChosenStep
  const steps = spineFor(step, status, enrolled)
  const here = steps.findIndex((s) => s.key === step)
  const hint = hintFor(step, recoveryMode, Boolean(recoveryCodes))

  useEffect(() => {
    if (!loading && status?.authenticated) router.replace("/")
  }, [loading, status, router])

  // Where the page is open decides whether a passkey can be offered at all.
  const place = usePasskeyPlace()
  // The quiet offer: the browser lists this dashboard's passkeys in the
  // username field's autofill while the form is shown, and picking one signs
  // in. It is withdrawn for the button's prompt, which asks out loud.
  const conditional = useRef<AbortController | null>(null)
  const advanceRef = useRef<(next: AuthStatus) => void>(() => undefined)
  useEffect(() => {
    if (step !== "credentials" || !place?.usable || loading) return
    const controller = new AbortController()
    conditional.current = controller
    void (async () => {
      if (!(await conditionalSignInAvailable()) || controller.signal.aborted) return
      try {
        advanceRef.current(await loginWithPasskey({ conditional: true, signal: controller.signal }))
      } catch (err) {
        if (controller.signal.aborted) return
        notify.error("Passkey sign-in failed", err instanceof ApiError ? err.message : String(err))
      }
    })()
    return () => controller.abort()
  }, [step, place, loading, loginWithPasskey])

  const advance = (next: AuthStatus) => {
    if (next.authenticated) router.replace("/")
    else if (next.needsTotp) setStep("totp")
    else if (next.needsEnrollment) setStep("enroll")
    else if (next.needsPasswordChange) setStep("password")
  }

  useEffect(() => {
    advanceRef.current = advance
  })

  const signInWithPasskey = async () => {
    conditional.current?.abort()
    setBusy(true)
    try {
      advance(await loginWithPasskey({}))
    } catch (err) {
      // Closing the browser's prompt is a choice, not a failure.
      if (err instanceof DOMException && err.name === "NotAllowedError") return
      notify.error("Passkey sign-in failed", err instanceof ApiError ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const submitCredentials = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try {
      advance(await login(username, password))
    } catch (err) {
      notify.error("Sign in failed", err instanceof ApiError ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const beginEnrollment = async () => {
    setBusy(true)
    try {
      setEnrollment(await post<{ secret: string; otpauthUrl: string }>("/auth/2fa/setup"))
    } catch (err) {
      notify.error("Could not start enrolment", err instanceof ApiError ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const submitEnrollment = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try {
      const res = await post<{ recoveryCodes: string[] }>("/auth/2fa/enable", { code })
      setRecoveryCodes(res.recoveryCodes)
      setEnrolled(true)
      setCode("")
    } catch (err) {
      notify.error("Code rejected", err instanceof ApiError ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const submitTotp = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try {
      const next = await verifyTotp(code)
      if (next.authenticated) router.replace("/")
      else if (next.needsPasswordChange) setStep("password")
    } catch (err) {
      notify.error("Code rejected", err instanceof ApiError ? err.message : String(err))
      setCode("")
    } finally {
      setBusy(false)
    }
  }

  const submitPassword = async (e: React.FormEvent) => {
    e.preventDefault()
    if (newPassword !== repeatPassword) {
      notify.error("Passwords do not match")
      return
    }
    // The server ends every session on a change, this one included. Signing
    // straight back in with the password just chosen is what the reader would
    // do next anyway; asking them to type it a third time was the step that
    // made a forced change feel like a loop.
    const account = status?.user?.username ?? username
    setBusy(true)
    try {
      await post("/account/password", { currentPassword: password, newPassword })
    } catch (err) {
      notify.error("Could not change password", err instanceof ApiError ? err.message : String(err))
      setBusy(false)
      return
    }
    setPassword("")
    setNewPassword("")
    setRepeatPassword("")
    setCode("")
    setRecoveryMode(false)
    setUsername(account)
    notify.success("Password changed")
    try {
      advance(await login(account, newPassword))
    } catch {
      // Whatever stopped it, the new password is set; the reader signs in
      // with it by hand from here.
      await refresh()
      setStep("credentials")
    } finally {
      setBusy(false)
    }
  }

  // Enrollment consumes its proof and completes this session atomically.
  // Only advance after the operator has saved the one-time recovery codes.
  const finishEnrollment = async () => {
    setBusy(true)
    try {
      const next = await refresh()
      setRecoveryCodes(null)
      setEnrollment(null)
      setStep("auto")
      if (!next?.needsPasswordChange) setPassword("")
      notify.success("Two-factor enabled", {
        description: next?.needsPasswordChange
          ? "Set your new password to continue."
          : "Your authenticator is ready.",
      })
    } catch (err) {
      notify.error(
        "Could not refresh your session",
        err instanceof ApiError ? err.message : String(err),
      )
    } finally {
      setBusy(false)
    }
  }

  const startOver = async () => {
    await logout()
    setStep("auto")
    setCode("")
    setPassword("")
    setRecoveryMode(false)
    setEnrollment(null)
    setEnrolled(false)
  }

  return (
    <div className="auth-backdrop flex min-h-svh flex-col bg-background">
      <main className="mx-auto flex w-full max-w-[60rem] flex-1 flex-col justify-center px-4 py-6 sm:px-6 sm:py-10">
        <FlowPanel className="animate-rise lg:grid lg:min-h-[36rem] lg:grid-cols-[minmax(0,1fr)_minmax(0,1.05fr)]">
          <div className="flex min-w-0 flex-col gap-7 p-5 sm:gap-8 sm:p-9">
            <Logo size="md" />
            <ProductOrbit layout="row" className="-mx-1 lg:hidden" />

            <div className="flex flex-1 flex-col justify-center gap-6">
              <header className="space-y-4">
                <div className="space-y-1.5">
                  <p className="eyebrow">
                    {steps.length > 1 ? `Step ${here + 1} of ${steps.length}` : "Welcome"}
                  </p>
                  <h1 className="text-2xl leading-tight font-semibold tracking-tight text-balance">
                    {recoveryCodes ? "Save your recovery codes" : HEADINGS[step]}
                  </h1>
                  {hint && (
                    <p className="text-body leading-relaxed text-pretty text-muted-foreground">
                      {hint}
                    </p>
                  )}
                </div>
                {steps.length > 1 && <FlowSteps steps={steps} current={here} />}
              </header>

              {(sessionEnded || endedInUrl) && step === "credentials" && (
                <Notice tone="warning" title="Session ended">
                  Your session ended. Sign in again to continue.
                </Notice>
              )}

              {recoveryCodes ? (
                <RecoveryCodes codes={recoveryCodes} onDone={finishEnrollment} busy={busy} />
              ) : (
                <>
                  {step === "credentials" && (
                    <form onSubmit={submitCredentials} className="space-y-4">
                      <div className="space-y-1.5">
                        <Label htmlFor="username">Username</Label>
                        <Input
                          id="username"
                          autoFocus
                          // "webauthn" lets the browser list passkeys here.
                          autoComplete="username webauthn"
                          autoCapitalize="none"
                          spellCheck={false}
                          value={username}
                          onChange={(e) => setUsername(e.target.value)}
                          required
                        />
                      </div>
                      <div className="space-y-1.5">
                        <div className="flex items-center justify-between gap-3">
                          <Label htmlFor="password">Password</Label>
                          <button
                            type="button"
                            aria-expanded={forgot}
                            onClick={() => setForgot((v) => !v)}
                            className="rounded-sm text-hint text-muted-foreground underline-offset-4 focus-ring transition-colors hover:text-foreground hover:underline"
                          >
                            Forgot it?
                          </button>
                        </div>
                        <div className="relative">
                          <Input
                            id="password"
                            type={showPassword ? "text" : "password"}
                            autoComplete="current-password"
                            className="pr-10"
                            value={password}
                            onChange={(e) => setPassword(e.target.value)}
                            required
                          />
                          <button
                            type="button"
                            onClick={() => setShowPassword((v) => !v)}
                            aria-label={showPassword ? "Hide password" : "Show password"}
                            className="absolute top-1/2 right-1.5 flex size-8 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground focus-ring transition-colors hover:text-foreground"
                          >
                            {showPassword ? (
                              <EyeOff className="size-4" />
                            ) : (
                              <Eye className="size-4" />
                            )}
                          </button>
                        </div>
                      </div>
                      {forgot && <ForgotPassword username={username} />}
                      <Button type="submit" size="lg" className="w-full" pending={busy}>
                        Continue
                        {!busy && <ArrowRight className="size-4" />}
                      </Button>
                      {place?.usable ? (
                        <Button
                          type="button"
                          variant="outline"
                          size="lg"
                          className="w-full"
                          disabled={busy}
                          onClick={() => void signInWithPasskey()}
                        >
                          <Fingerprint className="size-4" />
                          Sign in with a passkey
                        </Button>
                      ) : (
                        place &&
                        place.reason && (
                          <p className="flex items-start gap-2 text-hint text-muted-foreground">
                            <Fingerprint aria-hidden className="mt-px size-3.5 shrink-0" />
                            {place.reason}
                          </p>
                        )
                      )}
                    </form>
                  )}

                  {step === "password" && (
                    <ChangePassword
                      knowsCurrent={password !== ""}
                      current={password}
                      onCurrent={setPassword}
                      next={newPassword}
                      onNext={setNewPassword}
                      repeat={repeatPassword}
                      onRepeat={setRepeatPassword}
                      busy={busy}
                      onSubmit={submitPassword}
                    />
                  )}

                  {step === "totp" && (
                    <form onSubmit={submitTotp} className="space-y-4">
                      <div className="space-y-1.5">
                        <Label htmlFor="code">
                          {recoveryMode ? "Recovery code" : "Verification code"}
                        </Label>
                        <Input
                          id="code"
                          autoFocus
                          key={recoveryMode ? "recovery" : "totp"}
                          inputMode={recoveryMode ? "text" : "numeric"}
                          autoComplete="one-time-code"
                          placeholder={recoveryMode ? "xxxx-xxxx" : "000000"}
                          maxLength={recoveryMode ? 32 : 6}
                          className={cn(
                            "h-14 text-center font-mono sm:h-14",
                            recoveryMode ? "text-base" : "text-2xl tracking-[0.45em] sm:text-2xl",
                          )}
                          value={code}
                          onChange={(e) => setCode(e.target.value)}
                          required
                        />
                      </div>
                      <Button
                        type="submit"
                        size="lg"
                        className="w-full"
                        disabled={busy || !code}
                        pending={busy}
                      >
                        Verify
                      </Button>
                      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
                        <TextButton
                          onClick={() => {
                            setRecoveryMode((v) => !v)
                            setCode("")
                          }}
                        >
                          {recoveryMode
                            ? "Use your authenticator app instead"
                            : "Use a recovery code instead"}
                        </TextButton>
                        <TextButton onClick={() => void startOver()}>Start over</TextButton>
                      </div>
                    </form>
                  )}

                  {step === "enroll" &&
                    (!enrollment ? (
                      <div className="space-y-4">
                        <Notice title="One secret, kept encrypted" icon={ShieldCheck}>
                          The seed is generated on the server and sealed with the dashboard&apos;s
                          master key. It is shown to you exactly once, here.
                        </Notice>
                        <Button
                          size="lg"
                          className="w-full"
                          onClick={beginEnrollment}
                          pending={busy}
                        >
                          Generate a secret
                        </Button>
                      </div>
                    ) : (
                      <form onSubmit={submitEnrollment} className="space-y-4">
                        <TotpSecret secret={enrollment.secret} otpauthUrl={enrollment.otpauthUrl} />
                        <div className="space-y-1.5">
                          <Label htmlFor="enroll-code">Code from your app</Label>
                          <Input
                            id="enroll-code"
                            autoFocus
                            inputMode="numeric"
                            maxLength={6}
                            placeholder="000000"
                            className="h-14 text-center font-mono text-2xl tracking-[0.45em] sm:h-14 sm:text-2xl"
                            value={code}
                            onChange={(e) => setCode(e.target.value)}
                            required
                          />
                        </div>
                        <Button
                          type="submit"
                          size="lg"
                          className="w-full"
                          disabled={busy || code.length < 6}
                          pending={busy}
                        >
                          Enable two-factor
                        </Button>
                      </form>
                    ))}
                </>
              )}
            </div>

            <p className="flex items-start gap-2 text-hint leading-relaxed text-pretty text-muted-foreground">
              <ShieldCheck aria-hidden className="mt-px size-3.5 shrink-0 text-brand" />
              Only addresses on this server&apos;s allowlist reach this page, and every sign-in
              attempt is recorded.
            </p>
          </div>

          <aside
            aria-hidden
            className="auth-art relative hidden min-w-0 flex-col justify-between gap-8 overflow-hidden border-l border-hairline p-9 lg:flex"
          >
            <ProductOrbit className="mx-auto max-w-[22rem]" />
            <div className="space-y-2">
              <p className="eyebrow">Just Dashboard</p>
              <p className="max-w-[22rem] text-2xl leading-tight font-semibold tracking-tight text-balance">
                Apps, databases, containers and the network, on one screen.
              </p>
            </div>
          </aside>
        </FlowPanel>
      </main>
    </div>
  )
}

/**
 * The steps this account has, in the order it meets them. Before the server
 * has answered a sign-in there is only the first, and no spine is drawn: a
 * rail promising a code to an account without an authenticator was a promise
 * the next screen broke.
 */
function spineFor(step: Step, status: AuthStatus | null, enrolled: boolean) {
  const steps: { key: Step; label: string }[] = [{ key: "credentials", label: "Sign in" }]
  if (step === "enroll" || status?.needsEnrollment || enrolled)
    steps.push({ key: "enroll", label: "Authenticator" })
  // An enrolled account is always asked for its code, so the step stays drawn
  // — ticked — once it is behind the reader.
  if (step === "totp" || status?.needsTotp || (status?.user?.totpEnabled && !enrolled))
    steps.push({ key: "totp", label: "Code" })
  if (step === "password" || status?.user?.mustChangePassword)
    steps.push({ key: "password", label: "New password" })
  return steps
}

function hintFor(step: Step, recoveryMode: boolean, savingCodes: boolean) {
  if (savingCodes)
    return "Each one works once, in place of your authenticator. This is the only time they are shown."
  if (step === "totp")
    return recoveryMode
      ? "Enter one of the recovery codes you saved when you set up two-factor."
      : "Enter the six-digit code from your authenticator app."
  if (step === "enroll") return "This dashboard asks every account for an authenticator."
  if (step === "password")
    return "This account was given a password to start with. Choose your own to continue."
  return null
}

/**
 * A forgotten password is recovered on the server, not here — there is no
 * mail to send a link to. Saying where, with the command filled in, is the
 * difference between a dead end and a thirty-second fix.
 */
function ForgotPassword({ username }: { username: string }) {
  const { copy, copied } = useCopy()
  const command = `sudo ./scripts/reset-password.sh ${username.trim().toLowerCase() || "USER"}`
  return (
    <div className="space-y-2 rounded-lg border border-hairline bg-surface-sunken p-3">
      <p className="text-hint leading-relaxed text-muted-foreground">
        Reset it from a shell on the server, in the dashboard&apos;s folder. The password you set
        there is the one you sign in with.
      </p>
      <div className="flex min-w-0 items-center gap-2">
        <code className="min-w-0 flex-1 truncate font-mono text-xs">{command}</code>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label={copied ? "Copied" : "Copy command"}
          onClick={() => void copy(command)}
        >
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
        </Button>
      </div>
    </div>
  )
}

/**
 * The change an account owes. The current password is asked for only when
 * this page does not already hold it: an account that has just typed it on
 * the first step is not asked for it again thirty seconds later.
 */
function ChangePassword({
  knowsCurrent,
  current,
  onCurrent,
  next,
  onNext,
  repeat,
  onRepeat,
  busy,
  onSubmit,
}: {
  knowsCurrent: boolean
  current: string
  onCurrent: (value: string) => void
  next: string
  onNext: (value: string) => void
  repeat: string
  onRepeat: (value: string) => void
  busy: boolean
  onSubmit: (e: React.FormEvent) => void
}) {
  // Decided once, when the step opens; typing into the field must not hide it.
  const [askCurrent] = useState(!knowsCurrent)
  const checks = passwordChecks(next)
  const mismatch = repeat !== "" && next !== repeat
  return (
    <form onSubmit={onSubmit} className="space-y-4">
      {askCurrent && (
        <div className="space-y-1.5">
          <Label htmlFor="current-password">Current password</Label>
          <Input
            id="current-password"
            type="password"
            autoComplete="current-password"
            value={current}
            onChange={(e) => onCurrent(e.target.value)}
            required
          />
        </div>
      )}
      <div className="space-y-1.5">
        <Label htmlFor="new-password">New password</Label>
        <Input
          id="new-password"
          type="password"
          autoFocus
          autoComplete="new-password"
          value={next}
          onChange={(e) => onNext(e.target.value)}
          required
        />
        <p className="flex flex-wrap gap-x-4 gap-y-1" aria-live="polite">
          <FieldCheck met={checks.long}>12 characters or more</FieldCheck>
          <FieldCheck met={checks.mixed}>three of upper, lower, digits, symbols</FieldCheck>
        </p>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="repeat-password">Repeat new password</Label>
        <Input
          id="repeat-password"
          type="password"
          autoComplete="new-password"
          aria-invalid={mismatch || undefined}
          value={repeat}
          onChange={(e) => onRepeat(e.target.value)}
          required
        />
        {mismatch && <p className="text-hint text-destructive">These do not match yet.</p>}
      </div>
      <Button
        type="submit"
        size="lg"
        className="w-full"
        disabled={busy || !checks.ok || next !== repeat || !current}
        pending={busy}
      >
        Change password
        {!busy && <ArrowRight className="size-4" />}
      </Button>
    </form>
  )
}

function TextButton({ onClick, children }: { onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="rounded-sm text-hint text-muted-foreground underline-offset-4 focus-ring transition-colors hover:text-foreground hover:underline"
    >
      {children}
    </button>
  )
}

function RecoveryCodes({
  codes,
  onDone,
  busy,
}: {
  codes: string[]
  onDone: () => void
  busy: boolean
}) {
  const { copy, copied } = useCopy()

  return (
    <div className="space-y-4">
      <Well plain className="grid grid-cols-2 gap-x-4 gap-y-1.5 font-mono text-xs">
        {codes.map((c) => (
          <span key={c} className="tracking-wider">
            {c}
          </span>
        ))}
      </Well>

      <div className="flex flex-col gap-2 sm:flex-row">
        <Button
          type="button"
          variant="outline"
          size="lg"
          className="sm:flex-1"
          onClick={() => void copy(codes.join("\n"))}
        >
          {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
          {copied ? "Copied" : "Copy all"}
        </Button>
        <Button size="lg" className="sm:flex-1" onClick={onDone} pending={busy}>
          <Key className="size-4" />I have saved them
        </Button>
      </div>
    </div>
  )
}
