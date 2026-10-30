"use client"

import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { createContext, useContext, useState } from "react"
import {
  ArrowLeft,
  FolderOpen,
  LockClosed,
  LockOpen,
  Logout,
  Terminal,
  Trash,
} from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { Address } from "@/components/security/marks"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { signInSummary } from "@/components/system-users/access"
import { ActivityPanel, HistoryPanel } from "@/components/system-users/account-activity"
import { RemoveAccountDialog } from "@/components/system-users/account-removal"
import { RootPanel } from "@/components/system-users/account-root"
import { AccountSettings } from "@/components/system-users/account-settings"
import { SignInPanel } from "@/components/system-users/account-sign-in"
import { AccountMark, shellProduct } from "@/components/system-users/marks"
import { Button } from "@/components/ui/button"
import { VerbBar, type Verb } from "@/components/verbs"
import { usePoll } from "@/hooks/use-poll"
import { del, get, patch } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { SystemUserDetail } from "@/lib/types"

type AccountContextValue = {
  user: SystemUserDetail
  /** The account's path, ready to have a route appended. */
  path: string
  /** Sent with every write: says a system account's editing was turned on. */
  query?: { allowSystem: true }
  readOnly: boolean
  refresh: () => void
}

const AccountContext = createContext<AccountContextValue | null>(null)

export function useAccount() {
  const value = useContext(AccountContext)
  if (!value) throw new Error("useAccount outside an account page")
  return value
}

/**
 * One account, everything that can be done to it, in the order somebody comes
 * here for it: how it gets in, how far it can reach, what it is doing now, what
 * it has done. There is no tab bar: the sections are short, and an operator
 * changing a key is usually about to look at what that key could do.
 *
 * Deleting is a verb and not a section, because it is rare and it opens its own
 * reading of what would go. The fields nobody needs twice a year are folded at
 * the foot, so they are there without being in the way.
 */
export function AccountPage() {
  const params = useParams<{ name: string }>()
  const name = decodeURIComponent(params.name)
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useState(false)
  const [removing, setRemoving] = useState(false)
  const path = `/system-users/${encodeURIComponent(name)}`
  const {
    data: user,
    error,
    loading,
    refresh,
  } = usePoll((signal) => get<SystemUserDetail>(path, undefined, signal), 30000, [path])
  const { data: disk } = usePoll(
    (signal) => get<{ bytes: number }>(`${path}/disk`, undefined, signal),
    0,
    [path],
  )

  const isDaemon = Boolean(user?.system && user.uid !== 0)
  const readOnly = isDaemon && !editing
  const query = editing ? ({ allowSystem: true } as const) : undefined

  const setDisabled = (disabled: boolean) => {
    if (!disabled) {
      void (async () => {
        try {
          await patch(path, { disabled: false }, { query })
          notify.success(`${name} can sign in again`)
          refresh()
        } catch (err) {
          notify.error("Could not enable the account", err)
        }
      })()
      return
    }
    confirm({
      title: `Disable ${name}`,
      confirmLabel: "Disable",
      description: (
        <p>
          Closes every way in, SSH keys included, and ends what it is running now. Nothing is
          deleted, and enabling it brings everything back.
        </p>
      ),
      action: async () => {
        await patch(path, { disabled: true, endSessions: true }, { query })
        refresh()
      },
    })
  }

  const endSessions = () =>
    confirm({
      title: `End everything ${name} is running`,
      confirmLabel: "End sessions",
      description: <p>Signs it out everywhere and stops every process it owns.</p>,
      action: async (typed) => {
        await del(`${path}/sessions`, { confirm: typed, query })
        refresh()
      },
    })

  const verbs: Verb[] = !user
    ? []
    : [
        ...(user.canLogin
          ? [
              {
                key: "terminal",
                label: "Terminal",
                icon: Terminal,
                inline: true,
                run: () => router.push(`/terminal?as=${encodeURIComponent(name)}`),
              },
            ]
          : []),
        {
          key: "files",
          label: "Files",
          icon: FolderOpen,
          inline: true,
          run: () => router.push(`/files?path=${encodeURIComponent(user.home)}`),
        },
        ...(readOnly || user.protected
          ? []
          : [
              {
                key: "disable",
                label: user.disabled ? "Enable" : "Disable",
                icon: user.disabled ? LockOpen : LockClosed,
                inline: true,
                run: () => setDisabled(!user.disabled),
              },
              { key: "end", label: "End sessions", icon: Logout, run: endSessions },
              {
                key: "delete",
                label: "Delete",
                icon: Trash,
                danger: true,
                run: () => setRemoving(true),
              },
            ]),
      ]

  const state = user && signInSummary(user)

  return (
    <Page>
      <PageContext
        eyebrow={
          <Link
            href="/system-users"
            className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
          >
            <ArrowLeft className="size-3" /> System users
          </Link>
        }
        title={name}
        actions={user && <VerbBar verbs={verbs} menuLabel={`More for ${name}`} />}
      />
      {error && !user && <ErrorState error={error} />}
      {loading && !user && <LoadingRows />}
      {user && state && (
        <AccountContext.Provider value={{ user, path, query, readOnly, refresh }}>
          <HostIdentity
            className="animate-rise"
            logo={<AccountMark user={user} size="lg" />}
            title={
              <span className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                <span className="font-mono">{user.username}</span>
                {user.comment && user.comment !== user.username && (
                  <span className="text-body font-normal text-muted-foreground">
                    {user.comment}
                  </span>
                )}
              </span>
            }
            facts={
              <>
                <span className="numeric">uid {user.uid}</span>
                <FactDot />
                <HostFact product={shellProduct(user.shell)}>
                  <span className="font-mono">{user.shell}</span>
                </HostFact>
                <FactDot />
                <span className="font-mono">{user.home}</span>
                {disk && disk.bytes > 0 && <span className="numeric">{bytes(disk.bytes)}</span>}
                <FactDot />
                {user.lastLogin ? (
                  <span className="inline-flex min-w-0 items-center gap-1.5">
                    <span className="numeric shrink-0">{relativeTime(user.lastLogin)}</span>
                    {user.lastLoginFrom && <Address ip={user.lastLoginFrom} className="min-w-0" />}
                  </span>
                ) : (
                  <span>never signed in</span>
                )}
              </>
            }
            aside={<Status tone={state.tone} label={state.label} />}
          />

          {isDaemon && !editing && (
            <Notice title="This account belongs to the system">
              <span className="flex flex-wrap items-center justify-between gap-2">
                A package made it and expects it as it left it, so it is read-only here.
                <Button size="xs" variant="outline" onClick={() => setEditing(true)}>
                  Allow changes
                </Button>
              </span>
            </Notice>
          )}

          <div className="grid min-w-0 gap-x-10 gap-y-8 xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
            <SignInPanel />
            <RootPanel />
          </div>
          <ActivityPanel />
          <HistoryPanel />
          {!readOnly && <AccountSettings />}
          {removing && <RemoveAccountDialog onClose={() => setRemoving(false)} />}
        </AccountContext.Provider>
      )}
      {dialog}
    </Page>
  )
}
