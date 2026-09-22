package harness

type Status struct {
	Detection
	DefaultRoute   string
	SmallTaskModel string
	RouteDetails   []Route
	State          string
	Managed        []string
	Drift          []string
	Routes         []string
	Note           string
}

// inspectConfig reports changes to managed settings without exposing values.
func inspectConfig(d Detection) (Status, map[string]any, *journal, error) {
	r := Status{Detection: d, State: "not-configured", Note: "Local configuration only; no API authorization or inference was checked. Rerun setup to refresh routes, then restart the harness."}
	_, data, _, j, err := readConfig(d.Path)
	if err != nil {
		return r, nil, nil, err
	}
	r.DefaultRoute, _ = data["model"].(string)
	if j != nil {
		r.State = "configured"
		for _, v := range j.Settings {
			r.Managed = append(r.Managed, pathKey(v.Path))
			if !same(get(data, v.Path), v.Installed) {
				r.Drift = append(r.Drift, pathKey(v.Path))
			}
		}
		if len(r.Drift) > 0 {
			r.State = "drifted"
		}
	}
	return r, data, j, nil
}

func (r *Status) addRoutes(j *journal, labels map[string]string) {
	if j == nil {
		return
	}
	for _, name := range j.Routes {
		if label, ok := labels[name]; ok {
			r.Routes = append(r.Routes, name)
			r.RouteDetails = append(r.RouteDetails, Route{Name: name, DisplayName: label})
		}
	}
}
