"use client"

import { useState } from "react"
import { CloudUpload, FileText, Trash } from "@/components/icons"
import { del } from "@/lib/api"
import { calendarDate, relativeTime } from "@/lib/format"
import { keyTypeName } from "@/lib/private-certificates"
import type { SigningRequest } from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { VerbBar, type Verb } from "@/components/verbs"
import { CompleteRequestDialog, SigningRequestView } from "@/components/proxy/csr-dialog"

/**
 * The signing requests still waiting for an authority's certificate: what the
 * operator owes an answer to, so the panel is drawn only while one waits.
 * Anyone signed in can read a request — it is what gets sent out — but only
 * an administrator adds its certificate or discards it with its key.
 */
export function SigningRequests({
  requests,
  error,
  admin,
  onRetry,
  onChanged,
}: {
  requests: SigningRequest[] | undefined
  error: Error | undefined
  admin: boolean
  onRetry: () => void
  /** A request was completed or discarded: the list and the inventory change. */
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const [shown, setShown] = useState<{ open: boolean; request: SigningRequest | null }>({
    open: false,
    request: null,
  })
  const [completing, setCompleting] = useState<{ open: boolean; request: SigningRequest | null }>({
    open: false,
    request: null,
  })

  const discard = (request: SigningRequest) =>
    confirm({
      title: `Discard the request for ${request.name}`,
      confirmLabel: "Discard",
      description: (
        <p>
          Its key is deleted from this server. A certificate an authority has already signed for
          this request can then never be used: make a new request and have that one signed instead.
        </p>
      ),
      action: async () => {
        await del(`/certificates/csr/${encodeURIComponent(request.name)}`)
        onChanged()
      },
    })

  const verbsFor = (request: SigningRequest): Verb[] => [
    ...(admin && !request.error
      ? [
          {
            key: "complete",
            label: "Add certificate",
            icon: CloudUpload,
            inline: true,
            run: () => setCompleting({ open: true, request }),
          },
        ]
      : []),
    {
      key: "show",
      label: "Show request",
      icon: FileText,
      inline: true,
      run: () => setShown({ open: true, request }),
    },
    ...(admin
      ? [
          {
            key: "discard",
            label: "Discard",
            icon: Trash,
            danger: true,
            run: () => discard(request),
          },
        ]
      : []),
  ]

  // The dialogs outlive the list: completing the last request empties it,
  // and the dialog still has its outcome to show.
  return (
    <>
      {error ? (
        <Panel plain>
          <PanelHeader title="Signing requests" />
          <PanelBody flush>
            <ErrorState error={error} onRetry={onRetry} />
          </PanelBody>
        </Panel>
      ) : (
        requests &&
        requests.length > 0 && <SigningRequestList requests={requests} verbsFor={verbsFor} />
      )}
      <Modal
        open={shown.open}
        onOpenChange={(open) => setShown((s) => ({ ...s, open }))}
        size="lg"
        title={shown.request ? `Signing request for ${shown.request.name}` : "Signing request"}
        description="The request to send to a certificate authority. Its key stays on this server."
        initialFocus="body"
      >
        {shown.request && <SigningRequestView request={shown.request} />}
      </Modal>
      {admin && (
        <CompleteRequestDialog
          open={completing.open}
          request={completing.request}
          onOpenChange={(open) => setCompleting((s) => ({ ...s, open }))}
          onDone={onChanged}
        />
      )}
      {dialog}
    </>
  )
}

function SigningRequestList({
  requests,
  verbsFor,
}: {
  requests: SigningRequest[]
  verbsFor: (request: SigningRequest) => Verb[]
}) {
  return (
    <Panel plain>
      <PanelHeader title="Signing requests" />
      <PanelBody flush>
        <ul aria-label="Signing requests" className="animate-rise divide-y divide-hairline">
          {requests.map((request) => (
            <li key={request.name} className="space-y-3 py-4 first:pt-1">
              <div className="min-w-0">
                <p className="truncate text-body font-medium" title={request.name}>
                  {request.name}
                </p>
                <p className="font-mono text-hint break-all text-muted-foreground">
                  {request.domains.join(", ")}
                </p>
              </div>
              <p className="text-hint text-muted-foreground">
                {keyTypeName(request.keyType)} · made {relativeTime(request.created)}
                {request.replaces &&
                  ` · replaces the certificate kept now, which expires ${calendarDate(request.replaces.notAfter)}`}
              </p>
              {request.error && (
                <p className="text-hint break-words text-destructive">{request.error}</p>
              )}
              <div className="flex flex-wrap items-center justify-between gap-2">
                {request.error ? (
                  <Status verdict="critical" label="cannot be completed" />
                ) : (
                  <Status verdict="notice" label="waiting for the certificate" />
                )}
                <VerbBar
                  verbs={verbsFor(request)}
                  menuLabel={`More actions for the request ${request.name}`}
                />
              </div>
            </li>
          ))}
        </ul>
      </PanelBody>
    </Panel>
  )
}
