"use client"

import { useState } from "react"
import { Key, LockClosed, LockOpen, Plus, Trash, UserPlus, Users } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, patch, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import type { SSHKey, SystemUser } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { Page, PageHeader } from "@/components/page"
import { Group, Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { EmptyState, ErrorState, LoadingPanel, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { IconAction, RowActions } from "@/components/icon-action"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { Checkbox } from "@/components/ui/checkbox"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

export default function SystemUsersPage() {
  const { confirm, dialog } = useConfirm()
  const [showSystem, setShowSystem] = useViewState("system-users.show-system", false)
  const [keysFor, setKeysFor] = useState<string | null>(null)
  const { data, error, loading, refresh } = usePoll(
    (signal) => get<SystemUser[]>("/system-users/", { system: showSystem }, signal),
    30000,
    [showSystem],
  )

  const setLocked = async (user: SystemUser, locked: boolean) => {
    try {
      await patch(`/system-users/${encodeURIComponent(user.username)}`, { locked })
      notify.success(`${user.username} ${locked ? "locked" : "unlocked"}`)
      refresh()
    } catch (err) {
      notify.error("Could not change lock state", err)
    }
  }

  return (
    <Page>
      <PageHeader
        eyebrow="Operations"
        title="System users"
        actions={<CreateUserDialog onDone={refresh} />}
      />

      {loading && <LoadingPanel />}
      {error && <ErrorState error={error} />}

      {data && (
        <Panel>
          <PanelHeader title="Accounts" />
          <PanelToolbar>
            <label className="flex items-center gap-2 text-body text-muted-foreground">
              <Checkbox checked={showSystem} onCheckedChange={(v) => setShowSystem(v === true)} />
              Include system accounts
            </label>
          </PanelToolbar>
          <PanelBody flush>
            <Table containerClassName="max-h-[calc(100svh-20rem)]">
              <TableHeader className={stickyTableHeader}>
                <TableRow>
                  <TableHead>User</TableHead>
                  <TableHead className="w-20">UID</TableHead>
                  <TableHead className="w-full">Groups</TableHead>
                  <TableHead>Shell</TableHead>
                  <TableHead>Last login</TableHead>
                  <TableHead>State</TableHead>
                  <TableHead className="w-px" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.map((user) => (
                  <TableRow key={user.username} className="group">
                    <TableCell>
                      <div className="max-w-[14rem] min-w-0">
                        <div className="truncate text-body font-medium">{user.username}</div>
                        {user.comment && (
                          <p className="truncate text-hint text-muted-foreground">{user.comment}</p>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="numeric font-mono">{user.uid}</TableCell>
                    <TableCell className="max-w-48 truncate text-muted-foreground">
                      {user.groups.join(", ")}
                    </TableCell>
                    <TableCell className="font-mono text-hint text-muted-foreground">
                      {user.shell}
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {user.lastLogin ? relativeTime(user.lastLogin) : "never"}
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1">
                        {user.locked && <Tag>locked</Tag>}
                        {user.noPassword && <Tag tone="warning">no password</Tag>}
                        {!user.canLogin && <Tag>no shell</Tag>}
                      </div>
                    </TableCell>
                    <TableCell>
                      <RowActions>
                        <Button size="xs" variant="ghost" onClick={() => setKeysFor(user.username)}>
                          <Key className="size-3" />
                          {user.sshKeyCount}
                        </Button>
                        <IconAction
                          label={user.locked ? "Unlock" : "Lock"}
                          onClick={() => setLocked(user, !user.locked)}
                        >
                          {user.locked ? <LockOpen /> : <LockClosed />}
                        </IconAction>
                        <IconAction
                          label="Delete"
                          className="text-destructive"
                          onClick={() =>
                            confirm({
                              title: "Delete system user",
                              phrase: user.username,
                              confirmLabel: "Delete",
                              description: (
                                <p className="text-destructive">
                                  Removes the account <b>{user.username}</b> from this host. Its
                                  home directory is left in place.
                                </p>
                              ),
                              action: async (c) => {
                                await del(`/system-users/${encodeURIComponent(user.username)}`, {
                                  confirm: c,
                                })
                                refresh()
                              },
                            })
                          }
                        >
                          <Trash />
                        </IconAction>
                      </RowActions>
                    </TableCell>
                  </TableRow>
                ))}
                {data.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={7} className="p-0">
                      <EmptyState icon={Users} title="No accounts to show" />
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </PanelBody>
        </Panel>
      )}

      <SSHKeysSheet
        username={keysFor}
        onOpenChange={(o) => !o && setKeysFor(null)}
        onChanged={refresh}
      />
      {dialog}
    </Page>
  )
}

function CreateUserDialog({ onDone }: { onDone: () => void }) {
  const [open, setOpen] = useState(false)
  const [username, setUsername] = useState("")
  const [comment, setComment] = useState("")
  const [shell, setShell] = useState("/bin/bash")
  const [groups, setGroups] = useState("")
  const [sshKey, setSshKey] = useState("")

  const create = async () => {
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
        footer={
          <>
            <Button onClick={create} disabled={!username}>
              Create
            </Button>
          </>
        }
      >
        <div className="grid gap-3">
          <div className="space-y-1.5">
            <Label htmlFor="new-username">Username</Label>
            <Input
              id="new-username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="new-comment">Full name</Label>
            <Input id="new-comment" value={comment} onChange={(e) => setComment(e.target.value)} />
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="new-shell">Shell</Label>
              <Input id="new-shell" value={shell} onChange={(e) => setShell(e.target.value)} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="new-groups">Groups</Label>
              <Input
                id="new-groups"
                value={groups}
                onChange={(e) => setGroups(e.target.value)}
                placeholder="sudo, docker"
              />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="new-key">SSH public key</Label>
            <Textarea
              id="new-key"
              value={sshKey}
              onChange={(e) => setSshKey(e.target.value)}
              className="font-mono text-xs"
              rows={3}
              placeholder="ssh-ed25519 AAAA…"
            />
            <p className="text-xs leading-relaxed text-muted-foreground">
              The account is created with no password, so a key is the only way in. It is validated
              before the account exists.
            </p>
          </div>
        </div>
      </Modal>
    </>
  )
}

function SSHKeysSheet({
  username,
  onOpenChange,
  onChanged,
}: {
  username: string | null
  onOpenChange: (open: boolean) => void
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const [newKey, setNewKey] = useState("")
  const { data, error, loading, refresh } = usePoll(
    (signal) =>
      username
        ? get<{ path: string; keys: SSHKey[] }>(
            `/system-users/${encodeURIComponent(username)}/keys`,
            undefined,
            signal,
          )
        : Promise.resolve({ path: "", keys: [] }),
    0,
    [username],
  )

  const add = async () => {
    if (!username) return
    try {
      await post(`/system-users/${encodeURIComponent(username)}/keys`, { key: newKey.trim() })
      notify.success("Key authorised")
      setNewKey("")
      refresh()
      onChanged()
    } catch (err) {
      notify.error("Key rejected", err)
    }
  }

  return (
    <>
      <SidePanel
        open={username !== null}
        onOpenChange={onOpenChange}
        width="md"
        title={`SSH keys for ${username ?? ""}`}
        description={data?.path}
      >
        <div className="space-y-4">
          {loading && <LoadingRows />}
          {error && <ErrorState error={error} />}

          {data?.keys.map((key) => (
            <Group key={key.fingerprint}>
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <Tag>{key.type}</Tag>
                    <span className="truncate text-body">{key.comment || "no comment"}</span>
                  </div>
                  <p className="mt-1 truncate font-mono text-hint text-muted-foreground">
                    {key.fingerprint}
                  </p>
                </div>
                <Button
                  size="icon-xs"
                  variant="ghost"
                  aria-label="Revoke key"
                  className="shrink-0 text-destructive"
                  onClick={() =>
                    confirm({
                      title: "Revoke SSH key",
                      confirmLabel: "Revoke",
                      description: (
                        <p>
                          Whoever holds this key loses SSH access as <b>{username}</b>.
                        </p>
                      ),
                      action: async (c) => {
                        await del(`/system-users/${encodeURIComponent(username!)}/keys`, {
                          confirm: c,
                          query: { fingerprint: key.fingerprint },
                        })
                        refresh()
                        onChanged()
                      },
                    })
                  }
                >
                  <Trash />
                </Button>
              </div>
            </Group>
          ))}

          {data?.keys.length === 0 && !loading && (
            <EmptyState icon={Key} title="No authorised keys" />
          )}

          <div className="space-y-2 border-t border-hairline pt-4">
            <Label htmlFor="add-key">Authorise another key</Label>
            <Textarea
              id="add-key"
              value={newKey}
              onChange={(e) => setNewKey(e.target.value)}
              className="font-mono text-xs"
              rows={3}
              placeholder="ssh-ed25519 AAAA…"
            />
            <Button size="sm" onClick={add} disabled={!newKey.trim()}>
              <Plus className="size-4" />
              Add key
            </Button>
          </div>
        </div>
      </SidePanel>
      {dialog}
    </>
  )
}
