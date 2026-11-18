"use client"

import { useState } from "react"
import { ArrowRight, FileText, Question, Slash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, put } from "@/lib/api"
import type { DefaultChoice, DefaultListener, DefaultSite, DefaultSiteResult } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { Field, FormNote } from "@/components/form"
import { ErrorState, Notice } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { SectionLink } from "@/components/section-link"
import { Tag } from "@/components/tag"
import { useSaveBarEntry } from "@/components/settings/save-bar"
import { SettingSection, settingStatus } from "@/components/settings/setting-card"
import { reloadFailure } from "@/components/proxy/site-outcome"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

const CHOICES = [
  { choice: "close", label: "Close", icon: Slash },
  { choice: "not_found", label: "404", icon: Question },
  { choice: "redirect", label: "Redirect", icon: ArrowRight },
  { choice: "page", label: "A page", icon: FileText },
] as const satisfies readonly { choice: DefaultChoice; label: string; icon: unknown }[]

/** What a stranger's plain-HTTP request gets under a choice, in one sentence. */
function outcome(choice: DefaultChoice, redirectTo: string, pageDir: string) {
  switch (choice) {
    case "close":
      return "The connection is closed without an answer (444)."
    case "not_found":
      return "404 Not Found."
    case "redirect":
      return `A 301 to ${redirectTo.trim() || "the address below"}, keeping the path asked for.`
    case "page":
      return `${pageDir}/index.html, for every path. A plain one is written if there is none.`
  }
}

/** The listen spelling a reader knows: ":80" for every IPv4 address. */
function socketLabel(listen: string) {
  return listen.startsWith("*:") ? listen.slice(1) : listen
}

function answeredBy(entry: DefaultListener) {
  if (entry.ours) return "the catch-all"
  const name = entry.serverNames.find((n) => n !== "_") ?? entry.file.split("/").pop()
  return entry.claimed
    ? `${name}, which claims default_server`
    : `${name}, the first server nginx reads there`
}

/** Where the catch-all is set: a section of the engine's settings. */
export const UNKNOWN_HOSTS_HREF = "/proxy/config?tab=settings#unknown-hosts"

/** What an unknown host gets now, in a word or two. */
function answerWord(data: DefaultSite): string {
  if (!data.installed) return "the first site"
  switch (data.choice) {
    case "close":
      return "closed"
    case "not_found":
      return "404"
    case "redirect":
      return data.redirectTo ? `redirected to ${data.redirectTo}` : "redirected"
    case "page":
      return "a page"
    default:
      return "changed by hand"
  }
}

const readDefaultSite = (signal: AbortSignal) =>
  get<DefaultSite>("/proxy/default-site", undefined, signal)

/**
 * What an unknown host gets, said on the Sites page as one line with the way
 * to the setting. The setting itself was a panel of its own between the site
 * list and Password files, with its own Apply: the one setting on a list
 * page. It is a section of the engine's settings now (`DefaultSiteSection`).
 */
export function UnknownHostsFact({ admin }: { admin: boolean }) {
  const { data } = usePoll<DefaultSite>(readDefaultSite, 60_000)
  if (!data || data.error) return null
  const words = `Unknown hosts: ${answerWord(data)}`
  return (
    <p data-slot="unknown-hosts" className="flex min-w-0 text-hint text-muted-foreground">
      {admin ? (
        <SectionLink href={UNKNOWN_HOSTS_HREF} className="min-w-0 truncate">
          {words}
        </SectionLink>
      ) : (
        <span className="min-w-0 truncate">{words}</span>
      )}
    </p>
  )
}

/**
 * The catch-all default site: what nginx answers when a request's Host names
 * no site. Without one, the first site nginx reads on a port serves every
 * scanner on the bare IP, and on 443 shows it that site's certificate.
 *
 * A section of the engine's settings (Configuration, Settings), saved by the
 * page's one floating bar with the rest of them. The catch-all is its own
 * file, written and reloaded at once whether or not the page reloads after
 * its other settings.
 */
export function DefaultSiteSection({ admin }: { admin: boolean }) {
  const { data, error, loading, refresh } = usePoll<DefaultSite>(readDefaultSite, 60_000)
  const [draft, setDraft] = useState<DefaultChoice>()
  const [redirectDraft, setRedirectDraft] = useState<string>()
  const [busy, setBusy] = useState(false)
  const { confirm, dialog } = useConfirm()

  const choice = draft ?? data?.choice ?? "close"
  const redirectTo = redirectDraft ?? data?.redirectTo ?? ""
  const wildcard = (data?.answering ?? []).filter((a) => /^(\*|\[::\]):(80|443)$/.test(a.listen))
  const tls = data?.covers.filter((c) => c.endsWith(":443")) ?? []
  const unchanged =
    data?.installed &&
    choice === data.choice &&
    (choice !== "redirect" || redirectTo === data.redirectTo)
  const blocked = !data || Boolean(data.error) || data.others.length > 0
  const dirty = Boolean(data) && (draft !== undefined || redirectDraft !== undefined) && !unchanged
  const invalid = blocked || (choice === "redirect" && !redirectTo.trim())

  const apply = async (): Promise<boolean> => {
    setBusy(true)
    try {
      const res = await put<DefaultSiteResult>("/proxy/default-site", {
        choice,
        redirectTo: choice === "redirect" ? redirectTo.trim() : "",
      })
      const failure = reloadFailure(res)
      if (failure) {
        notify.warning("Catch-all written, not reloaded", {
          description: failure,
          duration: 12_000,
        })
      } else {
        notify.success("The catch-all is live")
      }
      setDraft(undefined)
      setRedirectDraft(undefined)
      return true
    } catch (err) {
      notify.error("Catch-all not applied", err)
      return false
    } finally {
      setBusy(false)
      refresh()
    }
  }

  useSaveBarEntry(
    admin,
    { name: "Unknown hosts", dirty, changes: 1, saving: busy, invalid, applies: "immediately" },
    {
      save: apply,
      discard: () => {
        setDraft(undefined)
        setRedirectDraft(undefined)
      },
    },
  )

  const remove = () =>
    confirm({
      title: "Remove the catch-all",
      confirmLabel: "Remove and reload",
      description: (
        <p>
          <code className="font-mono break-all">{data?.path}</code> is deleted, and unknown hosts go
          back to whichever site nginx reads first on each port — on 443 with that site&apos;s
          certificate. The page directory stays.
        </p>
      ),
      action: async () => {
        const res = await del<DefaultSiteResult>("/proxy/default-site")
        refresh()
        const failure = reloadFailure(res)
        if (failure) notify.warning("Removed, not reloaded", { description: failure })
      },
    })

  return (
    <SettingSection
      id="unknown-hosts"
      title="Unknown hosts"
      status={settingStatus({ dirty })}
      actions={
        admin &&
        data?.installed && (
          <Button variant="outline" size="xs" onClick={remove}>
            Remove
          </Button>
        )
      }
    >
      <div className="space-y-4">
        {loading && !data ? (
          <LoadingRows rows={2} />
        ) : error && !data ? (
          <ErrorState error={error} />
        ) : data ? (
          <>
            {data.error ? (
              <FormNote tone="danger">{data.error}</FormNote>
            ) : (
              <ul className="space-y-1.5">
                {wildcard.map((entry) => (
                  <li key={entry.listen} className="flex min-w-0 items-baseline gap-2 text-body">
                    <Tag mono>{socketLabel(entry.listen)}</Tag>
                    <span className="min-w-0 break-words">Served by {answeredBy(entry)}</span>
                  </li>
                ))}
              </ul>
            )}
            {data.uncovered.length > 0 && (
              <FormNote>
                {data.uncovered.join(", ")} {data.uncovered.length === 1 ? "is" : "are"} on a named
                address, which nginx matches before the catch-all&apos;s wildcard: unknown hosts
                there still get the first server on it.
              </FormNote>
            )}
            {data.tlsSkipped && (
              <FormNote tone="warning">443 is left out: {data.tlsSkipped}.</FormNote>
            )}
            {data.others.length > 0 && (
              <Notice tone="warning" title="Another file already claims default_server">
                {data.others.map((c) => (
                  <span key={`${c.file}:${c.line}:${c.listen}`} className="block font-mono text-xs">
                    {c.file}:{c.line} on {socketLabel(c.listen)}
                  </span>
                ))}
                <span className="mt-1 block">
                  nginx takes one per port. Disable that site, or take default_server off its listen
                  lines, then apply the catch-all.
                </span>
              </Notice>
            )}

            {admin && (
              <>
                <ChoiceGrid className="grid-cols-2 lg:grid-cols-4">
                  {CHOICES.map(({ choice: value, label, icon: Icon }) => (
                    <ChoiceCard
                      key={value}
                      selected={choice === value}
                      onClick={() => setDraft(value)}
                      className="min-h-16 justify-center"
                    >
                      <Icon aria-hidden className="size-4 text-muted-foreground" />
                      <span className="text-body font-medium">{label}</span>
                    </ChoiceCard>
                  ))}
                </ChoiceGrid>
                {choice === "redirect" && (
                  <Field
                    label="Send them to"
                    htmlFor="default-site-redirect"
                    hint="A scheme and host only; the path asked for is added to it."
                  >
                    <Input
                      id="default-site-redirect"
                      className="font-mono text-xs"
                      placeholder="https://example.com"
                      value={redirectTo}
                      onChange={(e) => setRedirectDraft(e.target.value)}
                    />
                  </Field>
                )}
                <div className="space-y-1.5">
                  <p className="text-hint font-medium text-muted-foreground">
                    What an unknown host gets once saved
                  </p>
                  <ul className="space-y-1.5 text-body">
                    <li>
                      <b>HTTP</b> on{" "}
                      {data.covers
                        .filter((c) => !c.endsWith(":443"))
                        .map(socketLabel)
                        .join(", ") || ":80"}
                      : {outcome(choice, redirectTo, data.pageDir)}
                    </li>
                    {tls.length > 0 ? (
                      <li>
                        <b>HTTPS</b> on {tls.map(socketLabel).join(", ")}: the TLS handshake is
                        refused, so no certificate is shown.
                      </li>
                    ) : (
                      !data.tlsSkipped && (
                        <li className="text-muted-foreground">
                          HTTPS is not covered: no site listens on 443 at the wildcard address yet.
                        </li>
                      )
                    )}
                    <li className="text-muted-foreground">
                      /.well-known/acme-challenge/ still answers from /srv/just-dashboard-acme, so
                      certbot can prove a name before its site exists.
                    </li>
                  </ul>
                </div>
                {data.installed && !data.choice && (
                  <FormNote>{data.path} was changed by hand; saving replaces it.</FormNote>
                )}
              </>
            )}
          </>
        ) : null}
      </div>
      {dialog}
    </SettingSection>
  )
}
