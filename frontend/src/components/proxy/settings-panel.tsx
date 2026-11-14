"use client"

import { useEffect, useMemo, useState } from "react"
import { RefreshClockwise, Warning } from "@/components/icons"
import { ApiError, errorMessage, get, post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { plural } from "@/lib/format"
import type { ProxySetting, ProxySettingEdit, ProxySettings, ProxyValidation } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Field, OptionList, OptionRow } from "@/components/form"
import { Well } from "@/components/panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Segments } from "@/components/deploy/settings/segments"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { relativePath } from "@/components/proxy/config-tree"
import { refusalOf } from "@/components/proxy/engine-lifecycle"
import { RefusedConfig } from "@/components/proxy/config-search"

/** The directives by what they decide, in the order the page reads them. */
const SECTIONS: { title: string; names: string[] }[] = [
  { title: "Responses", names: ["server_tokens", "gzip"] },
  {
    title: "Requests",
    names: ["client_max_body_size", "keepalive_timeout", "server_names_hash_bucket_size"],
  },
  { title: "TLS", names: ["ssl_protocols"] },
  { title: "Workers", names: ["worker_connections"] },
]

/** What a field starts at: the value nginx loads, or empty for a default no file sets. */
function initialOf(setting: ProxySetting): string {
  return setting.set || setting.choices ? setting.value : ""
}

function normal(value: string) {
  return value.trim().replace(/\s+/g, " ")
}

type Preview = { value: string; edits?: ProxySettingEdit[]; error?: string }

/**
 * nginx's server-wide directives as one settings column, the shape a
 * project's settings take: a section per thing they decide, each directive a
 * field with the file and line that sets it at its right — a press opens the
 * file there — and one save bar for the page. Every edited directive shows
 * the line it writes and where as it is typed (the server's own preview), and
 * Save writes them all through nginx's test, reloading after unless that is
 * switched off. A value nginx refuses leaves every file as it was.
 *
 * It was a list with a Change button per row and a dialog per change.
 */
export function ConfigSettingsView({
  root,
  onOpen,
  onChanged,
}: {
  root: string
  onOpen: (path: string, line?: number) => void
  onChanged: () => void
}) {
  const settings = usePoll((signal) => get<ProxySettings>("/proxy/settings", undefined, signal), 0)
  const refusal = refusalOf(settings.error)
  const report = settings.error ? undefined : settings.data

  return (
    <SaveBarProvider>
      <div className="mx-auto w-full max-w-3xl min-w-0 space-y-8 pb-20">
        {refusal ? (
          <RefusedConfig
            refusal={refusal}
            root={root}
            onOpen={onOpen}
            what="With nothing it would load, its settings cannot be read."
          />
        ) : settings.error ? (
          <ErrorState error={settings.error} onRetry={settings.refresh} />
        ) : !report ? (
          <LoadingRows rows={7} />
        ) : (
          <SettingsForm
            key={report.settings.map((s) => `${s.name}=${s.value}@${s.file}:${s.line}`).join("|")}
            report={report}
            root={root}
            reading={settings.loading}
            onReadAgain={settings.refresh}
            onOpen={onOpen}
            onSaved={() => {
              settings.refresh()
              onChanged()
            }}
          />
        )}
      </div>
    </SaveBarProvider>
  )
}

function SettingsForm({
  report,
  root,
  reading,
  onReadAgain,
  onOpen,
  onSaved,
}: {
  report: ProxySettings
  root: string
  reading: boolean
  onReadAgain: () => void
  onOpen: (path: string, line?: number) => void
  onSaved: () => void
}) {
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const [reload, setReload] = useState(true)
  const [saving, setSaving] = useState(false)
  const [refused, setRefused] = useState<ProxyValidation>()
  const [previews, setPreviews] = useState<Record<string, Preview>>({})

  const byName = useMemo(
    () => new Map(report.settings.map((setting) => [setting.name, setting])),
    [report.settings],
  )
  const changes = report.settings
    .filter((setting) => {
      const draft = drafts[setting.name]
      return draft !== undefined && normal(draft) !== normal(initialOf(setting))
    })
    .map((setting) => ({ name: setting.name, value: normal(drafts[setting.name]) }))
  const empty = changes.filter((change) => change.value === "").map((change) => change.name)
  const refusedPreview = changes.some((change) => {
    const preview = previews[change.name]
    return preview?.value === change.value && preview.error
  })
  const changeKey = JSON.stringify(changes)

  // Each changed directive's line, previewed on its own so a refusal lands
  // under the field it is about. A preview answers the value it was asked
  // for, never one typed since.
  useEffect(() => {
    const wanted: { name: string; value: string }[] = JSON.parse(changeKey)
    const controller = new AbortController()
    const timer = window.setTimeout(() => {
      for (const change of wanted) {
        if (change.value === "") continue
        post<{ edits: ProxySettingEdit[] }>(
          "/proxy/settings/preview",
          { changes: [change] },
          { signal: controller.signal },
        )
          .then((res) =>
            setPreviews((was) => ({
              ...was,
              [change.name]: { value: change.value, edits: res.edits },
            })),
          )
          .catch((err: unknown) => {
            if (controller.signal.aborted) return
            setPreviews((was) => ({
              ...was,
              [change.name]: { value: change.value, error: errorMessage(err) },
            }))
          })
      }
    }, 300)
    return () => {
      controller.abort()
      window.clearTimeout(timer)
    }
  }, [changeKey])

  const save = async () => {
    if (changes.length === 0 || empty.length > 0) return false
    setSaving(true)
    setRefused(undefined)
    try {
      await put("/proxy/settings", { changes, reload })
      const names = changes.map((change) => change.name).join(", ")
      notify.success(reload ? `${names} applied and reloaded` : `${names} written, not reloaded`)
      setDrafts({})
      onSaved()
      return true
    } catch (err) {
      const refusal = refusalOf(err)
      if (refusal) {
        setRefused(
          refusal.validation ?? {
            valid: false,
            output: refusal.message,
            command: "",
            diagnostics: [],
          },
        )
        return false
      }
      if (err instanceof ApiError && err.code === "reload_failed") {
        // Written and tested before the reload was tried, so it is on disk
        // even though nginx is not serving it yet.
        notify.warning("Written, not reloaded", { description: errorMessage(err) })
        onSaved()
        return true
      }
      notify.error("Not changed", err)
      return false
    } finally {
      setSaving(false)
    }
  }

  const known = new Set(SECTIONS.flatMap((section) => section.names))
  const others = report.settings.filter((setting) => !known.has(setting.name))
  const sections = [
    ...SECTIONS.map((section) => ({
      title: section.title,
      settings: section.names.flatMap((name) => byName.get(name) ?? []),
    })),
    ...(others.length > 0 ? [{ title: "Other", settings: others }] : []),
  ].filter((section) => section.settings.length > 0)

  const sectionState = (title: string) => {
    if (title === "Workers" && report.openFiles > 0) {
      return (
        <span>
          <span className="numeric">{report.openFiles.toLocaleString()}</span> open files per
          worker, from {report.openFilesFrom}
        </span>
      )
    }
    return undefined
  }

  return (
    <SettingForm
      name="nginx settings"
      canEdit
      dirty={changes.length > 0}
      changes={changes.length}
      saving={saving}
      invalid={empty.length > 0 || refusedPreview}
      applies={reload ? "immediately" : "next-reload"}
      onSave={save}
      onDiscard={() => {
        setDrafts({})
        setRefused(undefined)
      }}
      error={
        refused && (
          <div className="space-y-2">
            <p className="font-medium">Not written: nginx&apos;s test refuses it</p>
            {refused.note && <p>{refused.note}</p>}
            {refused.output && (
              <Well className="max-h-40 break-words whitespace-pre-wrap">{refused.output}</Well>
            )}
          </div>
        )
      }
    >
      <div className="flex min-w-0 items-center justify-between gap-3 pb-6 text-hint text-muted-foreground">
        <span>As nginx loads them</span>
        <Button size="xs" variant="ghost" onClick={onReadAgain} pending={reading}>
          <RefreshClockwise />
          {reading ? "Reading…" : "Read again"}
        </Button>
      </div>
      {sections.map((section) => {
        const dirty = section.settings.some((setting) =>
          changes.some((change) => change.name === setting.name),
        )
        return (
          <SettingSection
            key={section.title}
            title={section.title}
            state={sectionState(section.title)}
            status={settingStatus({ dirty, refused: dirty && refused !== undefined })}
          >
            <div className="space-y-6">
              {section.settings.map((setting) => {
                const draft = drafts[setting.name] ?? initialOf(setting)
                const value = normal(draft)
                const changed = changes.some((change) => change.name === setting.name)
                const preview = previews[setting.name]
                const shown = changed && preview?.value === value ? preview : undefined
                return (
                  <SettingField
                    key={setting.name}
                    setting={setting}
                    report={report}
                    root={root}
                    value={draft}
                    error={
                      changed && value === ""
                        ? `Give it a value; nginx's default is ${setting.default}.`
                        : shown?.error
                    }
                    edits={shown?.edits}
                    onChange={(next) => setDrafts((was) => ({ ...was, [setting.name]: next }))}
                    onOpen={onOpen}
                  />
                )
              })}
            </div>
          </SettingSection>
        )
      })}
      <SettingSection title="Applying">
        <OptionList>
          <OptionRow
            title="Reload nginx after saving"
            checked={reload}
            onCheckedChange={setReload}
          />
        </OptionList>
      </SettingSection>
    </SettingForm>
  )
}

function SettingField({
  setting,
  report,
  root,
  value,
  error,
  edits,
  onChange,
  onOpen,
}: {
  setting: ProxySetting
  report: ProxySettings
  root: string
  value: string
  error?: string
  edits?: ProxySettingEdit[]
  onChange: (value: string) => void
  onOpen: (path: string, line?: number) => void
}) {
  const id = `setting-${setting.name}`
  const file = setting.file
  const where = file ? (
    <button
      type="button"
      onClick={() => onOpen(file, setting.line)}
      aria-label={`Open ${relativePath(file, root)} at line ${setting.line}`}
      className="truncate rounded-sm font-mono text-hint text-muted-foreground underline-offset-2 focus-ring hover:text-foreground hover:underline"
    >
      {relativePath(file, root)}:{setting.line}
    </button>
  ) : (
    <Tag>nginx default</Tag>
  )
  const info = [
    `nginx's default: ${setting.default}.`,
    !file &&
      setting.context === "http" &&
      `Set here, it is added to ${relativePath(report.target, root)}.`,
  ]
    .filter(Boolean)
    .join(" ")
  return (
    <div className="min-w-0 space-y-3">
      <Field
        label={<span className="font-mono">{setting.name}</span>}
        htmlFor={id}
        info={info}
        trailing={where}
        error={error}
        hint={
          setting.overrides > 0
            ? `${plural(setting.overrides, "block")} inside http ${setting.overrides === 1 ? "sets" : "set"} it again, where this value does not apply.`
            : undefined
        }
      >
        {setting.choices ? (
          <Segments
            id={id}
            label={setting.name}
            value={value}
            options={setting.choices.map((choice) => ({
              value: choice,
              label: choice,
              mono: true,
            }))}
            onChange={onChange}
          />
        ) : (
          <Input
            id={id}
            value={value}
            placeholder={setting.set ? undefined : setting.value}
            onChange={(event) => onChange(event.target.value)}
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
          />
        )}
      </Field>
      {edits?.map((edit) => (
        <EditPreview key={`${edit.file}:${edit.line}`} edit={edit} root={root} />
      ))}
    </div>
  )
}

/** The one line a change writes, as a diff against the line it replaces. */
function EditPreview({ edit, root }: { edit: ProxySettingEdit; root: string }) {
  const name = relativePath(edit.file, root)
  return (
    <div className="min-w-0 animate-rise space-y-2">
      <p className="text-hint text-muted-foreground">
        {edit.created ? "Creates " : edit.before ? "Changes " : "Adds to "}
        <span className="font-mono text-foreground">{name}</span>
        {edit.created ? "" : `, line ${edit.line}`}
      </p>
      <Well className="font-mono text-xs break-all whitespace-pre-wrap">
        {edit.before &&
          edit.before.split("\n").map((line, i) => (
            <div key={`b${i}`} className="text-destructive">
              - {line}
            </div>
          ))}
        {edit.after.split("\n").map((line, i) => (
          <div key={`a${i}`} className="text-success">
            + {line}
          </div>
        ))}
      </Well>
      {edit.conffile && (
        <Notice tone="warning" icon={Warning} title={`${name} belongs to the nginx package`}>
          It is a dpkg conffile: when an nginx upgrade ships a new version of it, dpkg asks whether
          to keep this edit or take the package&apos;s.
        </Notice>
      )}
    </div>
  )
}
