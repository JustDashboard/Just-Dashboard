/**
 * Hands the browser a file to save: a report, a log, a schema, a key.
 *
 * The link is put in the document before it is clicked (Firefox ignores a
 * click on a detached one) and taken out after, and the object URL is let go
 * a moment later rather than in the same tick: revoked at once, a slow
 * browser can start the download after its source is gone.
 *
 * There were three of these, with three argument orders (text first, name
 * first, name and blob), and swapped strings type-check. So the name always
 * comes first, as it does in the save dialog.
 */
export function saveBlob(filename: string, blob: Blob): void {
  const url = URL.createObjectURL(blob)
  const link = document.createElement("a")
  link.href = url
  link.download = filename
  link.style.display = "none"
  document.body.append(link)
  link.click()
  link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}

/** `saveBlob` for a text: a CSV is "text/csv", a report "application/json". */
export function saveText(filename: string, text: string, type = "text/plain"): void {
  saveBlob(filename, new Blob([text], { type }))
}
