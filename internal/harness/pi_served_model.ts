import type { AssistantMessage } from "@earendil-works/pi-ai";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Text } from "@earendil-works/pi-tui";

const intelligentRoutes = new Map<string, string>(Object.entries({}));
const entryType = "baseten-served-model";
const offValues = ["0", "f", "F", "false", "FALSE", "False"];

function servedModelStatus(message: AssistantMessage | undefined): string | undefined {
	const route = message?.provider === "baseten-harness" ? intelligentRoutes.get(message.model) : undefined;
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

export default function (pi: ExtensionAPI) {
	if (offValues.includes(process.env.BASETEN_PI_SERVED_MODEL ?? "")) {
		return;
	}
	pi.registerEntryRenderer<{ label?: string }>(entryType, (entry, _options, theme) =>
		entry.data?.label ? new Text(theme.fg("dim", entry.data.label), 1, 0) : undefined,
	);
	pi.on("agent_before_settle", (event, ctx) => {
		const label = servedModelStatus(latestAssistantMessage(ctx));
		return label ? { entries: [...event.entries, { type: "custom", customType: entryType, data: { label } }] } : undefined;
	});
}
