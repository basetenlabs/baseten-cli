// This test-only executable drives the native loopback probes. It is not part
// of the baseten CLI and never reads login credentials or the system keyring.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/basetenlabs/baseten-cli/internal/harness"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 3 || os.Args[1] != "harness" {
		return fmt.Errorf("expected harness setup, status, or teardown")
	}
	flags := flag.NewFlagSet("fixture", flag.ContinueOnError)
	name := flags.String("harness", "claude-code", "")
	path := flags.String("config", "", "")
	catalog := flags.String("catalog-fixture", "", "")
	endpoint := flags.String("fixture-endpoint", "", "")
	primary := flags.String("model", "", "")
	background := flags.String("background-model", "", "")
	subagent := flags.String("subagent-model", "", "")
	fallback := flags.String("fallback-model", "", "")
	replace := flags.Bool("replace-existing", false, "")
	dry := flags.Bool("dry-run", false, "")
	flags.Bool("yes", false, "")
	flags.String("output", "json", "")
	if err := flags.Parse(os.Args[3:]); err != nil {
		return err
	}
	if *path == "" {
		return fmt.Errorf("an explicit test config path is required")
	}
	var plans []*harness.Plan
	var err error
	switch os.Args[2] {
	case "setup":
		if err := harness.FixtureEndpoint(*endpoint); err != nil {
			return err
		}
		routes, err := (harness.FixtureCatalog{Path: *catalog}).Routes(context.Background())
		if err != nil {
			return err
		}
		plans, err = harness.PrepareHarness(*name, *path, routes, harness.Selection{Primary: *primary, Background: *background, Subagent: *subagent, Fallback: *fallback}, *endpoint, harness.FixtureToken, *name == "claude-code", *replace)
		if err != nil {
			return err
		}
	case "teardown":
		plans, err = harness.PrepareHarnessTeardown(*name, *path)
		if err != nil {
			return err
		}
	case "status":
		status, err := harness.Inspect(harness.Detection{Name: *name, Path: *path})
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(status)
	default:
		return fmt.Errorf("unknown fixture operation")
	}
	if !*dry {
		if err := harness.ApplyPlans(plans); err != nil {
			return err
		}
	}
	if len(plans) == 1 {
		return json.NewEncoder(os.Stdout).Encode(plans[0])
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"changes": plans})
}
