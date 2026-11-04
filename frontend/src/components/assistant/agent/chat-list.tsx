"use client"

import { useRef, useState } from "react"
import { AnimatePresence, motion } from "motion/react"
import {
  Database,
  MoreHorizontal,
  Pencil,
  Plus,
  Rocket,
  Servers,
  SettingsSliders,
  Trash,
  type Icon,
} from "@/components/icons"
import { ApiError } from "@/lib/api"
import { timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useArrivals } from "@/hooks/use-arrivals"
import { useConfirm } from "@/components/confirm-dialog"
import { rowReveal } from "@/components/icon-action"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Skeleton } from "@/components/ui/skeleton"
import {
  deleteAgentChat,
  renameAgentChat,
  type AgentChatSummary,
} from "@/components/assistant/agent/chats"
import { agentChatId, agentBusy, useAgentSessions } from "@/components/assistant/agent/sessions"

/**
 * The person's saved chats, newest first: each a row of one height in every state —
 * its title, how long ago it last moved, a lit dot while a tab is answering it and
 * a word when calls in it wait on them. Renaming happens in the row's own line;
 * deleting asks first. Nothing ever unfolds under a row.
 *
 * `grouped` is the server page's list of every chat, under what each is about.
 */
export function AgentChatList({
  chats,
  error,
  activeId,
  grouped,
  onOpen,
  onNew,
  onDeleted,
  environmentOf,
}: {
  chats: AgentChatSummary[] | undefined
  error?: Error
  activeId: string
  grouped?: boolean
  onOpen: (chat: AgentChatSummary) => void
  onNew: () => void
  /** A chat deleted, for a page whose address names it. */
  onDeleted?: (chat: AgentChatSummary) => void
  /** A word for a chat from another environment of the same deployment. */
  environmentOf?: (chat: AgentChatSummary) => string | undefined
}) {
  const { confirm, dialog } = useConfirm()
  // A chat this tab is answering is lit before the list has read it again.
  const sessions = useAgentSessions()
  const answering = new Set(sessions.filter(agentBusy).map(agentChatId).filter(Boolean))
  const arrived = useArrivals((chats ?? []).map((chat) => chat.id))
  const remove = (chat: AgentChatSummary) =>
    confirm({
      title: "Delete this chat",
      confirmLabel: "Delete chat",
      subject: {
        mark: <ScopeGlyph chat={chat} />,
        name: chat.title || "Untitled chat",
        facts: chatFacts(chat),
      },
      description: (
        <p>
          What was asked and answered in it goes. What it changed stays changed, and stays in the
          audit log.
        </p>
      ),
      action: async () => {
        await deleteAgentChat(chat.id)
      },
      onDone: () => onDeleted?.(chat),
    })
  const groups: ChatGroup[] = grouped
    ? groupChats(chats ?? [])
    : [{ key: "all", label: "", icon: Servers, chats: chats ?? [] }]
  return (
    <section data-slot="agent-chats-list" aria-label="Chats" className="space-y-1.5">
      <div className="flex h-7 items-center justify-between gap-2">
        <h2 className="eyebrow">Chats</h2>
        <Button
          size="xs"
          variant="ghost"
          className="text-muted-foreground"
          data-slot="agent-new-chat"
          onClick={onNew}
        >
          <Plus />
          New chat
        </Button>
      </div>
      {chats === undefined ? (
        error ? (
          <p className="text-hint text-muted-foreground">
            {error instanceof ApiError && error.status === 404
              ? "This server keeps no chats yet."
              : "The chats could not be read."}
          </p>
        ) : (
          <div aria-hidden className="space-y-1">
            {[0, 1, 2].map((at) => (
              <Skeleton key={at} className="h-9 w-full rounded-md" />
            ))}
          </div>
        )
      ) : chats.length === 0 ? (
        <p className="text-hint text-muted-foreground">
          No chats yet. What you ask is kept here, to read again or carry on.
        </p>
      ) : (
        groups.map((group) => (
          <div key={group.key} className="space-y-0.5">
            {group.label && (
              <p className="flex h-7 min-w-0 items-center gap-1.5 px-2 pt-1 text-hint text-muted-foreground">
                <group.icon aria-hidden className="size-3.5 shrink-0" />
                <span className="truncate">{group.label}</span>
              </p>
            )}
            <ul className="space-y-0.5">
              {group.chats.map((chat) => (
                <ChatRow
                  key={chat.id}
                  chat={chat}
                  active={chat.id === activeId}
                  running={chat.running || answering.has(chat.id)}
                  arrived={arrived.has(chat.id)}
                  environment={environmentOf?.(chat)}
                  onOpen={() => onOpen(chat)}
                  onDelete={() => remove(chat)}
                />
              ))}
            </ul>
          </div>
        ))
      )}
      {dialog}
    </section>
  )
}

const KIND_ICON: Record<AgentChatSummary["scope"]["kind"], Icon> = {
  server: Servers,
  deployment: Rocket,
  draft: SettingsSliders,
  database: Database,
}

/** What a chat is about, in a few words: the page it was begun from, else its subject. */
const chatFacts = (chat: AgentChatSummary) =>
  chat.scope.about?.title ? `About ${chat.scope.about.title}` : chat.scope.name

function ScopeGlyph({ chat }: { chat: AgentChatSummary }) {
  const Glyph = KIND_ICON[chat.scope.kind] ?? Servers
  return (
    <span className="grid size-8 place-items-center rounded-lg border border-hairline bg-surface-header">
      <Glyph aria-hidden className="size-4 text-muted-foreground" />
    </span>
  )
}

interface ChatGroup {
  key: string
  label: string
  icon: Icon
  chats: AgentChatSummary[]
}

/**
 * Every chat under what it is about: the server's first, then each deployment's and
 * each connection's under its name, and new deployments' last.
 */
function groupChats(chats: AgentChatSummary[]): ChatGroup[] {
  const groups = new Map<string, ChatGroup>()
  for (const chat of chats) {
    const { scope } = chat
    const key =
      scope.kind === "server"
        ? "server"
        : scope.kind === "deployment"
          ? `deployment.${scope.deployment}`
          : scope.kind === "database"
            ? `database.${scope.database}`
            : "drafts"
    const label =
      scope.kind === "server"
        ? "This server"
        : scope.kind === "deployment" || scope.kind === "database"
          ? scope.name
          : "New deployments"
    let group = groups.get(key)
    if (!group) {
      group = { key, label, icon: KIND_ICON[scope.kind] ?? Servers, chats: [] }
      groups.set(key, group)
    }
    group.chats.push(chat)
  }
  // The server's first and new deployments' last; the rest keep the order their
  // newest chat put them in.
  const rank = (group: ChatGroup) => (group.key === "server" ? 0 : group.key === "drafts" ? 2 : 1)
  return [...groups.values()].sort((a, b) => rank(a) - rank(b))
}

function ChatRow({
  chat,
  active,
  running,
  arrived,
  environment,
  onOpen,
  onDelete,
}: {
  chat: AgentChatSummary
  active: boolean
  running: boolean
  arrived: boolean
  environment?: string
  onOpen: () => void
  onDelete: () => void
}) {
  const [renaming, setRenaming] = useState(false)
  // Choosing Rename hands the focus to the field, not back to the menu's button.
  const toField = useRef(false)
  const title = chat.title || "Untitled chat"
  return (
    <li
      data-slot="agent-chat-row"
      data-chat={chat.id}
      data-running={running || undefined}
      data-waiting={chat.waiting || undefined}
      aria-current={active ? "true" : undefined}
      className={cn(
        "group/row relative flex h-9 min-w-0 items-center rounded-md transition-colors",
        active ? "bg-accent" : "hover:bg-row-hover",
        arrived && "animate-rise",
      )}
    >
      {renaming ? (
        <RenameField
          title={title}
          onDone={async (next) => {
            setRenaming(false)
            if (!next || next === chat.title) return
            try {
              await renameAgentChat(chat.id, next)
            } catch (err) {
              notify.error("The chat could not be renamed", err)
            }
          }}
        />
      ) : (
        <button
          type="button"
          onClick={onOpen}
          // The page a chat was begun from is said on a hover, not on a second line.
          title={chat.scope.about?.title ? chatFacts(chat) : undefined}
          className="flex h-full min-w-0 flex-1 items-center gap-2 rounded-md pr-1 pl-2 text-left focus-ring-inset"
        >
          <span aria-hidden className="relative flex size-1.5 shrink-0">
            <AnimatePresence initial={false}>
              {running && (
                <motion.span
                  key="live"
                  className="absolute inset-0 flex"
                  initial={{ opacity: 0, scale: 0.5 }}
                  animate={{ opacity: 1, scale: 1 }}
                  exit={{ opacity: 0, scale: 0.5 }}
                  transition={{ duration: 0.18 }}
                >
                  <span className="absolute inset-0 animate-breathe rounded-full bg-signal" />
                  <span className="relative size-1.5 rounded-full bg-signal" />
                </motion.span>
              )}
            </AnimatePresence>
          </span>
          <span className="min-w-0 flex-1 truncate text-body">{title}</span>
          {running && <span className="sr-only">, answering now</span>}
          {chat.waiting && !running && <Tag tone="warning">Waits</Tag>}
          {environment && <Tag>{environment}</Tag>}
          <span
            className="numeric shrink-0 text-hint text-muted-foreground"
            title={timestamp(chat.updatedAt)}
          >
            {shortAgo(chat.updatedAt)}
          </span>
        </button>
      )}
      {!renaming && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={`More for ${title}`}
              className={cn("size-7 shrink-0 text-muted-foreground", rowReveal("row"))}
            >
              <MoreHorizontal className="size-3.5" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            align="end"
            onCloseAutoFocus={(event) => {
              if (toField.current) event.preventDefault()
              toField.current = false
            }}
          >
            <DropdownMenuItem
              onSelect={() => {
                toField.current = true
                setRenaming(true)
              }}
            >
              <Pencil />
              Rename
            </DropdownMenuItem>
            <DropdownMenuItem variant="destructive" disabled={running} onSelect={onDelete}>
              <Trash />
              Delete
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </li>
  )
}

/** "now", "5m", "3h", "2d": how long ago, in the width of a column of them. */
function shortAgo(iso: string) {
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return ""
  const minutes = Math.floor(Math.max(0, Date.now() - then) / 60_000)
  if (minutes < 1) return "now"
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days}d`
  return new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric" })
}

/** The title in the row's own line, the same height, saved on Enter or on leaving it. */
function RenameField({ title, onDone }: { title: string; onDone: (next: string) => void }) {
  const [value, setValue] = useState(title)
  const [done, setDone] = useState(false)
  const finish = (next: string) => {
    if (done) return
    setDone(true)
    onDone(next.trim())
  }
  return (
    <input
      // The row's line becomes the field; the person has just asked to type here.
      autoFocus
      aria-label="Chat title"
      value={value}
      maxLength={120}
      onFocus={(event) => event.currentTarget.select()}
      onChange={(event) => setValue(event.target.value)}
      onBlur={() => finish(value)}
      onKeyDown={(event) => {
        if (event.key === "Enter") {
          event.preventDefault()
          finish(value)
        } else if (event.key === "Escape") {
          event.preventDefault()
          finish("")
        }
      }}
      className="mx-1 h-7 min-w-0 flex-1 rounded-md border border-input bg-card px-2 text-body focus-ring"
    />
  )
}
