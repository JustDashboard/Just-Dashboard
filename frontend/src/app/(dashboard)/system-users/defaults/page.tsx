"use client"

import Link from "next/link"
import { useState } from "react"
import { ArrowLeft, ArrowRight, FolderOpen, Trash } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { Field, FieldRow } from "@/components/form"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ErrorState, LoadingPanel } from "@/components/state"
import { ShellGlyph } from "@/components/system-users/marks"
import { Button } from "@/components/ui/button"
import { IconAction } from "@/components/icon-action"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { usePoll } from "@/hooks/use-poll"
import { get, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { AccountDefaults, AccountTemplate } from "@/lib/types"

const days = (value: number) => (value < 0 ? "" : String(value))
const fromBox = (value: string) => (value.trim() === "" ? -1 : Number(value))

/**
 * The rules an account is born under. The host keeps them in three files with
 * three formats and a directory, and each field here names which it writes, since
 * that is the file somebody will open when it surprises them. Only the keys below
 * are touched: every other line and comment stays as it was.
 */
export default function AccountDefaultsPage() {
  const { data, error, loading, refresh } = usePoll(
    (signal) => get<AccountDefaults>("/system-accounts/defaults", undefined, signal),
    0,
    [],
  )
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
      {loading && !data && <LoadingPanel />}
      {error && !data && <ErrorState error={error} />}
      {data && <DefaultsForm key={JSON.stringify(data)} defaults={data} onSaved={refresh} />}
      <TemplatesPanel />
    </Page>
  )
}

function DefaultsForm({ defaults, onSaved }: { defaults: AccountDefaults; onSaved: () => void }) {
  const [shell, setShell] = useState(defaults.shell)
  const [maxDays, setMaxDays] = useState(days(defaults.passwordMaxDays))
  const [minDays, setMinDays] = useState(days(defaults.passwordMinDays))
  const [warnDays, setWarnDays] = useState(days(defaults.passwordWarnDays))
  const [inactive, setInactive] = useState(days(defaults.inactiveDays))
  const [minLength, setMinLength] = useState(String(defaults.quality.minLength))
  const [minClasses, setMinClasses] = useState(String(defaults.quality.minClasses))
  const [busy, setBusy] = useState(false)
  const shells = Array.from(new Set([...defaults.shells, defaults.shell].filter(Boolean)))

  const save = async () => {
    setBusy(true)
    try {
      await put("/system-accounts/defaults", {
        ...(shell ? { shell } : {}),
        passwordMaxDays: fromBox(maxDays),
        passwordMinDays: Math.max(fromBox(minDays), 0),
        passwordWarnDays: Math.max(fromBox(warnDays), 0),
        inactiveDays: fromBox(inactive),
        ...(defaults.quality.available
          ? { minLength: Number(minLength), minClasses: Number(minClasses) }
          : {}),
      })
      notify.success("Defaults saved")
      onSaved()
    } catch (err) {
      notify.error("The defaults were not saved", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <div className="grid min-w-0 gap-x-10 gap-y-8 xl:grid-cols-2">
        <Panel plain aria-label="New accounts">
          <PanelHeader title="New accounts" />
          <PanelBody className="grid gap-4 pt-3">
            <FieldRow>
              <Field label="Shell" htmlFor="defaults-shell" hint="/etc/default/useradd">
                <Select value={shell} onValueChange={setShell}>
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
                hint="Days. Empty is no limit."
              >
                <Input
                  id="defaults-inactive"
                  type="number"
                  min={0}
                  value={inactive}
                  onChange={(e) => setInactive(e.target.value)}
                />
              </Field>
            </FieldRow>
            <div className="min-w-0">
              <p className="flex items-center justify-between gap-2 pb-1 text-body font-medium">
                Every new home starts with
                <Link
                  href="/files?path=%2Fetc%2Fskel"
                  className="inline-flex items-center gap-1 rounded-sm text-hint text-muted-foreground focus-ring hover:text-foreground"
                >
                  <FolderOpen className="size-3" />
                  /etc/skel
                  <ArrowRight className="size-3" />
                </Link>
              </p>
              <p className="font-mono text-hint text-muted-foreground">
                {defaults.skel.length > 0
                  ? defaults.skel.map((file) => file.name).join("  ")
                  : "Nothing"}
              </p>
            </div>
          </PanelBody>
        </Panel>
        <Panel plain aria-label="Passwords">
          <PanelHeader title="Passwords" />
          <PanelBody className="grid gap-4 pt-3">
            <div className="grid gap-3 sm:grid-cols-3">
              <Field
                label="Last for"
                htmlFor="defaults-max"
                hint="Days. Empty is forever. /etc/login.defs"
              >
                <Input
                  id="defaults-max"
                  type="number"
                  min={1}
                  value={maxDays}
                  onChange={(e) => setMaxDays(e.target.value)}
                />
              </Field>
              <Field label="Keep at least" htmlFor="defaults-min" hint="Days">
                <Input
                  id="defaults-min"
                  type="number"
                  min={0}
                  value={minDays}
                  onChange={(e) => setMinDays(e.target.value)}
                />
              </Field>
              <Field label="Warn before expiry" htmlFor="defaults-warn" hint="Days">
                <Input
                  id="defaults-warn"
                  type="number"
                  min={0}
                  value={warnDays}
                  onChange={(e) => setWarnDays(e.target.value)}
                />
              </Field>
            </div>
            {defaults.quality.available && (
              <FieldRow>
                <Field
                  label="Shortest allowed"
                  htmlFor="defaults-length"
                  hint="Characters. /etc/security/pwquality.conf"
                >
                  <Input
                    id="defaults-length"
                    type="number"
                    min={6}
                    value={minLength}
                    onChange={(e) => setMinLength(e.target.value)}
                  />
                </Field>
                <Field
                  label="Kinds of character"
                  htmlFor="defaults-classes"
                  hint="Lower, upper, digit, other. 0 to 4."
                >
                  <Input
                    id="defaults-classes"
                    type="number"
                    min={0}
                    max={4}
                    value={minClasses}
                    onChange={(e) => setMinClasses(e.target.value)}
                  />
                </Field>
              </FieldRow>
            )}
          </PanelBody>
        </Panel>
      </div>
      <div>
        <Button size="sm" onClick={() => void save()} pending={busy}>
          Save defaults
        </Button>
      </div>
    </div>
  )
}

function TemplatesPanel() {
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
    <Panel plain aria-label="Your templates">
      <PanelHeader title="Your templates" />
      <PanelBody flush>
        <ul className="divide-y divide-hairline">
          {own.map((template) => (
            <li key={template.id} className="flex min-w-0 items-center gap-3 py-2 text-body">
              <span className="min-w-0 flex-1 truncate">{template.name}</span>
              <span className="min-w-0 truncate text-hint text-muted-foreground">
                {template.groups.join(", ") || "No extra groups"} · {template.shell}
              </span>
              <IconAction label={`Remove ${template.name}`} onClick={() => remove(template)}>
                <Trash />
              </IconAction>
            </li>
          ))}
        </ul>
      </PanelBody>
      {dialog}
    </Panel>
  )
}
