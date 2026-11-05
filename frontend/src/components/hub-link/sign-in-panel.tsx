"use client"

import { useState } from "react"
import { errorMessage } from "@/lib/api"
import { calendarDate } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { HubLinkStatus } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { ConfirmDialog, type ConfirmRequest } from "@/components/confirm-dialog"
import { Field, FormNote } from "@/components/form"
import { Section } from "@/components/page"
import { Row, RowList } from "@/components/row-list"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

type Grant = NonNullable<NonNullable<HubLinkStatus["admin"]>["grants"]>[number]

/**
 * "Let Just Hub sign me in" (hub-link-protocol §9). Each account decides for itself, with its
 * password and, where it has one, its authenticator code; the operator's JD_HUB_SIGNIN and the
 * hub's own setting must both be on as well. The screen says plainly what turning it on costs:
 * whoever takes over the hub can then open a session as this account, the Files page (and an
 * administrator's terminal) is not contained, so such a session reads every file and, where it may
 * change files, is as good as root (§9.1); the dashboard's own forms refuse it credentials, links
 * and the dashboard's own settings, and the audit log marks what it does (§9.10); and ending
 * sign-in ends those sessions without undoing what was done in them (§9.12).
 *
 * Everything here is this account's own, except the list of every account's choice, which only
 * an administrator is sent (`admin.grants`) and may end.
 *
 * `hubName` is the hub's name as the page attributes it, "Just Hub (hub)" (§8.10). `idle` is the
 * page's reading of an idle link for every reader (`isIdle`, the status's `idle`): the server
 * revoked every hub session when the link went idle and refuses new ones and new choices until a
 * disconnect, so no account is offered the form; a choice already made can still be ended.
 *
 * The page's brand face stays Connect's alone: opting in is a deliberate choice with a cost, so its
 * command is an outline button, as stopping is.
 */
export function SignInPanel({
  status,
  hubName,
  idle = false,
  onAllow,
  onRevoke,
  onRemove,
  className,
}: {
  status: HubLinkStatus
  hubName: string
  idle?: boolean
  onAllow: (hubUser: string, password: string, code: string) => Promise<void>
  onRevoke: () => Promise<void>
  onRemove: (userId: number) => Promise<void>
  className?: string
}) {
  const { status: auth } = useAuth()
  const signIn = status.signIn
  const grants = status.admin?.grants
  const me = auth?.user?.username ?? "this account"

  return (
    <Section title="Sign in from Just Hub" className={className}>
      {!signIn?.enabled ? (
        <p className="max-w-prose text-body text-muted-foreground">
          Off on this server. With <code className="font-mono">JD_HUB_SIGNIN=true</code> in its{" "}
          <code className="font-mono">.env</code>, each account here may choose to let a Just Hub
          user open it already signed in. Until then the hub reaches this server only through the
          commands its scope allows.
        </p>
      ) : idle ? (
        <div data-slot="hub-signin-idle" className="flex min-w-0 flex-col gap-6">
          <p className="max-w-prose text-body text-muted-foreground">
            Paused while the link is idle. Every session {hubName} opened on this server ended when
            the link went idle, and none opens until it is disconnected and connected again, which
            also ends every account&apos;s choice to let the hub sign it in.
          </p>
          {signIn.mine && (
            <Mine grant={signIn.mine} hubName={hubName} me={me} onRevoke={onRevoke} />
          )}
        </div>
      ) : !signIn.offered ? (
        <p className="max-w-prose text-body text-muted-foreground">
          This server allows it, but {hubName} does not offer it. Its owner turns sign-in on in the
          hub&apos;s settings.
        </p>
      ) : signIn.mine ? (
        <Mine grant={signIn.mine} hubName={hubName} me={me} onRevoke={onRevoke} />
      ) : (
        <Allow
          users={signIn.users}
          hubName={hubName}
          me={me}
          totp={Boolean(auth?.user?.totpEnabled)}
          onAllow={onAllow}
        />
      )}
      {grants && grants.length > 0 && <Grants grants={grants} onRemove={onRemove} />}
    </Section>
  )
}

/** What turning it on means, said before the form and kept beside the grant once it exists. */
function Costs({ hubName, me }: { hubName: string; me: string }) {
  return (
    <div className="flex max-w-prose flex-col gap-2 text-body text-muted-foreground">
      <p>
        Opening this server from {hubName} then lands you here signed in as {me}, without this
        server asking for its own code. The hub asks for your authenticator code in its own session
        first, and the link it opens works once, for about a minute, on the device that asked.
      </p>
      <p>
        <span className="text-foreground">What it costs:</span> anyone who takes over the hub could
        open a session as {me} from a device on your tailnet, with everything {me} can do here. That
        includes the Files page, which reads every file it reaches (the whole server unless the
        operator narrowed it), passwords and keys included; with permission to change files it
        writes them too, and an administrator&apos;s session has the terminal. Either of those is as
        good as root.
      </p>
      <p>
        A session opened this way lasts at most two hours. This dashboard&apos;s own forms refuse it
        API keys, accounts, passwords, authenticators, passkeys, Linux users, database users, VPN
        peers, saved credentials, revealed secrets, links and this dashboard&apos;s own settings:
        for those, sign in on this server itself. The audit log marks what it does as done through
        Just Hub.
      </p>
      <p>
        This server tells the hub your username and the hub user you choose, and nothing else about
        your account. Turning it off ends every session the hub opened for you at once; it does not
        undo what was done in them.
      </p>
    </div>
  )
}

function Allow({
  users,
  hubName,
  me,
  totp,
  onAllow,
}: {
  users: string[]
  hubName: string
  me: string
  totp: boolean
  onAllow: (hubUser: string, password: string, code: string) => Promise<void>
}) {
  const [hubUser, setHubUser] = useState(users.length === 1 ? users[0] : "")
  const [password, setPassword] = useState("")
  const [code, setCode] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setError(undefined)
    try {
      await onAllow(hubUser, password, code)
      notify.success(`${hubName} can sign you in`, {
        description: `As ${me}, when ${hubUser} opens this server from the hub.`,
      })
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="flex min-w-0 flex-col gap-6">
      <Costs hubName={hubName} me={me} />
      {users.length === 0 ? (
        <p className="text-body text-muted-foreground">{hubName} offers no hub users yet.</p>
      ) : (
        <div className="flex max-w-md min-w-0 flex-col gap-4">
          <Field
            label="Hub user"
            htmlFor="hub-signin-user"
            hint="Only this hub user's sign-in links open this account."
          >
            {users.length === 1 ? (
              // An <output> is labelable, so "Hub user" names it as it names the select.
              <output id="hub-signin-user" className="block font-mono text-body">
                {users[0]}
              </output>
            ) : (
              <Select value={hubUser} onValueChange={setHubUser}>
                <SelectTrigger id="hub-signin-user" className="w-full">
                  <SelectValue placeholder="Choose a hub user" />
                </SelectTrigger>
                <SelectContent>
                  {users.map((user) => (
                    <SelectItem key={user} value={user}>
                      {user}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </Field>
          <Field label="Your password on this server" htmlFor="hub-signin-password">
            <Input
              id="hub-signin-password"
              type="password"
              autoComplete="current-password"
              value={password}
              disabled={busy}
              onChange={(event) => setPassword(event.target.value)}
            />
          </Field>
          {totp && (
            <Field label="Code from your authenticator" htmlFor="hub-signin-code">
              <Input
                id="hub-signin-code"
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={6}
                placeholder="000000"
                className="max-w-48 text-center font-mono tracking-[0.4em]"
                value={code}
                disabled={busy}
                onChange={(event) => setCode(event.target.value)}
              />
            </Field>
          )}
          <div>
            <Button
              type="submit"
              variant="outline"
              pending={busy}
              disabled={!hubUser || !password || (totp && code.length < 6)}
            >
              Let Just Hub sign me in
            </Button>
          </div>
          {error && (
            <FormNote tone="danger" role="alert">
              {error}
            </FormNote>
          )}
        </div>
      )}
    </form>
  )
}

function Mine({
  grant,
  hubName,
  me,
  onRevoke,
}: {
  grant: { hubUser: string; since: string }
  hubName: string
  me: string
  onRevoke: () => Promise<void>
}) {
  const [busy, setBusy] = useState(false)
  const stop = async () => {
    setBusy(true)
    try {
      await onRevoke()
      notify.success("Just Hub can no longer sign you in", {
        description: "Every session it opened for you has ended.",
      })
    } catch (err) {
      notify.error("Could not turn it off", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <p className="text-body">
        <span className="font-mono">{grant.hubUser}</span> on {hubName} can sign you in as {me},
        since {calendarDate(grant.since)}.
      </p>
      <Costs hubName={hubName} me={me} />
      <div>
        <Button variant="outline" pending={busy} onClick={stop}>
          Stop letting Just Hub sign me in
        </Button>
      </div>
    </div>
  )
}

/** Every account's choice, for an administrator, who may end any of them. */
function Grants({
  grants,
  onRemove,
}: {
  grants: Grant[]
  onRemove: (userId: number) => Promise<void>
}) {
  const [confirm, setConfirm] = useState<ConfirmRequest | null>(null)
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <p className="eyebrow">Accounts that let Just Hub sign them in</p>
      <RowList>
        {grants.map((grant) => (
          <Row
            key={grant.userId}
            className="px-0 py-2.5"
            title={grant.username}
            subtitle={`by hub user ${grant.hubUser} · since ${calendarDate(grant.since)}`}
            trailing={
              <Button
                size="xs"
                variant="outline"
                onClick={() =>
                  setConfirm({
                    title: `Stop Just Hub signing in as ${grant.username}`,
                    description:
                      "Every session the hub opened for that account ends now. The account can turn it on again for itself.",
                    confirmLabel: "Remove",
                    action: () => onRemove(grant.userId),
                  })
                }
              >
                Remove
              </Button>
            }
          />
        ))}
      </RowList>
      <ConfirmDialog request={confirm} onOpenChange={(open) => !open && setConfirm(null)} />
    </div>
  )
}
