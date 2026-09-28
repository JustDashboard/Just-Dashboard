/** One database on the server, as `GET /databases/{id}/schemas` lists it. */
export type Database = {
  name: string
  size?: number
  owner?: string
  encoding?: string
}
