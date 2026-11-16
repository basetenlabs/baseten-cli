package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// optionalFromFlag sends a flag the user passed, even an empty one, and omits
// one they didn't.
func optionalFromFlag[T any](flag cmd.OptionalFlag[T]) managementapi.Optional[T] {
	if !flag.IsSet() {
		return managementapi.Optional[T]{}
	}
	return managementapi.NewOptional(flag.Pointer())
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// outputCreated prints a created resource: the response under JSON output,
// otherwise its ID on stdout, for scripts, and a confirmation on stderr.
func outputCreated(ctx *CommandContext, response any, kind, name, id string) {
	if ctx.JSON {
		ctx.OutputJSON(response)
		return
	}
	ctx.Logf("Created %s %s\n", kind, name)
	ctx.OutputLine(id)
}

// outputDeleted prints a deletion tombstone: the response under JSON output,
// otherwise a confirmation on stderr.
func outputDeleted(ctx *CommandContext, tomb any, kind, id string) {
	if ctx.JSON {
		ctx.OutputJSON(tomb)
		return
	}
	ctx.Logf("Deleted %s %s\n", kind, id)
}

func valueOrDash(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

func timeOrDash(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

// listOrAll renders an access restriction list; an omitted list means no
// restriction.
func listOrAll(values *[]string) string {
	if values == nil {
		return "all"
	}
	return strings.Join(*values, ", ")
}

// listAllPages follows a cursor-paginated list endpoint to its last page.
// fetch requests the page at cursor, nil for the first.
func listAllPages[T any](fetch func(cursor *string) ([]T, managementapi.PaginationResponse, error)) ([]T, error) {
	var items []T
	var cursor *string
	for {
		page, pagination, err := fetch(cursor)
		if err != nil {
			return nil, err
		}
		items = append(items, page...)
		if !pagination.HasMore || pagination.Cursor == nil {
			return items, nil
		}
		cursor = pagination.Cursor
	}
}

// onlyMatch returns the one item a name-filtered list returned, or nil when it
// returned none. Names are unique per organization, so more than one means the
// lookup matched by something other than the name.
func onlyMatch[T any](kind, name string, items []T) (*T, error) {
	switch len(items) {
	case 0:
		return nil, nil
	case 1:
		return &items[0], nil
	default:
		return nil, fmt.Errorf("multiple %ss match %q; pass the ID instead", kind, name)
	}
}
