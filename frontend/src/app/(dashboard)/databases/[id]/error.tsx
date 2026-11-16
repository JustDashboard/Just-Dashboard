"use client"

import DashboardError from "@/app/(dashboard)/error"

/**
 * A page of a database that threw while rendering: the dashboard's own
 * boundary (`app/(dashboard)/error.tsx`), mounted again here.
 *
 * The boundary sits inside the database's layout, so what is lost is the one
 * page: the rail still lists the database's other pages and the header still
 * says which database this is. The dashboard-wide one above would have taken
 * both with it, and left the reader of a broken Data page with no way to its
 * Settings but the address bar. What it draws is the same block; it was a
 * copy of it, with an "All databases" button the rail already offers.
 */
export default DashboardError
