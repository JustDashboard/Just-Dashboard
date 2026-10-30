"use client"

import { useState } from "react"
import {
  Copy,
  Download,
  Key,
  LockClosed,
  LockOpen,
  Pencil,
  Plus,
  Trash,
  Users,
} from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { SecretField } from "@/components/database/ops/access-parts"
import { generatePassword } from "@/components/database/connect/rules"
import { Field, FieldRow, FormNote, OptionRow } from "@/components/form"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { Address } from "@/components/security/marks"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { useAccount } from "@/components/system-users/account-page"
import {
  readRestrictions,
  restrictionWords,
  whyClosed,
  writeRestrictions,
  type KeyRestrictions,
} from "@/components/system-users/access"
import { Segments } from "@/components/deploy/settings/segments"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { VerbBar, VerbMenu, type Verb } from "@/components/verbs"
import { usePoll } from "@/hooks/use-poll"
import { del, get, patch, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { keyProduct } from "@/lib/clients"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { SSHKey, SystemUser, SystemUserDetail } from "@/lib/types"

/**
 * The account's ways in, one row each: its password, then every key. A key is a
 * row with its own mark and the same weight as the password, because a server's
 * operator treats them as the same question — who holds something that opens
 * this — and the old page answered it in two different places.
 */
export function SignInPanel() {
  const { path, readOnly, refresh, query } = useAccount()
  const [adding, setAdding] = useState(false)
  const [passwording, setPasswording] = useState(false)
  const {
    data,
    error,
    loading,
    refresh: refreshKeys,
  } = usePoll(
    (signal) => get<{ path: string; keys: SSHKey[] }>(`${path}/keys`, undefined, signal),
    0,
    [path],
  )
  const changed = () => {
    refreshKeys()
    refresh()
  }
  return (
    <Panel plain aria-label="Sign-in">
      <PanelHeader
        title="Sign-in"
        actions={
          !readOnly && (
            <Button size="xs" variant="outline" onClick={() => setAdding(true)}>
              <Plus className="size-3" />
              Add key
            </Button>
          )
        }
      />
      <PanelBody flush>
        <RowList className="animate-rise">
          <PasswordRow onSet={() => setPasswording(true)} />
          {loading && !data && <LoadingRows rows={2} className="py-3" />}
          {error && <ErrorState error={error} />}
          {data?.keys.map((key) => (
            <KeyRow key={key.fingerprint} sshKey={key} onChanged={changed} />
          ))}
        </RowList>
        {data && data.keys.length === 0 && (
          <EmptyNote className="px-0 py-3 text-left">
            No SSH keys yet. Paste one, import it from GitHub, or generate a new pair.
          </EmptyNote>
        )}
      </PanelBody>
      {adding && <AddKeyDialog onClose={() => setAdding(false)} onAdded={changed} query={query} />}
      {passwording && <SetPasswordDialog onClose={() => setPasswording(false)} />}
    </Panel>
  )
}

/**
 * The shadow file counts in whole days, so "11h ago" would be a number it never
 * held: it is the distance to the midnight of the day the password changed.
 */
function daysAgo(date: string) {
  const days = Math.floor((Date.now() - new Date(date).getTime()) / 86_400_000)
  if (days <= 0) return "today"
  return days === 1 ? "yesterday" : `${days} days ago`
}

function passwordReading(user: SystemUserDetail): { tone: DotTone; label: string; detail: string } {
  const closed = user.signIn.password.allowed ? "" : whyClosed(user.signIn.password.why)
  const aging = [
    user.aging.lastChanged && `Changed ${daysAgo(user.aging.lastChanged)}`,
    user.aging.mustChange && "Must change at the next sign-in",
    user.aging.expiresOn && `Expires ${user.aging.expiresOn}`,
    user.failedAttempts.available &&
      user.failedAttempts.count > 0 &&
      plural(user.failedAttempts.count, "failed attempt"),
  ]
    .filter(Boolean)
    .join(" · ")
  switch (user.password) {
    case "set":
      return user.signIn.password.allowed
        ? { tone: "running", label: "Set", detail: aging }
        : { tone: "notice", label: "Set, not usable over SSH", detail: closed }
    case "locked":
      return {
        tone: "unknown",
        label: "Locked",
        detail: "It stops working until unlocked, and unlocking brings the same password back.",
      }
    case "empty":
      return {
        tone: "danger",
        label: "Empty",
        detail: user.signIn.password.allowed ? "No password is asked for." : closed,
      }
    case "none":
      return { tone: "unknown", label: "None", detail: "Nobody can sign in with a password." }
  }
  return { tone: "unknown", label: "Unknown", detail: "This process cannot read the shadow file." }
}

function PasswordRow({ onSet }: { onSet: () => void }) {
  const { user, path, query, refresh, readOnly } = useAccount()
  const { confirm, dialog } = useConfirm()
  const reading = passwordReading(user)

  const act = async (run: () => Promise<unknown>, done: string) => {
    try {
      await run()
      notify.success(done)
      refresh()
    } catch (err) {
      notify.error("That did not go through", err)
    }
  }
  const lock = (locked: boolean) =>
    act(() => patch(path, { locked }, { query }), locked ? "Password locked" : "Password unlocked")
  const remove = () =>
    confirm({
      title: `Remove ${user.username}'s password`,
      confirmLabel: "Remove",
      description: (
        <p>
          The password is gone for good, not hidden. Only a key can sign in as this account
          afterwards.
        </p>
      ),
      action: async (typed) => {
        await del(`${path}/password`, { confirm: typed, query })
        refresh()
      },
    })

  const verbs: Verb[] = readOnly
    ? []
    : [
        {
          key: "set",
          label: user.password === "none" || user.password === "empty" ? "Set password" : "Change",
          icon: Pencil,
          inline: true,
          run: onSet,
        },
        ...(user.password === "locked"
          ? [
              {
                key: "unlock",
                label: "Unlock",
                icon: LockOpen,
                inline: true,
                run: () => void lock(false),
              },
            ]
          : []),
        ...(user.password === "set"
          ? [
              {
                key: "must",
                label: "Require a change at next sign-in",
                icon: Pencil,
                run: () =>
                  void act(
                    () => patch(path, { mustChangePassword: true }, { query }),
                    "The next sign-in will have to change it",
                  ),
              },
              ...(user.protected
                ? []
                : [{ key: "lock", label: "Lock", icon: LockClosed, run: () => void lock(true) }]),
            ]
          : []),
        // Root is the one protected account with a password an operator holds, and
        // going keys-only is what they are told to do. The server's guard refuses it
        // while that password is the last way in.
        ...((user.password === "set" || user.password === "locked") &&
        (!user.protected || user.uid === 0)
          ? [{ key: "remove", label: "Remove", icon: Trash, danger: true, run: remove }]
          : []),
        ...(user.failedAttempts.count > 0
          ? [
              {
                key: "reset",
                label: "Clear failed attempts",
                icon: LockOpen,
                run: () =>
                  void act(
                    () => post(`${path}/failed-attempts/reset`, undefined, { query }),
                    "Failed attempts cleared",
                  ),
              },
            ]
          : []),
      ]

  return (
    <>
      <Row
        leading={<ProductLogo size="sm" fallback={LockClosed} />}
        title={
          <span className="flex flex-wrap items-center gap-x-2.5">
            Password
            <Status tone={reading.tone} label={reading.label} />
          </span>
        }
        subtitle={reading.detail || undefined}
        trailing={<VerbBar verbs={verbs} menuLabel="More for the password" />}
      />
      {dialog}
    </>
  )
}

function KeyRow({ sshKey, onChanged }: { sshKey: SSHKey; onChanged: () => void }) {
  const { user, path, query, readOnly } = useAccount()
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useState(false)
  const [copying, setCopying] = useState(false)
  const restrictions = restrictionWords(sshKey.options)

  const revoke = () =>
    confirm({
      title: "Revoke SSH key",
      confirmLabel: "Revoke",
      subject: {
        mark: <ProductLogo id={keyProduct(sshKey.comment)} size="sm" fallback={Key} />,
        name: sshKey.comment || "No label",
      },
      description: (
        <p>
          Whoever holds this key loses SSH access as <b>{user.username}</b>.
        </p>
      ),
      action: async (typed) => {
        await del(`${path}/keys`, {
          confirm: typed,
          query: { ...query, fingerprint: sshKey.fingerprint },
        })
        onChanged()
      },
    })

  const verbs: Verb[] = [
    {
      key: "copy",
      label: "Copy public key",
      icon: Copy,
      run: () => void copyText(sshKey.raw, "Public key copied"),
    },
    ...(readOnly || sshKey.file
      ? []
      : [
          {
            key: "edit",
            label: "Label and restrictions",
            icon: Pencil,
            run: () => setEditing(true),
          },
          {
            key: "give",
            label: "Give to other accounts",
            icon: Users,
            run: () => setCopying(true),
          },
          { key: "revoke", label: "Revoke", icon: Trash, danger: true, run: revoke },
        ]),
  ]

  return (
    <>
      <Row
        leading={<ProductLogo id={keyProduct(sshKey.comment)} size="sm" fallback={Key} />}
        title={
          <span className="flex min-w-0 flex-wrap items-center gap-x-2">
            <span className="min-w-0 truncate">
              {keyLabel(sshKey) || <span className="text-muted-foreground">No label</span>}
            </span>
            {sshKey.weak && <Status tone="warning" label="Weak" />}
          </span>
        }
        subtitle={
          <span className="flex min-w-0 flex-wrap items-center gap-x-2">
            <span className="truncate font-mono">{sshKey.fingerprint}</span>
            {sshKey.file && <span className="truncate font-mono">{sshKey.file}</span>}
            {restrictions.map((word) => (
              <span key={word} className="text-warning">
                {word}
              </span>
            ))}
            {sshKey.weak && <span className="text-warning">{sshKey.weak}</span>}
          </span>
        }
        trailing={
          <span className="flex items-center gap-2.5">
            {sshKey.lastUsed && (
              <span className="hidden items-center gap-1.5 text-hint text-muted-foreground lg:inline-flex">
                <span className="numeric shrink-0">{relativeTime(sshKey.lastUsed)}</span>
                {sshKey.lastUsedFrom && <Address ip={sshKey.lastUsedFrom} className="min-w-0" />}
              </span>
            )}
            <Tag mono>
              {sshKey.bits ? `${shortType(sshKey.type)} ${sshKey.bits}` : shortType(sshKey.type)}
            </Tag>
            <VerbMenu verbs={verbs} label={`More for ${sshKey.comment || "this key"}`} />
          </span>
        }
      />
      {editing && (
        <EditKeyDialog sshKey={sshKey} onClose={() => setEditing(false)} onChanged={onChanged} />
      )}
      {copying && <GiveKeyDialog sshKey={sshKey} onClose={() => setCopying(false)} />}
      {dialog}
    </>
  )
}

const shortType = (type: string) => type.replace(/^ssh-/, "")

/** An import's label says where it came from, and the row's GitHub mark already says that. */
const keyLabel = (key: SSHKey) => key.comment.replace(/^github:/, "")

function SetPasswordDialog({ onClose }: { onClose: () => void }) {
  const { user, path, query, refresh } = useAccount()
  const [password, setPassword] = useState(() => generatePassword(20))
  const [mustChange, setMustChange] = useState(false)
  const [busy, setBusy] = useState(false)

  const save = async () => {
    setBusy(true)
    try {
      await post(`${path}/password`, { password, mustChange }, { query })
      notify.success(`${user.username}'s password was set`)
      refresh()
      onClose()
    } catch (err) {
      notify.error("The password was not set", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onOpenChange={(open) => !open && onClose()}
      title={`Set a password for ${user.username}`}
      footer={
        <Button onClick={() => void save()} disabled={password.length < 8} pending={busy}>
          Set password
        </Button>
      }
    >
      <div className="grid gap-4">
        <SecretField
          id="account-password"
          label="Password"
          hint="Copy it before you save. The server keeps only a hash, so nobody can read it again."
          value={password}
          onChange={setPassword}
        />
        <OptionRow
          title="Make them change it"
          hint="The next sign-in has to choose a new one."
          checked={mustChange}
          onCheckedChange={setMustChange}
        />
      </div>
    </Modal>
  )
}

type AddMode = "paste" | "github" | "generate"

function AddKeyDialog({
  onClose,
  onAdded,
  query,
}: {
  onClose: () => void
  onAdded: () => void
  query?: { allowSystem: true }
}) {
  const { user, path } = useAccount()
  const [mode, setMode] = useState<AddMode>("paste")
  const [key, setKey] = useState("")
  const [label, setLabel] = useState("")
  const [github, setGithub] = useState("")
  const [passphrase, setPassphrase] = useState("")
  const [restrictions, setRestrictions] = useState<KeyRestrictions>(() => readRestrictions(""))
  const [busy, setBusy] = useState(false)
  const [privateKey, setPrivateKey] = useState("")

  const add = async () => {
    setBusy(true)
    try {
      if (mode === "paste") {
        const options = writeRestrictions(restrictions)
        await post(
          `${path}/keys`,
          {
            key: key.trim(),
            ...(label.trim() ? { comment: label.trim() } : {}),
            ...(options.length > 0 ? { options } : {}),
          },
          { query },
        )
        notify.success("Key authorised")
      } else if (mode === "github") {
        const result = await post<{ added: SSHKey[]; skipped: number }>(
          `${path}/keys/import`,
          { githubUser: github.trim() },
          { query },
        )
        notify.success(
          result.added.length > 0
            ? `${plural(result.added.length, "key")} imported from ${github.trim()}`
            : `${github.trim()}'s keys were already here`,
        )
      } else {
        const result = await post<{ key: SSHKey; privateKey: string }>(
          `${path}/keys/generate`,
          { comment: label.trim(), passphrase },
          { query },
        )
        onAdded()
        setPrivateKey(result.privateKey)
        return
      }
      onAdded()
      onClose()
    } catch (err) {
      notify.error("The key was not added", err)
    } finally {
      setBusy(false)
    }
  }

  if (privateKey) {
    return (
      <Modal
        open
        onOpenChange={(open) => !open && onClose()}
        title="Save the private key now"
        footer={<Button onClick={onClose}>I have saved it</Button>}
      >
        <div className="grid gap-3" data-slot="private-key-shown">
          <Notice tone="warning" title="This is the only time it is shown">
            The server kept the public half and never stored this. Whoever holds it can sign in as{" "}
            {user.username}.
          </Notice>
          <Textarea readOnly value={privateKey} rows={8} className="font-mono text-xs" />
          <div className="flex flex-wrap gap-2">
            <Button
              size="sm"
              variant="outline"
              onClick={() => void copyText(privateKey, "Private key copied")}
            >
              <Copy className="size-4" />
              Copy
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => saveKeyFile(user.username, privateKey)}
            >
              <Download className="size-4" />
              Download
            </Button>
          </div>
        </div>
      </Modal>
    )
  }

  const ready =
    mode === "paste" ? key.trim() !== "" : mode === "github" ? github.trim() !== "" : true
  return (
    <Modal
      open
      onOpenChange={(open) => !open && onClose()}
      title={`Add a key to ${user.username}`}
      footer={
        <Button onClick={() => void add()} disabled={!ready} pending={busy}>
          {mode === "generate" ? "Generate" : mode === "github" ? "Import" : "Add key"}
        </Button>
      }
    >
      <div className="grid gap-4">
        <Segments
          label="Where the key comes from"
          value={mode}
          onChange={setMode}
          fill
          options={[
            { value: "paste", label: "Paste" },
            {
              value: "github",
              label: (
                <span className="inline-flex items-center gap-1.5">
                  <ProductGlyph id="github" />
                  GitHub
                </span>
              ),
            },
            { value: "generate", label: "Generate" },
          ]}
        />
        {mode === "paste" && (
          <>
            <Field
              label="Public key"
              htmlFor="add-key"
              hint="The contents of a .pub file. It is checked before anything is written."
            >
              <Textarea
                id="add-key"
                value={key}
                onChange={(e) => setKey(e.target.value)}
                className="font-mono text-xs"
                rows={3}
                placeholder="ssh-ed25519 AAAA…"
              />
            </Field>
            <Field label="Label" htmlFor="add-key-label" hint="Optional. Who or what holds it.">
              <Input id="add-key-label" value={label} onChange={(e) => setLabel(e.target.value)} />
            </Field>
            <RestrictionFields value={restrictions} onChange={setRestrictions} />
          </>
        )}
        {mode === "github" && (
          <Field
            label="GitHub username"
            htmlFor="add-key-github"
            hint="Fetches the public keys GitHub publishes for them and labels each one."
          >
            <Input
              id="add-key-github"
              value={github}
              onChange={(e) => setGithub(e.target.value)}
              autoComplete="off"
              placeholder="octocat"
            />
          </Field>
        )}
        {mode === "generate" && (
          <>
            <FieldRow>
              <Field label="Label" htmlFor="gen-key-label">
                <Input
                  id="gen-key-label"
                  value={label}
                  onChange={(e) => setLabel(e.target.value)}
                  placeholder="deploy"
                />
              </Field>
              <Field label="Passphrase" htmlFor="gen-key-pass" hint="Optional.">
                <Input
                  id="gen-key-pass"
                  type="password"
                  autoComplete="new-password"
                  value={passphrase}
                  onChange={(e) => setPassphrase(e.target.value)}
                />
              </Field>
            </FieldRow>
            <FormNote>
              Makes an Ed25519 pair. The public half is authorised here and the private half is
              shown once, then exists only where you save it.
            </FormNote>
          </>
        )}
      </div>
    </Modal>
  )
}

function saveKeyFile(username: string, content: string) {
  const url = URL.createObjectURL(new Blob([content], { type: "application/x-pem-file" }))
  const link = document.createElement("a")
  link.href = url
  link.download = `${username}_ed25519`
  link.click()
  URL.revokeObjectURL(url)
}

function RestrictionFields({
  value,
  onChange,
}: {
  value: KeyRestrictions
  onChange: (next: KeyRestrictions) => void
}) {
  const set = (patch: Partial<KeyRestrictions>) => onChange({ ...value, ...patch })
  return (
    <div className="grid gap-3">
      <OptionRow
        title="No forwarding or terminal extras"
        hint="Turns off port, agent and X11 forwarding for this key."
        checked={value.restrict}
        onCheckedChange={(restrict) => set({ restrict })}
      />
      <FieldRow>
        <Field label="Only from" htmlFor="key-from" hint="Addresses or networks, comma-separated.">
          <Input
            id="key-from"
            className="font-mono"
            value={value.from}
            onChange={(e) => set({ from: e.target.value })}
            placeholder="203.0.113.0/24"
          />
        </Field>
        <Field label="Expires" htmlFor="key-expires" hint="Refused after this date.">
          <Input
            id="key-expires"
            type="date"
            value={value.expires}
            onChange={(e) => set({ expires: e.target.value })}
          />
        </Field>
      </FieldRow>
      <Field
        label="Only this command"
        htmlFor="key-command"
        hint="The key can run this and nothing else."
      >
        <Input
          id="key-command"
          className="font-mono"
          value={value.command}
          onChange={(e) => set({ command: e.target.value })}
          placeholder="/usr/local/bin/backup"
        />
      </Field>
    </div>
  )
}

function EditKeyDialog({
  sshKey,
  onClose,
  onChanged,
}: {
  sshKey: SSHKey
  onClose: () => void
  onChanged: () => void
}) {
  const { path, query } = useAccount()
  const [label, setLabel] = useState(sshKey.comment)
  const [restrictions, setRestrictions] = useState(() => readRestrictions(sshKey.options))
  const [busy, setBusy] = useState(false)
  const save = async () => {
    setBusy(true)
    try {
      const options = writeRestrictions(restrictions)
      await patch(
        `${path}/keys`,
        {
          fingerprint: sshKey.fingerprint,
          comment: label.trim(),
          ...(options.length > 0 ? { options } : { clearOptions: true }),
        },
        { query },
      )
      notify.success("Key updated")
      onChanged()
      onClose()
    } catch (err) {
      notify.error("The key was not changed", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onOpenChange={(open) => !open && onClose()}
      title="Label and restrictions"
      footer={
        <Button onClick={() => void save()} pending={busy}>
          Save
        </Button>
      }
    >
      <div className="grid gap-4">
        <Field label="Label" htmlFor="edit-key-label" hint={sshKey.fingerprint}>
          <Input id="edit-key-label" value={label} onChange={(e) => setLabel(e.target.value)} />
        </Field>
        <RestrictionFields value={restrictions} onChange={setRestrictions} />
      </div>
    </Modal>
  )
}

function GiveKeyDialog({ sshKey, onClose }: { sshKey: SSHKey; onClose: () => void }) {
  const { user, path, query } = useAccount()
  const [chosen, setChosen] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const { data } = usePoll(
    (signal) => get<SystemUser[]>("/system-users/", undefined, signal),
    0,
    [],
  )
  const others = (data ?? []).filter((u) => u.username !== user.username)
  const give = async () => {
    setBusy(true)
    try {
      const { results } = await post<{ results: Record<string, string> }>(
        `${path}/keys/copy`,
        { fingerprint: sshKey.fingerprint, to: chosen },
        { query },
      )
      const refused = Object.entries(results).filter(([, error]) => error)
      if (refused.length === 0) notify.success(`Key given to ${chosen.join(", ")}`)
      else
        notify.error(
          "Not every account took it",
          new Error(refused.map(([n, e]) => `${n}: ${e}`).join("; ")),
        )
      onClose()
    } catch (err) {
      notify.error("The key was not given", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onOpenChange={(open) => !open && onClose()}
      title="Give this key to other accounts"
      footer={
        <Button onClick={() => void give()} disabled={chosen.length === 0} pending={busy}>
          Give key
        </Button>
      }
    >
      <div className="grid gap-1">
        {others.map((other) => (
          <OptionRow
            key={other.username}
            title={<span className="font-mono">{other.username}</span>}
            checked={chosen.includes(other.username)}
            onCheckedChange={(on) =>
              setChosen((now) =>
                on ? [...now, other.username] : now.filter((name) => name !== other.username),
              )
            }
          />
        ))}
      </div>
    </Modal>
  )
}
