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
  return `repo=${origin.replace(/\/\/[^@/]*@/, "//")} branch=${branch} commit=${commit}`
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
