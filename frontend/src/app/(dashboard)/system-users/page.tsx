"use client"

import { useMemo, useState } from "react"
import { Key, LockClosed, LockOpen, Plus, Trash, UserPlus, Users } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, patch, post } from "@/lib/api"
import { keyProduct } from "@/lib/clients"
import { plural, relativeTime } from "@/lib/format"
import type { SSHKey, SystemUser } from "@/lib/types"
import { useSessionState, useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow, GroupRule } from "@/components/flow"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { Field, FieldRow, FormFact, FormFacts, FormNote } from "@/components/form"
import { ProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { Address } from "@/components/security/marks"
import { SidePanel } from "@/components/side-panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel, LoadingRows } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { AccountMark, FaceRun, GroupList, isAdmin } from "@/components/system-users/marks"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { IconAction } from "@/components/icon-action"
import { VerbActions, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { NumberTicker } from "@/components/ui/number-ticker"
import { Textarea } from "@/components/ui/textarea"

type Pending = Record<string, string | undefined>

type Scope = "all" | "signin" | "admins" | "locked"

/**
 * The one word an account's state comes down to. Locked wins over everything —
 * a locked account cannot be signed into however it is otherwise arranged —
 * and a missing password is the only state on this page that is a warning.
 */
function accountState(user: SystemUser): { tone: DotTone; label: string } {
  if (user.locked) return { tone: "stopped", label: "Locked" }
  if (user.noPassword) return { tone: "warning", label: "No password" }
  if (!user.canLogin) return { tone: "unknown", label: "No login" }
  return { tone: "running", label: "Active" }
}

/**
 * The host's own accounts, as the people and daemons they are.
 *
 * Four readings open the page — how many accounts there are with the people
 * among them drawn as their faces, who can run anything as root, who can sign
 * in and whether any of them needs no password to, and who last did — and the
 * accounts are cards under them (§12, §16): each opens its authorised keys,
 * so the list is a run of places to go rather than a table of readings. The
 * locked count that had a tile of its own is a filter chip over the cards,
 * where it also narrows the list to what it counts, beside who can sign in
 * and who administers the host. With system accounts shown they are a second
 * shelf under the people, each drawn as the daemon it runs where its name
 * says (`system-users/marks.tsx`).
 */
export default function SystemUsersPage() {
  const { confirm, dialog } = useConfirm()
  const wide = useMediaQuery("(min-width: 1024px)")
  const [showSystem, setShowSystem] = useViewState("system-users.show-system", false)
  const [query, setQuery] = useSessionState("system-users.query", "")
  const [scope, setScope] = useSessionState<Scope>("system-users.scope", "all")
  const [keysFor, setKeysFor] = useState<string | null>(null)
  const [pending, setPending] = useState<Pending>({})
  const { data, error, loading, refresh } = usePoll(
    (signal) => get<SystemUser[]>("/system-users/", { system: showSystem }, signal),
    30000,
    [showSystem],
  )

  const setLocked = async (user: SystemUser, locked: boolean) => {
    setPending((p) => ({ ...p, [user.username]: locked ? "Locking" : "Unlocking" }))
    try {
      await patch(`/system-users/${encodeURIComponent(user.username)}`, { locked })
      notify.success(`${user.username} ${locked ? "locked" : "unlocked"}`)
      refresh()
    } catch (err) {
      notify.error("Could not change lock state", err)
    } finally {
      setPending((p) => ({ ...p, [user.username]: undefined }))
    }
  }

  const remove = (user: SystemUser) =>
    confirm({
      title: "Delete system user",
      phrase: user.username,
      confirmLabel: "Delete",
      subject: {
        mark: <AccountMark user={user} size="sm" />,
        name: <span className="font-mono">{user.username}</span>,
        facts: (
          <>
            <FormFact label="uid">{user.uid}</FormFact>
            <FormFact label="Home" mono>
              {user.home}
            </FormFact>
          </>
        ),
      },
      description: (
        <p className="text-destructive">
          Removes the account <b>{user.username}</b> from this host. Its home directory is left in
          place.
        </p>
      ),
      action: async (c) => {
        await del(`/system-users/${encodeURIComponent(user.username)}`, { confirm: c })
        refresh()
      },
    })

  // The figures are read over everything the host returned, so the search box
  // and the chips narrow the cards without narrowing the readings above them.
  const figures = useMemo(() => {
    const users = data ?? []
    const latest = users
      .filter((u) => u.lastLogin)
      .sort((a, b) => (b.lastLogin ?? "").localeCompare(a.lastLogin ?? ""))[0]
    const admins = users.filter(isAdmin)
    return {
      people: users.filter((u) => !u.system),
      system: users.filter((u) => u.system).length,
      signIn: users.filter((u) => u.canLogin && !u.locked),
      noPassword: users.filter((u) => u.noPassword && !u.locked).length,
      admins,
      bareAdmins: admins.filter((u) => u.noPassword && !u.locked).length,
      locked: users.filter((u) => u.locked).length,
      keys: users.reduce((sum, u) => sum + u.sshKeyCount, 0),
      latest,
    }
  }, [data])

  const rows = useMemo(() => {
    if (!data) return []
    const q = query.trim().toLowerCase()
    return data.filter((u) => {
      if (scope === "signin" && !(u.canLogin && !u.locked)) return false
      if (scope === "admins" && !isAdmin(u)) return false
      if (scope === "locked" && !u.locked) return false
      if (!q) return true
      return (
        u.username.toLowerCase().includes(q) ||
        u.comment.toLowerCase().includes(q) ||
        u.shell.toLowerCase().includes(q) ||
        u.groups.some((g) => g.toLowerCase().includes(q))
      )
    })
  }, [data, query, scope])

  // People first, then the accounts packages made — the order the host lists
  // them in, as two shelves once both are on the page.
  const shelves = useMemo(
    () =>
      [
        { key: "people", label: "People", users: rows.filter((u) => !u.system) },
        { key: "system", label: "System accounts", users: rows.filter((u) => u.system) },
      ].filter((shelf) => shelf.users.length > 0),
    [rows],
  )

  const cardProps = { pending, setLocked, remove, onKeys: setKeysFor, wide }

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Advanced" title="System users" />

      {data && (
        <StatGrid columns={4} key="figures" className="animate-rise">
          <StatTile
            label="Accounts"
            value={<NumberTicker value={data.length} />}
            hint={
              <span className="inline-flex max-w-full min-w-0 items-center gap-2">
                <FaceRun names={figures.people.map((u) => u.username)} />
                <span className="truncate">
                  {showSystem
                    ? `${figures.people.length} people · ${figures.system} system`
                    : "system accounts hidden"}
                </span>
              </span>
            }
          />
          <StatTile
            label="Administrators"
            value={<NumberTicker value={figures.admins.length} />}
            tone={figures.bareAdmins > 0 ? "warning" : "default"}
            hint={
              <span className="inline-flex max-w-full min-w-0 items-center gap-2">
                <FaceRun names={figures.admins.map((u) => u.username)} />
                <span className="truncate">
                  {figures.bareAdmins > 0
                    ? `${figures.bareAdmins} without a password`
                    : figures.admins.length === 1
                      ? figures.admins[0].username
                      : figures.admins.length > 0
                        ? "can run anything as root"
                        : "nobody holds sudo"}
                </span>
              </span>
            }
          />
          <StatTile
            label="Can sign in"
            value={<NumberTicker value={figures.signIn.length} />}
            tone={figures.noPassword > 0 ? "warning" : "default"}
            hint={
              <span className="inline-flex max-w-full min-w-0 items-center gap-1.5">
                {figures.noPassword === 0 && (
                  <Key aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
                )}
                <span className="truncate">
                  {figures.noPassword > 0
                    ? `${figures.noPassword} without a password`
                    : `${plural(figures.keys, "SSH key")} authorised`}
                </span>
              </span>
            }
          />
          <StatTile
            label="Last sign-in"
            value={figures.latest ? relativeTime(figures.latest.lastLogin) : "Never"}
            hint={
              figures.latest ? (
                <span className="inline-flex max-w-full min-w-0 items-center gap-1.5">
                  <FaceRun names={[figures.latest.username]} />
                  <span className="shrink-0">{figures.latest.username}</span>
                  {figures.latest.lastLoginFrom && (
                    <Address ip={figures.latest.lastLoginFrom} className="min-w-0" />
                  )}
                </span>
              ) : (
                "no sign-in recorded"
              )
            }
          />
        </StatGrid>
      )}

      {/* The accounts are cards you open, so the list around them is plain: a
          frame around framed cards is two nested frames (§12). */}
      <Panel plain>
        <PanelHeader title="Accounts" actions={<CreateUserDialog onDone={refresh} />} />
        <PanelToolbar>
          <SearchInput
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Name, group or shell"
            containerClassName="sm:w-64"
          />
          {data && (
            <ChipStrip>
              <FilterChip selected={scope === "all"} onClick={() => setScope("all")}>
                All <ChipCount>{data.length}</ChipCount>
              </FilterChip>
              <FilterChip selected={scope === "signin"} onClick={() => setScope("signin")}>
                Can sign in <ChipCount>{figures.signIn.length}</ChipCount>
              </FilterChip>
              <FilterChip selected={scope === "admins"} onClick={() => setScope("admins")}>
                Administrators <ChipCount>{figures.admins.length}</ChipCount>
              </FilterChip>
              <FilterChip selected={scope === "locked"} onClick={() => setScope("locked")}>
                Locked <ChipCount>{figures.locked}</ChipCount>
              </FilterChip>
            </ChipStrip>
          )}
          <FilterChip
            className="sm:ml-auto"
            selected={showSystem}
            onClick={() => setShowSystem(!showSystem)}
          >
            System accounts
          </FilterChip>
        </PanelToolbar>
        <PanelBody flush>
          {loading && !data && <LoadingPanel />}
          {error && !data && <ErrorState error={error} />}
          {data && (
            <div key="rows" className="animate-rise space-y-5 pt-3">
              {shelves.map((shelf) => (
                <section key={shelf.key} className="space-y-2.5">
                  {shelves.length > 1 && (
                    <GroupRule label={shelf.label} count={shelf.users.length} />
                  )}
                  <ChoiceList>
                    {shelf.users.map((user, index) => (
                      <AccountCard key={user.username} user={user} index={index} {...cardProps} />
                    ))}
                  </ChoiceList>
                </section>
              ))}
              {rows.length === 0 && (
                <EmptyState
                  icon={Users}
                  title={data.length === 0 ? "No accounts to show" : "No accounts match"}
                  description={
                    data.length === 0
                      ? "Every account on this host is a system account."
                      : "Clear the search, pick All, or include system accounts."
                  }
                />
              )}
            </div>
          )}
        </PanelBody>
        {data && (
          <PanelFooter className="text-hint text-muted-foreground">
            <span className="numeric">
              {rows.length === data.length
                ? plural(data.length, "account")
                : `${rows.length} of ${plural(data.length, "account")}`}
            </span>
            <span className="text-muted-foreground/40">·</span>
            <span>{showSystem ? "system accounts included" : "system accounts hidden"}</span>
          </PanelFooter>
        )}
      </Panel>

      <SSHKeysSheet
        user={data?.find((u) => u.username === keysFor)}
        username={keysFor}
        onOpenChange={(o) => !o && setKeysFor(null)}
        onChanged={refresh}
      />
      {dialog}
    </Page>
  )
}

type CardProps = {
  user: SystemUser
  index: number
  wide: boolean
  pending: Pending
  setLocked: (user: SystemUser, locked: boolean) => void
  remove: (user: SystemUser) => void
  onKeys: (username: string) => void
}

/**
 * What can be done to an account, declared once. The card itself opens the
 * keys, so the lock is the one verb drawn beside it; deleting an account is
 * rare and unrecoverable, so it lives in the menu.
 */
function userVerbs({ user, pending, setLocked, remove }: CardProps): Verb[] {
  return [
    {
      key: "lock",
      label: user.locked ? "Unlock" : "Lock",
      icon: user.locked ? LockOpen : LockClosed,
      inline: true,
      disabled: Boolean(pending[user.username]),
      run: () => setLocked(user, !user.locked),
    },
    {
      key: "delete",
      label: "Delete",
      icon: Trash,
      danger: true,
      run: () => remove(user),
    },
  ]
}

/**
 * One account, as the person or the daemon it is: its mark, its name, and the
 * four readings compared down the list — what it belongs to, when it last
 * signed in and from where, how many keys open it, and its state. From `lg`
 * the readings sit beside the name in fixed measures; below it they go
 * beneath it at the card's full width, chosen once rather than drawn twice.
 * While the lock is changing a light runs round its edge (§11 *live*).
 */
function AccountCard(props: CardProps) {
  const { user, index, wide, pending, onKeys } = props
  const busy = pending[user.username]
  const state = accountState(user)
  const readings = (
    <span className="flex min-w-0 flex-wrap items-center gap-x-5 gap-y-2 text-xs">
      <GroupList groups={user.groups} className="w-52" />
      <span className="inline-flex w-44 min-w-0 items-center gap-1.5 text-muted-foreground">
        {user.lastLogin ? (
          <>
            <span className="shrink-0">{relativeTime(user.lastLogin)}</span>
            {user.lastLoginFrom && <Address ip={user.lastLoginFrom} className="min-w-0" />}
          </>
        ) : (
          <span className="text-muted-foreground/60">never signed in</span>
        )}
      </span>
      <span
        className="inline-flex w-14 items-center gap-1 text-muted-foreground"
        title={plural(user.sshKeyCount, "authorised key")}
      >
        <Key aria-hidden className="size-3.5" />
        <span className={user.sshKeyCount > 0 ? "numeric text-foreground" : "numeric"}>
          {user.sshKeyCount}
        </span>
      </span>
      <span className="flex w-28">
        <Status tone={busy ? "warning" : state.tone} label={busy ? `${busy}…` : state.label} />
      </span>
    </span>
  )

  return (
    <ChoiceRow
      index={index}
      verb={`SSH keys for ${user.username}`}
      onSelect={() => onKeys(user.username)}
      busy={Boolean(busy)}
      className={user.locked ? "opacity-70" : undefined}
      leading={<AccountMark user={user} />}
      title={<span className="font-mono">{user.username}</span>}
      description={
        <>
          {user.comment && <>{user.comment} · </>}
          <span className="numeric">uid {user.uid}</span> ·{" "}
          <span className="font-mono">{user.shell}</span>
        </>
      }
      trailing={wide ? readings : undefined}
      actions={<VerbActions dim verbs={userVerbs(props)} menuLabel={`More for ${user.username}`} />}
    >
      {!wide && <div className="pl-12">{readings}</div>}
    </ChoiceRow>
  )
}

function CreateUserDialog({ onDone }: { onDone: () => void }) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [username, setUsername] = useState("")
  const [comment, setComment] = useState("")
  const [shell, setShell] = useState("/bin/bash")
  const [groups, setGroups] = useState("")
  const [sshKey, setSshKey] = useState("")

  const create = async () => {
    setBusy(true)
    try {
      await post("/system-users/", {
        username,
        comment,
        shell,
        groups: groups
          .split(",")
          .map((g) => g.trim())
          .filter(Boolean),
        sshKey: sshKey.trim(),
      })
      notify.success(`Created ${username}`)
      setOpen(false)
      setUsername("")
      setComment("")
      setGroups("")
      setSshKey("")
      onDone()
    } catch (err) {
      notify.error("Could not create account", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}>
        <UserPlus className="size-4" />
        New account
      </Button>
      <Modal
        open={open}
        onOpenChange={setOpen}
        title="New system account"
        description="Create a local account on this host"
        footer={
          <Button onClick={create} disabled={!username} pending={busy}>
            Create
          </Button>
        }
      >
        <div className="grid gap-4">
          <Field label="Username" htmlFor="new-username">
            <Input
              id="new-username"
              autoComplete="off"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
          </Field>
          <Field
            label="Full name"
            htmlFor="new-comment"
            hint="Optional. Stored as the account's comment."
          >
            <Input id="new-comment" value={comment} onChange={(e) => setComment(e.target.value)} />
          </Field>
          <FieldRow>
            <Field label="Shell" htmlFor="new-shell">
              <Input
                id="new-shell"
                className="font-mono"
                value={shell}
                onChange={(e) => setShell(e.target.value)}
              />
            </Field>
            <Field
              label="Groups"
              htmlFor="new-groups"
              hint="Comma-separated; each must already exist."
            >
              <Input
                id="new-groups"
                value={groups}
                onChange={(e) => setGroups(e.target.value)}
                placeholder="sudo, docker"
              />
            </Field>
          </FieldRow>
          <Field label="SSH public key" htmlFor="new-key">
            <Textarea
              id="new-key"
              value={sshKey}
              onChange={(e) => setSshKey(e.target.value)}
              className="font-mono text-xs"
              rows={3}
              placeholder="ssh-ed25519 AAAA…"
            />
          </Field>
          <FormNote>
            The account is created with no password, so a key is the only way in. It is validated
            before the account exists.
          </FormNote>
        </div>
      </Modal>
    </>
  )
}

/**
 * An account's authorised keys. The sheet opens on the account itself — its
 * mark and its name — and each key is drawn as the service its comment says
 * holds it (`keyProduct`: `github-actions` is GitHub's), a key glyph where the
 * comment says nothing.
 */
function SSHKeysSheet({
  user,
  username,
  onOpenChange,
  onChanged,
}: {
  user?: SystemUser
  username: string | null
  onOpenChange: (open: boolean) => void
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const [newKey, setNewKey] = useState("")
  const [adding, setAdding] = useState(false)
  const { data, error, loading, refresh } = usePoll(
    (signal) =>
      get<{ path: string; keys: SSHKey[] }>(
        `/system-users/${encodeURIComponent(username ?? "")}/keys`,
        undefined,
        signal,
      ),
    0,
    [username],
    { enabled: username !== null },
  )

  const add = async () => {
    if (!username) return
    setAdding(true)
    try {
      await post(`/system-users/${encodeURIComponent(username)}/keys`, { key: newKey.trim() })
      notify.success("Key authorised")
      setNewKey("")
      refresh()
      onChanged()
    } catch (err) {
      notify.error("Key rejected", err)
    } finally {
      setAdding(false)
    }
  }

  const revoke = (key: SSHKey) =>
    confirm({
      title: "Revoke SSH key",
      confirmLabel: "Revoke",
      description: (
        <p>
          Whoever holds this key loses SSH access as <b>{username}</b>.
        </p>
      ),
      action: async (c) => {
        await del(`/system-users/${encodeURIComponent(username ?? "")}/keys`, {
          confirm: c,
          query: { fingerprint: key.fingerprint },
        })
        refresh()
        onChanged()
      },
    })

  return (
    <>
      <SidePanel
        open={username !== null}
        onOpenChange={onOpenChange}
        width="md"
        title={
          <>
            {user && <AccountMark user={user} size="sm" />}
            <span className="min-w-0 truncate font-mono">{username ?? ""}</span>
          </>
        }
        description="Authorised SSH keys for this account"
      >
        <div className="space-y-6">
          <Panel plain>
            <PanelHeader
              title="Authorised keys"
              actions={
                data && (
                  <span className="numeric text-hint text-muted-foreground">
                    {plural(data.keys.length, "key")}
                  </span>
                )
              }
            />
            <PanelBody flush className="py-1">
              {loading && !data && <LoadingRows rows={3} className="py-3" />}
              {error && <ErrorState error={error} />}
              {data && data.keys.length === 0 && <EmptyNote>No authorised keys yet.</EmptyNote>}
              {data && data.keys.length > 0 && (
                <RowList className="animate-rise">
                  {data.keys.map((key) => (
                    <Row
                      key={key.fingerprint}
                      leading={
                        <ProductLogo id={keyProduct(key.comment)} size="sm" fallback={Key} />
                      }
                      title={
                        key.comment || <span className="text-muted-foreground">No comment</span>
                      }
                      subtitle={key.fingerprint}
                      mono
                      className="py-2.5"
                      trailing={
                        <>
                          <Tag mono>{key.bits ? `${key.type} ${key.bits}` : key.type}</Tag>
                          <IconAction
                            label="Revoke key"
                            className="text-muted-foreground hover:text-destructive"
                            onClick={() => revoke(key)}
                          >
                            <Trash />
                          </IconAction>
                        </>
                      }
                    />
                  ))}
                </RowList>
              )}
            </PanelBody>
            {data?.path && (
              <PanelFooter>
                <FormFacts>
                  <FormFact label="File" mono>
                    {data.path}
                  </FormFact>
                </FormFacts>
              </PanelFooter>
            )}
          </Panel>

          <Panel plain>
            <PanelHeader title="Authorise another key" />
            <PanelBody className="space-y-3">
              <Field
                label="Public key"
                htmlFor="add-key"
                hint="One public key. It is checked before anything is written."
              >
                <Textarea
                  id="add-key"
                  value={newKey}
                  onChange={(e) => setNewKey(e.target.value)}
                  className="font-mono text-xs"
                  rows={3}
                  placeholder="ssh-ed25519 AAAA…"
                />
              </Field>
              <Button size="sm" onClick={add} disabled={!newKey.trim()} pending={adding}>
                <Plus className="size-4" />
                Add key
              </Button>
            </PanelBody>
          </Panel>
        </div>
      </SidePanel>
      {dialog}
    </>
  )
}
