"use client"

import { useEffect, useState } from "react"
import { useRouter } from "next/navigation"
import { ArrowRight, Check, Copy, Eye, EyeOff, Key, ShieldCheck } from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError, post } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Notice } from "@/components/state"
import { Logo } from "@/components/logo"
import { useCopy } from "@/hooks/use-copy"
import { Well } from "@/components/panel"
import { TotpSecret } from "@/components/account/totp-secret"

type Step = "credentials" | "totp" | "enroll" | "password"

/**
 * Signing in.
 *
 * One column, centred, and that is the whole layout. It used to be a split
 * screen with the product's three security claims down the left, which read
 * well in a mock-up and poorly in use: the thing a person came here to do was
 * pinned to one side of a very wide page, and on the laptop screens these
 * dashboards are actually opened on, the panel took half the width to say
 * something nobody reads twice. The claims are true and they are still made —
 * on the marketing page, in the README, and in the one line under the card
 * that matters at the moment of signing in.
 *
 * Everything here is the shared design system: the card is the same surface as
 * every panel in the app, the inputs are the same inputs, and the backdrop is
 * the same two washes used elsewhere. Sign-in should look like the beginning
 * of the product rather than a page from a different one.
 */
export default function LoginPage() {
  const router = useRouter()
  const { status, loading, login, verifyTotp, refresh } = useAuth()
  // "auto" defers to whatever the session says; the explicit values are set
  // only once the user has moved the flow forward themselves.
  const [chosenStep, setChosenStep] = useState<Step | "auto">("auto")
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [newPassword, setNewPassword] = useState("")
  const [repeatPassword, setRepeatPassword] = useState("")
  const [showPassword, setShowPassword] = useState(false)
  const [code, setCode] = useState("")
  const [recoveryMode, setRecoveryMode] = useState(false)
  const [busy, setBusy] = useState(false)
  const [enrollment, setEnrollment] = useState<{ secret: string; otpauthUrl: string } | null>(null)
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null)

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

  useEffect(() => {
    if (!loading && status?.authenticated) router.replace("/")
  }, [loading, status, router])

  const submitCredentials = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try {
      const next = await login(username, password)
      if (next.authenticated) router.replace("/")
      else if (next.needsTotp) setStep("totp")
      else if (next.needsEnrollment) setStep("enroll")
      else if (next.needsPasswordChange) setStep("password")
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
    setBusy(true)
    try {
      await post("/account/password", { currentPassword: password, newPassword })
      setPassword("")
      setNewPassword("")
      setRepeatPassword("")
      setCode("")
      setStep("credentials")
      await refresh()
      notify.success("Password changed", { description: "Sign in with your new password." })
    } catch (err) {
      notify.error("Could not change password", err instanceof ApiError ? err.message : String(err))
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

  return (
    <div className="relative flex min-h-svh flex-col items-center justify-center bg-background px-5 py-10">
      <div className="auth-backdrop pointer-events-none absolute inset-0" />
      <div className="auth-grid pointer-events-none absolute inset-0" />

      <main className="relative z-10 flex w-full max-w-[25rem] flex-col items-center">
        <Logo size="md" className="mb-7" />

        {/* Two steps on an ordinary install, three where an authenticator is
            compulsory. The rail is hidden entirely when there is only one step
            left to show, because a progress indicator with a single segment is
            decoration pretending to be information. */}
        <Steps current={recoveryCodes ? "totp" : step} enrolling={step === "enroll"} />

        <div className="mt-5 w-full rounded-xl border bg-card p-6 sm:p-7">
          {recoveryCodes ? (
            <RecoveryCodes codes={recoveryCodes} onDone={finishEnrollment} />
          ) : (
            <>
              <header className="mb-6 space-y-1.5 text-center">
                <h1 className="text-lg leading-tight font-semibold">
                  {step === "credentials" && "Sign in"}
                  {step === "totp" && "Two-factor code"}
                  {step === "enroll" && "Set up two-factor"}
                  {step === "password" && "Change your password"}
                </h1>
                <p className="text-body leading-relaxed text-balance text-muted-foreground">
                  {step === "credentials" && "Administrator access to this server."}
                  {step === "totp" && "Enter the six-digit code from your authenticator app."}
                  {step === "enroll" &&
                    "This dashboard requires an authenticator. Enrol one to continue."}
                  {step === "password" && "Set a new password before accessing this server."}
                </p>
              </header>

              {step === "credentials" && (
                <form onSubmit={submitCredentials} className="space-y-4">
                  <div className="space-y-1.5">
                    <Label htmlFor="username">Username</Label>
                    <Input
                      id="username"
                      autoFocus
                      autoComplete="username"
                      value={username}
                      onChange={(e) => setUsername(e.target.value)}
                      required
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="password">Password</Label>
                    <div className="relative">
                      <Input
                        id="password"
                        type={showPassword ? "text" : "password"}
                        autoComplete="current-password"
                        className="pr-9"
                        value={password}
                        onChange={(e) => setPassword(e.target.value)}
                        required
                      />
                      <button
                        type="button"
                        onClick={() => setShowPassword((v) => !v)}
                        aria-label={showPassword ? "Hide password" : "Show password"}
                        className="absolute top-1/2 right-1 flex size-7 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground transition-colors hover:text-foreground"
                      >
                        {showPassword ? (
                          <EyeOff className="size-3.5" />
                        ) : (
                          <Eye className="size-3.5" />
                        )}
                      </button>
                    </div>
                  </div>
                  <Button type="submit" className="w-full" pending={busy}>
                    Continue
                    {!busy && <ArrowRight className="size-4" />}
                  </Button>
                </form>
              )}

              {step === "password" && (
                <form onSubmit={submitPassword} className="space-y-4">
                  <div className="space-y-1.5">
                    <Label htmlFor="current-password">Current password</Label>
                    <Input
                      id="current-password"
                      type="password"
                      autoComplete="current-password"
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      required
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="new-password">New password</Label>
                    <Input
                      id="new-password"
                      type="password"
                      autoComplete="new-password"
                      value={newPassword}
                      onChange={(e) => setNewPassword(e.target.value)}
                      required
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="repeat-password">Repeat new password</Label>
                    <Input
                      id="repeat-password"
                      type="password"
                      autoComplete="new-password"
                      value={repeatPassword}
                      onChange={(e) => setRepeatPassword(e.target.value)}
                      required
                    />
                  </div>
                  <Button type="submit" className="w-full" pending={busy}>
                    Change password
                  </Button>
                </form>
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
                        "h-12 text-center font-mono",
                        recoveryMode ? "text-base" : "text-xl tracking-[0.45em]",
                      )}
                      value={code}
                      onChange={(e) => setCode(e.target.value)}
                      required
                    />
                  </div>
                  <Button type="submit" className="w-full" disabled={busy || !code} pending={busy}>
                    Verify
                  </Button>
                  <button
                    type="button"
                    className="block w-full text-center text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
                    onClick={() => {
                      setRecoveryMode((v) => !v)
                      setCode("")
                    }}
                  >
                    {recoveryMode
                      ? "Use your authenticator app instead"
                      : "Use a recovery code instead"}
                  </button>
                </form>
              )}

              {step === "enroll" &&
                (!enrollment ? (
                  <div className="space-y-4">
                    <Notice title="One secret, kept encrypted" icon={ShieldCheck}>
                      The seed is generated on the server and sealed with the dashboard&apos;s
                      master key. It is shown to you exactly once, here.
                    </Notice>
                    <Button className="w-full" onClick={beginEnrollment} pending={busy}>
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
                        className="h-12 text-center font-mono text-xl tracking-[0.45em]"
                        value={code}
                        onChange={(e) => setCode(e.target.value)}
                        required
                      />
                    </div>
                    <Button
                      type="submit"
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

        <p className="mt-6 max-w-[22rem] text-center text-hint leading-relaxed text-balance text-muted-foreground">
          This host only accepts connections from its configured allowlist, and every sign-in
          attempt — successful or not — is recorded.
        </p>
      </main>
    </div>
  )
}

/**
 * Where you are, when there is more than one place to be.
 *
 * Two dashes for the ordinary flow, three while enrolling. It is a rail rather
 * than numbered circles because the steps are seconds apart and what is worth
 * saying is "one more after this", not "step 2 of 3".
 */
function Steps({ current, enrolling }: { current: Step; enrolling: boolean }) {
  if (current === "password") return null
  const steps: { id: Step; label: string }[] = enrolling
    ? [
        { id: "credentials", label: "Identify" },
        { id: "enroll", label: "Enrol" },
        { id: "totp", label: "Verify" },
      ]
    : [
        { id: "credentials", label: "Identify" },
        { id: "totp", label: "Verify" },
      ]
  const index = steps.findIndex((s) => s.id === current)

  return (
    <ol className="flex w-full items-center gap-2">
      {steps.map((step, i) => {
        const done = i < index
        const active = i === index
        return (
          <li key={step.id} className="flex min-w-0 flex-1 flex-col gap-1.5">
            <span
              className={cn(
                "h-0.5 w-full rounded-full transition-colors",
                done || active ? "bg-primary" : "bg-border",
              )}
            />
            <span
              className={cn(
                "flex items-center gap-1 text-micro font-semibold tracking-[0.14em] uppercase transition-colors",
                active ? "text-foreground" : "text-muted-foreground",
              )}
            >
              {done && <Check className="size-2.5 text-primary" />}
              {step.label}
            </span>
          </li>
        )
      })}
    </ol>
  )
}

function RecoveryCodes({ codes, onDone }: { codes: string[]; onDone: () => void }) {
  const { copy, copied } = useCopy()

  return (
    <div className="space-y-4">
      <header className="space-y-1.5 text-center">
        <h1 className="text-lg leading-tight font-semibold">Save your recovery codes</h1>
        <p className="text-body leading-relaxed text-balance text-muted-foreground">
          Each one works once, in place of your authenticator. This is the only time they are shown.
        </p>
      </header>

      <Well plain className="grid grid-cols-2 gap-x-4 gap-y-1.5">
        {codes.map((c) => (
          <span key={c} className="tracking-wider">
            {c}
          </span>
        ))}
      </Well>

      <div className="flex gap-2">
        <Button
          type="button"
          variant="outline"
          className="flex-1"
          onClick={() => void copy(codes.join("\n"))}
        >
          {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
          {copied ? "Copied" : "Copy all"}
        </Button>
        <Button className="flex-1" onClick={onDone}>
          <Key className="size-4" />I have saved them
        </Button>
      </div>
    </div>
  )
}
