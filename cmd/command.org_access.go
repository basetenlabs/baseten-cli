package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

const orgAccessPrereleaseNotice = "PRE-RELEASE: custom role commands are not GA yet. " +
	"Their arguments, flags, and output may change.\n\n"

const orgAccessUnavailable = "Organizations without custom roles get not found."

// resourceSetUsage explains the --resource-set syntax shared by role-assignment create and access-check.
const resourceSetUsage = "A resource set is TYPE:ID entries joined by commas, at most one resource and at most one " +
	"environment, such as model:<model-id>,environment:production. An environment entry names the " +
	"environment, such as staging."

const orgAccessDescribeText = "The principal on the first line, then a table of roles with columns: ROLE, " +
	"HELD VIA, TEAM, RESOURCE SETS, ASSIGNMENT, then a table of permissions with columns: " +
	"PERMISSION, TEAM, RESOURCE SET. Prints \"Effective permissions aren't available yet.\" to " +
	"stderr instead of the permissions until fine-grained authorization decides the " +
	"organization's requests."

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

// commandOrgRoleAssignment groups the `baseten org role-assignment` subcommands.
var commandOrgRoleAssignment = Command{
	Name:    "role-assignment",
	Summary: "Manage role assignments (PRE-RELEASE)",
	Description: orgAccessPrereleaseNotice +
		"Give roles to users, service accounts and directory groups, for the organization or in a team. " +
		orgAccessUnavailable,
	Children: []Command{
		{
			Name:    "create",
			Summary: "Assign a role (PRE-RELEASE)",
			Description: orgAccessPrereleaseNotice +
				"Give a role to a user, service account or directory group. An organization " +
				"role applies across the organization; a team role needs --team. Pass --resource-set " +
				"to limit the role to what any of the sets selects; omit it for every resource where " +
				"the role applies. " + resourceSetUsage,
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
			Description: orgAccessPrereleaseNotice + "Remove a role assignment by ID.",
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
			Description: orgAccessPrereleaseNotice + "Describe a role assignment by ID.",
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
			Description: orgAccessPrereleaseNotice +
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

// commandOrgPermission groups the `baseten org permission` subcommands.
var commandOrgPermission = Command{
	Name:    "permission",
	Summary: "View the permissions roles can grant (PRE-RELEASE)",
	Description: orgAccessPrereleaseNotice +
		"View the permissions custom roles can grant. " + orgAccessUnavailable,
	Children: []Command{
		{
			Name:    "list",
			Summary: "List the permissions roles can grant (PRE-RELEASE)",
			Description: orgAccessPrereleaseNotice +
				"List the permissions roles can grant. A custom role can hold a permission " +
				"only when it's assignable: available to the organization and not reserved for " +
				"built-in admin roles.",
			Flags: OrgPermissionListFlags{},
			Output: &CommandOutput[managementapi.AccessPermissions]{
				TextDescription: "Table with columns: NAME, LABEL, ROLE KINDS, ASSIGNABLE.",
				Examples: []CommandExample{
					{
						Description: "List the permissions.",
						Command:     "baseten org permission list",
					},
				},
				JQExample: CommandExample{
					Description: "Print the permissions a team role can hold.",
					CommandLines: []string{
						"baseten org permission list",
						"--jq '.permissions[] | select(.role_kinds | index(\"TEAM\")) | .name'",
					},
				},
			},
		},
	},
}

// commandOrgAccessCheck is `baseten org access-check`.
var commandOrgAccessCheck = Command{
	Name:    "access-check",
	Summary: "Check whether you hold permissions (PRE-RELEASE)",
	Description: orgAccessPrereleaseNotice +
		"Check whether you hold each --permission: in --team, where --resource-set selects, " +
		"or across the organization when neither is given. To check whether you can create something " +
		"in a team, check its write permission in the team. " + resourceSetUsage + " " + orgAccessUnavailable,
	Flags: OrgAccessCheckFlags{},
	Output: &CommandOutput[managementapi.CheckAccessResponse]{
		TextDescription: "Table with columns: PERMISSION, RESOURCE SET (- when none is given), ALLOWED (yes or no).",
		Examples: []CommandExample{
			{
				Description: "Check whether you can push deployments in a team.",
				Command:     "baseten org access-check --permission model.deployment.write --team <team>",
			},
			{
				Description: "Check two permissions on one model in production.",
				CommandLines: []string{
					"baseten org access-check --permission model.read --permission model.deployment.write",
					"--resource-set model:<model-id>,environment:production",
				},
			},
		},
		JQExample: CommandExample{
			Description: "Print whether the first check is allowed.",
			Command:     "baseten org access-check --permission access.role.write --jq '.results[0].allowed'",
		},
	},
}

// commandOrgUserAccess is `baseten org user access`.
var commandOrgUserAccess = Command{
	Name:    "access",
	Summary: "Describe a user's roles and access (PRE-RELEASE)",
	Description: orgAccessPrereleaseNotice +
		"List every role a user holds, through an assignment to them or to a directory group " +
		"they're in, or through organization and team membership, and the permissions they grant. " +
		"Pass --user-id me for the authenticated user. " + orgAccessUnavailable,
	Flags: OrgUserAccessFlags{},
	Output: &CommandOutput[managementapi.EffectiveAccess]{
		TextDescription: orgAccessDescribeText,
		Examples: []CommandExample{
			{
				Description: "Describe your own access.",
				Command:     "baseten org user access --user-id me",
			},
			{
				Description: "Describe a user's access by email.",
				Command:     "baseten org user access --user-email <email>",
			},
		},
		JQExample: CommandExample{
			Description: "Print the permissions you hold.",
			Command:     "baseten org user access --user-id me --jq '.permissions[].name'",
		},
	},
}

// commandOrgServiceAccountAccess is `baseten org service-account access`.
var commandOrgServiceAccountAccess = Command{
	Name:    "access",
	Summary: "Describe a service account's roles and access (PRE-RELEASE)",
	Description: orgAccessPrereleaseNotice +
		"List every role a service account holds, through an assignment to it or through " +
		"organization and team membership, and the permissions they grant. " + orgAccessUnavailable,
	Flags: OrgServiceAccountAccessFlags{},
	Output: &CommandOutput[managementapi.EffectiveAccess]{
		TextDescription: orgAccessDescribeText,
		Examples: []CommandExample{
			{
				Description: "Describe a service account's access.",
				Command:     "baseten org service-account access --name ci-deployer",
			},
		},
		JQExample: CommandExample{
			Description: "Print the names of the roles it holds.",
			Command:     "baseten org service-account access --name ci-deployer --jq '.assignments[].role_name'",
		},
	},
}

// OrgRoleList is the JSON output of `baseten org role list`: the roles
// aggregated across all pages.
type OrgRoleList struct {
	Items []managementapi.AccessRole `json:"items"`
}

// OrgRoleAssignmentList is the JSON output of `baseten org role-assignment
// list`: the assignments aggregated across all pages.
type OrgRoleAssignmentList struct {
	Items []managementapi.AccessAssignment `json:"items"`
}

// OrgRoleAssignmentHolderFlags picks who holds a role, exactly one of them.
type OrgRoleAssignmentHolderFlags struct {
	UserID             string `flag:"user-id" desc:"User ID." oneof:"subject"`
	UserEmail          string `flag:"user-email" desc:"Email of the user." oneof:"subject"`
	ServiceAccountID   string `flag:"service-account-id" desc:"Service account ID." oneof:"subject"`
	ServiceAccountName string `flag:"service-account-name" desc:"Name of the service account." oneof:"subject"`
	DirectoryGroupID   string `flag:"directory-group-id" desc:"Directory group ID." oneof:"subject"`
}

// OrgRoleAssignmentHolderFilterFlags optionally narrows a listing to one holder.
type OrgRoleAssignmentHolderFilterFlags struct {
	UserID             string `flag:"user-id" desc:"Only this user's. Mutually exclusive with the other holder flags."`
	UserEmail          string `flag:"user-email" desc:"Only the user with this email's."`
	ServiceAccountID   string `flag:"service-account-id" desc:"Only this service account's."`
	ServiceAccountName string `flag:"service-account-name" desc:"Only the service account with this name's."`
	DirectoryGroupID   string `flag:"directory-group-id" desc:"Only this directory group's."`
}

type OrgRoleAssignmentCreateFlags struct {
	CommandFlags
	OrgRoleAssignmentHolderFlags

	RoleID       string   `flag:"role-id" desc:"Role to assign, by ID." oneof:"role-ref"`
	RoleName     string   `flag:"role-name" desc:"Role to assign, by name." oneof:"role-ref"`
	Team         string   `flag:"team" desc:"Team to assign a team role in, by name or ID. Omit for an organization role."`
	ResourceSets []string `flag:"resource-set" desc:"Limit the role to what this set selects, such as model:<model-id>,environment:staging. May be repeated."`
}

type OrgRoleAssignmentListFlags struct {
	CommandFlags
	OrgRoleAssignmentHolderFilterFlags

	Team     string `flag:"team" desc:"Only assignments in this team, by name or ID."`
	RoleID   string `flag:"role-id" desc:"Only assignments of the role with this ID. Mutually exclusive with --role-name."`
	RoleName string `flag:"role-name" desc:"Only assignments of the role with this name."`
}

// OrgRoleAssignmentRefFlags picks one role assignment.
type OrgRoleAssignmentRefFlags struct {
	CommandFlags

	ID string `flag:"id" desc:"Role assignment ID." required:"true"`
}

type OrgAccessCheckFlags struct {
	CommandFlags

	Permissions []string `flag:"permission" desc:"Permission to check, such as model.deployment.write. May be repeated." required:"true"`
	Team        string   `flag:"team" desc:"Check in this team, by name or ID."`
	ResourceSet string   `flag:"resource-set" desc:"Check where this set selects, such as model:<model-id>,environment:production."`
}

type OrgUserAccessFlags struct {
	CommandFlags

	UserID    string `flag:"user-id" desc:"User ID. Pass 'me' for the authenticated user." oneof:"user-ref"`
	UserEmail string `flag:"user-email" desc:"Email of the user." oneof:"user-ref"`
}

type OrgServiceAccountAccessFlags struct {
	CommandFlags
	OrgServiceAccountRefFlags
}

type OrgPermissionListFlags struct {
	CommandFlags
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
