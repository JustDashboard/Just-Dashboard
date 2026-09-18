import { Outlet, Scripts } from "react-router"
export default function Root() {
  return (
    <html>
      <body>
        <Outlet />
        <Scripts />
      </body>
    </html>
  )
}
