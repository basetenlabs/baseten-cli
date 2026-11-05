import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const section = "baseten_git_context";

export default function (pi: ExtensionAPI) {
	let context = "";
	pi.on("session_start", async (_event, ctx) => {
		context = "";
		const git = (...args: string[]) => pi.exec("git", args, { cwd: ctx.cwd });
		const inside = await git("rev-parse", "--is-inside-work-tree");
		if (inside.code !== 0 || inside.stdout.trim() !== "true") {
			return;
		}
		const [origin, branch, commit] = await Promise.all([
			git("remote", "get-url", "origin"),
			git("branch", "--show-current"),
			git("rev-parse", "--verify", "HEAD"),
		]);
		const value = (r: { code: number; stdout: string }) => (r.code === 0 ? r.stdout.trim() : "");
		context = `repo=${value(origin).replace(/\/\/[^@/]*@/, "//")} branch=${value(branch)} commit=${value(commit)}`;
	});
	pi.on("before_agent_start", (event) => {
		if (context && event.systemPromptOptions?.sections) {
			event.systemPromptOptions.sections[section] = context;
		}
	});
}
