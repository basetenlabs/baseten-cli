import { execFile } from "node:child_process"

const sessions = new Map()

function git(cwd, args) {
  return new Promise((resolve) => {
    execFile("git", args, { cwd, windowsHide: true, timeout: 10000 }, (_error, stdout) => resolve(String(stdout ?? "").trim()))
  })
}

async function gitContext(cwd) {
  if ((await git(cwd, ["rev-parse", "--is-inside-work-tree"])) !== "true") return ""
  const [origin, branch, commit] = await Promise.all([
    git(cwd, ["remote", "get-url", "origin"]),
    git(cwd, ["branch", "--show-current"]),
    git(cwd, ["rev-parse", "--verify", "HEAD"]),
  ])
  return `repo=${sanitize(origin)} branch=${branch} commit=${commit}`
}

function sanitize(url) {
  let prefix = ""
  let m
  while ((m = /^[A-Za-z0-9+.-]+::/.exec(url))) {
    prefix += m[0]
    url = url.slice(m[0].length)
  }
  if (prefix && /\s/.test(url)) return ""
  const sep = url.indexOf("://")
  if (sep >= 0) {
    const rest = url.slice(sep + 3)
    const end = rest.search(/[/?#]|$/)
    const authority = rest.slice(0, end)
    const path = rest.slice(end).split(/[?#]/)[0]
    const at = authority.lastIndexOf("@")
    if (at < 0) return prefix + url.slice(0, sep + 3) + authority + path
    const scheme = url.slice(0, sep)
    const exempt = ["ssh", "git+ssh", "ssh+git"].includes(scheme) && authority.slice(0, at).split(":")[0] === "git"
    return prefix + scheme + "://" + (exempt ? "git@" : "") + authority.slice(at + 1) + path
  }
  const scp = /^([^/]*)@(\[[^\]@]*\]|[^/:@]*):/.exec(url)
  if (!scp) return prefix + url
  return prefix + (scp[1].split(":")[0] === "git" ? "git@" : "") + url.slice(scp[1].length + 1)
}

function sessionContext(sessionID, cwd) {
  if (!sessions.has(sessionID)) sessions.set(sessionID, gitContext(cwd))
  return sessions.get(sessionID)
}

export default {
  id: "baseten-git-context",
  async server(input) {
    return {
      async "experimental.chat.system.transform"(event, output) {
        if (!event.sessionID) return
        const line = await sessionContext(event.sessionID, input.directory)
        if (line) output.system.push(line)
      },
    }
  },
  async setup(ctx) {
    await ctx.session.hook("context", async (event) => {
      const line = await sessionContext(event.sessionID, ctx.location.directory)
      if (line) event.system.push({ type: "text", text: line })
    })
  },
}
