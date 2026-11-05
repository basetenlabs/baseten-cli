import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const section = "baseten_git_context";

function sanitize(url: string) {
	let prefix = "";
	let m;
	while ((m = /^[A-Za-z0-9+.-]+::/.exec(url))) {
		prefix += m[0];
		url = url.slice(m[0].length);
	}
	if (prefix && /\s/.test(url)) return "";
	const sep = url.indexOf("://");
	if (sep >= 0) {
		const rest = url.slice(sep + 3);
		const end = rest.search(/[/?#]|$/);
		const authority = rest.slice(0, end);
		const path = rest.slice(end).split(/[?#]/)[0];
		const at = authority.lastIndexOf("@");
		if (at < 0) return prefix + url.slice(0, sep + 3) + authority + path;
		const scheme = url.slice(0, sep);
		const exempt = ["ssh", "git+ssh", "ssh+git"].includes(scheme) && authority.slice(0, at).split(":")[0] === "git";
		return prefix + scheme + "://" + (exempt ? "git@" : "") + authority.slice(at + 1) + path;
	}
	const scp = /^([^/]*)@(\[[^\]@]*\]|[^/:@]*):/.exec(url);
	if (!scp) return prefix + url;
	return prefix + (scp[1].split(":")[0] === "git" ? "git@" : "") + url.slice(scp[1].length + 1);
}

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
		context = `repo=${sanitize(value(origin))} branch=${value(branch)} commit=${value(commit)}`;
	});
	pi.on("before_agent_start", (event) => {
		if (context && event.systemPromptOptions?.sections) {
			event.systemPromptOptions.sections[section] = context;
		}
	});
}
