"use client"

import { useEffect, useState } from "react"
import { usePathname } from "next/navigation"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { get, post } from "@/lib/api"
import { setNetworkPendingApply } from "@/lib/network-pending"
import type { NetworkConfirmationView } from "@/lib/types"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import { useConfirm } from "@/components/confirm-dialog"

/** The pending change survives navigation, polling failures and backend restarts in the host journal. */
export function NetworkChangeConfirmation() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const inNetwork = usePathname().startsWith("/network")
  const [observed, setObserved] = useState<string>()
  const state = usePoll(
    async (signal) => {
      const view = await get<NetworkConfirmationView>("/network/changes/current", undefined, signal)
      if (view.change?.phase === "awaiting_confirmation") setObserved(view.change.id)
      return view
    },
    2000,
    [],
    { enabled: admin },
  )
  const refresh = state.refresh
  const [enabled, setEnabled] = useState(true)
  const [dismissed, setDismissed] = useState<string>()
  const [verification, setVerification] = useState<{ id: string; challenge: string }>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [now, setNow] = useState(() => Date.now())
  const { confirm, dialog } = useConfirm()
  const available = state.data?.available === true
  const change = state.data?.change
  const pending = change?.phase === "awaiting_confirmation"
  const cleanupPending = change?.cleanup === "pending" || change?.cleanup === "failed"
  const owned = state.data?.owned === true
  const deadline = change?.expiresAt ? Date.parse(change.expiresAt) : 0
  const seconds = Math.max(0, Math.ceil((deadline - now) / 1000))

  useEffect(() => {
    setNetworkPendingApply(admin && available && enabled)
    return () => setNetworkPendingApply(false)
  }, [admin, available, enabled])

  useEffect(() => {
    const changed = (event: Event) => {
      setObserved((event as CustomEvent<string>).detail)
      refresh()
    }
    window.addEventListener("jd:network-change", changed)
    return () => window.removeEventListener("jd:network-change", changed)
  }, [refresh])

  useEffect(() => {
    if (!pending) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [pending])

  const verify = async () => {
    if (!change) return
    setBusy(true)
    setError(undefined)
    try {
      const result = await post<{ challenge: string }>(`/network/changes/${change.id}/verify`)
      setVerification({ id: change.id, challenge: result.challenge })
      refresh()
    } catch (failure) {
      setVerification(undefined)
      setError(failure instanceof Error ? failure.message : String(failure))
      refresh()
    } finally {
      setBusy(false)
    }
  }

  const save = async () => {
    if (!change || verification?.id !== change.id) return
    setBusy(true)
    setError(undefined)
    try {
      await post(`/network/changes/${change.id}/confirm`, { challenge: verification.challenge })
      setVerification(undefined)
      refresh()
    } catch (failure) {
      setVerification(undefined)
      setError(failure instanceof Error ? failure.message : String(failure))
      refresh()
    } finally {
      setBusy(false)
    }
  }

  const recover = () => {
    if (!change) return
    void confirm({
      title: "Restore previous network settings",
      description:
        "Undo this pending change now. The host will restore the network and boot files captured before the apply.",
      confirmLabel: "Restore settings",
      action: async () => {
        await post(`/network/changes/${change.id}/recover`)
        refresh()
      },
    })
  }

  const cleanup = async () => {
    if (!change || !owned) return
    setBusy(true)
    setError(undefined)
    try {
      await post(`/network/changes/${change.id}/cleanup`)
      refresh()
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure))
      refresh()
    } finally {
      setBusy(false)
    }
  }

  if (!admin) return null
  const outcome =
    change &&
    ["recovered", "degraded", "confirmed"].includes(change.phase) &&
    dismissed !== change.id &&
    (inNetwork || observed === change.id || change.phase === "degraded" || cleanupPending)
  if (!pending && !outcome && !(observed && state.error) && !(inNetwork && available)) return null
  return (
    <aside aria-label="Network change confirmation" className="mx-4 my-3 min-w-0">
      {pending ? (
        <Notice tone="warning" title="Network change is awaiting confirmation">
          <p>
            {seconds > 0
              ? `The host will restore the previous settings unless confirmed within ${seconds} seconds.`
              : "The recovery deadline has passed. Waiting for the host's recovery status."}
            {deadline > 0 && ` Deadline: ${new Date(deadline).toLocaleTimeString()}.`}
          </p>
          <p className="mt-1">
            {owned
              ? "Verify a new dashboard response, then explicitly confirm to keep these settings."
              : "The administrator who applied this change must reconnect and confirm it."}
          </p>
          {state.error && (
            <p className="mt-1">
              Reconnection failed. The last known status is shown; confirmation is unavailable.
            </p>
          )}
          {error && (
            <p role="alert" className="mt-1">
              {error}
            </p>
          )}
          {owned && (
            <div className="mt-3 flex flex-wrap gap-2">
              <Button
                size="sm"
                variant="outline"
                disabled={busy || seconds === 0}
                onClick={() => void verify()}
              >
                Verify reconnection
              </Button>
              <Button
                size="sm"
                disabled={
                  busy || seconds === 0 || verification?.id !== change.id || Boolean(state.error)
                }
                onClick={() => void save()}
              >
                Confirm network change
              </Button>
              {can("destructive") && (
                <Button size="sm" variant="outline" disabled={busy} onClick={recover}>
                  Restore settings
                </Button>
              )}
            </div>
          )}
        </Notice>
      ) : outcome && change ? (
        <Notice
          tone={change.phase === "degraded" || cleanupPending ? "warning" : "default"}
          title={
            change.phase === "confirmed"
              ? "Network change confirmed"
              : change.phase === "recovered"
                ? "Previous network settings restored"
                : "Network recovery needs attention"
          }
        >
          <p>
            {change.phase === "confirmed"
              ? "The applying session received and confirmed a fresh dashboard response. The settings are retained."
              : change.phase === "recovered"
                ? "The host restored the captured network settings."
                : "Recovery could not restore every step. Further changes remain blocked."}
          </p>
          {change.recoveryErrors?.map((failure) => (
            <p key={failure} className="mt-1">
              {failure}
            </p>
          ))}
          {cleanupPending && (
            <p className="mt-1">
              The network decision is saved, but native cleanup remains incomplete. Further
              journaled changes are blocked until the owned checkpoint and recovery stages are
              cleaned.
            </p>
          )}
          {cleanupPending && owned && can("destructive") && (
            <Button
              size="sm"
              variant="outline"
              className="mt-2"
              pending={busy}
              disabled={busy || Boolean(state.error)}
              onClick={() => void cleanup()}
            >
              Retry native cleanup
            </Button>
          )}
          {error && (
            <p role="alert" className="mt-1">
              {error}
            </p>
          )}
          {state.error && (
            <p className="mt-1">The latest status request failed; this is the last known result.</p>
          )}
          {change.phase !== "degraded" && !cleanupPending && (
            <Button
              size="sm"
              variant="outline"
              className="mt-2"
              onClick={() => setDismissed(change.id)}
            >
              Dismiss network outcome
            </Button>
          )}
        </Notice>
      ) : observed && state.error ? (
        <Notice tone="warning" title="Waiting for network recovery status">
          <p>
            The dashboard cannot currently read the pending change. The independent host watchdog
            remains responsible for recovery.
          </p>
        </Notice>
      ) : null}
      {inNetwork && available && !pending && (
        <label className="mt-2 flex min-h-11 items-center gap-3 text-body">
          <Switch
            aria-label="Require network reconnection confirmation"
            checked={enabled}
            onCheckedChange={setEnabled}
          />
          Confirm managed network changes after reconnecting (90 seconds)
        </label>
      )}
      {dialog}
    </aside>
  )
}
