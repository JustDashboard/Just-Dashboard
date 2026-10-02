import { get, type Query } from "@/lib/api"

/**
 * A read whose answer is checked before the page is handed it.
 *
 * The home draws a dozen answers at once, and each block trusts the shape of
 * its own: a list it maps, a record it reads a field of. An answer in another
 * shape — a server a release behind, a route that answers a list where a
 * report was expected — would otherwise be a thrown render and the whole page
 * gone for one block's sake. Checked here, it is that block's "could not
 * read", with its retry, and every other block is untouched.
 */
export async function read<T>(
  path: string,
  shaped: (answer: T) => boolean,
  query?: Query,
  signal?: AbortSignal,
): Promise<T> {
  const answer = await get<T>(path, query, signal)
  if (answer === null || typeof answer !== "object" || !shaped(answer)) {
    throw new Error("The server answered in a form this page does not read.")
  }
  return answer
}

/** A plain record: what a report or a group of readings arrives as. */
export function record(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value)
}
