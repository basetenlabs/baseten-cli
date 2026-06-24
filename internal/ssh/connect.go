package ssh

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/basetenlabs/baseten-go/client"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// Sign signs an SSH certificate for the workload named by hostname, writes the
// cert next to the private key, and caches the JWT and proxy address for the
// subsequent Proxy call.
func Sign(ctx context.Context, mgmt *client.ManagementClient, hostname string) (*managementapi.SignSSHCertificateResponse, error) {
	h, err := parseHostname(hostname)
	if err != nil {
		return nil, err
	}
	d, err := dir()
	if err != nil {
		return nil, err
	}

	// Read the public key to sign.
	keyPath := findKey(d)
	if keyPath == "" {
		return nil, fmt.Errorf("no SSH keypair found; run `baseten model ssh setup` first")
	}
	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return nil, fmt.Errorf("reading public key: %w", err)
	}
	body := managementapi.SignSSHCertificateRequest{PublicKey: strings.TrimSpace(string(pub))}
	if h.replica != "" {
		replica := h.replica
		body.ReplicaId = &replica
	}

	// Call the signing endpoint for this workload type. Training needs the
	// project id, which we look up from the job id.
	var resp *managementapi.SignSSHCertificateResponse
	switch h.kind {
	case workloadModel:
		resp, err = mgmt.API().PostModelsDeploymentsSshSign(ctx, h.id, h.deploymentID, body)
	case workloadTraining:
		projectID, perr := resolveTrainingProject(ctx, mgmt, h.id)
		if perr != nil {
			return nil, perr
		}
		resp, err = mgmt.API().PostTrainingProjectsJobsSshSign(ctx, projectID, h.id, body)
	}
	if err != nil {
		return nil, fmt.Errorf("signing ssh certificate: %w", err)
	}

	// Persist the cert (loaded by ssh via CertificateFile) and the JWT/proxy
	// address (read by the Proxy step).
	if err := os.WriteFile(keyPath+"-cert.pub", []byte(resp.SshCertificate), 0o644); err != nil {
		return nil, fmt.Errorf("writing ssh certificate: %w", err)
	}
	if err := saveJWT(d, h, resp.Jwt, resp.ProxyAddress); err != nil {
		return nil, err
	}
	return resp, nil
}

// resolveTrainingProject looks up the project id owning a training job.
func resolveTrainingProject(ctx context.Context, mgmt *client.ManagementClient, jobID string) (string, error) {
	resp, err := mgmt.API().PostTrainingJobsSearch(ctx, managementapi.SearchTrainingJobsRequest{JobId: &jobID})
	if err != nil {
		return "", fmt.Errorf("looking up training job %s: %w", jobID, err)
	}
	if len(resp.TrainingJobs) == 0 {
		return "", fmt.Errorf("training job %q not found", jobID)
	}
	return resp.TrainingJobs[0].TrainingProjectId, nil
}

// Proxy connects to the SSH proxy for the workload named by hostname using the
// JWT cached by Sign, then relays between in and out until either side closes.
func Proxy(ctx context.Context, hostname string, in io.Reader, out io.Writer) error {
	h, err := parseHostname(hostname)
	if err != nil {
		return err
	}
	d, err := dir()
	if err != nil {
		return err
	}
	cache, ok := loadJWT(d, h)
	if !ok {
		return fmt.Errorf("no cached SSH credential for %s; the signing step did not run", hostname)
	}
	return relay(ctx, cache.ProxyAddress, cache.JWT, in, out)
}

func relay(ctx context.Context, proxyAddr, jwt string, in io.Reader, out io.Writer) error {
	host, _, err := net.SplitHostPort(proxyAddr)
	if err != nil {
		return fmt.Errorf("invalid proxy address %q: %w", proxyAddr, err)
	}

	// Dial the proxy (plain TCP only when explicitly opted in for local dev).
	var conn net.Conn
	if insecure() {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", proxyAddr)
	} else {
		conn, err = (&tls.Dialer{Config: &tls.Config{ServerName: host}}).DialContext(ctx, "tcp", proxyAddr)
	}
	if err != nil {
		return fmt.Errorf("connecting to ssh proxy: %w", err)
	}
	defer conn.Close()

	// Authorize: 4-byte big-endian JWT length, the JWT bytes, then a status byte.
	jb := []byte(jwt)
	var lp [4]byte
	binary.BigEndian.PutUint32(lp[:], uint32(len(jb)))
	if _, err := conn.Write(lp[:]); err != nil {
		return fmt.Errorf("sending proxy authorization: %w", err)
	}
	if _, err := conn.Write(jb); err != nil {
		return fmt.Errorf("sending proxy authorization: %w", err)
	}
	var status [1]byte
	if _, err := io.ReadFull(conn, status[:]); err != nil {
		return fmt.Errorf("reading proxy status: %w", err)
	}
	if status[0] != 0x00 {
		return fmt.Errorf("ssh proxy rejected the connection; is the workload running with SSH enabled?")
	}

	// Relay both directions; return when either the local or remote end closes.
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(conn, in); done <- struct{}{} }()
	go func() { _, _ = io.Copy(out, conn); done <- struct{}{} }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func insecure() bool {
	switch strings.ToLower(os.Getenv("BASETEN_SSH_PROXY_INSECURE")) {
	case "1", "true", "yes":
		return true
	}
	return false
}

type workloadKind int

const (
	workloadModel workloadKind = iota
	workloadTraining
)

// hostname is a parsed SSH hostname for a model deployment or training job.
type hostname struct {
	kind         workloadKind
	id           string // model id or training job id
	deploymentID string // models only
	replica      string // models: optional replica; training: node (required)
}

// parseHostname parses the first DNS label of an SSH hostname. The domain that
// follows is env-specific and irrelevant here, since the REST target comes from
// the resolved profile. Supported label forms:
//
//	training-job-<jobID>-<node>
//	model-<modelID>-<deploymentID>[-<replica>]
func parseHostname(h string) (hostname, error) {
	label := h
	if i := strings.IndexByte(h, '.'); i != -1 {
		label = h[:i]
	}
	switch {
	case strings.HasPrefix(label, "training-job-"):
		return parseTrainingHostname(h, label[len("training-job-"):])
	case strings.HasPrefix(label, "model-"):
		return parseModelHostname(h, label[len("model-"):])
	default:
		return hostname{}, fmt.Errorf(
			"invalid ssh hostname %q: expected training-job-<job>-<node> or model-<model>-<deployment>[-<replica>]", h)
	}
}

// parseModelHostname parses the part after "model-". Model and deployment IDs
// contain no dashes; the optional replica (after the second dash) may.
func parseModelHostname(h, rest string) (hostname, error) {
	parts := strings.SplitN(rest, "-", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return hostname{}, fmt.Errorf("invalid ssh hostname %q: cannot parse model and deployment IDs", h)
	}
	hn := hostname{kind: workloadModel, id: parts[0], deploymentID: parts[1]}
	if len(parts) == 3 {
		if parts[2] == "" {
			return hostname{}, fmt.Errorf("invalid ssh hostname %q: empty replica", h)
		}
		hn.replica = parts[2]
	}
	return hn, nil
}

// parseTrainingHostname parses the part after "training-job-". The node is the
// numeric suffix after the final dash; everything before it is the job id.
func parseTrainingHostname(h, rest string) (hostname, error) {
	i := strings.LastIndexByte(rest, '-')
	if i == -1 {
		return hostname{}, fmt.Errorf("invalid ssh hostname %q: cannot parse job ID and node", h)
	}
	jobID, node := rest[:i], rest[i+1:]
	if jobID == "" {
		return hostname{}, fmt.Errorf("invalid ssh hostname %q: empty job ID", h)
	}
	if node == "" || !isDigits(node) {
		return hostname{}, fmt.Errorf("invalid ssh hostname %q: node must be a number", h)
	}
	return hostname{kind: workloadTraining, id: jobID, replica: node}, nil
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
