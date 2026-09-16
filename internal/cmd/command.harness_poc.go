package cmd

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/harnesspoc"
)

func init() {
	Register("harness-poc setup", commandHarnessPOCSetup)
	Register("harness-poc serve", commandHarnessPOCServe)
}

func commandHarnessPOCSetup(ctx *CommandContext, flags *cmd.HarnessPOCSetupFlags) error {
	catalog, err := harnesspoc.LoadCatalog(flags.Models)
	if err != nil {
		return cmd.NewErrUsagef("%s", err)
	}
	harnesses, err := harnesspoc.Select(flags.Harness)
	if err != nil {
		return cmd.NewErrUsagef("%s", err)
	}
	root, err := harnessPOCExpandRoot(flags.Root)
	if err != nil {
		return err
	}

	opts := harnesspoc.SetupOptions{
		Root:            root,
		BaseURL:         flags.BaseURL,
		APIKey:          flags.APIKey,
		Mode:            harnesspoc.Mode(flags.Mode),
		ReplaceBuiltins: flags.ReplaceBuiltins,
		Catalog:         catalog,
	}

	result := cmd.HarnessPOCSetupResult{}
	for _, h := range harnesses {
		res, err := h.Setup(opts)
		if err != nil {
			return fmt.Errorf("configuring %s: %w", h.Name(), err)
		}
		result.Harnesses = append(result.Harnesses, cmd.HarnessPOCSetupHarness{
			Harness: res.Harness,
			Paths:   res.Paths,
			Launch:  res.Launch,
			Notes:   res.Notes,
		})
	}

	if ctx.JSON {
		ctx.OutputJSON(result)
		return nil
	}
	for _, h := range result.Harnesses {
		ctx.Logf("%s\n", h.Harness)
		for _, p := range h.Paths {
			ctx.Logf("   wrote:   %s\n", p)
		}
		ctx.Logf("   launch:  %s\n", inlineCodeStyle.Render(h.Launch))
		for _, n := range h.Notes {
			ctx.Logf("   note:    %s\n", n)
		}
	}
	return nil
}

func commandHarnessPOCServe(ctx *CommandContext, flags *cmd.HarnessPOCServeFlags) error {
	catalog, err := harnesspoc.LoadCatalog(flags.Models)
	if err != nil {
		return cmd.NewErrUsagef("%s", err)
	}
	srv := &harnesspoc.Server{
		Catalog:            catalog,
		WithModelDiscovery: flags.WithModelDiscovery,
		LogDir:             flags.LogBodies,
		Logf:               ctx.Logf,
	}

	listener, err := net.Listen("tcp", flags.Addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", flags.Addr, err)
	}
	ctx.Logf("Serving %d models on http://%s\n", len(catalog.Models), listener.Addr())
	if flags.LogBodies != "" {
		ctx.Logf("Writing request bodies to %s\n", flags.LogBodies)
	}

	httpSrv := &http.Server{Handler: srv.Handler()}
	go func() {
		<-ctx.Done()
		_ = httpSrv.Close()
	}()
	if err := httpSrv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// harnessPOCExpandRoot resolves a leading ~ in the root directory, which the
// flag default carries so help shows the real path.
func harnessPOCExpandRoot(root string) (string, error) {
	if !strings.HasPrefix(root, "~") {
		return root, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, strings.TrimPrefix(root, "~")), nil
}
