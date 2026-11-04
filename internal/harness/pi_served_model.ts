import type { AssistantMessage } from "@earendil-works/pi-ai";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

const routers = new Map<string, string>(Object.entries({}));
const statusKey = "baseten-served-model";
const offValues = ["0", "f", "F", "false", "FALSE", "False"];

function servedModelStatus(message: AssistantMessage | undefined): string | undefined {
	const route = message?.provider === "baseten-harness" ? routers.get(message.model) : undefined;
	return route && message?.responseModel ? `${route} → ${message.responseModel}` : undefined;
}

function latestAssistantMessage(ctx: ExtensionContext): AssistantMessage | undefined {
	for (const entry of ctx.sessionManager.getBranch().toReversed()) {
		if (entry.type === "message" && entry.message.role === "assistant") {
			return entry.message;
		}
	}
	return undefined;
}

function showServedModel(ctx: ExtensionContext, message: AssistantMessage | undefined) {
	ctx.ui.setStatus(statusKey, servedModelStatus(message));
}

export default function (pi: ExtensionAPI) {
	if (offValues.includes(process.env.BASETEN_PI_SERVED_MODEL ?? "")) {
		return;
	}
	pi.on("message_end", (event, ctx) => {
		if (event.message.role === "assistant") {
			showServedModel(ctx, event.message);
		}
	});
	pi.on("session_start", (_event, ctx) => showServedModel(ctx, latestAssistantMessage(ctx)));
	pi.on("session_tree", (_event, ctx) => showServedModel(ctx, latestAssistantMessage(ctx)));
}
