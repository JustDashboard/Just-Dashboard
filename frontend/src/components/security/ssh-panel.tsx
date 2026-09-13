"use client"

import { useEffect, useMemo, useState } from "react"
import { Key, TerminalWindow, Warning } from "@/components/icons"
import { get, post } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { Job, SSHDConfig, SSHSetting } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { JobConsole, RecentJobs, useJobConsole } from "@/components/job-console"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

/**
 * The SSH server's own settings, which is where a single-server operator is
 * actually attacked.
 *
 * The firewall decides who may knock. sshd decides what happens next, and its
 * defaults are a compromise struck for compatibility across twenty years of
 * clients rather than for a machine on a public address. Three of these are
 * one line each in a file nobody opens, and the difference between them being
 * right and wrong is the difference between a bot wasting its time and a bot
 * getting in.
 *
 * Changes are staged and applied together: sshd is tested with its own parser
 * before the daemon is asked to reload, and the file is put back if the test
 * fails. The one refusal that is not about syntax is the important one —
 * turning off password authentication on a host where nobody has a key.
 */
export function SSHPanel() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const admin = can("system.admin")
  const { data, error, loading, refresh } = usePoll<SSHDConfig>(
    (signal) => get("/ssh/config", undefined, signal),
    0,
    [],
    { enabled: admin },
  )
  const [pending, setPending] = useState<Record<string, string>>({})
  const [only, setOnly] = useState<"all" | "attention">("all")
  const [busy, setBusy] = useState(false)
  const console_ = useJobConsole()

  // The effective configuration is only right once sshd has reloaded.
  const jobStatus = console_.job?.status
  useEffect(() => {
    if (jobStatus === "succeeded") refresh()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [jobStatus])

  const dirty = useMemo(() => Object.keys(pending).length > 0, [pending])

  if (!admin) {
    return (
      <EmptyState
        icon={TerminalWindow}
        title="SSH settings need the admin capability"
        description="They name the accounts that hold keys, which is a map of who can reach this machine."
      />
    )
  }
  if (loading) return <LoadingPanel />
  if (error) return <ErrorState error={error} />
  if (!data?.available) {
    return (
      <EmptyState
        icon={TerminalWindow}
        title="No SSH server on this host"
        description={data?.error ?? "Neither sshd nor its configuration was found."}
      />
    )
  }

  const valueOf = (setting: SSHSetting) => pending[setting.key] ?? setting.value
  const changed = (setting: SSHSetting) =>
    pending[setting.key] !== undefined && pending[setting.key] !== setting.value

  const noKeys = data.keyedAccounts.length === 0
  const insecure = data.settings.filter((s) => !s.secure).length
  const shown =
    only === "attention" ? data.settings.filter((s) => !s.secure || changed(s)) : data.settings

  const apply = () =>
    confirm({
      title: "Apply SSH changes",
      phrase: "change ssh",
      confirmLabel: "Test and apply",
      description: (
        <div className="space-y-2">
          <p>
            The new configuration is written to{" "}
            <code className="font-mono">{data.managedFile}</code>, tested with sshd&rsquo;s own
            parser and put back if the test fails. Existing sessions are not disconnected by a
            reload.
          </p>
          <p className="text-destructive">
            Keep this session open and confirm you can still log in from a second terminal before
            closing it.
          </p>
        </div>
      ),
      action: async (c) => {
        setBusy(true)
        try {
          // The plan — including every lockout guard — is checked before this
          // returns, so a refusal is this dialog's answer. What comes back is
          // a job for the write, the sshd -t and the reload, which is the part
          // worth watching: this is the one operation where "it said it
          // worked" is not the same as knowing the daemon came back.
          const job = await post<Job>("/ssh/config", { settings: pending }, { confirm: c })
          console_.attach(job)
          setPending({})
        } finally {
          setBusy(false)
        }
      },
    })

  return (
    <>
      <div className="flex min-w-0 flex-col gap-4">
        <JobConsole
          job={console_.job}
          lines={console_.lines}
          onDismiss={console_.dismiss}
          onCancel={console_.cancel}
        />

        {/* The one banner left standing. It is not background: it is the
            reason the control below it will refuse, so it belongs above the
            control rather than in a footnote. The socket and Match-block facts
            used to sit beside it as two more banners of the same weight, and
            three paragraphs of prose before the first setting is what made
            this page read as a warning label rather than a configuration. */}
        {noKeys && (
          <Notice tone="warning" icon={Key} title="No account on this host has an SSH key">
            Password authentication cannot safely be turned off until one does — with no key
            anywhere, doing so would leave nobody a way in, and the server refuses the change for
            that reason. Add a key from the Users page first.
          </Notice>
        )}

        <Panel>
          <PanelHeader
            title="SSH server"
            advanced
            actions={
              <>
                {data.socket?.unit && <Tag mono>{data.socket.unit}</Tag>}
                {data.hasMatchBlocks && (
                  <Tag
                    tone="warning"
                    icon={Warning}
                    title="Some values are overridden for particular users or addresses. What is shown here is the unconditional configuration; the conditional parts are not editable from this page."
                  >
                    match blocks
                  </Tag>
                )}
                <RecentJobs kinds={["ssh."]} onOpen={console_.open} />
                <Status
                  verdict={insecure === 0 ? "ok" : "warning"}
                  label={insecure === 0 ? "Hardened" : `${insecure} below recommendation`}
                />
              </>
            }
          />
          <PanelToolbar>
            <ToggleGroup
              type="single"
              value={only}
              onValueChange={(next) => next && setOnly(next as "all" | "attention")}
              variant="outline"
              size="sm"
              aria-label="Which settings to show"
            >
              <ToggleGroupItem value="all" className="px-2.5 text-hint">
                All {data.settings.length}
              </ToggleGroupItem>
              <ToggleGroupItem value="attention" className="px-2.5 text-hint">
                Below recommendation {insecure}
              </ToggleGroupItem>
            </ToggleGroup>
            <span className="flex-1" />
            <span className="text-hint text-muted-foreground">
              Changes are staged here and applied together.
            </span>
          </PanelToolbar>
          <PanelBody flush>
            <div className="divide-y divide-hairline">
              {shown.map((setting) => (
                <SettingRow
                  key={setting.key}
                  setting={setting}
                  value={valueOf(setting)}
                  changed={changed(setting)}
                  note={
                    setting.key === "Port" && data.socket?.unit
                      ? `${data.socket.unit} holds this listener, so sshd never binds a port of its own. Changing it here writes the directive and a drop-in for the socket, then restarts it — which is the half that moves where connections land.`
                      : undefined
                  }
                  onChange={(v) => setPending((p) => ({ ...p, [setting.key]: v }))}
                />
              ))}
              {shown.length === 0 && (
                <EmptyNote>Every setting is at or above its recommendation.</EmptyNote>
              )}
            </div>
          </PanelBody>
          {dirty && (
            <PanelFooter>
              <span className="text-xs text-muted-foreground">
                {Object.keys(pending).length} pending — written to {data.managedFile}
              </span>
              <span className="flex-1" />
              <Button size="sm" variant="outline" onClick={() => setPending({})} disabled={busy}>
                Discard
              </Button>
              <Button size="sm" onClick={apply} disabled={busy}>
                Test and apply
              </Button>
            </PanelFooter>
          )}
        </Panel>

        <Panel>
          <PanelHeader
            title="Accounts with an authorized key"
            actions={
              <Status
                verdict={noKeys ? "warning" : "ok"}
                label={
                  noKeys
                    ? "none"
                    : `${data.keyedAccounts.length} account${data.keyedAccounts.length === 1 ? "" : "s"}`
                }
              />
            }
          />
          <PanelBody>
            {noKeys ? (
              <p className="text-body text-muted-foreground">
                None. Every login on this host currently depends on a password.
              </p>
            ) : (
              <DetailList>
                {data.keyedAccounts.map((account) => (
                  <Detail key={account.user} label={account.user}>
                    <span className="numeric">
                      {account.keys} {account.keys === 1 ? "key" : "keys"}
                    </span>
                  </Detail>
                ))}
              </DetailList>
            )}
          </PanelBody>
        </Panel>
      </div>
      {dialog}
    </>
  )
}

/**
 * One sshd directive as a row in a plain divided list.
 *
 * Three columns, always in the same place: what it is, what it is set to, and
 * — only where the value is below the recommendation — why that matters. The
 * row used to be a flex with `justify-between`, which put the control hard
 * against the right edge of a 1600px panel: the label and the answer to it
 * were eight hundred pixels apart with nothing in between, and the reader had
 * to track across a blank line to find out what a setting was set to. The
 * third column is what the space is actually for.
 *
 * The row only raises its voice when the setting is below the recommendation:
 * a warning-tinted left edge and the "recommended … because …" line appear
 * there and nowhere else. Every row used to be its own bordered card in one of
 * three colours, which made a list of twelve mostly-fine settings look like
 * twelve problems.
 *
 * A two-value choice (password auth on/off, root login) is a segmented control
 * rather than a dropdown — the two states are the whole decision and both
 * should be visible without opening a menu.
 */
function SettingRow({
  setting,
  value,
  changed,
  note,
  onChange,
}: {
  setting: SSHSetting
  value: string
  changed: boolean
  /** An aside that belongs to this directive alone — the socket that owns Port. */
  note?: string
  onChange: (value: string) => void
}) {
  const below = !setting.secure
  const segmented =
    setting.kind === "choice" && setting.options?.length === 2 && setting.options.includes(value)

  return (
    <div
      className={cn(
        "grid min-w-0 grid-cols-1 items-start gap-x-6 gap-y-3 border-l-2 border-transparent px-4 py-3 md:grid-cols-[minmax(0,1fr)_13rem] xl:grid-cols-[minmax(0,30rem)_13rem_minmax(0,1fr)]",
        changed && "border-rule-primary bg-wash-primary",
        !changed && below && "border-rule-warning bg-wash-warning",
      )}
    >
      <div className="min-w-0 space-y-1">
        <div className="flex flex-wrap items-baseline gap-x-2">
          <span className="text-body font-medium">{setting.label}</span>
          <code className="font-mono text-hint text-muted-foreground">{setting.key}</code>
          {changed && (
            <Tag tone="default" className="border-rule-primary text-primary">
              pending
            </Tag>
          )}
        </div>
        <p className="text-hint leading-relaxed text-muted-foreground">{setting.detail}</p>
        {note && (
          <p className="text-hint leading-relaxed text-muted-foreground/90 italic">{note}</p>
        )}
      </div>

      <div className={cn("min-w-0", setting.kind === "list" && "xl:col-span-2")}>
        {setting.kind === "list" ? (
          <Input
            value={value}
            placeholder="deploy admin — empty allows everyone"
            className="font-mono text-xs"
            onChange={(e) => onChange(e.target.value)}
          />
        ) : segmented ? (
          <ToggleGroup
            type="single"
            value={value}
            onValueChange={(v) => v && onChange(v)}
            variant="outline"
            size="sm"
            className="w-full"
            aria-label={setting.label}
          >
            {setting.options!.map((option) => (
              <ToggleGroupItem key={option} value={option} className="flex-1 text-xs capitalize">
                {option}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        ) : setting.kind === "choice" ? (
          <Select value={value} onValueChange={onChange}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {setting.options?.map((option) => (
                <SelectItem key={option} value={option}>
                  {option}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <Input value={value} inputMode="numeric" onChange={(e) => onChange(e.target.value)} />
        )}
      </div>

      {below && setting.kind !== "list" && (
        <p className="min-w-0 text-hint leading-relaxed xl:col-start-3">
          <span className="font-medium text-warning">Recommended {setting.recommended}.</span>
          {setting.risk && <span className="text-foreground/75"> {setting.risk}</span>}
        </p>
      )}
      {below && setting.kind === "list" && (
        <p className="min-w-0 text-hint leading-relaxed md:col-span-2 xl:col-span-3">
          <span className="font-medium text-warning">Recommended {setting.recommended}.</span>
          {setting.risk && <span className="text-foreground/75"> {setting.risk}</span>}
        </p>
      )}
    </div>
  )
}
