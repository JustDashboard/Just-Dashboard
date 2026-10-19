"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import type { ExcalidrawElement } from "@excalidraw/excalidraw/element/types"
import type {
  Collaborator,
  ExcalidrawImperativeAPI,
  ExcalidrawProps,
  SocketId,
} from "@excalidraw/excalidraw/types"
import { remember, unsent, type LiveMessage, type LivePeer } from "@/lib/board-live"
import { useSocket } from "@/hooks/use-socket"

// A drag sends a scene frame per element version; a frame every 50 ms is
// smooth to watch and keeps a long stroke from becoming a thousand frames.
const SCENE_EVERY_MS = 50
const POINTER_EVERY_MS = 40

/**
 * The drawing's live room: who else has it open, their pointers, and every
 * change as it is made. Joining needs nothing but `read`; only an editor with
 * `service.control` sends changes, and the server refuses a scene from anyone
 * else.
 */
export function useBoardLive({
  boardId,
  api,
  canWrite,
  onScene,
  onFiles,
  onDeleted,
}: {
  boardId: number
  api: ExcalidrawImperativeAPI | null
  canWrite: boolean
  onScene: (elements: readonly ExcalidrawElement[]) => void
  onFiles: (ids: string[]) => void
  onDeleted: () => void
}) {
  const [joined, setJoined] = useState(false)
  const [peers, setPeers] = useState<LivePeer[]>([])
  const peersRef = useRef<LivePeer[]>([])
  const sentRef = useRef(new Map<string, number>())
  const collaboratorsRef = useRef(new Map<SocketId, Collaborator>())
  const sceneTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const pointerAtRef = useRef(0)
  const buttonRef = useRef<"up" | "down">("up")
  const sendRef = useRef<(payload: unknown) => boolean>(() => false)
  const handlers = useRef({ onScene, onFiles, onDeleted })
  useEffect(() => {
    handlers.current = { onScene, onFiles, onDeleted }
  })

  const showCollaborators = useCallback(() => {
    api?.updateScene({ collaborators: new Map(collaboratorsRef.current) })
  }, [api])

  const setRoom = useCallback((next: LivePeer[]) => {
    peersRef.current = next
    setPeers(next)
  }, [])

  const sendScene = useCallback(() => {
    if (!canWrite || !api || peersRef.current.length === 0) return
    const changed = unsent(api.getSceneElementsIncludingDeleted(), sentRef.current)
    if (changed.length > 0 && sendRef.current({ type: "scene", elements: changed })) {
      remember(sentRef.current, changed)
    }
  }, [api, canWrite])

  /** Sends what changed since the last frame; called on every change, sent at most every 50 ms. */
  const broadcast = useCallback(() => {
    if (!canWrite || peersRef.current.length === 0 || sceneTimerRef.current) return
    sceneTimerRef.current = setTimeout(() => {
      sceneTimerRef.current = null
      sendScene()
    }, SCENE_EVERY_MS)
  }, [canWrite, sendScene])

  const onPointerUpdate: NonNullable<ExcalidrawProps["onPointerUpdate"]> = useCallback(
    (payload) => {
      if (peersRef.current.length === 0) return
      // A press or a release always goes out; movement is thinned.
      const now = performance.now()
      if (payload.button === buttonRef.current && now - pointerAtRef.current < POINTER_EVERY_MS)
        return
      pointerAtRef.current = now
      buttonRef.current = payload.button
      sendRef.current({
        type: "pointer",
        pointer: payload.pointer,
        button: payload.button,
        selectedElementIds: api?.getAppState().selectedElementIds,
      })
    },
    [api],
  )

  const announceFiles = useCallback((ids: string[]) => {
    if (ids.length > 0 && peersRef.current.length > 0) sendRef.current({ type: "files", ids })
  }, [])

  const socket = useSocket(`/boards/${boardId}/live`, {
    enabled: api !== null,
    onMessage: (envelope) => {
      const message = envelope as unknown as LiveMessage
      switch (message.type) {
        case "hello":
          setRoom(message.peers)
          setJoined(true)
          // Whatever was drawn while the socket was down goes to everyone.
          sentRef.current.clear()
          if (message.peers.length > 0) sendScene()
          return
        case "joined":
          setRoom([...peersRef.current.filter((peer) => peer.id !== message.peer.id), message.peer])
          // The server keeps no scene: a newcomer has the last save, and the
          // rest from whoever is already here.
          if (canWrite && api) {
            const elements = api.getSceneElementsIncludingDeleted()
            if (sendRef.current({ type: "scene", to: message.peer.id, elements })) {
              remember(sentRef.current, elements)
            }
          }
          return
        case "left":
          setRoom(peersRef.current.filter((peer) => peer.id !== message.id))
          collaboratorsRef.current.delete(message.id as SocketId)
          showCollaborators()
          return
        case "scene":
          handlers.current.onScene(message.elements as unknown as ExcalidrawElement[])
          remember(sentRef.current, message.elements)
          return
        case "pointer": {
          const peer = peersRef.current.find((candidate) => candidate.id === message.from)
          const id = message.from as SocketId
          collaboratorsRef.current.set(id, {
            ...collaboratorsRef.current.get(id),
            id: message.from,
            socketId: id,
            username: peer?.name || peer?.user,
            pointer: message.pointer,
            button: message.button,
            selectedElementIds: message.selectedElementIds,
          })
          showCollaborators()
          return
        }
        case "idle": {
          const id = message.from as SocketId
          const current = collaboratorsRef.current.get(id)
          if (!current) return
          collaboratorsRef.current.set(id, {
            ...current,
            userState: message.state as Collaborator["userState"],
          })
          showCollaborators()
          return
        }
        case "files":
          handlers.current.onFiles(message.ids)
          return
        case "deleted":
          handlers.current.onDeleted()
          return
      }
    },
    onClose: () => {
      setJoined(false)
      setRoom([])
      collaboratorsRef.current.clear()
      showCollaborators()
    },
  })
  useEffect(() => {
    sendRef.current = socket.send
  })

  useEffect(() => {
    if (!joined) return
    const away = () =>
      sendRef.current({
        type: "idle",
        state: document.visibilityState === "hidden" ? "away" : "active",
      })
    document.addEventListener("visibilitychange", away)
    return () => document.removeEventListener("visibilitychange", away)
  }, [joined])

  useEffect(
    () => () => {
      if (sceneTimerRef.current) clearTimeout(sceneTimerRef.current)
    },
    [],
  )

  return { joined, peers, broadcast, onPointerUpdate, announceFiles }
}
