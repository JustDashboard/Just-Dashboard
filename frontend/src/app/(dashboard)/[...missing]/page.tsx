import { notFound } from "next/navigation"

/**
 * An address no page claims. Without this catch-all Next answers from the root,
 * outside this layout; through it the 404 is drawn by `(dashboard)/not-found`,
 * inside the shell, and someone signed out is sent to sign in like anywhere else.
 */
export default function MissingAddress() {
  notFound()
}
