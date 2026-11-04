import { Show } from "solid-js"
import routes from "./routes.json" with { type: "json" }
import { ServedModel, enabled } from "./shared.js"

export default {
  id: "baseten-served-model",
  setup(context) {
    if (!enabled) return
    const [latest, update] = context.storage.memory("latest", { initial: {} })
    const unsubscribe = context.client.rpc(ServedModel).events.on("served", (event) => {
      update((draft) => {
        draft[event.data.sessionID] = event.data
      })
    })
    const label = (sessionID) => {
      const served = latest[sessionID]
      const model = context.ui.model.current()
      return served && model?.providerID === routes.provider && model.modelID === served.route ? served.label : ""
    }
    context.ui.slot({
      append: "prompt.footer.status",
      render: (input) => (
        <Show when={label(input.sessionID)}>
          <text fg={context.theme.text.muted} minWidth={0} wrapMode="none" truncate>
            {label(input.sessionID)}
          </text>
        </Show>
      ),
    })
    return unsubscribe
  },
}
