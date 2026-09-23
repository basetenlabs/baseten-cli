package harness

import "sort"

type Status struct {
	Detection
	DefaultRoute   string
	SmallTaskModel string
	RouteDetails   []Route
	State          string
	Managed        []string
	Routes         []string
	Note           string
}

// Status describes the current configuration; no previous values are retained
// to compare against. Native harness defaults apply once our settings are removed.
func inspectConfig(d Detection) (Status, map[string]any, error) {
	r := Status{Detection: d, State: "not-configured", Note: "Local configuration only; no API authorization or inference was checked. Rerun setup to refresh routes, then restart the harness."}
	_, data, err := readConfig(d.Path)
	if err != nil {
		return r, nil, err
	}
	r.DefaultRoute, _ = data["model"].(string)
	return r, data, nil
}

func (r *Status) managed(data map[string]any, paths [][]string) {
	for _, path := range paths {
		if get(data, path).Exists {
			r.Managed = append(r.Managed, pathKey(path))
		}
	}
	if len(r.Managed) > 0 {
		r.State = "configured"
	}
}

func (r *Status) addRoutes(labels map[string]string) {
	for name := range labels {
		r.Routes = append(r.Routes, name)
	}
	sort.Strings(r.Routes)
	for _, name := range r.Routes {
		r.RouteDetails = append(r.RouteDetails, Route{Name: name, DisplayName: labels[name]})
	}
}
