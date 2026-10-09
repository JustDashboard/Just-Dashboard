"use client"

import { useLayoutEffect, useRef, useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { get, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { readNativeProfile, type NativeProfile } from "@/lib/network-native-profile"
import type { DNSView } from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { Field, OptionRow } from "@/components/form"
import { Detail, DetailList } from "@/components/page"
import { Row, RowList } from "@/components/row-list"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { encryptionOf } from "@/components/network/dns/resolvers"
import {
  splitDNSDraft,
  splitDNSEffect,
  splitDNSIntent,
  type SplitDNSDraft,
} from "@/components/network/dns/split-dns"

type Link = DNSView["resolved"]["links"][number]

/**
 * Each link resolved holds DNS settings for: its servers, its search and
 * routing domains, whether names nothing claims may go to it, and its own
 * DNS over TLS and DNSSEC. The global upstreams are this page's drop-in; a
 * link's are its network manager's, so split DNS for a link — "names under
 * corp.example go to the VPN's server" — is written through the link's native
 * profile, with the same temporary apply and reconnection confirmation as
 * every other native profile change.
 */
export function LinkScopes({ view, onChanged }: { view: DNSView; onChanged: () => void }) {
  const { can } = useAuth()
  const [open, setOpen] = useState<string>()
  const links = view.resolved.links
  return (
    <>
      {links.length === 0 ? (
        <EmptyNote>
          {view.resolved.active
            ? "No link carries DNS servers or domains of its own."
            : "systemd-resolved is not running, so no link has a DNS scope."}
        </EmptyNote>
      ) : (
        <RowList>
          {links.map((link) => (
            <LinkRow
              key={link.name}
              link={link}
              onSplit={can("system.admin") ? () => setOpen(link.name) : undefined}
            />
          ))}
        </RowList>
      )}
      <SidePanel
        open={Boolean(open)}
        onOpenChange={(next) => !next && setOpen(undefined)}
        title={open ? `Split DNS for ${open}` : "Split DNS"}
        description="The link's DNS servers and domains in its native persistent profile."
        width="lg"
        initialFocus="body"
      >
        {open && (
          <SplitDNSEditor
            key={open}
            device={open}
            onApplied={() => {
              setOpen(undefined)
              onChanged()
            }}
          />
        )}
      </SidePanel>
    </>
  )
}

function LinkRow({ link, onSplit }: { link: Link; onSplit?: () => void }) {
  const routing = link.domains.filter((d) => d.startsWith("~"))
  const search = link.domains.filter((d) => !d.startsWith("~"))
  const encryption = encryptionOf(link.dnsOverTLS)
  const route = link.defaultRoute ? "Default route" : "Routed names only"
  return (
    <Row
      title={link.name}
      mono
      subtitle={
        <>
          {[
            link.servers.length > 0 ? link.servers.join(" ") : "no servers",
            routing.length > 0 && `routes ${routing.join(" ")}`,
            search.length > 0 && `search ${search.join(" ")}`,
          ]
            .filter(Boolean)
            .join(" · ")}
          {/* On a phone the readings at the row's edge give way to its name. */}
          <span className="sm:hidden"> · {route.toLowerCase()}</span>
        </>
      }
      trailing={
        <>
          <Tag className="max-md:hidden">
            {encryption === "plain" ? "classic" : `DoT ${link.dnsOverTLS}`}
          </Tag>
          <Tag className="max-md:hidden">DNSSEC {link.dnssec || "default"}</Tag>
          <span className="max-sm:hidden">
            <Status tone={link.defaultRoute ? "running" : "notice"} label={route} />
          </span>
          {onSplit && (
            <Button
              size="xs"
              variant="outline"
              onClick={onSplit}
              aria-label={`Edit split DNS for ${link.name}`}
            >
              Split DNS
            </Button>
          )}
        </>
      }
    />
  )
}

function SplitDNSEditor({ device, onApplied }: { device: string; onApplied: () => void }) {
  const { can } = useAuth()
  const writable = can("system.admin") && can("destructive")
  const profile = usePoll<NativeProfile>(
    async (signal) =>
      readNativeProfile(
        await get(`/network/native/profiles/${encodeURIComponent(device)}`, undefined, signal),
        device,
      ),
    30_000,
    [device],
  )
  const view = profile.data
  const [draft, setDraft] = useState<SplitDNSDraft & { generation: string }>()
  const [error, setError] = useState<string>()
  const { confirm, dialog } = useConfirm()
  const baseline = view?.intent && view.generation ? view : undefined
  if (baseline && !draft) {
    setDraft({ ...splitDNSDraft(baseline.intent!), generation: baseline.generation! })
  }
  const prepared = baseline && draft ? splitDNSIntent(baseline.intent!, draft) : undefined
  const changed = Boolean(draft && view?.generation && draft.generation !== view.generation)
  const blocked =
    !writable || !view?.editable || Boolean(profile.error) || changed || !prepared?.intent
  // An open confirmation survives polls; its callback checks the review it
  // was opened for is still the one rendered.
  const latest = useRef({ blocked, draft })
  useLayoutEffect(() => {
    latest.current = { blocked, draft }
  })
  const edit = (patch: Partial<SplitDNSDraft>) => {
    if (draft) setDraft({ ...draft, ...patch })
    setError(undefined)
  }
  const apply = () => {
    if (!draft || blocked || !prepared?.intent || !view) return
    const intent = prepared.intent
    void confirm({
      title: `Apply split DNS to ${device}`,
      description: `The existing ${view.owner} profile ${view.profile ?? ""} restarts this connection with the new DNS servers and domains. Reconnect and confirm within 90 seconds to keep them; otherwise the independent host watchdog restores the previous profile.`,
      confirmLabel: "Apply temporary settings",
      action: async () => {
        if (latest.current.blocked || latest.current.draft !== draft)
          throw new Error("The native profile changed. Review the retained draft before applying.")
        try {
          await put(`/network/native/profiles/${encodeURIComponent(device)}`, {
            generation: draft.generation,
            intent,
          })
        } catch (err) {
          setError(err instanceof Error ? err.message : String(err))
          profile.refresh()
          throw err
        }
        notify.success(`Split DNS applied to ${device}; confirm the reconnection to keep it`)
        onApplied()
        return "reported"
      },
    })
  }
  return (
    <div className="flex min-w-0 flex-col gap-6">
      <NetworkReadWarning
        error={profile.error}
        refresh={profile.refresh}
        lastSuccess={profile.lastSuccess}
        reading="native profile"
      />
      {!view && !profile.error && (
        <p className="text-body text-muted-foreground">Reading the link&rsquo;s native owner…</p>
      )}
      {view && (
        <DetailList>
          <Detail label="Profile">{view.profile ?? "No supported persistent owner"}</Detail>
          <Detail label="Manager">
            {view.owner}
            {view.version && ` · ${view.version}`}
          </Detail>
          <Detail label="Runtime">
            {view.runtime.status} · {view.runtime.reason ?? "Not verified"}
          </Detail>
        </DetailList>
      )}
      {view && !view.editable && (
        <Notice title="This link's DNS is not edited here" tone="warning">
          {view.refusal ??
            "Its native owner is not one the dashboard edits, so its DNS is changed there."}
        </Notice>
      )}
      {draft && view?.editable && (
        <div className="flex min-w-0 flex-col gap-5">
          <Field
            label="DNS servers"
            htmlFor="split-dns-servers"
            hint="IP literals, one per line; IPv4 and IPv6 go to their own family."
            error={prepared?.error?.includes("server") ? prepared.error : undefined}
          >
            <Textarea
              id="split-dns-servers"
              className="font-mono"
              rows={2}
              value={draft.servers}
              onChange={(event) => edit({ servers: event.target.value })}
            />
          </Field>
          <Field
            label="Routing domains"
            htmlFor="split-dns-routing"
            hint="Names under these go only to this link. Write . to make it the route for every name."
            error={
              prepared?.error && !prepared.error.includes("server") ? prepared.error : undefined
            }
          >
            <Textarea
              id="split-dns-routing"
              className="font-mono"
              rows={2}
              value={draft.routing}
              onChange={(event) => edit({ routing: event.target.value })}
            />
          </Field>
          <Field
            label="Search domains"
            htmlFor="split-dns-search"
            hint="Tried after single-label names; names under them also route to this link."
          >
            <Textarea
              id="split-dns-search"
              className="font-mono"
              rows={2}
              value={draft.search}
              onChange={(event) => edit({ search: event.target.value })}
            />
          </Field>
          <OptionRow
            title="Use only these servers on this link"
            hint="Ignores the DNS servers DHCP or router advertisements hand out for it."
            checked={draft.exclusive}
            onCheckedChange={(checked) => edit({ exclusive: checked })}
          />
          <ul aria-label="What resolved will do" className="flex flex-col gap-1">
            {splitDNSEffect(draft, device).map((line) => (
              <li key={line} className="text-body text-muted-foreground">
                {line}
              </li>
            ))}
          </ul>
          {changed && (
            <Notice title="The native profile changed" tone="warning">
              The draft is kept. Review the latest profile, then use it as the baseline.
              <Button
                size="xs"
                variant="outline"
                className="ml-2"
                onClick={() =>
                  view?.generation && setDraft({ ...draft, generation: view.generation })
                }
              >
                Use the latest profile
              </Button>
            </Notice>
          )}
          {error && (
            <p role="alert" className="text-body text-destructive">
              {error}
            </p>
          )}
          <div className="flex flex-wrap gap-2">
            <Button onClick={apply} disabled={blocked}>
              Apply temporary split DNS
            </Button>
          </div>
          <p className="text-hint text-muted-foreground">
            Only DNS servers and domains change; addresses, methods and routes stay the
            profile&rsquo;s own.
          </p>
        </div>
      )}
      {dialog}
    </div>
  )
}
