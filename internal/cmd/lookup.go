package cmd

import (
	"fmt"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

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

// findByName returns the one item whose name matches input. kind names the
// resource in errors ("service account").
func findByName[T any](kind, input string, items []T, matchesName func(T, string) bool) (T, error) {
	var zero T
	found := -1
	for i, it := range items {
		if !matchesName(it, input) {
			continue
		}
		if found >= 0 {
			return zero, fmt.Errorf("multiple %ss named %q; pass the ID instead", kind, input)
		}
		found = i
	}
	if found < 0 {
		return zero, cmd.NewErrNotFound(fmt.Errorf("no %s named %q", kind, input))
	}
	return items[found], nil
}

// findByIDOrName is findByName for a flag that takes a name or an ID. An exact
// ID match wins, so an ID always works even if some name is spelled the same.
func findByIDOrName[T any](kind, input string, items []T, id func(T) string, matchesName func(T, string) bool) (T, error) {
	for _, it := range items {
		if id(it) == input {
			return it, nil
		}
	}
	return findByName(kind, input, items, matchesName)
}
