import routes from "./routes.json" with { type: "json" }
import { ServedModel, enabled } from "./shared.js"

const routers = new Map(Object.entries(routes.routers))

function chunkModel(line) {
  if (!line.startsWith("data:")) return ""
  try {
    const model = JSON.parse(line.slice(5)).model
    return typeof model === "string" ? model : ""
  } catch {
    return ""
  }
}

function watchServedModel(response, found) {
  const decoder = new TextDecoder()
  let pending = ""
  let done = false
  const body = response.body.pipeThrough(
    new TransformStream({
      transform(chunk, controller) {
        controller.enqueue(chunk)
        if (done) return
        const lines = (pending + decoder.decode(chunk, { stream: true })).split("\n")
        pending = lines.pop()
        const model = lines.map(chunkModel).find(Boolean)
        if (model) {
          done = true
          found(model)
        }
      },
    }),
  )
  return new Response(body, { status: response.status, statusText: response.statusText, headers: response.headers })
}

export default {
  id: "baseten-served-model",
  async setup(ctx) {
    if (!enabled) return
    const registration = await ctx.rpc.register(ServedModel, {})
    const latest = new Map()
    await ctx.session.hook("model.request", (event) => {
      if (event.kind === "primary") latest.delete(event.sessionID)
    })
    await ctx.session.hook(
      "http.response",
      (event) => {
        if (event.kind !== "primary") return
        const display = routers.get(event.model.id)
        if (!display || !event.response.ok || !event.response.body) return
        event.response = watchServedModel(event.response, (model) => latest.set(event.sessionID, { route: event.model.id, label: `${display} → ${model}` }))
      },
      { providerID: routes.provider },
    )
    await ctx.session.hook("experimental.idle", (event) => {
      const served = latest.get(event.sessionID)
      latest.delete(event.sessionID)
      if (served) registration.events.emit("served", { sessionID: event.sessionID, ...served }).catch(() => {})
    })
  },
}
