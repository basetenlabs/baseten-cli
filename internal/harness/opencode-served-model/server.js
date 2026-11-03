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
    const emit = (event, label) =>
      registration.events.emit("served", { sessionID: event.sessionID, route: event.model.id, label }).catch(() => {})
    await ctx.session.hook(
      "http.response",
      (event) => {
        if (event.kind !== "primary") return
        const display = routers.get(event.model.id)
        if (!display) return void emit(event, "")
        if (!event.response.ok || !event.response.body) return
        event.response = watchServedModel(event.response, (model) => emit(event, `${display} → ${model}`))
      },
      { providerID: routes.provider },
    )
  },
}
