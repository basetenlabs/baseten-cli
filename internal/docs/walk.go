package docs

import (
	cmdpkg "github.com/basetenlabs/baseten-cli/cmd"
)

// WalkCommand converts a declarative cmdpkg.Command into the JSON-emittable
// Command at the given parent path. The returned node's Path is parentPath
// with c.Name appended.
func WalkCommand(parentPath []string, c cmdpkg.Command) Command {
	path := append(append([]string{}, parentPath...), c.Name)
	out := Command{
		Name:               c.Name,
		Path:               path,
		Summary:            c.Summary,
		Description:        c.Description,
		IsLeaf:             len(c.Children) == 0,
		ArgsUsage:          c.ArgsUsage,
		ExactArgs:          c.ExactArgs,
		MaxArgs:            c.MaxArgs,
		DisableFlagParsing: c.DisableFlagParsing,
		Flags:              flagsFor(c),
	}
	applyOutput(&out, c.Output)
	for _, e := range c.Errors {
		out.Errors = append(out.Errors, ErrorEntry{
			Name:    e.Name,
			Code:    int(e.Code),
			Meaning: e.Meaning,
		})
	}
	for _, child := range c.Children {
		out.Children = append(out.Children, WalkCommand(path, child))
	}
	return out
}

func applyOutput(dst *Command, spec cmdpkg.CommandOutputSpec) {
	if spec == nil {
		return
	}
	dst.TextDescription = spec.Text()
	dst.JSONDescription = spec.JSON()
	dst.JSONArrayStreamed = spec.JSONArrayStreamedBool()
	if t := spec.JSONOutputType(); t != nil {
		dst.JSONOutputType = t.String()
	}
	for _, ex := range spec.ExampleList() {
		dst.Examples = append(dst.Examples, Example{Description: ex.Description, Command: ex.Command})
	}
	jq := spec.JQ()
	if jq.Command != "" || jq.Description != "" {
		dst.JQExample = &Example{Description: jq.Description, Command: jq.Command}
	}
}

func flagsFor(c cmdpkg.Command) []Flag {
	raw := c.LoadFlags()
	if len(raw) == 0 {
		return nil
	}
	out := make([]Flag, 0, len(raw))
	for _, f := range raw {
		out = append(out, Flag{
			Name:        f.Name,
			Short:       f.Short,
			Description: f.Desc,
			Default:     f.Default,
			Enum:        f.Enum,
			Required:    f.Required,
			Oneof:       f.Oneof,
			Type:        f.Type.String(),
			FieldName:   f.FieldName,
			Group:       f.Group,
			GroupPri:    f.GroupPri,
		})
	}
	return out
}

// Walk produces the full Schema for the given root command. cliVersion is
// embedded as-is (e.g. "v0.1.0", "dev"); generatedAt is the RFC3339 timestamp
// to embed.
func Walk(cliVersion, generatedAt string, root cmdpkg.Command) Schema {
	std := cmdpkg.StandardErrors()
	out := Schema{
		SchemaVersion: SchemaVersion,
		CLIVersion:    cliVersion,
		GeneratedAt:   generatedAt,
		Root:          WalkCommand(nil, root),
	}
	for _, e := range std {
		out.StandardErrors = append(out.StandardErrors, ErrorEntry{
			Name:    e.Name,
			Code:    int(e.Code),
			Meaning: e.Meaning,
		})
	}
	return out
}
