"use client"

import {
  keepingSentence,
  scrapeConfig,
  shortens,
  storedSentence,
  windowEstimate,
} from "@/components/database/fleet/metrics-export"
import { useApiOrigin } from "@/components/account/api-keys"
import { useConfirm } from "@/components/confirm-dialog"
import { Segments } from "@/components/deploy/settings/segments"
import { Field, FormSection } from "@/components/form"
import { Check, Copy, LineChart } from "@/components/icons"
import { Modal } from "@/components/modal"
import { Well } from "@/components/panel"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { useCopy } from "@/hooks/use-copy"
import { usePoll } from "@/hooks/use-poll"
import { get, post, put } from "@/lib/api"
import { bytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { ApiToken, DbMetricsExport } from "@/lib/types"
import { useState } from "react"

const PATH = "/databases/metrics-export"

/**
 * Where the recorded database activity leaves the dashboard, and for how long
 * it stays. The recorder keeps a reading of every server on a timer whether or
 * not a page is open; this is the one place that says how to scrape those
 * readings with Prometheus and how long they are kept, so it opens from the
 * control center, which is where all the databases are.
 */
export function MetricsExport({ admin }: { admin: boolean }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
        <LineChart className="size-4" />
        Metrics export
      </Button>
      {open && <MetricsExportDialog admin={admin} onClose={() => setOpen(false)} />}
    </>
  )
}

function MetricsExportDialog({ admin, onClose }: { admin: boolean; onClose: () => void }) {
  const origin = useApiOrigin()
  const { confirm, dialog } = useConfirm()
  const read = usePoll((signal) => get<DbMetricsExport>(PATH, undefined, signal), 30_000)
  // The answer to a change stands until the next poll has read it back.
  const [saved, setSaved] = useState<{ report: DbMetricsExport; at: number }>()
  const [saving, setSaving] = useState(false)
  const [keyName, setKeyName] = useState("prometheus")
  const [minting, setMinting] = useState(false)
  const [secret, setSecret] = useState<string>()
  const report = saved && saved.at > (read.lastSuccess ?? 0) ? saved.report : read.data

  const choose = (days: number) => {
    if (!report || days === report.retentionDays || saving) return
    const write = async () => {
      setSaving(true)
      try {
        setSaved({
          report: await put<DbMetricsExport>(PATH, { retentionDays: days }),
          at: Date.now(),
        })
        read.refresh()
      } catch (err) {
        notify.error("Could not change how long history is kept", err)
      } finally {
        setSaving(false)
      }
    }
    if (!shortens(report, days)) {
      void write()
      return
    }
    confirm({
      title: `Keep ${days} days of history`,
      confirmLabel: `Delete older than ${days} days`,
      description: (
        <p>
          Readings older than {days} days are deleted at the recorder&rsquo;s next pass, within a
          minute, and cannot be recovered. Charts and the metrics endpoint show only what is kept.
        </p>
      ),
      action: write,
    })
  }

  const mint = async () => {
    if (!keyName.trim() || minting) return
    setMinting(true)
    try {
      const made = await post<{ secret: string; token: ApiToken }>("/tokens/", {
        name: keyName.trim(),
        scope: "metrics.read",
        ttlDays: 0,
      })
      setSecret(made.secret)
    } catch (err) {
      notify.error("Could not create the key", err)
    } finally {
      setMinting(false)
    }
  }

  return (
    <>
      <Modal
        open
        onOpenChange={(next) => !next && onClose()}
        size="lg"
        title="Metrics export"
        description="Scrape the recorded database metrics with Prometheus, and choose how long they are kept."
        footer={<Button onClick={onClose}>Done</Button>}
      >
        {read.error && !report ? (
          <ErrorState error={read.error} onRetry={read.refresh} retryable />
        ) : !report ? (
          <div role="status" aria-label="Loading metrics export" className="space-y-4">
            <Skeleton className="h-16 rounded-lg" />
            <Skeleton className="h-28 rounded-lg" />
            <Skeleton className="h-20 rounded-lg" />
          </div>
        ) : (
          <div className="min-w-0 space-y-8">
            <FormSection title="Endpoint">
              <Field
                label="Metrics URL"
                hint="One set of series per database, labelled with its driver, name and id. Only an API key can read it."
                trailing={<CopyButton label="Copy URL" text={`${origin}${report.endpoint}`} />}
              >
                <Well className="break-all">{`${origin}${report.endpoint}`}</Well>
              </Field>
              <Field
                label="Key"
                htmlFor="metrics-key-name"
                hint="A key made here can read these metrics and nothing else on this dashboard."
              >
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <Input
                    id="metrics-key-name"
                    value={keyName}
                    onChange={(event) => setKeyName(event.target.value)}
                    className="min-w-0 flex-1 sm:max-w-xs"
                  />
                  <Button
                    variant="outline"
                    onClick={() => void mint()}
                    disabled={!keyName.trim()}
                    pending={minting}
                  >
                    Create metrics key
                  </Button>
                </div>
              </Field>
              {secret && (
                <Field
                  label="New key"
                  hint="Copy it now. It is stored hashed, so it cannot be shown again."
                  trailing={<CopyButton label="Copy key" text={secret} />}
                >
                  <Well className="break-all select-all">{secret}</Well>
                </Field>
              )}
            </FormSection>

            <FormSection title="Scrape config">
              <Field
                label="prometheus.yml"
                hint={secret ? undefined : "Create a key above to fill in its value."}
                trailing={
                  <CopyButton
                    label="Copy config"
                    text={scrapeConfig(origin, report.endpoint, secret)}
                  />
                }
              >
                <Well className="whitespace-pre">
                  {scrapeConfig(origin, report.endpoint, secret)}
                </Well>
              </Field>
            </FormSection>

            <FormSection title="Retention">
              <Field
                label="Keep history for"
                hint={
                  admin
                    ? keepingSentence(report)
                    : "Only an administrator can change how long history is kept."
                }
              >
                <Segments
                  label="Keep history for"
                  fill
                  value={String(report.retentionDays)}
                  disabled={!admin || saving}
                  onChange={(value) => choose(Number(value))}
                  options={report.options.map((option) => ({
                    value: String(option.days),
                    label: `${option.days} days`,
                  }))}
                />
              </Field>
              <p className="numeric text-hint text-muted-foreground">
                {storedSentence(report)} {estimate(report)}
              </p>
            </FormSection>
          </div>
        )}
      </Modal>
      {dialog}
    </>
  )
}

function estimate(report: DbMetricsExport) {
  const weight = windowEstimate(report, report.retentionDays)
  if (weight === null)
    return "An estimate of the space a window takes appears once readings are saved."
  const servers = report.connections === 1 ? "1 server" : `${report.connections} servers`
  return `${report.retentionDays} days for ${servers} comes to about ${bytes(weight)}.`
}

function CopyButton({ label, text }: { label: string; text: string }) {
  const { copy, copied } = useCopy()
  return (
    <Button size="xs" variant="outline" onClick={() => void copy(text)} aria-label={label}>
      {copied ? <Check /> : <Copy />}
      {copied ? "Copied" : "Copy"}
    </Button>
  )
}
