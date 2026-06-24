package cmd

import (
	"os/exec"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/ssh"
)

func init() {
	Register("ssh setup", commandSSHSetup)
	Register("ssh sign", commandSSHSign)
	Register("ssh proxy", commandSSHProxy)
}

func commandSSHSetup(ctx *CommandContext, flags *cmd.SSHSetupFlags) error {
	keyPath, reused, err := ssh.EnsureKeypair()
	if err != nil {
		return err
	}

	// Pin the profile this invocation resolves to so connections are
	// deterministic; --profile or BASETEN_PROFILE can still override at connect.
	var profile string
	if session, err := ctx.authInfo.Session(); err == nil {
		profile = session.ProfileName()
	}
	// Only pin names safe to embed bare in the config's shell command lines.
	if profile != "" && !ssh.SafeProfileName(profile) {
		ctx.Logf("warning: profile %q has characters unsafe to embed in SSH config; not pinning it. "+
			"Connections will use BASETEN_PROFILE or the current profile.\n", profile)
		profile = ""
	}

	if err := ssh.WriteConfig(keyPath, profile); err != nil {
		return err
	}

	// The generated config invokes `baseten`; warn if it is not on the PATH,
	// since the SSH connection will run it at connect time.
	if _, err := exec.LookPath("baseten"); err != nil {
		ctx.LogLine("warning: `baseten` was not found on your PATH; SSH connections will fail until it is.")
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.SSHSetupResult{KeyPath: keyPath, KeyReused: reused, Profile: profile})
		return nil
	}
	if reused {
		ctx.Logf("Reusing existing SSH keypair: %s\n", keyPath)
	} else {
		ctx.Logf("Generated SSH keypair: %s\n", keyPath)
	}
	ctx.LogLine("SSH config updated.")
	if profile != "" {
		ctx.Logf("Connections use profile %q by default.\n", profile)
	}
	ctx.LogLine("Connect with: ssh model-<model-id>-<deployment-id>.ssh.baseten.co")
	return nil
}

func commandSSHSign(ctx *CommandContext, flags *cmd.SSHSignFlags) error {
	ctx.SetDefaultProfile(flags.DefaultProfile)
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	// The signed cert and proxy authorization are written to disk by Sign; we
	// deliberately do not surface them on stdout.
	if _, err := ssh.Sign(ctx, cl, ctx.Args[0]); err != nil {
		return err
	}
	if ctx.JSON {
		ctx.OutputJSON(struct{}{})
	}
	return nil
}

func commandSSHProxy(ctx *CommandContext, flags *cmd.SSHProxyFlags) error {
	ctx.SetDefaultProfile(flags.DefaultProfile)
	return ssh.Proxy(ctx, ctx.Args[0], ctx.Stdin, ctx.Stdout)
}
