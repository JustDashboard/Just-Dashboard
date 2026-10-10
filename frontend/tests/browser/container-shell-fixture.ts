import type { Page } from "@playwright/test"

/**
 * A container's exec socket, answering as a shell inside it would.
 *
 * `XtermPane` speaks raw terminal bytes in binary frames both ways and JSON
 * control frames from the browser (resize); the exec handler writes every
 * frame that is not a resize straight into the container's stdin. So this
 * plays the far end of that: a prompt as the account the query asked for —
 * the image's own when there is no `user`, root when `user=root` — the echo of
 * what is typed, and the output of the read-only commands the Shell tab
 * offers, then the prompt again.
 *
 * An unanswered exec socket is what drew the tab as "— disconnected —": the
 * production server the specs run against does not upgrade `/api`, so the
 * socket closed at once.
 */
export async function mockContainerExec(
  page: Page,
  options: {
    /** The image's own account, when it declares one. */
    user?: string
    workdir?: string
    /**
     * Force xterm's DOM renderer. A headless Chromium on a busy host draws
     * WebGL through a software rasteriser, and a context it loses mid-frame
     * is a pane of whatever the canvas last held; the DOM renderer is the
     * product's own fallback (`jd.terminal.renderer`), so a screenshot of it
     * is still the product.
     */
    dom?: boolean
  } = {},
) {
  const { user = "node", workdir = "/home/node", dom = true } = options
  if (dom) {
    await page.addInitScript(() => {
      try {
        window.localStorage.setItem("jd.terminal.renderer", "dom")
      } catch {
        // A storage-less context renders through WebGL, as a browser would.
      }
    })
  }

  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/[0-9a-f]+\/exec/, (socket) => {
    const url = new URL(socket.url())
    const id = url.pathname.split("/").at(-2) ?? ""
    const account = url.searchParams.get("user") ?? user
    const root = account === "root"
    const host = id.slice(0, 12)
    const prompt = () =>
      root
        ? `\x1b[1;31mroot\x1b[0m@${host}:\x1b[1;34m${workdir}\x1b[0m# `
        : `\x1b[1;32m${account}\x1b[0m@${host}:\x1b[1;34m${workdir.replace(/^\/home\/node/, "~")}\x1b[0m$ `
    const write = (text: string) => socket.send(Buffer.from(text.replace(/\n/g, "\r\n")))

    let line = ""
    socket.onMessage((message) => {
      // A resize, or any other control frame: not keystrokes.
      if (typeof message === "string") return
      for (const char of Buffer.from(message).toString("utf8")) {
        if (char === "\r") {
          write(`\n${OUTPUT[line.trim()]?.(account, host) ?? notFound(line)}${prompt()}`)
          line = ""
        } else if (char === "\x7f") {
          if (line) {
            line = line.slice(0, -1)
            write("\b \b")
          }
        } else if (char === "\x03") {
          line = ""
          write(`^C\n${prompt()}`)
        } else if (char >= " ") {
          line += char
          write(char)
        }
      }
    })

    // What `docker exec -it … sh` prints first: nothing but a prompt, after
    // the line a login shell would show.
    setTimeout(() => {
      write(`\x1b[90m# a shell inside ${host}, as ${account}\x1b[0m\n${prompt()}`)
      write("cat /etc/os-release | head -2")
      write(`\n${OUTPUT["cat /etc/os-release | head -2"](account, host)}${prompt()}`)
    }, 50)
  })
}

const notFound = (line: string) => {
  const program = line.trim().split(/\s+/)[0]
  return program ? `sh: ${program}: not found\n` : ""
}

const OUTPUT: Record<string, (account: string, host: string) => string> = {
  id: (account) =>
    account === "root"
      ? "uid=0(root) gid=0(root) groups=0(root),1(bin),2(daemon),3(sys),4(adm)\n"
      : `uid=1000(${account}) gid=1000(${account}) groups=1000(${account})\n`,
  "ls -la": (account) =>
    [
      "total 24",
      `drwxr-sr-x    1 ${account}     ${account}          4096 Oct  8 09:12 \x1b[1;34m.\x1b[0m`,
      "drwxr-xr-x    1 root     root          4096 Oct  2 21:36 \x1b[1;34m..\x1b[0m",
      `drwxr-xr-x    5 ${account}     ${account}          4096 Oct  8 09:12 \x1b[1;34m.cache\x1b[0m`,
      `drwxr-xr-x    6 ${account}     ${account}          4096 Oct  8 14:51 \x1b[1;34m.n8n\x1b[0m`,
      `-rw-r--r--    1 ${account}     ${account}           214 Oct  2 21:36 .npmrc`,
      "",
    ].join("\n"),
  "ps aux": () =>
    [
      "PID   USER     TIME  COMMAND",
      "    1 node      0:04 tini -- /docker-entrypoint.sh",
      "    7 node     41:12 node /usr/local/bin/n8n",
      "   58 node      2:30 node /usr/local/lib/node_modules/n8n/task-runner",
      "  311 node      0:00 sh",
      "  318 node      0:00 ps aux",
      "",
    ].join("\n"),
  "df -h": () =>
    [
      "Filesystem                Size      Used Available Use% Mounted on",
      "overlay                  78.2G     51.4G     26.8G  66% /",
      "tmpfs                    64.0M         0     64.0M   0% /dev",
      "/dev/sda1                78.2G     51.4G     26.8G  66% /home/node/.n8n",
      "/dev/sda1                78.2G     51.4G     26.8G  66% /files",
      "tmpfs                     7.8G         0      7.8G   0% /tmp",
      "",
    ].join("\n"),
  "cat /etc/os-release": () =>
    [
      'NAME="Alpine Linux"',
      "ID=alpine",
      "VERSION_ID=3.21.3",
      'PRETTY_NAME="Alpine Linux v3.21"',
      'HOME_URL="https://alpinelinux.org/"',
      "",
    ].join("\n"),
  "cat /etc/os-release | head -2": () => ['NAME="Alpine Linux"', "ID=alpine", ""].join("\n"),
}
