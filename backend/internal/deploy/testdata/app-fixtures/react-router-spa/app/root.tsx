import { Links, Meta, Outlet, Scripts } from "react-router"
export function Layout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <head>
        <Meta />
        <Links />
      </head>
      <body>
        {children}
        <p>{import.meta.env.VITE_API_URL}</p>
        <Scripts />
      </body>
    </html>
  )
}
export default function Root() {
  return <Outlet />
}
export function HydrateFallback() {
  return <p>spa shell loading</p>
}
