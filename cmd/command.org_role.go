package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

// commandOrgRole groups the `baseten org role` subcommands.
var commandOrgRole = Command{
	Name:    "role",
	Summary: "Manage custom roles (PRE-RELEASE)",
	Description: orgAccessPrereleaseNotice +
		"View built-in roles and manage the organization's custom roles. " + orgAccessUnavailable,
	Children: []Command{
		{
			Name:    "create",
			Summary: "Create a custom role (PRE-RELEASE)",
			Description: orgAccessPrereleaseNotice +
				"Create a custom role. An organization role is assigned across the " +
				"organization; a team role is assigned in a team, any team unless --team names the " +
				"only one it can be assigned in. Run 'baseten org permission list' for the " +
				"permissions it can grant.",
			Flags: OrgRoleCreateFlags{},
			Output: &CommandOutput[managementapi.AccessRole]{
				TextDescription: "Prints the new role's ID on stdout, and \"Created role <name>\" on stderr.",
				Examples: []CommandExample{
					{
						Description: "Create a team role that pushes and views deployments.",
						CommandLines: []string{
							"baseten org role create --name deployer --kind team",
							"--permission model.deployment.write --permission model.read",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print just the new role's ID.",
					CommandLines: []string{
						"baseten org role create --name auditor --kind organization",
						"--permission access.member.read --jq '.id'",
					},
				},
			},
		},
		{
			Name:    "delete",
			Summary: "Delete a custom role (PRE-RELEASE)",
			Description: orgAccessPrereleaseNotice +
				"Delete a custom role. Fails while any assignment uses it; remove those " +
				"first. Built-in roles can't be deleted.",
			Flags: OrgRoleDeleteFlags{},
			Output: &CommandOutput[managementapi.AccessRoleTombstone]{
				TextDescription: "Prints \"Deleted role <ID>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Delete a role by name.",
						Command:     "baseten org role delete --name deployer",
					},
				},
				JQExample: CommandExample{
					Description: "Print the deleted role's ID.",
					Command:     "baseten org role delete --name deployer --jq '.id'",
				},
			},
		},
		{
			Name:        "describe",
			Summary:     "Describe a role (PRE-RELEASE)",
			Description: orgAccessPrereleaseNotice + "Describe a built-in or custom role by ID or name.",
			Flags:       OrgRoleDescribeFlags{},
			Output: &CommandOutput[managementapi.AccessRole]{
				TextDescription: "Field-per-line summary of the role, including every permission it grants itself.",
				Examples: []CommandExample{
					{
						Description: "Describe a role by name.",
						Command:     "baseten org role describe --name deployer",
					},
				},
				JQExample: CommandExample{
					Description: "Print the role's permissions.",
					Command:     "baseten org role describe --name deployer --jq '.permissions'",
				},
			},
		},
		{
			Name:    "list",
			Summary: "List roles (PRE-RELEASE)",
			Description: orgAccessPrereleaseNotice +
				"List built-in roles and the organization's custom roles. Pass --team for " +
				"only the roles that can be assigned in that team.",
			Flags: OrgRoleListFlags{},
			Output: &CommandOutput[OrgRoleList]{
				TextDescription: "Table with columns: ID, NAME, KIND, TEAM, BUILT-IN, PERMISSIONS (how many " +
					"the role grants itself; 'baseten org role describe' lists them). When none match, " +
					"prints \"No roles found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List every role.",
						Command:     "baseten org role list",
					},
					{
						Description: "List the roles a team can assign.",
						Command:     "baseten org role list --team <team>",
					},
				},
				JQExample: CommandExample{
					Description: "Print the names of the custom roles.",
					Command:     "baseten org role list --jq '.items[] | select(.built_in | not) | .name'",
				},
			},
		},
		{
			Name:    "update",
			Summary: "Update a custom role (PRE-RELEASE)",
			Description: orgAccessPrereleaseNotice +
				"Change a custom role's name or description, add or remove permissions, " +
				"and include or stop including built-in roles. Changes apply to every holder of the role.",
			Flags: OrgRoleUpdateFlags{},
			Output: &CommandOutput[managementapi.AccessRole]{
				TextDescription: "Prints \"Updated role <name>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Let a role delete deployments, and stop it pushing them.",
						CommandLines: []string{
							"baseten org role update --name deployer",
							"--add-permission model.deployment.delete --remove-permission model.deployment.write",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print the role's new permissions.",
					Command:     "baseten org role update --name deployer --description 'CI' --jq '.permissions'",
				},
			},
		},
	},
}

// OrgRoleList is the JSON output of `baseten org role list`: the roles
// aggregated across all pages.
type OrgRoleList struct {
	Items []managementapi.AccessRole `json:"items"`
}

// OrgRoleRefFlags picks one role.
type OrgRoleRefFlags struct {
	ID   string `flag:"id" desc:"Role ID." oneof:"role-ref"`
	Name string `flag:"name" desc:"Role name." oneof:"role-ref"`
}

type OrgRoleCreateFlags struct {
	CommandFlags

	Name        string   `flag:"name" desc:"Name, unique in the organization." required:"true"`
	Kind        string   `flag:"kind" desc:"Where the role is assigned: across the organization, or in teams." enum:"organization,team" required:"true"`
	Team        string   `flag:"team" desc:"For a team role, the only team it can be assigned in, by name or ID. Omit to let any team use it."`
	Description string   `flag:"description" desc:"Optional description."`
	Permissions []string `flag:"permission" desc:"Permission the role grants, such as model.read. May be repeated."`
	Includes    []string `flag:"include" desc:"ID of a built-in role whose permissions the role also grants, as they change, such as baseten:reader. May be repeated."`
}

type OrgRoleDeleteFlags struct {
	CommandFlags
	OrgRoleRefFlags
}

type OrgRoleDescribeFlags struct {
	CommandFlags
	OrgRoleRefFlags

	Team string `flag:"team" desc:"Describe the role as this team's role list shows it, by name or ID. Needed only to manage a team's roles without organization-wide access to roles."`
}

type OrgRoleListFlags struct {
	CommandFlags

	Team string `flag:"team" desc:"Only the roles that can be assigned in this team, by name or ID."`
}

type OrgRoleUpdateFlags struct {
	CommandFlags
	OrgRoleRefFlags

	NewName           string               `flag:"new-name" desc:"New name, unique in the organization."`
	Description       OptionalFlag[string] `flag:"description" desc:"New description. Pass an empty string to clear it."`
	AddPermissions    []string             `flag:"add-permission" desc:"Permission to add to the role. May be repeated."`
	RemovePermissions []string             `flag:"remove-permission" desc:"Permission to remove from the role. May be repeated."`
	AddIncludes       []string             `flag:"add-include" desc:"ID of a built-in role for the role to include. May be repeated."`
	RemoveIncludes    []string             `flag:"remove-include" desc:"ID of a built-in role for the role to stop including. May be repeated."`
}
