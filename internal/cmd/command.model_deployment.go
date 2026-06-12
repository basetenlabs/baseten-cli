package cmd

import (
	"archive/tar"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"gopkg.in/yaml.v3"
)

func init() {
	Register("model deployment list", commandModelDeploymentList)
	Register("model deployment fetch", commandModelDeploymentFetch)
	Register("model deployment config", commandModelDeploymentConfig)
	Register("model deployment activate", commandModelDeploymentActivate)
	Register("model deployment deactivate", commandModelDeploymentDeactivate)
	Register("model deployment delete", commandModelDeploymentDelete)
	Register("model deployment download", commandModelDeploymentDownload)
	Register("model deployment promote", commandModelDeploymentPromote)
}

func commandModelDeploymentList(ctx *CommandContext, flags *cmd.ModelDeploymentListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}
	resp, err := cl.API().GetModelsDeployments(ctx, ref.ID)
	if err != nil {
		return fmt.Errorf("list deployments for model %s: %w", ref.ID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	if len(resp.Deployments) == 0 {
		ctx.LogLine("No deployments found.")
		return nil
	}
	rows := make([][]string, 0, len(resp.Deployments))
	for _, d := range resp.Deployments {
		env := ""
		if d.Environment != nil {
			env = *d.Environment
		}
		instance := ""
		if d.InstanceTypeName != nil {
			instance = *d.InstanceTypeName
		}
		rows = append(rows, []string{
			d.Id,
			d.Name,
			env,
			string(d.Status),
			instance,
			fmt.Sprintf("%d", d.ActiveReplicaCount),
			d.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "NAME", "ENVIRONMENT", "STATUS", "INSTANCE", "REPLICAS", "CREATED"},
		Rows:    rows,
	})
	return nil
}

func commandModelDeploymentFetch(ctx *CommandContext, flags *cmd.ModelDeploymentFetchFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}
	dep, err := cl.API().GetModelsDeploymentsDeploymentId(ctx, ref.ID, flags.DeploymentID)
	if err != nil {
		return fmt.Errorf("fetch deployment %s: %w", flags.DeploymentID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(dep)
		return nil
	}
	ctx.Outputf("ID:           %s\n", dep.Id)
	ctx.Outputf("Name:         %s\n", dep.Name)
	ctx.Outputf("Model:        %s\n", dep.ModelId)
	if dep.Environment != nil {
		ctx.Outputf("Environment:  %s\n", *dep.Environment)
	}
	ctx.Outputf("Status:       %s\n", dep.Status)
	if dep.InstanceTypeName != nil {
		ctx.Outputf("Instance:     %s\n", *dep.InstanceTypeName)
	}
	ctx.Outputf("Replicas:     %d\n", dep.ActiveReplicaCount)
	ctx.Outputf("Created:      %s\n", dep.CreatedAt.UTC().Format(time.RFC3339))
	return nil
}

func commandModelDeploymentConfig(ctx *CommandContext, flags *cmd.ModelDeploymentConfigFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}
	resp, err := cl.API().GetModelsDeploymentsConfig(ctx, ref.ID, flags.DeploymentID,
		managementapi.GetV1ModelsModelIdDeploymentsDeploymentIdConfigParams{})
	if err != nil {
		return fmt.Errorf("fetch deployment config for %s: %w", flags.DeploymentID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	if resp.RawConfig != nil {
		ctx.Output(*resp.RawConfig)
		return nil
	}
	if resp.Config == nil {
		return nil
	}
	b, err := yaml.Marshal(*resp.Config)
	if err != nil {
		return fmt.Errorf("encode config as yaml: %w", err)
	}
	ctx.Output(string(b))
	return nil
}

func commandModelDeploymentActivate(ctx *CommandContext, flags *cmd.ModelDeploymentActivateFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}
	resp, err := cl.API().PostModelsDeploymentsActivate(ctx, ref.ID, flags.DeploymentID)
	if err != nil {
		return fmt.Errorf("activate deployment %s: %w", flags.DeploymentID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Activated deployment %s\n", flags.DeploymentID)
	return nil
}

func commandModelDeploymentDeactivate(ctx *CommandContext, flags *cmd.ModelDeploymentDeactivateFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}

	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Deactivate deployment %s?", flags.DeploymentID)); err != nil {
			return err
		}
	}

	resp, err := cl.API().PostModelsDeploymentsDeactivate(ctx, ref.ID, flags.DeploymentID)
	if err != nil {
		return fmt.Errorf("deactivate deployment %s: %w", flags.DeploymentID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Deactivated deployment %s\n", flags.DeploymentID)
	return nil
}

func commandModelDeploymentDelete(ctx *CommandContext, flags *cmd.ModelDeploymentDeleteFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}

	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Delete deployment %s? This cannot be undone.", flags.DeploymentID)); err != nil {
			return err
		}
	}

	tombstone, err := cl.API().DeleteModelsDeployments(ctx, ref.ID, flags.DeploymentID)
	if err != nil {
		return fmt.Errorf("delete deployment %s: %w", flags.DeploymentID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(tombstone)
		return nil
	}
	ctx.Logf("Deleted deployment %s\n", flags.DeploymentID)
	return nil
}

func commandModelDeploymentDownload(ctx *CommandContext, flags *cmd.ModelDeploymentDownloadFlags) error {
	outPath := flags.OutFile
	if outPath == "" {
		outPath = flags.OutDir
	}
	parent := filepath.Dir(outPath)
	if st, err := os.Stat(parent); err != nil || !st.IsDir() {
		return fmt.Errorf("parent directory does not exist: %s", parent)
	}
	if !flags.Overwrite {
		if flags.OutFile != "" {
			if _, err := os.Stat(flags.OutFile); err == nil {
				return fmt.Errorf("file already exists: %s; pass --overwrite to replace it", flags.OutFile)
			}
		} else {
			if entries, err := os.ReadDir(flags.OutDir); err == nil && len(entries) > 0 {
				return fmt.Errorf("directory is not empty: %s; pass --overwrite to write into it", flags.OutDir)
			}
		}
	}

	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}

	ctx.Logf("Fetching download URL...\n")
	resp, err := cl.API().GetModelsDeploymentsDownload(ctx, ref.ID, flags.DeploymentID)
	if err != nil {
		return fmt.Errorf("fetch download URL for deployment %s: %w", flags.DeploymentID, err)
	}

	ctx.Logf("Downloading truss...\n")
	req, err := http.NewRequestWithContext(ctx, "GET", resp.DownloadUrl, nil)
	if err != nil {
		return fmt.Errorf("build download request: %w", err)
	}
	httpResp, err := ctx.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("download truss: %w", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return fmt.Errorf("download truss: HTTP %d", httpResp.StatusCode)
	}

	if flags.OutFile != "" {
		f, err := os.Create(flags.OutFile)
		if err != nil {
			return fmt.Errorf("create %s: %w", flags.OutFile, err)
		}
		defer f.Close()
		if _, err := io.Copy(f, httpResp.Body); err != nil {
			return fmt.Errorf("write %s: %w", flags.OutFile, err)
		}
		ctx.Logf("Saved to %s\n", flags.OutFile)
		if ctx.JSON {
			ctx.OutputJSON(cmd.ModelDeploymentDownloadResult{OutFile: flags.OutFile})
		}
		return nil
	}

	if err := os.MkdirAll(flags.OutDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", flags.OutDir, err)
	}
	if err := extractTar(httpResp.Body, flags.OutDir); err != nil {
		return fmt.Errorf("extract truss into %s: %w", flags.OutDir, err)
	}
	ctx.Logf("Extracted to %s\n", flags.OutDir)
	if ctx.JSON {
		ctx.OutputJSON(cmd.ModelDeploymentDownloadResult{OutDir: flags.OutDir})
	}
	return nil
}

// extractTar extracts a tar stream into dir. Rejects entries with absolute
// paths or ".." components to avoid path traversal.
func extractTar(r io.Reader, dir string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(hdr.Name)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") || strings.Contains(clean, string(filepath.Separator)+".."+string(filepath.Separator)) {
			return fmt.Errorf("refusing tar entry with unsafe path: %s", hdr.Name)
		}
		target := filepath.Join(dir, clean)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&0o777|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777|0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}

func commandModelDeploymentPromote(ctx *CommandContext, flags *cmd.ModelDeploymentPromoteFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}

	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Promote deployment %s to environment %q?", flags.DeploymentID, flags.Environment)); err != nil {
			return err
		}
	}

	preserve := !flags.OverrideEnvInstanceType
	dep, err := cl.API().PostModelsEnvironmentsPromote(ctx, ref.ID, flags.Environment,
		managementapi.PromoteToEnvironmentRequest{
			DeploymentId:            flags.DeploymentID,
			PreserveEnvInstanceType: &preserve,
		})
	if err != nil {
		return fmt.Errorf("promote deployment %s to environment %s: %w", flags.DeploymentID, flags.Environment, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(dep)
		return nil
	}
	ctx.Logf("Promoted deployment %s to environment %s\n", flags.DeploymentID, flags.Environment)
	return nil
}
