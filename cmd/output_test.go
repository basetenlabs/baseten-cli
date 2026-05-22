package cmd_test

import (
	"reflect"
	"testing"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/invopop/jsonschema"
	"github.com/stretchr/testify/require"
)

// TestLeafJSONSchemas walks the command tree and confirms every leaf's
// JSONOutputType produces a JSON schema via invopop/jsonschema. JSONAny and
// JSONUndefined are sentinels that --help-output renders specially, so they
// are skipped here.
//
// Schemas are emitted without field descriptions for now: godoc comments are
// not visible to runtime reflection, and we rely on TextDescription prose
// instead of per-field docs. Revisit if we want richer field-level docs (see
// invopop's AddGoComments or a go:generate-based pipeline).
func TestLeafJSONSchemas(t *testing.T) {
	jsonAny := reflect.TypeOf((cmd.JSONAny)(nil))
	jsonUndefined := reflect.TypeFor[cmd.JSONUndefined]()
	r := require.New(t)
	walkLeaves(t, cmd.Root, "", func(path string, c cmd.Command) {
		t.Helper()
		spec := c.Output
		r.NotNil(spec, "leaf %q missing Output", path)
		typ := spec.JSONOutputType()
		if typ == jsonAny || typ == jsonUndefined {
			return
		}
		schema := (&jsonschema.Reflector{}).ReflectFromType(typ)
		r.NotNil(schema, "leaf %q produced nil schema for %v", path, typ)
	})
}

// TestFlagGroupPriorities walks every leaf's flag metadata and asserts that
// no two flags in the same group declare conflicting group-pri values. A
// group's priority is meant to be declared once (typically on one field of a
// shared embedded struct) and inherited by every flag in that group; this
// test is the registration-time safety net for that contract.
func TestFlagGroupPriorities(t *testing.T) {
	walkLeaves(t, cmd.Root, "", func(path string, c cmd.Command) {
		t.Helper()
		pris := map[string]int{}
		for _, f := range c.LoadFlags() {
			if f.GroupPri == 0 {
				continue
			}
			if existing, ok := pris[f.Group]; ok && existing != f.GroupPri {
				t.Fatalf("leaf %q group %q has conflicting group-pri: %d vs %d", path, f.Group, existing, f.GroupPri)
			}
			pris[f.Group] = f.GroupPri
		}
	})
}

func walkLeaves(t *testing.T, c cmd.Command, parentPath string, fn func(path string, c cmd.Command)) {
	t.Helper()
	path := c.Name
	if parentPath != "" {
		path = parentPath + " " + c.Name
	}
	if len(c.Children) == 0 {
		fn(path, c)
		return
	}
	for _, child := range c.Children {
		walkLeaves(t, child, path, fn)
	}
}
