"use client"

import { useRouter } from "next/navigation"
import { useState } from "react"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { Field, FieldRow, FormNote, FormSections, OptionRow } from "@/components/form"
import { LoadingRows } from "@/components/state"
import { accountHref } from "@/components/system-users/access"
import { useAccount } from "@/components/system-users/account-page"
import { countChanged, validDays, validLimit, validWhole } from "@/components/system-users/fields"
import { ShellGlyph } from "@/components/system-users/marks"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { usePoll } from "@/hooks/use-poll"
import { get, patch, put } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { AccountLimits, SystemGroup, SystemUser } from "@/lib/types"

const NO_LOGIN = "/usr/sbin/nologin"

/** The unit a box counts in, at the end of its label's line. */
function Unit({ children }: { children: React.ReactNode }) {
  return <span className="text-hint text-muted-foreground">{children}</span>
}

/**
 * The fields nobody needs twice a year, as one column of sections under one
 * bar: what the account is, the rules its password lives under, and how much
 * of the machine it may use. They were three folded boxes at the foot of the
 * account, each with a Save of its own.
 *
 * They are still three writes, because they are three different things on the
 * host: a change of shell has nothing to do with a memory limit, and a typo in
 * one must not refuse the other. The bar saves each form that holds an edit,
 * in this order, and a form the server turned down stays on it.
 *
 * A package's account is shown and cannot be changed until its changes are
 * allowed, which is said once at the head of the page.
 */
export function AccountSettings() {
  const { user } = useAccount()
  return (
    <SaveBarProvider>
      <div
        data-slot="account-settings"
        className="mx-auto w-full max-w-3xl min-w-0 animate-rise pb-20"
      >
        <FormSections>
          <ProfileForm
            key={JSON.stringify([
              user.username,
              user.comment,
              user.shell,
              user.home,
              user.uid,
              user.primaryGroup,
            ])}
          />
          <AgingForm key={JSON.stringify([user.expiresOn, user.disabled, user.aging])} />
          <LimitsForm />
        </FormSections>
      </div>
    </SaveBarProvider>
  )
}

function agingFacts(user: SystemUser) {
  const parts = [
    user.expiresOn && !user.disabled && `Account expires ${user.expiresOn}`,
    user.aging.maxDays !== undefined && `Password lasts ${plural(user.aging.maxDays, "day")}`,
  ].filter(Boolean)
  return parts.length > 0 ? parts.join(" · ") : "No expiry"
}

function ProfileForm() {
  const router = useRouter()
  const { user, path, query, refresh, readOnly } = useAccount()
  const [name, setName] = useState(user.username)
  const [comment, setComment] = useState(user.comment)
  const [shell, setShell] = useState(user.shell)
  const [home, setHome] = useState(user.home)
  const [moveHome, setMoveHome] = useState(true)
  const [uid, setUid] = useState(String(user.uid))
  const [primary, setPrimary] = useState(user.primaryGroup ?? "")
  const [busy, setBusy] = useState(false)
  const { data: groups } = usePoll(
    (signal) => get<SystemGroup[]>("/system-accounts/groups", undefined, signal),
    0,
    [],
  )
  const shells = Array.from(new Set([...user.shells, user.shell, NO_LOGIN]))
  const changedHome = home !== user.home
  const changes = countChanged([
    [name, user.username],
    [comment, user.comment],
    [shell, user.shell],
    [home, user.home],
    [uid, String(user.uid)],
    [primary, user.primaryGroup ?? ""],
  ])
  // Said about what was typed here, never about what the host already holds.
  const errors = {
    name: name !== user.username && name.trim() === "" ? "An account needs a name." : undefined,
    home: changedHome && home.trim() === "" ? "An account needs a home." : undefined,
    uid:
      uid === String(user.uid) || validWhole(uid, 0, 4294967294)
        ? undefined
        : "A whole number, 0 or more.",
  }

  const discard = () => {
    setName(user.username)
    setComment(user.comment)
    setShell(user.shell)
    setHome(user.home)
    setMoveHome(true)
    setUid(String(user.uid))
    setPrimary(user.primaryGroup ?? "")
  }

  const save = async () => {
    setBusy(true)
    try {
      const body = {
        ...(name !== user.username ? { newName: name } : {}),
        ...(comment !== user.comment ? { comment } : {}),
        ...(shell !== user.shell ? { shell } : {}),
        ...(changedHome ? { home, moveHome } : {}),
        ...(uid !== String(user.uid) ? { uid: Number(uid) } : {}),
        ...(primary !== (user.primaryGroup ?? "") ? { primaryGroup: primary } : {}),
      }
      const updated = await patch<SystemUser>(path, body, { query })
      notify.success("Saved")
      // A renamed account lives at another address, and its settings with it.
      if (updated.username !== user.username) {
        router.replace(`${accountHref(updated.username)}?tab=settings`)
      } else refresh()
      return true
    } catch (err) {
      notify.error("That was not saved", err)
      return false
    } finally {
      setBusy(false)
    }
  }

  return (
    <SettingForm
      name="Profile"
      onSave={save}
      dirty={changes > 0}
      changes={changes}
      saving={busy}
      invalid={Object.values(errors).some(Boolean)}
      canEdit={!readOnly}
      onDiscard={discard}
      applies="immediately"
    >
      <SettingSection
        title="Profile"
        state={
          <span className="flex min-w-0 flex-wrap items-center gap-x-2">
            <span className="inline-flex items-center gap-1.5 font-mono">
              <ShellGlyph shell={user.shell} />
              {user.shell}
            </span>
            <span className="text-muted-foreground/40">·</span>
            <span className="font-mono">{user.primaryGroup ?? `gid ${user.gid}`}</span>
          </span>
        }
        status={settingStatus({ dirty: changes > 0 })}
      >
        <FieldRow>
          <Field
            label="Username"
            htmlFor="profile-name"
            info="Its private group and crontab move with it."
            error={errors.name}
          >
            <Input
              id="profile-name"
              className="font-mono"
              autoComplete="off"
              value={name}
              disabled={readOnly || user.protected}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field label="Full name" htmlFor="profile-comment">
            <Input
              id="profile-comment"
              value={comment}
              disabled={readOnly}
              onChange={(e) => setComment(e.target.value)}
            />
          </Field>
        </FieldRow>
        <FieldRow>
          <Field label="Shell" htmlFor="profile-shell">
            <Select value={shell} onValueChange={setShell} disabled={readOnly}>
              <SelectTrigger id="profile-shell" className="w-full font-mono">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {shells.map((option) => (
                  <SelectItem key={option} value={option} className="font-mono">
                    <span className="inline-flex items-center gap-1.5">
                      <ShellGlyph shell={option} />
                      {option}
                    </span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field label="Main group" htmlFor="profile-primary">
            <Select
              value={primary}
              onValueChange={setPrimary}
              disabled={readOnly || user.protected}
            >
              <SelectTrigger id="profile-primary" className="w-full font-mono">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(groups ?? []).map((group) => (
                  <SelectItem key={group.name} value={group.name} className="font-mono">
                    {group.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </FieldRow>
        <FieldRow>
          <Field label="Home" htmlFor="profile-home" error={errors.home}>
            <Input
              id="profile-home"
              className="font-mono"
              value={home}
              disabled={readOnly || user.protected}
              onChange={(e) => setHome(e.target.value)}
            />
          </Field>
          <Field
            label="Account number"
            htmlFor="profile-uid"
            info="Files outside the home keep the old number."
            error={errors.uid}
          >
            <Input
              id="profile-uid"
              type="number"
              min={0}
              className="numeric"
              value={uid}
              disabled={readOnly || user.protected}
              onChange={(e) => setUid(e.target.value)}
            />
          </Field>
        </FieldRow>
        {changedHome && (
          <OptionRow
            title="Move the files too"
            hint="Otherwise the old directory stays where it is and the new one starts empty."
            checked={moveHome}
            onCheckedChange={setMoveHome}
          />
        )}
      </SettingSection>
    </SettingForm>
  )
}

function dayValue(value: number | undefined) {
  return value === undefined ? "" : String(value)
}

function AgingForm() {
  const { user, path, query, refresh, readOnly } = useAccount()
  const initial = {
    expires: user.disabled ? "" : (user.expiresOn ?? ""),
    maxDays: dayValue(user.aging.maxDays),
    minDays: dayValue(user.aging.minDays),
    warnDays: dayValue(user.aging.warnDays),
    inactiveDays: dayValue(user.aging.inactiveDays),
  }
  const [draft, setDraft] = useState(initial)
  const [busy, setBusy] = useState(false)
  const set = (change: Partial<typeof initial>) => setDraft((now) => ({ ...now, ...change }))
  const changes = countChanged([
    [draft.expires, initial.expires],
    [draft.maxDays, initial.maxDays],
    [draft.minDays, initial.minDays],
    [draft.warnDays, initial.warnDays],
    [draft.inactiveDays, initial.inactiveDays],
  ])
  const errors = {
    maxDays: validDays(draft.maxDays, 1) ? undefined : "A whole number of days from 1, or empty.",
    minDays: validDays(draft.minDays) ? undefined : "A whole number of days, or empty.",
    warnDays: validDays(draft.warnDays) ? undefined : "A whole number of days, or empty.",
    inactiveDays: validDays(draft.inactiveDays) ? undefined : "A whole number of days, or empty.",
  }

  // chage's own spelling of "no rule" is -1; an empty box says the same thing.
  const days = (value: string) => (value.trim() === "" ? -1 : Number(value))
  const save = async () => {
    setBusy(true)
    try {
      await patch(
        path,
        {
          // A disabled account is one the host has expired on day one. Its date
          // is not this form's to send: an empty one is how an account is
          // enabled, and saving a password rule used to do exactly that.
          ...(user.disabled ? {} : { expiresOn: draft.expires }),
          aging: {
            maxDays: days(draft.maxDays),
            minDays: Math.max(days(draft.minDays), 0),
            warnDays: Math.max(days(draft.warnDays), 0),
            inactiveDays: days(draft.inactiveDays),
          },
        },
        { query },
      )
      notify.success("Saved")
      refresh()
      return true
    } catch (err) {
      notify.error("That was not saved", err)
      return false
    } finally {
      setBusy(false)
    }
  }
  return (
    <SettingForm
      name="Password rules"
      onSave={save}
      dirty={changes > 0}
      changes={changes}
      saving={busy}
      invalid={Object.values(errors).some(Boolean)}
      canEdit={!readOnly}
      onDiscard={() => setDraft(initial)}
      applies="immediately"
    >
      <SettingSection
        title="Password rules and expiry"
        state={<span className="numeric">{agingFacts(user)}</span>}
        status={settingStatus({ dirty: changes > 0 })}
      >
        <FieldRow>
          <Field
            label="Account expires"
            htmlFor="aging-expires"
            info={
              user.disabled
                ? "The account is disabled, which the host keeps as a date already passed. Enable it to give it a date of its own."
                : "Every way in closes on this date."
            }
          >
            <Input
              id="aging-expires"
              type="date"
              value={draft.expires}
              disabled={readOnly || user.disabled}
              onChange={(e) => set({ expires: e.target.value })}
            />
          </Field>
          <Field
            label="Password lasts"
            htmlFor="aging-max"
            info="Empty is forever."
            trailing={<Unit>days</Unit>}
            error={errors.maxDays}
          >
            <Input
              id="aging-max"
              type="number"
              min={1}
              className="numeric"
              placeholder="Forever"
              value={draft.maxDays}
              disabled={readOnly}
              onChange={(e) => set({ maxDays: e.target.value })}
            />
          </Field>
        </FieldRow>
        <FieldRow columns={3}>
          <Field
            label="Warn before it expires"
            htmlFor="aging-warn"
            trailing={<Unit>days</Unit>}
            error={errors.warnDays}
          >
            <Input
              id="aging-warn"
              type="number"
              min={0}
              className="numeric"
              value={draft.warnDays}
              disabled={readOnly}
              onChange={(e) => set({ warnDays: e.target.value })}
            />
          </Field>
          <Field
            label="Keep at least"
            htmlFor="aging-min"
            info="How long before a new password can be changed again."
            trailing={<Unit>days</Unit>}
            error={errors.minDays}
          >
            <Input
              id="aging-min"
              type="number"
              min={0}
              className="numeric"
              value={draft.minDays}
              disabled={readOnly}
              onChange={(e) => set({ minDays: e.target.value })}
            />
          </Field>
          <Field
            label="Grace after expiry"
            htmlFor="aging-inactive"
            info="How long it can still sign in to change an expired password. Empty is no limit."
            trailing={<Unit>days</Unit>}
            error={errors.inactiveDays}
          >
            <Input
              id="aging-inactive"
              type="number"
              min={0}
              className="numeric"
              value={draft.inactiveDays}
              disabled={readOnly}
              onChange={(e) => set({ inactiveDays: e.target.value })}
            />
          </Field>
        </FieldRow>
      </SettingSection>
    </SettingForm>
  )
}

const GB = 1024 ** 3

function limitFacts(limits: AccountLimits) {
  const parts = [
    limits.cpuPercent > 0 && `${limits.cpuPercent}% of a core`,
    limits.memoryBytes > 0 && bytes(limits.memoryBytes),
    limits.tasks > 0 && plural(limits.tasks, "process", "processes"),
    limits.quota && limits.quota.hardBytes > 0 && `${bytes(limits.quota.hardBytes)} of disk`,
  ].filter(Boolean)
  return parts.length > 0 ? parts.join(" · ") : "No limits"
}

function LimitsForm() {
  const { path, query, user, readOnly } = useAccount()
  const { data, refresh } = usePoll(
    (signal) => get<AccountLimits>(`${path}/limits`, undefined, signal),
    0,
    [path],
  )
  if (!data) {
    return (
      <div className="min-w-0 py-8 last:pb-0">
        <SettingSection title="Limits">
          <LoadingRows rows={2} />
        </SettingSection>
      </div>
    )
  }
  return (
    <LimitsFields
      key={JSON.stringify(data)}
      limits={data}
      onSave={async (body) => {
        await put(`${path}/limits`, body, { query })
        refresh()
      }}
      protectedAccount={user.protected}
      readOnly={readOnly}
    />
  )
}

function LimitsFields({
  limits,
  onSave,
  protectedAccount,
  readOnly,
}: {
  limits: AccountLimits
  onSave: (body: object) => Promise<void>
  protectedAccount: boolean
  readOnly: boolean
}) {
  const initial = {
    cpu: limits.cpuPercent ? String(limits.cpuPercent) : "",
    memory: limits.memoryBytes ? String(limits.memoryBytes / GB) : "",
    tasks: limits.tasks ? String(limits.tasks) : "",
    soft: limits.quota?.softBytes ? String(limits.quota.softBytes / GB) : "",
    hard: limits.quota?.hardBytes ? String(limits.quota.hardBytes / GB) : "",
  }
  const [draft, setDraft] = useState(initial)
  const [busy, setBusy] = useState(false)
  const set = (change: Partial<typeof initial>) => setDraft((now) => ({ ...now, ...change }))
  const changes = countChanged([
    [draft.cpu, initial.cpu],
    [draft.memory, initial.memory],
    [draft.tasks, initial.tasks],
    [draft.soft, initial.soft],
    [draft.hard, initial.hard],
  ])
  const errors = {
    cpu: validLimit(draft.cpu, 5) ? undefined : "A whole number from 5, or empty.",
    memory: validLimit(draft.memory, 0, false) ? undefined : "A number of GB, or empty.",
    tasks: validLimit(draft.tasks, 16) ? undefined : "A whole number from 16, or empty.",
    soft: validLimit(draft.soft, 0, false) ? undefined : "A number of GB, or empty.",
    hard: validLimit(draft.hard, 0, false) ? undefined : "A number of GB, or empty.",
  }
  const nothing = !limits.systemd && !limits.quota
  const offered = !protectedAccount && !nothing

  const save = async () => {
    setBusy(true)
    try {
      await onSave({
        cpuPercent: Number(draft.cpu) || 0,
        memoryBytes: Math.round((Number(draft.memory) || 0) * GB),
        tasks: Number(draft.tasks) || 0,
        ...(limits.quota
          ? {
              softBytes: Math.round((Number(draft.soft) || 0) * GB),
              hardBytes: Math.round((Number(draft.hard) || 0) * GB),
            }
          : {}),
      })
      notify.success("Limits saved")
      return true
    } catch (err) {
      notify.error("The limits were not saved", err)
      return false
    } finally {
      setBusy(false)
    }
  }
  return (
    <SettingForm
      name="Limits"
      onSave={save}
      dirty={changes > 0}
      changes={changes}
      saving={busy}
      invalid={Object.values(errors).some(Boolean)}
      canEdit={offered && !readOnly}
      onDiscard={() => setDraft(initial)}
      applies="immediately"
    >
      <SettingSection
        title="Limits"
        state={offered ? <span className="numeric">{limitFacts(limits)}</span> : undefined}
        status={settingStatus({ dirty: changes > 0 })}
      >
        {protectedAccount && (
          <FormNote>The system depends on this account, so it keeps what it has.</FormNote>
        )}
        {!protectedAccount && nothing && (
          <FormNote>
            This host has neither systemd nor disk quotas, so nothing here can be limited.
          </FormNote>
        )}
        {offered && limits.systemd && (
          <FieldRow columns={3}>
            <Field
              label="CPU"
              htmlFor="limit-cpu"
              info="Percent of one core: 200 is two cores. Empty is no limit."
              trailing={<Unit>%</Unit>}
              error={errors.cpu}
            >
              <Input
                id="limit-cpu"
                type="number"
                min={5}
                className="numeric"
                placeholder="No limit"
                value={draft.cpu}
                disabled={readOnly}
                onChange={(e) => set({ cpu: e.target.value })}
              />
            </Field>
            <Field
              label="Memory"
              htmlFor="limit-memory"
              trailing={<Unit>GB</Unit>}
              error={errors.memory}
            >
              <Input
                id="limit-memory"
                type="number"
                min={0}
                step="0.5"
                className="numeric"
                placeholder="No limit"
                value={draft.memory}
                disabled={readOnly}
                onChange={(e) => set({ memory: e.target.value })}
              />
            </Field>
            <Field label="Processes" htmlFor="limit-tasks" error={errors.tasks}>
              <Input
                id="limit-tasks"
                type="number"
                min={16}
                className="numeric"
                placeholder="No limit"
                value={draft.tasks}
                disabled={readOnly}
                onChange={(e) => set({ tasks: e.target.value })}
              />
            </Field>
          </FieldRow>
        )}
        {offered && limits.quota && (
          <FieldRow>
            <Field
              label="Disk warning"
              htmlFor="limit-soft"
              info={`On ${limits.quota.mount}. It uses ${bytes(limits.quota.usedBytes)} now.`}
              trailing={<Unit>GB</Unit>}
              error={errors.soft}
            >
              <Input
                id="limit-soft"
                type="number"
                min={0}
                className="numeric"
                placeholder="No warning"
                value={draft.soft}
                disabled={readOnly}
                onChange={(e) => set({ soft: e.target.value })}
              />
            </Field>
            <Field
              label="Disk limit"
              htmlFor="limit-hard"
              info="Writes past this fail."
              trailing={<Unit>GB</Unit>}
              error={errors.hard}
            >
              <Input
                id="limit-hard"
                type="number"
                min={0}
                className="numeric"
                placeholder="No limit"
                value={draft.hard}
                disabled={readOnly}
                onChange={(e) => set({ hard: e.target.value })}
              />
            </Field>
          </FieldRow>
        )}
      </SettingSection>
    </SettingForm>
  )
}
