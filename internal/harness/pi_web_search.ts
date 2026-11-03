import { Type } from "@earendil-works/pi-ai";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

const toolName = "baseten_web_search";
const offValues = ["0", "f", "F", "false", "FALSE", "False"];

function targetsToolBank(ctx: ExtensionContext): boolean {
	return ctx.model?.provider === "baseten-harness" && ctx.model?.api === "openai-completions";
}

export default function (pi: ExtensionAPI) {
	if (offValues.includes(process.env.BASETEN_PI_WEB_SEARCH ?? "")) {
		return;
	}
	pi.registerTool({
		name: toolName,
		label: "Web search",
		description: "Search the web for up-to-date information.",
		parameters: Type.Object({ query: Type.String() }),
		async execute() {
			return {
				content: [{ type: "text", text: "Web search runs on Baseten's servers and is unavailable for this model or endpoint; continue without it." }],
				details: undefined,
				isError: true,
			};
		},
	});
	const sync = (ctx: ExtensionContext) => {
		const active = pi.getActiveTools();
		const wanted = targetsToolBank(ctx);
		if (active.includes(toolName) === wanted) {
			return;
		}
		pi.setActiveTools(wanted ? [...active, toolName] : active.filter((name) => name !== toolName));
	};
	pi.on("session_start", (_event, ctx) => sync(ctx));
	pi.on("model_select", (_event, ctx) => sync(ctx));
}
