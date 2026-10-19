/**
 * The live room every editor open on a drawing joins (`/boards/{id}/live`).
 * The server only relays: it keeps no scene, so a peer that joins is sent the
 * scene by the others, and saving stays each editor's own audited PUT.
 */

export type LivePeer = { id: string; user: string; name: string; canWrite: boolean }

export type LiveMessage =
  | { type: "hello"; you: string; peers: LivePeer[] }
  | { type: "joined"; peer: LivePeer }
  | { type: "left"; id: string }
  | { type: "scene"; from: string; elements: LiveElement[] }
  | {
      type: "pointer"
      from: string
      pointer: { x: number; y: number; tool: "pointer" | "laser" }
      button: "up" | "down"
      selectedElementIds?: Record<string, true>
    }
  | { type: "idle"; from: string; state: "active" | "idle" | "away" }
  | { type: "files"; from: string; ids: string[] }
  | { type: "deleted" }
  | { type: "error"; message: string }

/** Just what the relay protocol reads of an element; the rest travels untouched. */
export type LiveElement = { id: string; version: number }

/**
 * The elements a peer has not been sent at their present version. Excalidraw
 * bumps an element's version on every change, deletion included, so the
 * version is the whole of what decides whether it goes out again; remembering
 * the versions received keeps a peer from echoing back what it was just sent.
 */
export function unsent<T extends LiveElement>(
  elements: readonly T[],
  sent: ReadonlyMap<string, number>,
): T[] {
  return elements.filter((element) => sent.get(element.id) !== element.version)
}

export function remember(sent: Map<string, number>, elements: readonly LiveElement[]) {
  for (const element of elements) sent.set(element.id, element.version)
}
