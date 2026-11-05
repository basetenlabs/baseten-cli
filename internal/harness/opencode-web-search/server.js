const enabled = !["0", "f", "F", "FALSE", "false", "False"].includes(process.env.BASETEN_OPENCODE_WEB_SEARCH)

export default {
  id: "baseten-web-search",
  async setup(ctx) {
    if (!enabled) return
    await ctx.tool.transform((tools) => {
      tools.remove("websearch")
      tools.add({
        name: "baseten_web_search",
        options: { codemode: false },
        description: "Search the web for up-to-date information.",
        input: {
          type: "object",
          properties: { query: { type: "string" } },
          required: ["query"],
          additionalProperties: false,
        },
        execute: async () => {
          throw new Error(
            "Web search runs on Baseten's servers and is unavailable for this model or endpoint; continue without it.",
          )
        },
      })
    })
  },
}
