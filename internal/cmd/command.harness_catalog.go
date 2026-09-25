package cmd

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/harness"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// harnessCatalog converts routes to harness routes, skipping routes whose
// metadata is missing or unusable. The selected primary route must be usable.
func harnessCatalog(listed []managementapi.Route, primary string) (routes []harness.Route, skipped []string, err error) {
	for _, l := range listed {
		target, _ := l.Target.Discriminator()
		r := harness.Route{Name: l.Name, DisplayName: l.DisplayName, Target: target}
		err := fmt.Errorf("route %q has no model metadata", l.Name)
		if m := l.Metadata; m != nil {
			r.ContextWindow, r.OutputLimit = deref(m.ContextWindow), deref(m.MaxOutputTokens)
			r.InputModalities, r.Tools = m.InputModalities, deref(m.Tools)
			r.ReasoningLevels = harness.NormalizeReasoningLevels(deref(m.ReasoningEffortLevels))
			r.ParallelTools = deref(m.ParallelToolCalls)
			if f := m.SupportedApiFormats; f != nil {
				r.Responses = deref(f.Responses)
			}
			if c := m.Cost; c != nil {
				r.Cost = harnessCost(managementapi.ExploreCostValues{Input: c.Input, Output: c.Output, CacheRead: c.CacheRead, CacheWrite: c.CacheWrite})
				if c.LongContext != nil && r.Cost != nil {
					r.Cost.LongContext = harnessCost(*c.LongContext)
				}
			}
			err = harness.ValidateRoute(r)
		}
		switch {
		case err == nil:
			routes = append(routes, r)
		case l.Name == primary:
			return nil, nil, cmd.NewErrValidation(err)
		default:
			skipped = append(skipped, l.Name)
		}
	}
	if len(routes) == 0 {
		return nil, skipped, cmd.NewErrValidation(errors.New("none of the team's routes have usable model metadata"))
	}
	return routes, skipped, nil
}

// harnessCost returns nil unless both input and output prices are known.
func harnessCost(c managementapi.ExploreCostValues) *harness.Cost {
	if c.Input == nil || c.Output == nil {
		return nil
	}
	return &harness.Cost{Input: *price(c.Input), Output: *price(c.Output), CacheRead: price(c.CacheRead), CacheWrite: price(c.CacheWrite)}
}

// price widens the generated float32 by its shortest decimal form, so 0.3
// stays 0.3 rather than 0.30000001192092896.
func price(p *float32) *float64 {
	if p == nil {
		return nil
	}
	f, _ := strconv.ParseFloat(strconv.FormatFloat(float64(*p), 'g', -1, 32), 64)
	return &f
}
