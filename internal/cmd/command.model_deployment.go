package cmd

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
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
	Register("model deployment describe", commandModelDeploymentDescribe)
	Register("model deployment config", commandModelDeploymentConfig)
	Register("model deployment activate", commandModelDeploymentActivate)
	Register("model deployment deactivate", commandModelDeploymentDeactivate)
	Register("model deployment delete", commandModelDeploymentDelete)
	Register("model deployment download", commandModelDeploymentDownload)
	Register("model deployment promote", commandModelDeploymentPromote)
}

// DeploymentRef is the result of resolving [cmd.ModelDeploymentIDFlags]: a
// resolved model ID paired with a resolved deployment ID.
type DeploymentRef struct {
	ModelID      string
	DeploymentID string
}

// ResolveDeploymentRef resolves the model, then the deployment within it. When
// --deployment-id is set it is used directly; when --deployment-name is set the
// deployment is looked up by exact name within the model. Absent or ambiguous
// name matches return an error.
func ResolveDeploymentRef(
	ctx context.Context, api *managementapi.Client, flags cmd.ModelDeploymentIDFlags,
) (DeploymentRef, error) {
	modelRef, err := ResolveModelRef(ctx, api, flags.ModelRefFlags)
	if err != nil {
		return DeploymentRef{}, err
	}
	if flags.DeploymentID != "" {
		return DeploymentRef{ModelID: modelRef.ID, DeploymentID: flags.DeploymentID}, nil
	}
	deploymentID, err := findDeploymentIDByName(ctx, api, modelRef.ID, flags.DeploymentName)
	if err != nil {
		return DeploymentRef{}, err
	}
	return DeploymentRef{ModelID: modelRef.ID, DeploymentID: deploymentID}, nil
}

// findDeploymentIDByName returns the ID of the deployment with the given exact
// name within a model. The server filters by exact name, so at most one
// deployment matches; absent or (defensively) ambiguous matches return an error.
func findDeploymentIDByName(
	ctx context.Context, api *managementapi.Client, modelID, name string,
) (string, error) {
	resp, err := api.GetModelsDeployments(ctx, modelID,
		managementapi.GetV1ModelsModelIdDeploymentsParams{Name: &name})
	if err != nil {
		return "", fmt.Errorf("list deployments for model %s: %w", modelID, err)
	}
	if len(resp.Deployments) == 0 {
		return "", fmt.Errorf("no deployment named %q in model %s", name, modelID)
	} else if len(resp.Deployments) > 1 {
		return "", fmt.Errorf("multiple deployments named %q in model %s", name, modelID)
	}
	return resp.Deployments[0].Id, nil
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
	resp, err := cl.API().GetModelsDeployments(ctx, ref.ID,
		managementapi.GetV1ModelsModelIdDeploymentsParams{})
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

func commandModelDeploymentDescribe(ctx *CommandContext, flags *cmd.ModelDeploymentDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveDeploymentRef(ctx, cl.API(), flags.ModelDeploymentIDFlags)
	if err != nil {
		return err
	}
	dep, err := cl.API().GetModelsDeploymentsDeploymentId(ctx, ref.ModelID, ref.DeploymentID)
	if err != nil {
		return fmt.Errorf("describe deployment %s: %w", ref.DeploymentID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(dep)
		return nil
	}
	remote, err := ctx.authInfo.Remote()
	if err != nil {
		return err
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
	ctx.Outputf("Invoke URL:   %s\n", hyperlink(ctx.Stdout, remote.PredictURL(dep.ModelId, dep.Id, dep.IsDevelopment)))
	ctx.Outputf("Logs URL:     %s\n", hyperlink(ctx.Stdout, remote.LogsURL(dep.ModelId, dep.Id)))
	ctx.Outputf("Created:      %s\n", dep.CreatedAt.UTC().Format(time.RFC3339))
	return nil
}

func commandModelDeploymentConfig(ctx *CommandContext, flags *cmd.ModelDeploymentConfigFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveDeploymentRef(ctx, cl.API(), flags.ModelDeploymentIDFlags)
	if err != nil {
		return err
	}
	resp, err := cl.API().GetModelsDeploymentsConfig(ctx, ref.ModelID, ref.DeploymentID,
		managementapi.GetV1ModelsModelIdDeploymentsDeploymentIdConfigParams{})
	if err != nil {
		return fmt.Errorf("fetch deployment config for %s: %w", ref.DeploymentID, err)
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
	ref, err := ResolveDeploymentRef(ctx, cl.API(), flags.ModelDeploymentIDFlags)
	if err != nil {
		return err
	}
	resp, err := cl.API().PostModelsDeploymentsActivate(ctx, ref.ModelID, ref.DeploymentID)
	if err != nil {
		return fmt.Errorf("activate deployment %s: %w", ref.DeploymentID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Activated deployment %s\n", ref.DeploymentID)
	return nil
}

func commandModelDeploymentDeactivate(ctx *CommandContext, flags *cmd.ModelDeploymentDeactivateFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveDeploymentRef(ctx, cl.API(), flags.ModelDeploymentIDFlags)
	if err != nil {
		return err
	}

	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Deactivate deployment %s?", ref.DeploymentID)); err != nil {
			return err
		}
	}

	resp, err := cl.API().PostModelsDeploymentsDeactivate(ctx, ref.ModelID, ref.DeploymentID)
	if err != nil {
		return fmt.Errorf("deactivate deployment %s: %w", ref.DeploymentID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Deactivated deployment %s\n", ref.DeploymentID)
	return nil
}

func commandModelDeploymentDelete(ctx *CommandContext, flags *cmd.ModelDeploymentDeleteFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveDeploymentRef(ctx, cl.API(), flags.ModelDeploymentIDFlags)
	if err != nil {
		return err
	}

	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Delete deployment %s? This cannot be undone.", ref.DeploymentID)); err != nil {
			return err
		}
	}

	tombstone, err := cl.API().DeleteModelsDeployments(ctx, ref.ModelID, ref.DeploymentID)
	if err != nil {
		return fmt.Errorf("delete deployment %s: %w", ref.DeploymentID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(tombstone)
		return nil
	}
	ctx.Logf("Deleted deployment %s\n", ref.DeploymentID)
	return nil
}

// checkDownloadOutTarget rejects an output location that is already occupied,
// unless the caller passed --overwrite. Exactly one of outFile or outDir is set,
// matching the --out-file/--out-dir pair the download commands share.
func checkDownloadOutTarget(outFile, outDir string, overwrite bool) error {
	outPath := outFile
	if outPath == "" {
		outPath = outDir
	}
	parent := filepath.Dir(outPath)
	if st, err := os.Stat(parent); err != nil || !st.IsDir() {
		return fmt.Errorf("parent directory does not exist: %s", parent)
	}
	if overwrite {
		return nil
	}
	if outFile != "" {
		if _, err := os.Stat(outFile); err == nil {
			return fmt.Errorf("file already exists: %s; pass --overwrite to replace it", outFile)
		}
		return nil
	}
	if entries, err := os.ReadDir(outDir); err == nil && len(entries) > 0 {
		return fmt.Errorf("directory is not empty: %s; pass --overwrite to write into it", outDir)
	}
	return nil
}

// downloadTarArchive fetches url and either saves the bytes verbatim to outFile
// or extracts them into outDir, reporting where they landed. Exactly one of
// outFile or outDir must be set. what names the archive in messages ("truss",
// "artifact"). The URL is presigned, carrying its own credentials, so it goes
// out on the plain client rather than the authenticated management one.
func downloadTarArchive(ctx *CommandContext, url, what, outFile, outDir string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("build download request: %w", err)
	}
	httpResp, err := ctx.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", what, err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return fmt.Errorf("download %s: HTTP %d", what, httpResp.StatusCode)
	}

	if outFile != "" {
		f, err := os.Create(outFile)
		if err != nil {
			return fmt.Errorf("create %s: %w", outFile, err)
		}
		defer f.Close()
		if _, err := io.Copy(f, httpResp.Body); err != nil {
			return fmt.Errorf("write %s: %w", outFile, err)
		}
		ctx.Logf("Saved to %s\n", outFile)
		return nil
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", outDir, err)
	}
	if err := extractTar(httpResp.Body, outDir); err != nil {
		return fmt.Errorf("extract %s into %s: %w", what, outDir, err)
	}
	ctx.Logf("Extracted to %s\n", outDir)
	return nil
}

func commandModelDeploymentDownload(ctx *CommandContext, flags *cmd.ModelDeploymentDownloadFlags) error {
	if err := checkDownloadOutTarget(flags.OutFile, flags.OutDir, flags.Overwrite); err != nil {
		return err
	}

	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveDeploymentRef(ctx, cl.API(), flags.ModelDeploymentIDFlags)
	if err != nil {
		return err
	}

	ctx.Logf("Fetching download URL...\n")
	resp, err := cl.API().GetModelsDeploymentsDownload(ctx, ref.ModelID, ref.DeploymentID)
	if err != nil {
		return fmt.Errorf("fetch download URL for deployment %s: %w", ref.DeploymentID, err)
	}

	ctx.Logf("Downloading model source...\n")
	if err := downloadTarArchive(ctx, resp.DownloadUrl, "model source", flags.OutFile, flags.OutDir); err != nil {
		return err
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.ModelDeploymentDownloadResult{OutFile: flags.OutFile, OutDir: flags.OutDir})
	}
	return nil
}

// extractTar extracts a tar stream into dir, transparently decompressing a
// gzipped one. Rejects entries with absolute paths or ".." components to avoid
// path traversal.
func extractTar(r io.Reader, dir string) error {
	// Truss packs both truss and training archives uncompressed and detects
	// compression on the way back out, so trust the bytes rather than the file
	// name a caller gave them. A tar header opens with the NUL-padded entry name,
	// so the gzip magic cannot start a valid one.
	br := bufio.NewReader(r)
	if magic, err := br.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return fmt.Errorf("read gzip archive: %w", err)
		}
		defer gz.Close()
		r = gz
	} else {
		r = br
	}
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
	ref, err := ResolveDeploymentRef(ctx, cl.API(), flags.ModelDeploymentIDFlags)
	if err != nil {
		return err
	}

	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Promote deployment %s to environment %q?", ref.DeploymentID, flags.Environment)); err != nil {
			return err
		}
	}

	preserve := !flags.OverrideEnvInstanceType
	dep, err := cl.API().PostModelsEnvironmentsPromote(ctx, ref.ModelID, flags.Environment,
		managementapi.PromoteToEnvironmentRequest{
			DeploymentId:            ref.DeploymentID,
			PreserveEnvInstanceType: &preserve,
		})
	if err != nil {
		return fmt.Errorf("promote deployment %s to environment %s: %w", ref.DeploymentID, flags.Environment, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(dep)
		return nil
	}
	ctx.Logf("Promoted deployment %s to environment %s\n", ref.DeploymentID, flags.Environment)
	return nil
}
