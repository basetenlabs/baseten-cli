import routes from "./routes.json" with { type: "json" }
import { ServedModel, enabled } from "./shared.js"

export default {
  id: "baseten-served-model",
  setup(context) {
    if (!enabled) return
    return context.client.rpc(ServedModel).events.on("served", (event) => {
      const screen = context.ui.router.current()
      if (screen.type !== "session" || screen.sessionID !== event.data.sessionID) return
      const model = context.ui.model.current()
      if (!event.data.label || model?.providerID !== routes.provider || model.modelID !== event.data.route) return
      context.ui.toast.show({ title: "Served model", message: event.data.label.replace(" → ", "\n→ "), variant: "info", duration: 8000 })
    })
  },
}
