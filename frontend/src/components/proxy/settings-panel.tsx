"use client"

import { useEffect, useState } from "react"
import { Warning } from "@/components/icons"
import { ApiError, errorMessage, get, post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { plural } from "@/lib/format"
import type { ProxySetting, ProxySettingEdit, ProxySettings, ProxyValidation } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Field } from "@/components/form"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status, type Verdict } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Segments } from "@/components/deploy/settings/segments"
import { relativePath } from "@/components/proxy/config-tree"
import { refusalOf } from "@/components/proxy/engine-lifecycle"

const LEVEL: Record<ProxySetting["level"], { verdict: Verdict; label: string }> = {
  ok: { verdict: "ok", label: "Fine" },
  notice: { verdict: "notice", label: "Consider" },
  warning: { verdict: "warning", label: "Change" },
}

/**
 * nginx's server-wide directives as nginx loads them: each value, the file and
 * line that set it or that it is nginx's default, and what to make of it.
 * Change writes into the file that sets the directive — or the dashboard's own
 * conf.d file when none does — through the config editor's tested save, after
 * showing the line it writes.
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
  const [editing, setEditing] = useState<{ setting: ProxySetting; value: string }>()

  if (settings.error) return <ErrorState error={settings.error} onRetry={settings.refresh} />
  const report = settings.data
  return (
    <Panel plain>
      <PanelHeader
        title="Server-wide settings"
        actions={
          <Button size="sm" variant="outline" onClick={settings.refresh} pending={settings.loading}>
            Read again
          </Button>
        }
      />
      <PanelBody>
        {!report ? (
          <LoadingRows rows={7} />
        ) : (
          <ul className="min-w-0 divide-y divide-hairline" aria-label="Server-wide settings">
            {report.settings.map((setting) => (
              <SettingRow
                key={setting.name}
                setting={setting}
                report={report}
                root={root}
                onOpen={onOpen}
                onChange={(value) => setEditing({ setting, value })}
              />
            ))}
          </ul>
        )}
      </PanelBody>
      {editing && report && (
        <SettingDialog
          key={editing.setting.name}
          setting={editing.setting}
          initial={editing.value}
          root={root}
          onOpenChange={(open) => !open && setEditing(undefined)}
          onApplied={() => {
            setEditing(undefined)
            settings.refresh()
            onChanged()
          }}
        />
      )}
    </Panel>
  )
}

function SettingRow({
  setting,
  report,
  root,
  onOpen,
  onChange,
}: {
  setting: ProxySetting
  report: ProxySettings
  root: string
  onOpen: (path: string, line?: number) => void
  onChange: (value: string) => void
}) {
  const level = LEVEL[setting.level]
  const file = setting.file
  return (
    <li className="flex min-w-0 flex-col gap-2 py-3 sm:flex-row sm:items-start sm:gap-4">
      <div className="min-w-0 space-y-1 sm:w-56 sm:shrink-0">
        <p className="truncate font-mono text-body font-medium">{setting.name}</p>
        <Status verdict={level.verdict} label={level.label} />
      </div>
      <div className="min-w-0 flex-1 space-y-1">
        <p className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
          <span className="font-mono text-body break-all">{setting.value}</span>
          {!setting.set && <Tag>nginx default</Tag>}
          {file ? (
            <button
              type="button"
              onClick={() => onOpen(file, setting.line)}
              className="rounded-sm font-mono text-hint text-muted-foreground underline-offset-2 focus-ring hover:text-foreground hover:underline"
            >
              {relativePath(file, root)}:{setting.line}
            </button>
          ) : (
            <span className="text-hint text-muted-foreground">
              {setting.context === "http"
                ? `set in no file; Change adds it to ${relativePath(report.target, root)}`
                : "set in no file"}
            </span>
          )}
        </p>
        <p className="text-hint leading-relaxed text-muted-foreground">
          {setting.advice}
          {setting.name === "worker_connections" &&
            report.openFiles > 0 &&
            ` Open files per worker: ${report.openFiles}, from ${report.openFilesFrom}.`}
          {setting.overrides > 0 &&
            ` ${plural(setting.overrides, "block")} inside http ${setting.overrides === 1 ? "sets" : "set"} it again, where this value does not apply.`}
        </p>
      </div>
      <div className="flex shrink-0 gap-2">
        {setting.recommended && (
          <Button size="sm" variant="outline" onClick={() => onChange(setting.recommended ?? "")}>
            Use {setting.recommended}
          </Button>
        )}
        <Button
          size="sm"
          variant="outline"
          onClick={() => onChange(setting.set ? setting.value : "")}
        >
          Change
        </Button>
      </div>
    </li>
  )
}

type Preview = { value?: string; edits?: ProxySettingEdit[]; error?: string }

/**
 * One directive's new value, the line it writes and where, and Apply. The
 * preview is the server's own edit, so what is shown is what is written.
 */
function SettingDialog({
  setting,
  initial,
  root,
  onOpenChange,
  onApplied,
}: {
  setting: ProxySetting
  initial: string
  root: string
  onOpenChange: (open: boolean) => void
  onApplied: () => void
}) {
  const [value, setValue] = useState(initial)
  const [preview, setPreview] = useState<Preview>({})
  const [busy, setBusy] = useState<"only" | "reload">()
  const [refused, setRefused] = useState<ProxyValidation>()
  const trimmed = value.trim().replace(/\s+/g, " ")
  const unchanged = setting.set && trimmed === setting.value

  useEffect(() => {
    if (trimmed === "" || unchanged) return
    const controller = new AbortController()
    const timer = window.setTimeout(() => {
      post<{ edits: ProxySettingEdit[] }>(
        "/proxy/settings/preview",
        { changes: [{ name: setting.name, value: trimmed }] },
        { signal: controller.signal },
      )
        .then((res) => setPreview({ value: trimmed, edits: res.edits }))
        .catch((err: unknown) => {
          if (!controller.signal.aborted) setPreview({ value: trimmed, error: errorMessage(err) })
        })
    }, 300)
    return () => {
      controller.abort()
      window.clearTimeout(timer)
    }
  }, [setting.name, trimmed, unchanged])

  // A preview answers the value it was asked for, never the one typed since.
  const shown: Preview = preview.value === trimmed && !unchanged ? preview : {}
  const ready = Boolean(shown.edits?.length) && busy === undefined

  const apply = async (reload: boolean) => {
    setBusy(reload ? "reload" : "only")
    setRefused(undefined)
    try {
      await put("/proxy/settings", {
        changes: [{ name: setting.name, value: trimmed }],
        reload,
      })
      notify.success(
        reload ? `${setting.name} applied and reloaded` : `${setting.name} written, not reloaded`,
      )
      onApplied()
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
      } else if (err instanceof ApiError && err.code === "reload_failed") {
        // Written and tested before the reload was tried, so it is on disk
        // even though nginx is not serving it yet.
        notify.warning(`${setting.name} written, not reloaded`, {
          description: errorMessage(err),
        })
        onApplied()
      } else {
        notify.error(`${setting.name} not changed`, err)
      }
    } finally {
      setBusy(undefined)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(next) => {
        if (busy) return
        onOpenChange(next)
      }}
      size="lg"
      title={`Change ${setting.name}`}
      description="Shows the line this writes, then writes it through nginx's own test."
      footer={
        <>
          <span className="mr-auto text-hint text-muted-foreground">
            nginx tests the file first; a file it refuses is left as it was.
          </span>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={busy !== undefined}
          >
            Cancel
          </Button>
          <Button
            variant="outline"
            onClick={() => void apply(false)}
            pending={busy === "only"}
            disabled={!ready}
          >
            {busy === "only" ? "Writing…" : "Write only"}
          </Button>
          <Button onClick={() => void apply(true)} pending={busy === "reload"} disabled={!ready}>
            {busy === "reload" ? "Applying…" : "Apply and reload"}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field
          label="Value"
          htmlFor="setting-value"
          hint={`nginx's default: ${setting.default}`}
          error={shown.error}
        >
          {setting.choices ? (
            <Segments
              id="setting-value"
              label={setting.name}
              value={value}
              options={setting.choices.map((c) => ({ value: c, label: c, mono: true }))}
              onChange={setValue}
            />
          ) : (
            <Input
              id="setting-value"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
            />
          )}
        </Field>
        {unchanged && (
          <p className="text-hint text-muted-foreground">This is the value nginx loads now.</p>
        )}
        {shown.edits?.map((edit) => (
          <EditPreview key={`${edit.file}:${edit.line}`} edit={edit} root={root} />
        ))}
        {refused && (
          <div className="space-y-2">
            <Notice tone="danger" icon={Warning} title="Not written: the config test refuses it">
              {refused.note && <p>{refused.note}</p>}
            </Notice>
            {refused.output && (
              <Well className="max-h-40 break-words whitespace-pre-wrap">{refused.output}</Well>
            )}
          </div>
        )}
      </div>
    </Modal>
  )
}

/** The one line a change writes, as a diff against the line it replaces. */
function EditPreview({ edit, root }: { edit: ProxySettingEdit; root: string }) {
  const name = relativePath(edit.file, root)
  return (
    <div className="space-y-2">
      <p className="text-body">
        {edit.created ? "Creates " : edit.before ? "Changes " : "Adds to "}
        <span className="font-mono">{name}</span>
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
