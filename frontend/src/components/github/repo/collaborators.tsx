"use client"

import { useState } from "react"
import { LockClosed, UserMinus, UserPlus, Users } from "@/components/icons"
import { Field } from "@/components/form"
import { ForgeFace } from "@/components/git/marks"
import { DimActions, IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList, RowsSkeleton } from "@/components/row-list"
import { EmptyNote, EmptyState, ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { AbilityButton, useActReason, useRepo } from "@/components/github/repo/shared"
import { usePoll } from "@/hooks/use-poll"
import { del, errorMessage, get, put } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type {
  GitHubCollaborator,
  GitHubCollaboratorResult,
  GitHubCollaboratorRole,
  GitHubCollaborators,
  GitHubInvitation,
} from "@/lib/github-types"

const ROLES: { value: GitHubCollaboratorRole; label: string }[] = [
  { value: "read", label: "Read" },
  { value: "triage", label: "Triage" },
  { value: "write", label: "Write" },
  { value: "maintain", label: "Maintain" },
  { value: "admin", label: "Admin" },
]

/** GitHub says `pull`/`push` in some answers and `read`/`write` in others. */
function roleOf(role: string): GitHubCollaboratorRole | undefined {
  const value = role === "pull" ? "read" : role === "push" ? "write" : role
  return ROLES.find((r) => r.value === value)?.value
}

/**
 * Who can reach the repository and how far. Where GitHub would not list
 * them (it needs push access or more), that is the view's whole answer and
 * nothing here pretends otherwise. Roles, removing someone, inviting and
 * cancelling an invitation are drawn only where the repository may be
 * administered from here.
 */
export function RepoCollaborators() {
  const { repo, path, confirm } = useRepo()
  const read = usePoll(
    (signal) => get<GitHubCollaborators>(path("/collaborators"), undefined, signal),
    0,
    [repo.fullName],
  )
  const [inviting, setInviting] = useState(false)
  const manage = !useActReason("administer")
  const removeReason = useActReason("administer", { destructive: true })
  const data = read.data

  if (!data) {
    return read.error ? (
      <ErrorState error={read.error} onRetry={read.refresh} retryable />
    ) : (
      <RowsSkeleton rows={4} />
    )
  }
  if (!data.readable) {
    return (
      <EmptyState
        data-slot="github-collaborators-unreadable"
        icon={LockClosed}
        title="GitHub does not show who has access"
        description={data.message ?? "Listing collaborators needs push access or more."}
      />
    )
  }

  const setRole = async (person: GitHubCollaborator, role: GitHubCollaboratorRole) => {
    try {
      await put<GitHubCollaboratorResult>(
        path(`/collaborators/${encodeURIComponent(person.login)}`),
        {
          role,
        },
      )
      notify.success(`${person.login} now has ${role}`)
      read.refresh()
    } catch (err) {
      notify.error(`GitHub did not change ${person.login}'s role`, err)
    }
  }
  const remove = (person: GitHubCollaborator) =>
    confirm({
      title: `Remove ${person.login}`,
      confirmLabel: "Remove",
      subject: {
        mark: <ForgeFace login={person.login} provider="github" />,
        name: person.login,
      },
      description: (
        <p>
          {person.login} loses their access to {repo.fullName}. Access through an organisation or a
          team stays.
        </p>
      ),
      action: async () => {
        await del(path(`/collaborators/${encodeURIComponent(person.login)}`))
        read.refresh()
      },
    })
  const cancel = (invitation: GitHubInvitation) =>
    confirm({
      title: `Cancel the invitation to ${invitation.invitee}`,
      confirmLabel: "Cancel invitation",
      description: <p>The invitation stops working. They can be invited again.</p>,
      action: async () => {
        await del(path(`/invitations/${invitation.id}`))
        read.refresh()
      },
    })

  return (
    <div className="flex min-w-0 flex-col gap-8 pb-6">
      <Panel plain aria-label="Collaborators">
        <PanelHeader
          title="Collaborators"
          count={plural(data.collaborators.length, "person", "people")}
          actions={
            <AbilityButton
              ability="administer"
              size="sm"
              variant="outline"
              onClick={() => setInviting(true)}
            >
              <UserPlus className="size-3.5" />
              Invite
            </AbilityButton>
          }
        />
        <PanelBody flush>
          {data.collaborators.length === 0 ? (
            <EmptyNote>Nobody besides the owner has access.</EmptyNote>
          ) : (
            <RowList data-slot="github-collaborators">
              {data.collaborators.map((person) => {
                const role = roleOf(person.role)
                return (
                  <Row
                    key={person.login}
                    data-login={person.login}
                    leading={<ForgeFace login={person.login} provider="github" />}
                    title={person.login}
                    subtitle={person.type === "Bot" ? "bot" : undefined}
                    trailing={
                      manage && role ? (
                        <>
                          <Select
                            value={role}
                            onValueChange={(next) =>
                              void setRole(person, next as GitHubCollaboratorRole)
                            }
                          >
                            <SelectTrigger
                              aria-label={`Role of ${person.login}`}
                              size="sm"
                              className="w-32"
                            >
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {ROLES.map((r) => (
                                <SelectItem key={r.value} value={r.value}>
                                  {r.label}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                          {!removeReason && (
                            <DimActions>
                              <IconAction
                                label={`Remove ${person.login}`}
                                onClick={() => remove(person)}
                              >
                                <UserMinus />
                              </IconAction>
                            </DimActions>
                          )}
                        </>
                      ) : (
                        <span className="text-hint text-muted-foreground">
                          {role ?? person.role}
                        </span>
                      )
                    }
                  />
                )
              })}
            </RowList>
          )}
        </PanelBody>
      </Panel>

      {data.invitationsReadable && data.invitations.length > 0 && (
        <Panel plain aria-label="Invitations">
          <PanelHeader title="Invitations" count={data.invitations.length} />
          <PanelBody flush>
            <RowList data-slot="github-invitations">
              {data.invitations.map((invitation) => (
                <Row
                  key={invitation.id}
                  leading={<ForgeFace login={invitation.invitee} provider="github" />}
                  title={invitation.invitee}
                  subtitle={
                    <>
                      {roleOf(invitation.role) ?? invitation.role}
                      {invitation.inviter && ` · invited by ${invitation.inviter}`} ·{" "}
                      {invitation.expired ? "expired" : relativeTime(invitation.createdAt)}
                    </>
                  }
                  trailing={
                    manage && !removeReason ? (
                      <Button size="xs" variant="outline" onClick={() => cancel(invitation)}>
                        Cancel
                      </Button>
                    ) : undefined
                  }
                />
              ))}
            </RowList>
          </PanelBody>
        </Panel>
      )}

      {inviting && (
        <InviteDialog
          onClose={() => setInviting(false)}
          onInvited={() => {
            setInviting(false)
            read.refresh()
          }}
        />
      )}
    </div>
  )
}

function InviteDialog({ onClose, onInvited }: { onClose: () => void; onInvited: () => void }) {
  const { repo, path } = useRepo()
  const [login, setLogin] = useState("")
  const [role, setRole] = useState<GitHubCollaboratorRole>("write")
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState<string>()
  const name = login.trim().replace(/^@/, "")
  const valid = /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$/.test(name)
  const invite = async () => {
    if (!valid || busy) return
    setBusy(true)
    setRefused(undefined)
    try {
      const result = await put<GitHubCollaboratorResult>(
        path(`/collaborators/${encodeURIComponent(name)}`),
        { role },
      )
      notify.success(result.invited ? `Invited ${name}` : `${name} now has ${role}`)
      onInvited()
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onOpenChange={(open) => !open && !busy && onClose()}
      title={`Invite to ${repo.name}`}
      size="sm"
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={invite} disabled={!valid} pending={busy}>
            <Users className="size-3.5" />
            Invite
          </Button>
        </>
      }
    >
      <form
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault()
          void invite()
        }}
      >
        <Field
          label="GitHub username"
          htmlFor="invite-login"
          error={login && !valid ? "Not a GitHub username." : refused}
        >
          <Input
            id="invite-login"
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
            value={login}
            onChange={(event) => setLogin(event.target.value)}
          />
        </Field>
        <Field label="Role" htmlFor="invite-role">
          <Select value={role} onValueChange={(next) => setRole(next as GitHubCollaboratorRole)}>
            <SelectTrigger id="invite-role" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {ROLES.map((r) => (
                <SelectItem key={r.value} value={r.value}>
                  {r.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      </form>
    </Modal>
  )
}
