"use client"

import { useEffect, useState } from "react"
import { Connection, Plus, Trash, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post } from "@/lib/api"
import type { SiteResult, StreamSpec, StreamStatus } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { CodeEditor } from "@/components/code-editor"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { RowLink } from "@/components/page"
import { SidePanel } from "@/components/side-panel"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { IconAction } from "@/components/icon-action"
import { Status } from "@/components/status-dot"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * Forwarding the things that do not speak HTTP.
 *
 * A Postgres replica, a game server, an SSH bastion, a syslog collector —
 * nginx carries all of them through its stream module, and without it a
 * single-server operator with one non-HTTP service has to leave the dashboard
 * and write nginx by hand.
 *
 * The one thing this panel has to be loud about is that nginx's stream block
 * is a top-level context, not something a site file can reach. If nginx.conf
 * does not include this directory, the files are written and silently ignored
 * — which is the same failure as a drop-in the daemon never reads.
 */
export function StreamsPanel() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useState<StreamSpec | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const { data, error, loading, refresh } = usePoll<StreamStatus>(
    (signal) => get("/proxy/streams/", undefined, signal),
    60000,
  )
  const admin = can("system.admin")

  if (loading) return <LoadingPanel />
  if (error) return <ErrorState error={error} />
  if (!data) return null

  const open = (spec: StreamSpec | null) => {
    setEditing(spec)
    setFormOpen(true)
  }

  return (
    <>
      <div className="flex min-w-0 flex-col gap-4">
        {!data.included && (
          <Notice tone="warning" icon={Warning} title="nginx is not reading these yet">
            <div className="space-y-2">
              <p>
                A stream lives in nginx&rsquo;s top-level <code className="font-mono">stream</code>{" "}
                block, which a site file cannot reach. Until{" "}
                <code className="font-mono">nginx.conf</code> includes this directory, anything
                configured here is written and ignored.
              </p>
              <pre className="overflow-x-auto rounded-lg border border-hairline bg-surface-sunken p-2.5 font-mono text-hint">
                {data.snippet}
              </pre>
              <p>
                Add that at the top level of nginx.conf — beside the{" "}
                <code className="font-mono">http</code> block, not inside it. The dashboard does not
                edit nginx.conf itself: every other configuration on the host depends on that file,
                and a bad write there is a server that will not start.
              </p>
            </div>
          </Notice>
        )}

        <Panel>
          <PanelHeader
            title="Port forwarding"
            actions={
              admin && (
                // Kept enabled when nginx is not reading the directory yet:
                // staging the forward before editing nginx.conf is a
                // reasonable order to work in. What it must not do is look
                // like the thing that makes the port live, so it says which
                // of the two it is.
                <Button
                  size="sm"
                  variant={data.included ? "default" : "outline"}
                  onClick={() => open(null)}
                >
                  <Plus className="size-4" />
                  {data.included ? "New stream" : "Prepare a stream"}
                </Button>
              )
            }
          />
          <PanelBody flush>
            {data.streams.length === 0 ? (
              <EmptyState
                icon={Connection}
                title="Nothing forwarded"
                description="Point a port on this host at a service somewhere else — a database replica, a bastion, a game server. Anything TCP or UDP."
              />
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-full">Name</TableHead>
                    <TableHead>Listening</TableHead>
                    <TableHead>Forwards to</TableHead>
                    <TableHead>Restricted to</TableHead>
                    <TableHead className="w-px" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.streams.map((stream) => (
                    <TableRow key={stream.name} className="group">
                      <TableCell>
                        <RowLink onClick={() => open(stream)}>{stream.name}</RowLink>
                      </TableCell>
                      <TableCell className="font-mono">
                        {stream.listen}
                        <span className="ml-1 text-muted-foreground uppercase">
                          {stream.protocol}
                        </span>
                      </TableCell>
                      <TableCell className="font-mono">{stream.upstream}</TableCell>
                      <TableCell>
                        {stream.allowFrom.length > 0 ? (
                          <span className="font-mono text-hint">{stream.allowFrom.join(", ")}</span>
                        ) : (
                          <Status verdict="critical" label="anyone" />
                        )}
                      </TableCell>
                      <TableCell>
                        {admin && (
                          <IconAction
                            reveal
                            label="Delete stream"
                            className="text-destructive"
                            onClick={() =>
                              confirm({
                                title: `Delete ${stream.name}`,
                                confirmLabel: "Delete and reload",
                                description: (
                                  <p>
                                    {data.included
                                      ? `Port ${stream.listen} stops being forwarded as soon as nginx reloads.`
                                      : `nginx is not reading these yet, so port ${stream.listen} was never forwarded — this removes the file before it ever took effect.`}{" "}
                                    The previous file is kept as{" "}
                                    <code className="font-mono">{stream.name}.conf.bak</code>.
                                  </p>
                                ),
                                action: async () => {
                                  await del(`/proxy/streams/${encodeURIComponent(stream.name)}`)
                                  refresh()
                                },
                              })
                            }
                          >
                            <Trash />
                          </IconAction>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </PanelBody>
        </Panel>
      </div>

      <StreamForm
        key={editing?.name ?? "new"}
        open={formOpen}
        spec={editing}
        included={data.included}
        snippet={data.snippet}
        onOpenChange={setFormOpen}
        onSaved={refresh}
      />
      {dialog}
    </>
  )
}

const BLANK: StreamSpec = {
  name: "",
  listen: 0,
  protocol: "tcp",
  upstream: "",
  proxyProtocol: false,
  allowFrom: [],
}

function StreamForm({
  open,
  spec: initial,
  included,
  snippet,
  onOpenChange,
  onSaved,
}: {
  open: boolean
  spec: StreamSpec | null
  /** Whether nginx.conf actually includes the stream directory. Everything
   *  this form says about taking effect is conditional on it. */
  included: boolean
  snippet: string
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const [spec, setSpec] = useState<StreamSpec>(initial ?? BLANK)
  const [allow, setAllow] = useState((initial?.allowFrom ?? []).join(", "))
  const [preview, setPreview] = useState("")
  const [previewError, setPreviewError] = useState("")
  const [busy, setBusy] = useState(false)

  const body: StreamSpec = {
    ...spec,
    allowFrom: allow.split(/[\s,]+/).filter(Boolean),
  }

  useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    const ready = spec.name !== "" && spec.listen > 0 && spec.upstream !== ""
    const timer = setTimeout(
      () => {
        if (!ready) {
          setPreview("")
          setPreviewError("")
          return
        }
        post<{ content: string }>(
          "/proxy/streams/preview",
          { spec: body },
          {
            signal: controller.signal,
          },
        )
          .then((r) => {
            setPreview(r.content)
            setPreviewError("")
          })
          .catch((err) => {
            if (controller.signal.aborted) return
            setPreview("")
            setPreviewError(String(err))
          })
      },
      ready ? 400 : 0,
    )
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, spec, allow])

  const save = async () => {
    setBusy(true)
    try {
      // overwrite only when this form opened on an existing stream: without
      // it, "New stream" named after one that already exists replaced it in
      // silence, and a forwarding rule that quietly stopped pointing where it
      // used to is the worst way this can fail.
      const res = await post<SiteResult>("/proxy/streams/", {
        spec: body,
        reload: true,
        overwrite: initial !== null,
      })
      // The success message is the last thing between an operator and the
      // belief that the port is now forwarded. Without the include line it is
      // not, and saying "forwarding" here is how they find out hours later.
      if (included) {
        notify.success(`${spec.name} forwarding`, { description: res.warnings[0] })
      } else {
        notify.warning(`${spec.name} saved, not yet live`, {
          description:
            res.warnings[0] ??
            "nginx.conf still has no stream block including this directory, so nginx is not reading the file.",
        })
      }
      onSaved()
      onOpenChange(false)
    } catch (err) {
      notify.error("Not applied", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <SidePanel
      open={open}
      onOpenChange={(o) => !busy && onOpenChange(o)}
      width="lg"
      title={initial ? `Edit ${initial.name}` : "New stream"}
      description="A port on this host, forwarded somewhere else"
      bodyClassName="flex min-h-0 flex-1 flex-col gap-4 p-4"
      footer={
        <>
          <span className="mr-auto text-hint text-muted-foreground">
            {included
              ? "Tested with nginx’s own parser before it takes effect."
              : "Tested with nginx’s own parser, then written — but not read until nginx.conf includes this directory."}
          </span>
          <Button
            size="sm"
            onClick={save}
            disabled={busy || !spec.name || !spec.listen || !spec.upstream}
            pending={busy}
          >
            {included ? "Save and reload" : "Save for later"}
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        {!included && (
          <Notice tone="warning" icon={Warning} title="This will not forward anything yet">
            <div className="space-y-2">
              <p>
                nginx.conf has no <code className="font-mono">stream</code> block including this
                directory, so what you save here is written and ignored. Add the snippet below at
                the top level of nginx.conf — beside the <code className="font-mono">http</code>{" "}
                block, not inside it — and this stream starts forwarding on the next reload.
              </p>
              <pre className="overflow-x-auto rounded-lg border border-hairline bg-surface-sunken p-2.5 font-mono text-hint">
                {snippet}
              </pre>
            </div>
          </Notice>
        )}
        <div className="space-y-1.5">
          <Label htmlFor="stream-name">Name</Label>
          <Input
            id="stream-name"
            value={spec.name}
            onChange={(e) => setSpec((s) => ({ ...s, name: e.target.value }))}
            placeholder="postgres-replica"
            className="font-mono text-xs"
          />
        </div>

        <div className="grid gap-3 sm:grid-cols-[1fr_9rem]">
          <div className="space-y-1.5">
            <Label htmlFor="stream-listen">Listen on</Label>
            <Input
              id="stream-listen"
              value={spec.listen || ""}
              inputMode="numeric"
              onChange={(e) => setSpec((s) => ({ ...s, listen: Number(e.target.value) || 0 }))}
              placeholder="5432"
              className="font-mono text-xs"
            />
          </div>
          <div className="space-y-1.5">
            <Label>Protocol</Label>
            <ToggleGroup
              type="single"
              value={spec.protocol}
              onValueChange={(v) =>
                v && setSpec((s) => ({ ...s, protocol: v as StreamSpec["protocol"] }))
              }
              variant="outline"
              size="sm"
              className="w-full"
            >
              <ToggleGroupItem value="tcp" className="flex-1 text-hint">
                TCP
              </ToggleGroupItem>
              <ToggleGroupItem value="udp" className="flex-1 text-hint">
                UDP
              </ToggleGroupItem>
            </ToggleGroup>
          </div>
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="stream-upstream">Forward to</Label>
          <Input
            id="stream-upstream"
            value={spec.upstream}
            onChange={(e) => setSpec((s) => ({ ...s, upstream: e.target.value }))}
            placeholder="10.0.0.5:5432"
            className="font-mono text-xs"
          />
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="stream-allow">Allow only these</Label>
          <Input
            id="stream-allow"
            value={allow}
            onChange={(e) => setAllow(e.target.value)}
            placeholder="10.0.0.0/8, 203.0.113.9"
            className="font-mono text-xs"
          />
          <p className="text-hint leading-relaxed text-muted-foreground">
            A stream has no authentication of any kind — anything that reaches this port is through
            to the backend. Leave this empty only when the service behind it authenticates for
            itself.
          </p>
        </div>

        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0 space-y-0.5">
            <Label className="font-normal">Send the PROXY header</Label>
            <p className="text-hint leading-relaxed text-muted-foreground">
              Lets the backend see the real client address. It has to be expecting the header, or it
              reads it as the first bytes of the connection and fails in a way that looks like a
              protocol mismatch.
            </p>
          </div>
          <Switch
            checked={spec.proxyProtocol}
            onCheckedChange={(v) => setSpec((s) => ({ ...s, proxyProtocol: v }))}
            className="mt-0.5 shrink-0"
          />
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="stream-timeout">Timeout</Label>
          <Input
            id="stream-timeout"
            value={spec.timeout || ""}
            inputMode="numeric"
            onChange={(e) => setSpec((s) => ({ ...s, timeout: Number(e.target.value) || 0 }))}
            placeholder="seconds — empty leaves nginx's default"
            className="w-56 font-mono text-xs"
          />
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-hidden rounded-xl border border-hairline bg-surface-sunken">
        {previewError ? (
          <div className="flex h-full items-center justify-center p-6 text-center text-xs text-destructive">
            {previewError}
          </div>
        ) : preview ? (
          <CodeEditor className="h-full" language="ini" value={preview} readOnly />
        ) : (
          <div className="flex h-full items-center justify-center p-6 text-center text-xs text-muted-foreground">
            Fill in a name, a port and an upstream, and the nginx appears here.
          </div>
        )}
      </div>
    </SidePanel>
  )
}
