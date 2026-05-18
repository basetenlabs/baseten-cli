package cmd

import (
	"github.com/basetenlabs/baseten-cli/cmd"
)

func init() {
	Register("org billing usage", commandOrgBillingUsage)
}

func commandOrgBillingUsage(_ *CommandContext, _ *cmd.OrgBillingUsageFlags) error {
	panic("TODO: implement org billing usage")
}
