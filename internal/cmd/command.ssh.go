package cmd

import (
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
	if _, err := ctx.Execer().LookPath("baseten"); err != nil {
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
	ctx.LogLine("Connect with:")
	ctx.Logf("   deployment:    %s\n", inlineCodeStyle.Render("ssh model-<model-id>-<deployment-id>.ssh.baseten.co"))
	ctx.Logf("   environment:   %s\n", inlineCodeStyle.Render("ssh <environment>.model-<model-id>.ssh.baseten.co"))
	ctx.Logf("   training job:  %s\n", inlineCodeStyle.Render("ssh training-job-<job-id>-<node>.ssh.baseten.co"))
	return nil
}

func commandSSHSign(ctx *CommandContext, flags *cmd.SSHSignFlags) error {
	ctx.SetDefaultProfile(flags.DefaultProfile)
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	cacheIdentity, err := ctx.sshCacheIdentity()
	if err != nil {
		return err
	}
	// The signed cert and proxy authorization are written to disk by Sign; we
	// deliberately do not surface them on stdout.
	if _, err := ssh.Sign(ctx, cl, cacheIdentity, ctx.Args[0]); err != nil {
		return err
	}
	if ctx.JSON {
		ctx.OutputJSON(struct{}{})
	}
	return nil
}

func commandSSHProxy(ctx *CommandContext, flags *cmd.SSHProxyFlags) error {
	ctx.SetDefaultProfile(flags.DefaultProfile)
	cacheIdentity, err := ctx.sshCacheIdentity()
	if err != nil {
		return err
	}
	return ssh.Proxy(ctx, cacheIdentity, ctx.Args[0], ctx.Stdin, ctx.Stdout)
}

// sshCacheIdentity scopes the JWT cache to the credential this invocation
// resolves to. sign and proxy run as separate processes under one ssh connection,
// inheriting the same flags and environment, so both arrive at the same value.
// Must be called after SetDefaultProfile, which the session resolution reads.
func (c *CommandContext) sshCacheIdentity() (string, error) {
	session, err := c.authInfo.Session()
	if err != nil {
		return "", err
	}
	return session.CacheIdentity(), nil
}
