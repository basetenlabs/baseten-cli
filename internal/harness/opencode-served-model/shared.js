export const enabled = !["0", "f", "F", "FALSE", "false", "False"].includes(process.env.BASETEN_OPENCODE_SERVED_MODEL)

export const ServedModel = {
  id: "baseten-served-model",
  methods: {},
  events: {
    served: {
      schema: {
        type: "object",
        properties: {
          sessionID: { type: "string" },
          route: { type: "string" },
          label: { type: "string" },
        },
        required: ["sessionID", "route", "label"],
        additionalProperties: false,
      },
    },
  },
}
