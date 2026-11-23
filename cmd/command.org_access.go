package cmd

import "time"

const orgAccessUnavailable = "Organizations without custom roles get not found from every command."

// resourceSetUsage explains the --resource-set syntax shared by assignment create and check.
const resourceSetUsage = "A resource set is TYPE:ID entries joined by commas, at most one resource and at most one " +
	"environment, such as model:<model-id>,environment:production. An environment entry names the " +
	"environment, such as staging."

// commandOrgAccess groups the `baseten org access` subcommands.
var commandOrgAccess = Command{
	Name:    "access",
	Summary: "Manage custom roles and role assignments (PRE-RELEASE)",
	Description: "Manage custom roles, assign them to users, service accounts and directory groups, " +
		"and check what a principal can do. " + orgAccessUnavailable,
	Hidden: true,
	Children: []Command{
		{
			Name:        "assignment",
			Summary:     "Manage role assignments (PRE-RELEASE)",
			Description: "Give roles to users, service accounts and directory groups, for the organization or in a team.",
			Children: []Command{
				{
					Name:    "create",
					Summary: "Assign a role (PRE-RELEASE)",
					Description: "Give a role to a user, service account or directory group. An organization " +
						"role applies across the organization; a team role needs --team. Pass --resource-set " +
						"to limit the role to what any of the sets selects; omit it for every resource where " +
						"the role applies. " + resourceSetUsage,
					Flags: OrgAccessAssignmentCreateFlags{},
					Output: &CommandOutput[AccessAssignment]{
						TextDescription: "Prints the new assignment's ID on stdout, and \"Assigned role " +
							"<role name>\" on stderr.",
						Examples: []CommandExample{
							{
								Description: "Let a service account push one model's deployments in staging, and every model's in production.",
								CommandLines: []string{
									"baseten org access assignment create --role-name deployer",
									"--service-account-name ci-deployer --team <team>",
									"--resource-set model:<model-id>,environment:staging --resource-set environment:production",
								},
							},
							{
								Description: "Give a user an organization role.",
								Command:     "baseten org access assignment create --role-name auditor --user-email <email>",
							},
						},
						JQExample: CommandExample{
							Description: "Print where the role applies.",
							CommandLines: []string{
								"baseten org access assignment create --role-id <role> --user-id <user>",
								"--team <team> --jq '.resource_sets[].all_of'",
							},
						},
					},
				},
				{
					Name:        "delete",
					Summary:     "Remove a role assignment (PRE-RELEASE)",
					Description: "Remove a role assignment by ID.",
					Flags:       OrgAccessAssignmentRefFlags{},
					Output: &CommandOutput[AccessDeleted]{
						TextDescription: "Prints \"Removed role assignment <ID>\" to stderr; no stdout output.",
						Examples: []CommandExample{
							{
								Description: "Remove an assignment.",
								Command:     "baseten org access assignment delete --id <assignment>",
							},
						},
						JQExample: CommandExample{
							Description: "Print the removed assignment's ID.",
							Command:     "baseten org access assignment delete --id <assignment> --jq '.id'",
						},
					},
				},
				{
					Name:        "describe",
					Summary:     "Describe a role assignment (PRE-RELEASE)",
					Description: "Describe a role assignment by ID.",
					Flags:       OrgAccessAssignmentRefFlags{},
					Output: &CommandOutput[AccessAssignment]{
						TextDescription: "Field-per-line summary of the assignment.",
						Examples: []CommandExample{
							{
								Description: "Describe an assignment.",
								Command:     "baseten org access assignment describe --id <assignment>",
							},
						},
						JQExample: CommandExample{
							Description: "Print who holds the role.",
							Command:     "baseten org access assignment describe --id <assignment> --jq '.subject_name'",
						},
					},
				},
				{
					Name:    "list",
					Summary: "List role assignments (PRE-RELEASE)",
					Description: "List role assignments, optionally only one team's, one role's, or one " +
						"holder's. Roles held through organization and team membership aren't listed; " +
						"'baseten org access describe' shows them.",
					Flags: OrgAccessAssignmentListFlags{},
					Output: &CommandOutput[AccessAssignmentList]{
						TextDescription: "Table with columns: ID, ROLE, HOLDER, TEAM, RESOURCE SETS, CREATED. " +
							"When none match, prints \"No role assignments found.\" to stderr.",
						Examples: []CommandExample{
							{
								Description: "List a team's assignments.",
								Command:     "baseten org access assignment list --team <team>",
							},
							{
								Description: "List a service account's assignments.",
								Command:     "baseten org access assignment list --service-account-name ci-deployer",
							},
						},
						JQExample: CommandExample{
							Description: "Print the names of the roles assigned.",
							Command:     "baseten org access assignment list --jq '.items[].role_name'",
						},
					},
				},
			},
		},
		{
			Name:    "check",
			Summary: "Check whether you hold permissions (PRE-RELEASE)",
			Description: "Check whether you hold each --permission: in --team, where --resource-set selects, " +
				"or across the organization when neither is given. To check whether you can create something " +
				"in a team, check its write permission in the team. " + resourceSetUsage,
			Flags: OrgAccessCheckFlags{},
			Output: &CommandOutput[AccessCheckResults]{
				TextDescription: "Table with columns: PERMISSION, TEAM, RESOURCE SET, ALLOWED (yes or no).",
				Examples: []CommandExample{
					{
						Description: "Check whether you can push deployments in a team.",
						Command:     "baseten org access check --permission model.deployment.write --team <team>",
					},
					{
						Description: "Check two permissions on one model in production.",
						CommandLines: []string{
							"baseten org access check --permission model.read --permission model.deployment.write",
							"--resource-set model:<model-id>,environment:production",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print whether the first check is allowed.",
					Command:     "baseten org access check --permission access.role.write --jq '.results[0].allowed'",
				},
			},
		},
		{
			Name:    "describe",
			Summary: "Describe a principal's roles and access (PRE-RELEASE)",
			Description: "List every role a user or service account holds, through an assignment to it or to " +
				"a directory group it's in, or through organization and team membership, and the permissions " +
				"they grant. Without a flag, describes you.",
			Flags: OrgAccessDescribeFlags{},
			Output: &CommandOutput[Access]{
				TextDescription: "The principal on the first line, then a table of roles with columns: ROLE, " +
					"HELD VIA, TEAM, RESOURCE SETS, ASSIGNMENT, then a table of permissions with columns: " +
					"PERMISSION, TEAM, RESOURCE SET. Prints \"Effective permissions aren't available yet.\" to " +
					"stderr instead of the permissions until fine-grained authorization decides the " +
					"organization's requests.",
				Examples: []CommandExample{
					{
						Description: "Describe your own access.",
						Command:     "baseten org access describe",
					},
					{
						Description: "Describe a service account's access.",
						Command:     "baseten org access describe --service-account-name ci-deployer",
					},
				},
				JQExample: CommandExample{
					Description: "Print the permissions you hold.",
					Command:     "baseten org access describe --jq '.permissions[].name'",
				},
			},
		},
		{
			Name:    "permission",
			Summary: "View the permissions roles can hold (PRE-RELEASE)",
			Children: []Command{
				{
					Name:    "list",
					Summary: "List the permissions roles can grant (PRE-RELEASE)",
					Description: "List the permissions roles can grant. A custom role can hold a permission " +
						"only when it's assignable: available to the organization and not reserved for " +
						"built-in admin roles.",
					Flags: OrgAccessPermissionListFlags{},
					Output: &CommandOutput[AccessPermissionCatalog]{
						TextDescription: "Table with columns: NAME, LABEL, ROLE KINDS, ASSIGNABLE.",
						Examples: []CommandExample{
							{
								Description: "List the permissions.",
								Command:     "baseten org access permission list",
							},
						},
						JQExample: CommandExample{
							Description: "Print the permissions a team role can hold.",
							CommandLines: []string{
								"baseten org access permission list",
								"--jq '.permissions[] | select(.role_kinds | index(\"TEAM\")) | .name'",
							},
						},
					},
				},
			},
		},
		{
			Name:        "role",
			Summary:     "Manage roles (PRE-RELEASE)",
			Description: "View built-in roles and manage the organization's custom roles.",
			Children: []Command{
				{
					Name:    "create",
					Summary: "Create a custom role (PRE-RELEASE)",
					Description: "Create a custom role. An organization role is assigned across the " +
						"organization; a team role is assigned in a team, any team unless --team names the " +
						"only one it can be assigned in. Run 'baseten org access permission list' for the " +
						"permissions it can grant.",
					Flags: OrgAccessRoleCreateFlags{},
					Output: &CommandOutput[AccessRole]{
						TextDescription: "Prints the new role's ID on stdout, and \"Created role <name>\" on stderr.",
						Examples: []CommandExample{
							{
								Description: "Create a team role that pushes and views deployments.",
								CommandLines: []string{
									"baseten org access role create --name deployer --kind team",
									"--permission model.deployment.write --permission model.read",
								},
							},
						},
						JQExample: CommandExample{
							Description: "Print just the new role's ID.",
							CommandLines: []string{
								"baseten org access role create --name auditor --kind organization",
								"--permission access.member.read --jq '.id'",
							},
						},
					},
				},
				{
					Name:    "delete",
					Summary: "Delete a custom role (PRE-RELEASE)",
					Description: "Delete a custom role. Fails while any assignment uses it; remove those " +
						"first. Built-in roles can't be deleted.",
					Flags: OrgAccessRoleDeleteFlags{},
					Output: &CommandOutput[AccessDeleted]{
						TextDescription: "Prints \"Deleted role <ID>\" to stderr; no stdout output.",
						Examples: []CommandExample{
							{
								Description: "Delete a role by name.",
								Command:     "baseten org access role delete --name deployer",
							},
						},
						JQExample: CommandExample{
							Description: "Print the deleted role's ID.",
							Command:     "baseten org access role delete --name deployer --jq '.id'",
						},
					},
				},
				{
					Name:        "describe",
					Summary:     "Describe a role (PRE-RELEASE)",
					Description: "Describe a built-in or custom role by ID or name.",
					Flags:       OrgAccessRoleDescribeFlags{},
					Output: &CommandOutput[AccessRole]{
						TextDescription: "Field-per-line summary of the role.",
						Examples: []CommandExample{
							{
								Description: "Describe a role by name.",
								Command:     "baseten org access role describe --name deployer",
							},
						},
						JQExample: CommandExample{
							Description: "Print the role's permissions.",
							Command:     "baseten org access role describe --name deployer --jq '.permissions'",
						},
					},
				},
				{
					Name:    "list",
					Summary: "List roles (PRE-RELEASE)",
					Description: "List built-in roles and the organization's custom roles. Pass --team for " +
						"only the roles that can be assigned in that team.",
					Flags: OrgAccessRoleListFlags{},
					Output: &CommandOutput[AccessRoleList]{
						TextDescription: "Table with columns: ID, NAME, KIND, TEAM, BUILT-IN, PERMISSIONS (the " +
							"role's own, comma-separated).",
						Examples: []CommandExample{
							{
								Description: "List every role.",
								Command:     "baseten org access role list",
							},
							{
								Description: "List the roles a team can assign.",
								Command:     "baseten org access role list --team <team>",
							},
						},
						JQExample: CommandExample{
							Description: "Print the names of the custom roles.",
							Command:     "baseten org access role list --jq '.items[] | select(.built_in | not) | .name'",
						},
					},
				},
				{
					Name:    "update",
					Summary: "Update a custom role (PRE-RELEASE)",
					Description: "Change a custom role's name or description, add or remove permissions, " +
						"and include or stop including built-in roles. Changes apply to every holder of the role.",
					Flags: OrgAccessRoleUpdateFlags{},
					Output: &CommandOutput[AccessRole]{
						TextDescription: "Prints \"Updated role <name>\" to stderr; no stdout output.",
						Examples: []CommandExample{
							{
								Description: "Let a role delete deployments, and stop it pushing them.",
								CommandLines: []string{
									"baseten org access role update --name deployer",
									"--add-permission model.deployment.delete --remove-permission model.deployment.write",
								},
							},
						},
						JQExample: CommandExample{
							Description: "Print the role's new permissions.",
							Command:     "baseten org access role update --name deployer --description 'CI' --jq '.permissions'",
						},
					},
				},
			},
		},
		{
			Name:        "settings",
			Summary:     "Manage role settings (PRE-RELEASE)",
			Description: "Read and change the organization's role management settings.",
			Children: []Command{
				{
					Name:        "describe",
					Summary:     "Show role settings (PRE-RELEASE)",
					Description: "Show whether team admins can create, change and delete their team's custom roles.",
					Flags:       OrgAccessSettingsDescribeFlags{},
					Output: &CommandOutput[AccessSettings]{
						TextDescription: "Field-per-line summary of the settings.",
						Examples: []CommandExample{
							{
								Description: "Show the settings.",
								Command:     "baseten org access settings describe",
							},
						},
						JQExample: CommandExample{
							Description: "Print whether team admins manage roles.",
							Command:     "baseten org access settings describe --jq '.allow_team_admins_to_manage_roles'",
						},
					},
				},
				{
					Name:    "update",
					Summary: "Change role settings (PRE-RELEASE)",
					Description: "Change whether team admins can create, change and delete their team's custom " +
						"roles. Either way, team admins can still assign roles in their team. Needs " +
						"access.role.write for the organization.",
					Flags: OrgAccessSettingsUpdateFlags{},
					Output: &CommandOutput[AccessSettings]{
						TextDescription: "Prints \"Updated role settings\" to stderr; no stdout output.",
						Examples: []CommandExample{
							{
								Description: "Let only organization admins manage roles.",
								Command:     "baseten org access settings update --allow-team-admins-to-manage-roles false",
							},
						},
						JQExample: CommandExample{
							Description: "Print the new setting.",
							CommandLines: []string{
								"baseten org access settings update --allow-team-admins-to-manage-roles true",
								"--jq '.allow_team_admins_to_manage_roles'",
							},
						},
					},
				},
			},
		},
	},
}

// AccessPermission is a permission roles can grant.
type AccessPermission struct {
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	RoleKinds   []string `json:"role_kinds"`
	Reserved    bool     `json:"reserved"`
	Available   bool     `json:"available"`
}

// AccessPermissionCategory groups related permissions.
type AccessPermissionCategory struct {
	Name                 string   `json:"name"`
	DisplayName          string   `json:"display_name"`
	Description          string   `json:"description"`
	ResourceTypes        []string `json:"resource_types"`
	SupportsEnvironments bool     `json:"supports_environments"`
}

// AccessPermissionCatalog is the JSON output of `baseten org access permission list`.
type AccessPermissionCatalog struct {
	Categories  []AccessPermissionCategory `json:"categories"`
	Permissions []AccessPermission         `json:"permissions"`
}

// AccessRole is a built-in or custom role.
type AccessRole struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	BuiltIn        bool     `json:"built_in"`
	Kind           string   `json:"kind"`
	TeamID         *string  `json:"team_id"`
	TeamName       *string  `json:"team_name"`
	Permissions    []string `json:"permissions"`
	IncludeRoleIDs []string `json:"include_role_ids"`
}

// AccessRoleList is the JSON output of `baseten org access role list`: the
// roles aggregated across all pages.
type AccessRoleList struct {
	Items []AccessRole `json:"items"`
}

// AccessDeleted is the JSON output of the access delete commands.
type AccessDeleted struct {
	ID string `json:"id"`
}

// AccessResourceSetEntry is a resource, or an environment by name.
type AccessResourceSetEntry struct {
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
}

// AccessResourceSet selects what every entry in AllOf selects.
type AccessResourceSet struct {
	AllOf []AccessResourceSetEntry `json:"all_of"`
}

// AccessAssignment is a role held by a user, service account or directory
// group: assigned (ID set), or through membership (ID null).
type AccessAssignment struct {
	ID           *string             `json:"id"`
	GrantedVia   string              `json:"granted_via"`
	RoleID       string              `json:"role_id"`
	RoleName     string              `json:"role_name"`
	SubjectType  string              `json:"subject_type"`
	SubjectID    string              `json:"subject_id"`
	SubjectName  string              `json:"subject_name"`
	TeamID       *string             `json:"team_id"`
	TeamName     *string             `json:"team_name"`
	ResourceSets []AccessResourceSet `json:"resource_sets"`
	CreatedBy    *string             `json:"created_by"`
	CreatedAt    *time.Time          `json:"created_at"`
}

// AccessAssignmentList is the JSON output of `baseten org access assignment
// list`: the assignments aggregated across all pages.
type AccessAssignmentList struct {
	Items []AccessAssignment `json:"items"`
}

// AccessGrant is where a permission is held: the organization, a team, or in
// it what one resource set selects.
type AccessGrant struct {
	TeamID      *string                  `json:"team_id"`
	TeamName    *string                  `json:"team_name"`
	ResourceSet []AccessResourceSetEntry `json:"resource_set"`
}

// AccessHeldPermission is a permission held, and where.
type AccessHeldPermission struct {
	Name        string        `json:"name"`
	DisplayName string        `json:"display_name"`
	Category    string        `json:"category"`
	Grants      []AccessGrant `json:"grants"`
}

// Access is the JSON output of `baseten org access describe`.
type Access struct {
	SubjectType string                 `json:"subject_type"`
	SubjectID   string                 `json:"subject_id"`
	SubjectName string                 `json:"subject_name"`
	Assignments []AccessAssignment     `json:"assignments"`
	Permissions []AccessHeldPermission `json:"permissions"`
}

// AccessCheck is one permission to check: for the organization, in a team, or
// where a resource set selects.
type AccessCheck struct {
	Permission  string                   `json:"permission"`
	TeamID      *string                  `json:"team_id,omitempty"`
	ResourceSet []AccessResourceSetEntry `json:"resource_set,omitempty"`
}

// AccessCheckResult answers one check.
type AccessCheckResult struct {
	Permission  string                   `json:"permission"`
	TeamID      *string                  `json:"team_id"`
	ResourceSet []AccessResourceSetEntry `json:"resource_set"`
	Allowed     bool                     `json:"allowed"`
}

// AccessCheckResults is the JSON output of `baseten org access check`.
type AccessCheckResults struct {
	Results []AccessCheckResult `json:"results"`
}

// AccessSettings is the organization's role management settings.
type AccessSettings struct {
	AllowTeamAdminsToManageRoles *bool `json:"allow_team_admins_to_manage_roles"`
}

// OrgAccessSubjectFlags picks who holds a role, exactly one of them.
type OrgAccessSubjectFlags struct {
	UserID             string `flag:"user-id" desc:"ID of the user." oneof:"subject"`
	UserEmail          string `flag:"user-email" desc:"Email of the user." oneof:"subject"`
	ServiceAccountID   string `flag:"service-account-id" desc:"ID of the service account." oneof:"subject"`
	ServiceAccountName string `flag:"service-account-name" desc:"Name of the service account." oneof:"subject"`
	DirectoryGroupID   string `flag:"directory-group-id" desc:"ID of the directory group." oneof:"subject"`
}

// OrgAccessHolderFilterFlags optionally narrows a listing to one holder.
type OrgAccessHolderFilterFlags struct {
	UserID             string `flag:"user-id" desc:"Only this user's. Mutually exclusive with the other holder flags."`
	UserEmail          string `flag:"user-email" desc:"Only the user with this email's."`
	ServiceAccountID   string `flag:"service-account-id" desc:"Only this service account's."`
	ServiceAccountName string `flag:"service-account-name" desc:"Only the service account with this name's."`
	DirectoryGroupID   string `flag:"directory-group-id" desc:"Only this directory group's."`
}

type OrgAccessAssignmentCreateFlags struct {
	CommandFlags
	OrgAccessSubjectFlags

	RoleID       string   `flag:"role-id" desc:"ID of the role to assign." oneof:"role-ref"`
	RoleName     string   `flag:"role-name" desc:"Name of the role to assign." oneof:"role-ref"`
	Team         string   `flag:"team" desc:"Team to assign a team role in, by name or ID. Omit for an organization role."`
	ResourceSets []string `flag:"resource-set" desc:"Limit the role to what this set selects, such as model:<model-id>,environment:staging. May be repeated."`
}

type OrgAccessAssignmentListFlags struct {
	CommandFlags
	OrgAccessHolderFilterFlags

	Team     string `flag:"team" desc:"Only assignments in this team, by name or ID."`
	RoleID   string `flag:"role-id" desc:"Only assignments of the role with this ID. Mutually exclusive with --role-name."`
	RoleName string `flag:"role-name" desc:"Only assignments of the role with this name."`
}

// OrgAccessAssignmentRefFlags picks one role assignment.
type OrgAccessAssignmentRefFlags struct {
	CommandFlags

	ID string `flag:"id" desc:"Role assignment ID." required:"true"`
}

type OrgAccessCheckFlags struct {
	CommandFlags

	Permissions []string `flag:"permission" desc:"Permission to check, such as model.deployment.write. May be repeated." required:"true"`
	Team        string   `flag:"team" desc:"Check in this team, by name or ID."`
	ResourceSet string   `flag:"resource-set" desc:"Check where this set selects, such as model:<model-id>,environment:production."`
}

type OrgAccessDescribeFlags struct {
	CommandFlags

	UserID             string `flag:"user-id" desc:"Describe this user. Mutually exclusive with the other flags; omit all of them to describe yourself."`
	UserEmail          string `flag:"user-email" desc:"Describe the user with this email."`
	ServiceAccountID   string `flag:"service-account-id" desc:"Describe this service account."`
	ServiceAccountName string `flag:"service-account-name" desc:"Describe the service account with this name."`
}

type OrgAccessPermissionListFlags struct {
	CommandFlags
}

// OrgAccessRoleRefFlags picks one role.
type OrgAccessRoleRefFlags struct {
	ID   string `flag:"id" desc:"Role ID." oneof:"role-ref"`
	Name string `flag:"name" desc:"Role name." oneof:"role-ref"`
}

type OrgAccessRoleCreateFlags struct {
	CommandFlags

	Name        string   `flag:"name" desc:"Name, unique in the organization." required:"true"`
	Kind        string   `flag:"kind" desc:"Where the role is assigned: across the organization, or in teams." enum:"organization,team" required:"true"`
	Team        string   `flag:"team" desc:"For a team role, the only team it can be assigned in, by name or ID. Omit to let any team use it."`
	Description string   `flag:"description" desc:"Optional description."`
	Permissions []string `flag:"permission" desc:"Permission the role grants, such as model.read. May be repeated."`
	Includes    []string `flag:"include" desc:"ID of a built-in role whose permissions the role also grants, as they change, such as baseten:reader. May be repeated."`
}

type OrgAccessRoleDeleteFlags struct {
	CommandFlags
	OrgAccessRoleRefFlags
}

type OrgAccessRoleDescribeFlags struct {
	CommandFlags
	OrgAccessRoleRefFlags

	Team string `flag:"team" desc:"Describe the role as this team's role list shows it, by name or ID. Needed only to manage a team's roles without organization-wide access to roles."`
}

type OrgAccessRoleListFlags struct {
	CommandFlags

	Team string `flag:"team" desc:"Only the roles that can be assigned in this team, by name or ID."`
}

type OrgAccessRoleUpdateFlags struct {
	CommandFlags
	OrgAccessRoleRefFlags

	NewName           string               `flag:"new-name" desc:"New name, unique in the organization."`
	Description       OptionalFlag[string] `flag:"description" desc:"New description. Pass an empty string to clear it."`
	AddPermissions    []string             `flag:"add-permission" desc:"Permission to add to the role. May be repeated."`
	RemovePermissions []string             `flag:"remove-permission" desc:"Permission to remove from the role. May be repeated."`
	AddIncludes       []string             `flag:"add-include" desc:"ID of a built-in role for the role to include. May be repeated."`
	RemoveIncludes    []string             `flag:"remove-include" desc:"ID of a built-in role for the role to stop including. May be repeated."`
}

type OrgAccessSettingsDescribeFlags struct {
	CommandFlags
}

type OrgAccessSettingsUpdateFlags struct {
	CommandFlags

	AllowTeamAdminsToManageRoles OptionalFlag[bool] `flag:"allow-team-admins-to-manage-roles" desc:"Whether team admins can create, change and delete their team's custom roles." required:"true"`
}
