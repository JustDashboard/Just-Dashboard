"use client"

import { useState } from "react"
import { ApiError, del, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { SiteDeleteResult, SiteRenameResult, VHost, VHostLinkResult } from "@/lib/types"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { Modal } from "@/components/modal"
import { Well } from "@/components/panel"
import { siteTitle } from "@/components/proxy/site-details"
import { reloadFailure, reloadOutput } from "@/components/proxy/site-outcome"
import { reloadOutcome, type ReloadOutcome } from "@/components/proxy/site-serving"
import { retireRoute, routeRetirement } from "@/components/proxy/site-verbs"

/** nginx's own words about a change, kept for the operator who asks to see them. */
type NginxOutput = { title: string; output: string }

/**
 * What a change that landed says when nginx did not reload: `notRunning` when
 * there was no nginx to reload, which starts with the change, and
 * `notReloaded`, before nginx's reason, when the reload itself failed.
 */
export type ReloadCopy = { notRunning: string; notReloaded: string }

/**
 * The changes to what a host serves that the Sites list and a site's own
 * page both make: the switch, a rename, a delete, and the removal of a route
 * its deployment left. They were the list's alone, so an operator who opened
 * a site to remove it found nothing there to remove it with.
 *
 * What differs between the two is what each keeps while a change is in
 * flight and where it goes after: the list holds a busy word per row and
 * follows a reload through to what nginx then has loaded (`begin`, `heard`,
 * `settle`), and the page reads its site again, or leaves for the list once
 * the site is gone (`onDeleted`, `onRenamed`).
 */
export function useSiteChanges({
  confirm,
  hosts,
  begin,
  heard = (_name, res) => reloadOutcome(res, undefined),
  settle,
  onDeleted,
  onRenamed,
}: {
  confirm: (request: ConfirmRequest) => void
  /** Every site, for the one an enable would displace. */
  hosts: VHost[]
  /** A verb started on the site of this file name, with its present participle. */
  begin: (name: string, verb: string) => void
  /**
   * Takes in how the verb's reload went and returns it. `keepsCard` is a
   * change that leaves the site listed, which can then be drawn as changed
   * and not loaded.
   */
  heard?: (
    name: string,
    res: VHostLinkResult | SiteDeleteResult,
    keepsCard: boolean,
  ) => ReloadOutcome | undefined
  /** The verb has answered or been refused: read again. */
  settle: (name: string, reloaded: boolean) => void
  /** The site is off the host: deleted, or its route removed. */
  onDeleted?: (vhost: VHost) => void
  onRenamed?: (vhost: VHost, res: SiteRenameResult) => void
}) {
  const [output, setOutput] = useState<NginxOutput | null>(null)

  // The whole of what nginx printed, one press away from the toast that
  // carries its first line.
  const showOutput = (name: string, text: string | undefined) =>
    text
      ? { label: "Show nginx output", onClick: () => setOutput({ title: name, output: text }) }
      : undefined

  /**
   * Says when a change that landed did not reload, and returns whether it
   * said anything. With no nginx running there is nothing still serving the
   * configuration from before — "keeps serving" would claim a site is up
   * that is not — and nginx starts with the change. Any other failed reload
   * leaves whatever nginx had loaded, if it is running, which the page
   * cannot see from here and so does not claim.
   */
  const reportReload = (
    res: VHostLinkResult | SiteDeleteResult,
    outcome: ReloadOutcome | undefined,
    done: string,
    copy: ReloadCopy,
  ): boolean => {
    const failure = reloadFailure(res)
    if (!failure) return false
    const action = showOutput(res.name, reloadOutput(res))
    if (outcome === "notRunning") {
      notify.warning(`${done}; nginx is not running`, {
        description: copy.notRunning,
        duration: 12_000,
        action,
      })
    } else {
      notify.warning(`${done}, not reloaded`, {
        description: `${copy.notReloaded} ${failure}`,
        duration: 12_000,
        action,
      })
    }
    return true
  }
  /**
   * Says what a change to a link did. The change itself passed `nginx -t` —
   * a refused one comes back as a 422 carrying nginx's reason, and nothing
   * changed — but the reload after it can still fail, which the page has to
   * say.
   */
  const reportLink = (
    res: VHostLinkResult,
    outcome: ReloadOutcome | undefined,
    done: string,
    copy: ReloadCopy,
  ) => {
    if (!reportReload(res, outcome, done, copy)) notify.success(done)
  }
  const reportRefusal = (title: string, name: string, err: unknown) =>
    notify.error(title, err, {
      action: err instanceof ApiError ? showOutput(name, err.raw) : undefined,
    })

  const toggle = (vhost: VHost, enabled: boolean) => {
    const title = siteTitle(vhost)
    const apply = async (): Promise<"reported"> => {
      begin(vhost.name, enabled ? "Enabling" : "Disabling")
      let outcome: ReloadOutcome | undefined
      try {
        const res = await post<VHostLinkResult>(
          `/proxy/vhosts/${encodeURIComponent(vhost.name)}/enabled`,
          { enabled, reload: true },
        )
        outcome = heard(vhost.name, res, true)
        if (enabled) {
          reportLink(res, outcome, `${title} enabled`, {
            notRunning: "It serves once nginx starts.",
            notReloaded: "It is not serving until nginx reloads.",
          })
        } else {
          reportLink(res, outcome, `${title} disabled`, {
            notRunning: "nginx starts without it.",
            notReloaded: "If nginx is running, it still serves it until a reload succeeds.",
          })
        }
      } catch (err) {
        reportRefusal(`Could not ${enabled ? "enable" : "disable"} ${title}`, title, err)
      } finally {
        settle(vhost.name, outcome === "reloaded")
      }
      return "reported"
    }
    if (!enabled) {
      confirm({
        title: `Disable ${title}`,
        confirmLabel: "Disable and reload",
        description: (
          <p>
            <b>{title}</b> stops serving as soon as nginx reloads. The config file stays on disk.
            {vhost.linkedAs?.length ? (
              <>
                {" "}
                Disabling removes{" "}
                {vhost.linkedAs.map((alias, i) => (
                  <span key={alias}>
                    {i > 0 && " and "}
                    <code className="font-mono break-all">sites-enabled/{alias}</code>
                  </span>
                ))}
                , which {vhost.linkedAs.length === 1 ? "serves" : "serve"} it under another name.
              </>
            ) : null}{" "}
            If taking it out breaks a configuration nginx was loading, the link goes back and
            nothing changes.
          </p>
        ),
        action: apply,
      })
      return
    }
    // The name in sites-enabled is how nginx reads some other file; the
    // enable points it at this one, and that file loses its only way in.
    if (vhost.broken === "stale" && vhost.linkTarget && !vhost.targetServedElsewhere) {
      const displaced = hosts.find((h) => h.linkedAs?.includes(vhost.name))
      confirm({
        title: `Enable ${title}`,
        confirmLabel: "Enable and reload",
        description: (
          <p>
            <code className="font-mono break-all">sites-enabled/{vhost.name}</code> is how nginx
            serves{" "}
            {displaced ? (
              <b>{displaced.name}</b>
            ) : (
              <code className="font-mono break-all">{vhost.linkTarget}</code>
            )}{" "}
            now. Enabling points it at this site&apos;s file, and{" "}
            {displaced ? (
              <>
                <b>{displaced.name}</b> stops serving once nginx reloads, until it is enabled under
                its own name.
              </>
            ) : (
              "nginx stops reading that file once it reloads; nothing on this page links it back."
            )}{" "}
            If nginx refuses {vhost.name}, the link goes back and nothing changes.
          </p>
        ),
        action: apply,
      })
      return
    }
    void apply()
  }

  const remove = (vhost: VHost) =>
    confirm({
      title: `Delete ${siteTitle(vhost)}`,
      confirmLabel: "Delete and reload",
      description: (
        <p>
          The file and its symlink are removed and nginx reloads. The previous content is kept
          beside it as <code className="font-mono">{vhost.name}.bak</code>, which nginx does not
          read and this list does not show.
        </p>
      ),
      action: async () => {
        begin(vhost.name, "Deleting")
        let outcome: ReloadOutcome | undefined
        try {
          const res = await del<SiteDeleteResult>(`/proxy/sites/${encodeURIComponent(vhost.name)}`)
          outcome = heard(vhost.name, res, false)
          // The file is gone, and a running nginx still serves what it
          // loaded: "completed" would send the operator away from a site
          // that is up.
          const reported = reportReload(res, outcome, `${siteTitle(vhost)} deleted`, {
            notRunning: "nginx starts without it.",
            notReloaded: "If nginx is running, it still serves it until a reload succeeds.",
          })
          onDeleted?.(vhost)
          return reported ? "reported" : undefined
        } finally {
          settle(vhost.name, outcome === "reloaded")
        }
      },
    })

  const retire = (vhost: VHost) =>
    confirm({
      ...routeRetirement(vhost),
      action: async () => {
        begin(vhost.name, "Removing")
        try {
          const reported = await retireRoute(vhost)
          onDeleted?.(vhost)
          return reported
        } finally {
          settle(vhost.name, false)
        }
      },
    })

  /**
   * Moves a site to a new name. A refusal is thrown back into the dialog,
   * which keeps the typed name; on success the caller is told
   * (`onRenamed`), since whatever pointed at the old name — the list's
   * selection and cursor, the page's own address — has to follow it.
   */
  const rename = async (vhost: VHost, to: string) => {
    begin(vhost.name, "Renaming")
    let outcome: ReloadOutcome | undefined
    try {
      const res = await post<SiteRenameResult>(
        `/proxy/sites/${encodeURIComponent(vhost.name)}/rename`,
        { to, reload: true },
      )
      outcome = heard(vhost.name, res, false)
      onRenamed?.(vhost, res)
      reportLink(res, outcome, `${vhost.name} renamed to ${res.name}`, {
        notRunning: "nginx starts with it under the new name.",
        notReloaded:
          "If nginx is running, it keeps serving the site as it loaded it until a reload succeeds.",
      })
      for (const warning of res.warnings)
        notify.warning(`${res.name}: moved unchanged`, { description: warning })
    } finally {
      settle(vhost.name, outcome === "reloaded")
    }
  }

  const outputDialog = (
    <Modal
      open={output !== null}
      onOpenChange={(open) => !open && setOutput(null)}
      title={`nginx output — ${output?.title ?? ""}`}
      size="lg"
      initialFocus="body"
    >
      <Well className="max-h-[60svh] break-all whitespace-pre-wrap">{output?.output}</Well>
    </Modal>
  )

  return {
    toggle,
    remove,
    retire,
    rename,
    reportReload,
    reportLink,
    reportRefusal,
    showOutput,
    setOutput,
    outputDialog,
  }
}
