"use client"

import { useRouter } from "next/navigation"
import { useState } from "react"
import { Disclosure, Field, FieldRow, FormNote, OptionRow } from "@/components/form"
import { accountHref } from "@/components/system-users/access"
import { useAccount } from "@/components/system-users/account-page"
import { ShellGlyph } from "@/components/system-users/marks"
import { Button } from "@/components/ui/button"
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
import { bytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { AccountLimits, SystemGroup, SystemUser } from "@/lib/types"

const NO_LOGIN = "/usr/sbin/nologin"

/**
 * The fields nobody needs twice a year, folded at the foot of the account so they
 * are there without being in the way. Each fold saves itself: a change of shell
 * has nothing to do with a memory limit, and one Save for both would make a typo
 * in one refuse the other.
 */
export function AccountSettings() {
  const { user } = useAccount()
  return (
    <section aria-label="Settings" className="flex min-w-0 flex-col gap-1">
      <Disclosure
        summary="Profile"
        facts={`${user.shell} · ${user.primaryGroup ?? `gid ${user.gid}`}`}
      >
        <ProfileForm />
      </Disclosure>
      <Disclosure summary="Password rules and expiry" facts={agingFacts(user)}>
        <AgingForm />
      </Disclosure>
      <Disclosure summary="Limits" facts="CPU, memory, processes and disk">
        <LimitsForm />
      </Disclosure>
    </section>
  )
}

function agingFacts(user: SystemUser) {
  const parts = [
    user.expiresOn && !user.disabled && `Account expires ${user.expiresOn}`,
    user.aging.maxDays !== undefined && `Password lasts ${user.aging.maxDays} days`,
  ].filter(Boolean)
  return parts.length > 0 ? parts.join(" · ") : "No expiry"
}

function ProfileForm() {
  const router = useRouter()
  const { user, path, query, refresh } = useAccount()
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
  const dirty =
    name !== user.username ||
    comment !== user.comment ||
    shell !== user.shell ||
    changedHome ||
    uid !== String(user.uid) ||
    primary !== (user.primaryGroup ?? "")

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
      if (updated.username !== user.username) router.replace(accountHref(updated.username))
      else refresh()
    } catch (err) {
      notify.error("That was not saved", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="grid max-w-3xl gap-4 pt-2">
      <FieldRow>
        <Field
          label="Username"
          htmlFor="profile-name"
          hint="Its private group and crontab move with it."
        >
          <Input
            id="profile-name"
            className="font-mono"
            value={name}
            disabled={user.protected}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field label="Full name" htmlFor="profile-comment">
          <Input
            id="profile-comment"
            value={comment}
            onChange={(e) => setComment(e.target.value)}
          />
        </Field>
      </FieldRow>
      <FieldRow>
        <Field label="Shell" htmlFor="profile-shell">
          <Select value={shell} onValueChange={setShell}>
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
          <Select value={primary} onValueChange={setPrimary} disabled={user.protected}>
            <SelectTrigger id="profile-primary" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {(groups ?? []).map((group) => (
                <SelectItem key={group.name} value={group.name}>
                  {group.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      </FieldRow>
      <FieldRow>
        <Field label="Home" htmlFor="profile-home">
          <Input
            id="profile-home"
            className="font-mono"
            value={home}
            disabled={user.protected}
            onChange={(e) => setHome(e.target.value)}
          />
        </Field>
        <Field
          label="Account number"
          htmlFor="profile-uid"
          hint="Files outside the home keep the old number."
        >
          <Input
            id="profile-uid"
            type="number"
            className="numeric"
            value={uid}
            disabled={user.protected}
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
      <div>
        <Button size="sm" onClick={() => void save()} disabled={!dirty} pending={busy}>
          Save profile
        </Button>
      </div>
    </div>
  )
}

function dayValue(value: number | undefined) {
  return value === undefined ? "" : String(value)
}

function AgingForm() {
  const { user, path, query, refresh } = useAccount()
  const [expires, setExpires] = useState(user.disabled ? "" : (user.expiresOn ?? ""))
  const [maxDays, setMaxDays] = useState(dayValue(user.aging.maxDays))
  const [minDays, setMinDays] = useState(dayValue(user.aging.minDays))
  const [warnDays, setWarnDays] = useState(dayValue(user.aging.warnDays))
  const [inactiveDays, setInactiveDays] = useState(dayValue(user.aging.inactiveDays))
  const [busy, setBusy] = useState(false)

  // chage's own spelling of "no rule" is -1; an empty box says the same thing.
  const days = (value: string) => (value.trim() === "" ? -1 : Number(value))
  const save = async () => {
    setBusy(true)
    try {
      await patch(
        path,
        {
          expiresOn: expires,
          aging: {
            maxDays: days(maxDays),
            minDays: Math.max(days(minDays), 0),
            warnDays: Math.max(days(warnDays), 0),
            inactiveDays: days(inactiveDays),
          },
        },
        { query },
      )
      notify.success("Saved")
      refresh()
    } catch (err) {
      notify.error("That was not saved", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="grid max-w-3xl gap-4 pt-2">
      <FieldRow>
        <Field
          label="Account expires"
          htmlFor="aging-expires"
          hint="Every way in closes on this date."
        >
          <Input
            id="aging-expires"
            type="date"
            value={expires}
            onChange={(e) => setExpires(e.target.value)}
          />
        </Field>
        <Field label="Password lasts" htmlFor="aging-max" hint="Days. Empty is forever.">
          <Input
            id="aging-max"
            type="number"
            min={1}
            value={maxDays}
            onChange={(e) => setMaxDays(e.target.value)}
          />
        </Field>
      </FieldRow>
      <FieldRow>
        <Field label="Warn before it expires" htmlFor="aging-warn" hint="Days.">
          <Input
            id="aging-warn"
            type="number"
            min={0}
            value={warnDays}
            onChange={(e) => setWarnDays(e.target.value)}
          />
        </Field>
        <Field label="Keep at least" htmlFor="aging-min" hint="Days before it can change again.">
          <Input
            id="aging-min"
            type="number"
            min={0}
            value={minDays}
            onChange={(e) => setMinDays(e.target.value)}
          />
        </Field>
      </FieldRow>
      <Field
        label="Grace after expiry"
        htmlFor="aging-inactive"
        hint="Days it can still sign in to change an expired password. Empty is no limit."
      >
        <Input
          id="aging-inactive"
          type="number"
          min={0}
          className="max-w-40"
          value={inactiveDays}
          onChange={(e) => setInactiveDays(e.target.value)}
        />
      </Field>
      <div>
        <Button size="sm" onClick={() => void save()} pending={busy}>
          Save rules
        </Button>
      </div>
    </div>
  )
}

const GB = 1024 ** 3

function LimitsForm() {
  const { path, query, user } = useAccount()
  const { data, refresh } = usePoll(
    (signal) => get<AccountLimits>(`${path}/limits`, undefined, signal),
    0,
    [path],
  )
  if (!data) return null
  return (
    <LimitsFields
      key={JSON.stringify(data)}
      limits={data}
      onSave={async (body) => {
        await put(`${path}/limits`, body, { query })
        refresh()
      }}
      protectedAccount={user.protected}
    />
  )
}

function LimitsFields({
  limits,
  onSave,
  protectedAccount,
}: {
  limits: AccountLimits
  onSave: (body: object) => Promise<void>
  protectedAccount: boolean
}) {
  const [cpu, setCpu] = useState(limits.cpuPercent ? String(limits.cpuPercent) : "")
  const [memory, setMemory] = useState(limits.memoryBytes ? String(limits.memoryBytes / GB) : "")
  const [tasks, setTasks] = useState(limits.tasks ? String(limits.tasks) : "")
  const [soft, setSoft] = useState(
    limits.quota?.softBytes ? String(limits.quota.softBytes / GB) : "",
  )
  const [hard, setHard] = useState(
    limits.quota?.hardBytes ? String(limits.quota.hardBytes / GB) : "",
  )
  const [busy, setBusy] = useState(false)

  const save = async () => {
    setBusy(true)
    try {
      await onSave({
        cpuPercent: Number(cpu) || 0,
        memoryBytes: Math.round((Number(memory) || 0) * GB),
        tasks: Number(tasks) || 0,
        ...(limits.quota
          ? {
              softBytes: Math.round((Number(soft) || 0) * GB),
              hardBytes: Math.round((Number(hard) || 0) * GB),
            }
          : {}),
      })
      notify.success("Limits saved")
    } catch (err) {
      notify.error("The limits were not saved", err)
    } finally {
      setBusy(false)
    }
  }
  if (protectedAccount)
    return <FormNote>The system depends on this account, so it keeps what it has.</FormNote>
  if (!limits.systemd && !limits.quota) {
    return (
      <FormNote>
        This host has neither systemd nor disk quotas, so nothing here can be limited.
      </FormNote>
    )
  }
  return (
    <div className="grid max-w-3xl gap-4 pt-2">
      {limits.systemd && (
        <FieldRow>
          <Field label="CPU" htmlFor="limit-cpu" hint="Percent of one core. 200 is two.">
            <Input
              id="limit-cpu"
              type="number"
              min={5}
              value={cpu}
              onChange={(e) => setCpu(e.target.value)}
            />
          </Field>
          <Field label="Memory" htmlFor="limit-memory" hint="GB. Empty is no limit.">
            <Input
              id="limit-memory"
              type="number"
              min={0}
              step="0.5"
              value={memory}
              onChange={(e) => setMemory(e.target.value)}
            />
          </Field>
          <Field label="Processes" htmlFor="limit-tasks">
            <Input
              id="limit-tasks"
              type="number"
              min={16}
              value={tasks}
              onChange={(e) => setTasks(e.target.value)}
            />
          </Field>
        </FieldRow>
      )}
      {limits.quota && (
        <FieldRow>
          <Field
            label="Disk warning"
            htmlFor="limit-soft"
            hint={`GB on ${limits.quota.mount}. Uses ${bytes(limits.quota.usedBytes)} now.`}
          >
            <Input
              id="limit-soft"
              type="number"
              min={0}
              value={soft}
              onChange={(e) => setSoft(e.target.value)}
            />
          </Field>
          <Field label="Disk limit" htmlFor="limit-hard" hint="GB. Writes past this fail.">
            <Input
              id="limit-hard"
              type="number"
              min={0}
              value={hard}
              onChange={(e) => setHard(e.target.value)}
            />
          </Field>
        </FieldRow>
      )}
      <div>
        <Button size="sm" onClick={() => void save()} pending={busy}>
          Save limits
        </Button>
      </div>
    </div>
  )
}
