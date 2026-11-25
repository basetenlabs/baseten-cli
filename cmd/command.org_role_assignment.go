package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

// commandOrgRoleAssignment groups the `baseten org role-assignment` subcommands.
var commandOrgRoleAssignment = Command{
	Name:    "role-assignment",
	Summary: "Manage role assignments (PRE-RELEASE)",
	Description: orgRolePrereleaseNotice +
		"Give roles to users, service accounts and directory groups, for the organization or in a team. " +
		orgRoleUnavailable,
	Children: []Command{
		{
			Name:    "create",
			Summary: "Assign a role (PRE-RELEASE)",
			Description: orgRolePrereleaseNotice +
				"Give a role to a user, service account or directory group. An organization " +
				"role applies across the organization; a team role needs --team. Pass --resource-set " +
				"to limit the role to what any of the sets selects; omit it for every resource where " +
				"the role applies. " + orgRoleResourceSetUsage,
			Flags: OrgRoleAssignmentCreateFlags{},
			Output: &CommandOutput[managementapi.AccessAssignment]{
				TextDescription: "Prints the new assignment's ID on stdout, and \"Assigned role " +
					"<role name>\" on stderr.",
				Examples: []CommandExample{
					{
						Description: "Let a service account push one model's deployments in staging.",
						CommandLines: []string{
							"baseten org role-assignment create --role-name deployer",
							"--service-account-name ci-deployer --team <team>",
							"--resource-set model:<model-id>,environment:staging",
						},
					},
					{
						Description: "Give a user an organization role.",
						Command:     "baseten org role-assignment create --role-name auditor --user-email <email>",
					},
				},
				JQExample: CommandExample{
					Description: "Print where the role applies.",
					CommandLines: []string{
						"baseten org role-assignment create --role-id <role> --user-id <user>",
						"--team <team> --jq '.resource_sets[].all_of'",
					},
				},
			},
		},
		{
			Name:        "delete",
			Summary:     "Remove a role assignment (PRE-RELEASE)",
			Description: orgRolePrereleaseNotice + "Remove a role assignment by ID.",
			Flags:       OrgRoleAssignmentRefFlags{},
			Output: &CommandOutput[managementapi.AccessAssignmentTombstone]{
				TextDescription: "Prints \"Removed role assignment <ID>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Remove an assignment.",
						Command:     "baseten org role-assignment delete --id <assignment>",
					},
				},
				JQExample: CommandExample{
					Description: "Print the removed assignment's ID.",
					Command:     "baseten org role-assignment delete --id <assignment> --jq '.id'",
				},
			},
		},
		{
			Name:        "describe",
			Summary:     "Describe a role assignment (PRE-RELEASE)",
			Description: orgRolePrereleaseNotice + "Describe a role assignment by ID.",
			Flags:       OrgRoleAssignmentRefFlags{},
			Output: &CommandOutput[managementapi.AccessAssignment]{
				TextDescription: "Field-per-line summary of the assignment.",
				Examples: []CommandExample{
					{
						Description: "Describe an assignment.",
						Command:     "baseten org role-assignment describe --id <assignment>",
					},
				},
				JQExample: CommandExample{
					Description: "Print who holds the role.",
					Command:     "baseten org role-assignment describe --id <assignment> --jq '.subject_name'",
				},
			},
		},
		{
			Name:    "list",
			Summary: "List role assignments (PRE-RELEASE)",
			Description: orgRolePrereleaseNotice +
				"List role assignments, optionally only one team's, one role's, or one " +
				"holder's. Roles held through organization and team membership aren't listed; " +
				"'baseten org user access' and 'baseten org service-account access' show them.",
			Flags: OrgRoleAssignmentListFlags{},
			Output: &CommandOutput[OrgRoleAssignmentList]{
				TextDescription: "Table with columns: ID, ROLE, HOLDER, TEAM, RESOURCE SETS, CREATED. " +
					"When none match, prints \"No role assignments found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List a team's assignments.",
						Command:     "baseten org role-assignment list --team <team>",
					},
					{
						Description: "List a service account's assignments.",
						Command:     "baseten org role-assignment list --service-account-name ci-deployer",
					},
				},
				JQExample: CommandExample{
					Description: "Print the names of the roles assigned.",
					Command:     "baseten org role-assignment list --jq '.items[].role_name'",
				},
			},
		},
	},
}

// OrgRoleAssignmentList is the JSON output of `baseten org role-assignment
// list`: the assignments aggregated across all pages.
type OrgRoleAssignmentList struct {
	Items []managementapi.AccessAssignment `json:"items"`
}

type OrgRoleAssignmentCreateFlags struct {
	CommandFlags

	UserID             string   `flag:"user-id" desc:"User ID." oneof:"subject"`
	UserEmail          string   `flag:"user-email" desc:"Email of the user." oneof:"subject"`
	ServiceAccountID   string   `flag:"service-account-id" desc:"Service account ID." oneof:"subject"`
	ServiceAccountName string   `flag:"service-account-name" desc:"Name of the service account." oneof:"subject"`
	DirectoryGroupID   string   `flag:"directory-group-id" desc:"Directory group ID." oneof:"subject"`
	RoleID             string   `flag:"role-id" desc:"Role to assign, by ID." oneof:"role-ref"`
	RoleName           string   `flag:"role-name" desc:"Role to assign, by name." oneof:"role-ref"`
	Team               string   `flag:"team" desc:"Team to assign a team role in, by name or ID. Omit for an organization role."`
	ResourceSets       []string `flag:"resource-set" desc:"Limit the role to what this set selects, such as model:<model-id>,environment:staging. May be repeated."`
}

type OrgRoleAssignmentListFlags struct {
	CommandFlags

	UserID             string `flag:"user-id" desc:"Only this user's. Mutually exclusive with the other holder flags."`
	UserEmail          string `flag:"user-email" desc:"Only the user with this email's."`
	ServiceAccountID   string `flag:"service-account-id" desc:"Only this service account's."`
	ServiceAccountName string `flag:"service-account-name" desc:"Only the service account with this name's."`
	DirectoryGroupID   string `flag:"directory-group-id" desc:"Only this directory group's."`
	Team               string `flag:"team" desc:"Only assignments in this team, by name or ID."`
	RoleID             string `flag:"role-id" desc:"Only assignments of the role with this ID. Mutually exclusive with --role-name."`
	RoleName           string `flag:"role-name" desc:"Only assignments of the role with this name."`
}

// OrgRoleAssignmentRefFlags picks one role assignment.
type OrgRoleAssignmentRefFlags struct {
	CommandFlags

	ID string `flag:"id" desc:"Role assignment ID." required:"true"`
}
