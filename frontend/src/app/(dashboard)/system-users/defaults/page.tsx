"use client"

import Link from "next/link"
import { useState } from "react"
import {
  ArrowLeft,
  ArrowRight,
  FileText,
  FolderOpen,
  SettingsSliders,
  Trash,
} from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import { Segments } from "@/components/deploy/settings/segments"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { Field, FieldRow, FormSections } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { platformProduct } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import {
  boxToDays,
  countChanged,
  daysToBox,
  validDays,
  validWhole,
} from "@/components/system-users/fields"
import { ShellGlyph } from "@/components/system-users/marks"
import { AdminOnly, ReadFailed } from "@/components/system-users/read-state"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { useMetrics } from "@/hooks/use-metrics"
import { usePoll } from "@/hooks/use-poll"
import { get, put } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { AccountDefaults, AccountTemplate } from "@/lib/types"

/** What the form holds before the host has answered: its shape, with nothing in it. */
const WAITING: AccountDefaults = {
  passwordMaxDays: -1,
  passwordMinDays: 0,
  passwordWarnDays: 0,
  shell: "",
  inactiveDays: -1,
  quality: { available: true, minLength: 8, minClasses: 0 },
  skel: [],
  shells: [],
}

const CLASSES = ["0", "1", "2", "3", "4"] as const

/**
 * The rules an account is born under, as one column read top to bottom. The
 * host keeps them in three files with three formats and a directory, and each
 * section's head names the file it writes, since that is the file somebody will
 * open when a rule surprises them. Only the keys below are touched: every other
 * line and comment stays as it was.
 *
 * It was two columns of boxes with a sentence under each and a Save of its own
 * until 0.8.7. A page that is a form is drawn as the deployment settings are:
 * a head saying what the section is, fields under it, and the one bar that
 * comes up when something is changed.
 */
export default function AccountDefaultsPage() {
  return (
    <AdminOnly title="Account defaults">
      <Defaults />
    </AdminOnly>
  )
}

function Defaults() {
  const { host } = useMetrics()
  const { data, error, loading, refresh } = usePoll(
    (signal) => get<AccountDefaults>("/system-accounts/defaults", undefined, signal),
    0,
    [],
  )
  const waiting = !data && loading
  return (
    <Page className="animate-rise">
      <PageContext
        eyebrow={
          <Link
            href="/system-users"
            className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
          >
            <ArrowLeft className="size-3" /> System users
          </Link>
        }
        title="Account defaults"
      />
      <SaveBarProvider>
        <div className="mx-auto w-full max-w-3xl min-w-0 space-y-8 pb-20">
          <HostIdentity
            mark={platformProduct(host?.platform)}
            fallback={SettingsSliders}
            title="Account defaults"
            state={
              data ? (
                data.passwordMaxDays < 0 ? (
                  <Status tone="notice" label="passwords never expire" />
                ) : (
                  <Status
                    tone="running"
                    label={`passwords last ${plural(data.passwordMaxDays, "day")}`}
                  />
                )
              ) : null
            }
            facts={
              data ? (
                <>
                  {/* Held to the line's height: a mono word in a sans line
                      makes the line a pixel taller than it is while waiting. */}
                  <span className="inline-flex h-4 min-w-0 items-center gap-1.5">
                    <ShellGlyph shell={data.shell} />
                    <span className="truncate font-mono leading-4">{data.shell}</span>
                  </span>
                  {data.quality.available && (
                    <>
                      <FactDot />
                      <span className="numeric">
                        at least {plural(data.quality.minLength, "character")}
                      </span>
                    </>
                  )}
                  <FactDot />
                  <span className="numeric">
                    {data.skel.length === 0
                      ? "a new home starts empty"
                      : `${plural(data.skel.length, "file")} in a new home`}
                  </span>
                </>
              ) : error ? (
                <span>Not read</span>
              ) : (
                <TextShimmer>Reading the defaults…</TextShimmer>
              )
            }
          />
          {error && !data && <ReadFailed error={error} onRetry={refresh} />}
          {(data || waiting) && (
            <DefaultsForm
              key={data ? JSON.stringify(data) : "waiting"}
              defaults={data ?? WAITING}
              waiting={!data}
              onSaved={refresh}
            />
          )}
          <Templates />
        </div>
      </SaveBarProvider>
    </Page>
  )
}

/** A file a section writes, as a mono fact with a mark before it. */
function FileFact({ path }: { path: string }) {
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5">
      <FileText aria-hidden className="size-3.5 shrink-0" />
      <span className="truncate font-mono">{path}</span>
    </span>
  )
}

/** The unit a box counts in, at the end of its label's line. */
function Unit({ children }: { children: React.ReactNode }) {
  return <span className="text-hint text-muted-foreground">{children}</span>
}

function DefaultsForm({
  defaults,
  waiting,
  onSaved,
}: {
  defaults: AccountDefaults
  /** The host has not answered yet: the form is drawn, and nothing in it can be changed. */
  waiting: boolean
  onSaved: () => void
}) {
  const initial = {
    shell: defaults.shell,
    maxDays: daysToBox(defaults.passwordMaxDays),
    minDays: daysToBox(defaults.passwordMinDays),
    warnDays: daysToBox(defaults.passwordWarnDays),
    inactive: daysToBox(defaults.inactiveDays),
    minLength: String(defaults.quality.minLength),
    minClasses: String(defaults.quality.minClasses),
  }
  const [draft, setDraft] = useState(initial)
  const [busy, setBusy] = useState(false)
  const set = (patch: Partial<typeof initial>) => setDraft((now) => ({ ...now, ...patch }))
  const shells = Array.from(new Set([...defaults.shells, defaults.shell].filter(Boolean)))
  const quality = defaults.quality.available

  const accounts = countChanged([
    [draft.shell, initial.shell],
    [draft.inactive, initial.inactive],
  ])
  const passwords = countChanged([
    [draft.maxDays, initial.maxDays],
    [draft.minDays, initial.minDays],
    [draft.warnDays, initial.warnDays],
    [draft.minLength, initial.minLength],
    [draft.minClasses, initial.minClasses],
  ])
  const errors = {
    inactive: validDays(draft.inactive) ? undefined : "A whole number of days, or empty.",
    maxDays: validDays(draft.maxDays, 1) ? undefined : "A whole number of days from 1, or empty.",
    minDays: validDays(draft.minDays) ? undefined : "A whole number of days, or empty.",
    warnDays: validDays(draft.warnDays) ? undefined : "A whole number of days, or empty.",
    minLength:
      !quality || validWhole(draft.minLength, 6, 128) ? undefined : "Between 6 and 128 characters.",
  }
  const invalid = Object.values(errors).some(Boolean)

  const save = async () => {
    setBusy(true)
    try {
      await put("/system-accounts/defaults", {
        ...(draft.shell ? { shell: draft.shell } : {}),
        passwordMaxDays: boxToDays(draft.maxDays),
        passwordMinDays: Math.max(boxToDays(draft.minDays), 0),
        passwordWarnDays: Math.max(boxToDays(draft.warnDays), 0),
        inactiveDays: boxToDays(draft.inactive),
        ...(quality
          ? { minLength: Number(draft.minLength), minClasses: Number(draft.minClasses) }
          : {}),
      })
      notify.success("Defaults saved")
      onSaved()
      return true
    } catch (err) {
      notify.error("The defaults were not saved", err)
      return false
    } finally {
      setBusy(false)
    }
  }

  return (
    <SettingForm
      name="Account defaults"
      onSave={save}
      dirty={accounts + passwords > 0}
      changes={accounts + passwords}
      saving={busy}
      invalid={invalid}
      canEdit={!waiting}
      onDiscard={() => setDraft(initial)}
      applies="immediately"
    >
      <SettingSection
        title="New accounts"
        state={<FileFact path="/etc/default/useradd" />}
        status={settingStatus({ dirty: accounts > 0 })}
      >
        <FieldRow>
          <Field label="Shell" htmlFor="defaults-shell">
            <Select
              value={draft.shell}
              onValueChange={(shell) => set({ shell })}
              disabled={waiting}
            >
              <SelectTrigger id="defaults-shell" className="w-full font-mono">
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
          <Field
            label="Grace after a password expires"
            htmlFor="defaults-inactive"
            info="How long an account can still sign in to change an expired password. Empty is no limit."
            trailing={<Unit>days</Unit>}
            error={errors.inactive}
          >
            <Input
              id="defaults-inactive"
              type="number"
              min={0}
              className="numeric"
              value={draft.inactive}
              disabled={waiting}
              onChange={(e) => set({ inactive: e.target.value })}
            />
          </Field>
        </FieldRow>
        <div
          data-slot="skel"
          className="flex min-w-0 flex-wrap items-center justify-between gap-x-6 gap-y-1 border-t border-hairline pt-4"
        >
          <div className="min-w-0 space-y-1">
            <p className="text-body font-medium">Every new home starts with</p>
            <p className="min-h-4 font-mono text-hint break-words text-muted-foreground">
              {waiting
                ? " "
                : defaults.skel.length > 0
                  ? defaults.skel.map((file) => file.name).join("  ")
                  : "Nothing"}
            </p>
          </div>
          <Link
            href="/files?path=%2Fetc%2Fskel"
            className="inline-flex shrink-0 items-center gap-1 rounded-sm text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
          >
            <FolderOpen className="size-3" />
            <span className="font-mono">/etc/skel</span>
            <ArrowRight className="size-3" />
          </Link>
        </div>
      </SettingSection>

      <SettingSection
        title="Passwords"
        state={
          <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
            <FileFact path="/etc/login.defs" />
            {quality && <FileFact path="/etc/security/pwquality.conf" />}
          </span>
        }
        status={settingStatus({ dirty: passwords > 0 })}
      >
        <FieldRow columns={3}>
          <Field
            label="Last for"
            htmlFor="defaults-max"
            info="How long a password works before it has to be changed. Empty is forever."
            trailing={<Unit>days</Unit>}
            error={errors.maxDays}
          >
            <Input
              id="defaults-max"
              type="number"
              min={1}
              className="numeric"
              placeholder="Forever"
              value={draft.maxDays}
              disabled={waiting}
              onChange={(e) => set({ maxDays: e.target.value })}
            />
          </Field>
          <Field
            label="Keep at least"
            htmlFor="defaults-min"
            info="How long before a new password can be changed again."
            trailing={<Unit>days</Unit>}
            error={errors.minDays}
          >
            <Input
              id="defaults-min"
              type="number"
              min={0}
              className="numeric"
              value={draft.minDays}
              disabled={waiting}
              onChange={(e) => set({ minDays: e.target.value })}
            />
          </Field>
          <Field
            label="Warn before expiry"
            htmlFor="defaults-warn"
            trailing={<Unit>days</Unit>}
            error={errors.warnDays}
          >
            <Input
              id="defaults-warn"
              type="number"
              min={0}
              className="numeric"
              value={draft.warnDays}
              disabled={waiting}
              onChange={(e) => set({ warnDays: e.target.value })}
            />
          </Field>
        </FieldRow>
        {quality && (
          <FieldRow>
            <Field
              label="Shortest allowed"
              htmlFor="defaults-length"
              trailing={<Unit>characters</Unit>}
              error={errors.minLength}
            >
              <Input
                id="defaults-length"
                type="number"
                min={6}
                max={128}
                className="numeric"
                value={draft.minLength}
                disabled={waiting}
                onChange={(e) => set({ minLength: e.target.value })}
              />
            </Field>
            <Field
              label="Kinds of character"
              info="Lower case, upper case, digits and everything else: how many of the four a password has to use."
            >
              <Segments
                label="Kinds of character a password has to use"
                value={draft.minClasses}
                onChange={(minClasses) => set({ minClasses })}
                disabled={waiting}
                fill
                options={CLASSES.map((value) => ({
                  value,
                  label: value === "0" ? "Any" : value,
                }))}
              />
            </Field>
          </FieldRow>
        )}
      </SettingSection>
    </SettingForm>
  )
}

/**
 * The operator's own templates, each on one line with the one thing that can be
 * done to it here. They are made on the New account page, and built-in ones are
 * not listed: there is nothing to do to them.
 */
function Templates() {
  const { confirm, dialog } = useConfirm()
  const { data, refresh } = usePoll(
    (signal) => get<AccountTemplate[]>("/system-accounts/templates", undefined, signal),
    0,
    [],
  )
  const own = (data ?? []).filter((t) => !t.builtin)
  if (own.length === 0) return null
  const remove = (template: AccountTemplate) =>
    confirm({
      title: `Remove the template ${template.name}`,
      confirmLabel: "Remove",
      description: <p>Accounts already made from it keep what they have.</p>,
      action: async () => {
        await put(
          "/system-accounts/templates",
          own.filter((t) => t.id !== template.id),
        )
        refresh()
      },
    })
  return (
    <FormSections className="border-t border-hairline pt-8">
      <SettingSection
        title="Your templates"
        state={<span className="numeric">{plural(own.length, "template")}</span>}
      >
        <ul data-slot="templates" className="divide-y divide-hairline">
          {own.map((template) => (
            <li key={template.id} className="flex h-11 min-w-0 items-center gap-3 text-body">
              <span className="min-w-0 flex-1 truncate font-medium">{template.name}</span>
              <span className="min-w-0 truncate text-hint text-muted-foreground">
                {template.groups.join(", ") || "No extra groups"} ·{" "}
                <span className="font-mono">{template.shell}</span>
              </span>
              <IconAction label={`Remove ${template.name}`} onClick={() => remove(template)}>
                <Trash />
              </IconAction>
            </li>
          ))}
        </ul>
      </SettingSection>
      {dialog}
    </FormSections>
  )
}
