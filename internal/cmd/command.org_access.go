package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("org access-check", commandOrgAccessCheck)
	Register("org permission list", commandOrgPermissionList)
	Register("org role create", commandOrgRoleCreate)
	Register("org role delete", commandOrgRoleDelete)
	Register("org role describe", commandOrgRoleDescribe)
	Register("org role list", commandOrgRoleList)
	Register("org role update", commandOrgRoleUpdate)
	Register("org role-assignment create", commandOrgRoleAssignmentCreate)
	Register("org role-assignment delete", commandOrgRoleAssignmentDelete)
	Register("org role-assignment describe", commandOrgRoleAssignmentDescribe)
	Register("org role-assignment list", commandOrgRoleAssignmentList)
	Register("org service-account access", commandOrgServiceAccountAccess)
	Register("org user access", commandOrgUserAccess)
}

// orgListRoles follows the role listing to its end.
func orgListRoles(ctx context.Context, api *managementapi.Client, params managementapi.GetV1AccessRolesParams) ([]managementapi.AccessRole, error) {
	var roles []managementapi.AccessRole
	for {
		page, err := api.GetAccessRoles(ctx, params)
		if err != nil {
			return nil, err
		}
		roles = append(roles, page.Items...)
		if !page.Pagination.HasMore || page.Pagination.Cursor == nil {
			return roles, nil
		}
		params.Cursor = page.Pagination.Cursor
	}
}

// orgResolveTeamID resolves --team, when given, into a team ID; nil when not.
func orgResolveTeamID(ctx context.Context, api *managementapi.Client, team string) (*string, error) {
	teamID, err := ResolveTeam(ctx, api, team)
	if err != nil || teamID == "" {
		return nil, err
	}
	return &teamID, nil
}

// orgRoleID resolves a role reference. Names are unique in the organization. A
// name is looked up in teamID's role list when given, which a team admin can
// read without organization-wide access to roles.
func orgRoleID(ctx context.Context, api *managementapi.Client, id, name string, teamID *string) (string, error) {
	if id != "" {
		return id, nil
	}
	roles, err := orgListRoles(ctx, api, managementapi.GetV1AccessRolesParams{Name: &name, TeamId: teamID})
	if err != nil {
		return "", fmt.Errorf("looking up role %q: %w", name, err)
	}
	for _, role := range roles {
		if role.Name == name {
			return role.Id, nil
		}
	}
	return "", cmd.NewErrNotFound(fmt.Errorf("no role named %q", name))
}

// orgAccessSubject is who holds a role, as the access API names it.
type orgAccessSubject struct {
	Type managementapi.AccessSubjectType
	ID   string
}

// orgAccessSubjectFromFlags resolves the holder flags into an assignment
// subject. Returns nil when no flag is set.
func orgAccessSubjectFromFlags(ctx context.Context, api *managementapi.Client, f cmd.OrgRoleAssignmentHolderFilterFlags) (*orgAccessSubject, error) {
	set := 0
	for _, value := range []string{f.UserID, f.UserEmail, f.ServiceAccountID, f.ServiceAccountName, f.DirectoryGroupID} {
		if value != "" {
			set++
		}
	}
	if set > 1 {
		return nil, cmd.NewErrUsagef("pass at most one of --user-id, --user-email, --service-account-id, " +
			"--service-account-name and --directory-group-id")
	}
	switch {
	case f.UserID != "":
		return &orgAccessSubject{Type: managementapi.AccessSubjectType_USER, ID: f.UserID}, nil
	case f.UserEmail != "":
		user, err := orgUserByEmail(ctx, api, f.UserEmail)
		if err != nil {
			return nil, err
		}
		return &orgAccessSubject{Type: managementapi.AccessSubjectType_USER, ID: user.UserId}, nil
	case f.ServiceAccountID != "":
		return &orgAccessSubject{Type: managementapi.AccessSubjectType_SERVICE_ACCOUNT, ID: f.ServiceAccountID}, nil
	case f.ServiceAccountName != "":
		id, err := orgServiceAccountIDByName(ctx, api, f.ServiceAccountName)
		if err != nil {
			return nil, err
		}
		return &orgAccessSubject{Type: managementapi.AccessSubjectType_SERVICE_ACCOUNT, ID: id}, nil
	case f.DirectoryGroupID != "":
		return &orgAccessSubject{Type: managementapi.AccessSubjectType_DIRECTORY_GROUP, ID: f.DirectoryGroupID}, nil
	}
	return nil, nil
}

// orgAccessParseResourceSet reads TYPE:ID entries joined by commas, such as
// model:abc,environment:staging. Types are case-insensitive; the API checks
// the rest.
func orgAccessParseResourceSet(value string) ([]managementapi.AccessResourceRequest, error) {
	var entries []managementapi.AccessResourceRequest
	for _, part := range strings.Split(value, ",") {
		resourceType, id, ok := strings.Cut(strings.TrimSpace(part), ":")
		if !ok || resourceType == "" || id == "" {
			return nil, cmd.NewErrUsagef("resource set entry %q isn't TYPE:ID, such as model:<model-id> or environment:staging", part)
		}
		entries = append(entries, managementapi.AccessResourceRequest{
			Type: managementapi.AccessResourceType(strings.ToUpper(resourceType)), Identifier: id,
		})
	}
	return entries, nil
}

// orgAccessResourceSetText renders a resource set as the --resource-set
// syntax, or fallback when it's empty.
func orgAccessResourceSetText(set []managementapi.AccessResource, fallback string) string {
	if len(set) == 0 {
		return fallback
	}
	entries := make([]string, 0, len(set))
	for _, entry := range set {
		entries = append(entries, strings.ToLower(string(entry.Type))+":"+entry.Identifier)
	}
	return strings.Join(entries, ",")
}

func orgAccessResourceSetsText(sets []managementapi.AccessResourceSet) string {
	if len(sets) == 0 {
		return "all"
	}
	texts := make([]string, 0, len(sets))
	for _, set := range sets {
		texts = append(texts, orgAccessResourceSetText(set.AllOf, ""))
	}
	return strings.Join(texts, "; ")
}

// orgAccessSubjectText renders a role holder, such as "service account ci-deployer".
func orgAccessSubjectText(subjectType managementapi.AccessSubjectType, name string) string {
	return strings.ReplaceAll(strings.ToLower(string(subjectType)), "_", " ") + " " + name
}

func orgAccessOrDash(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

func orgAccessYesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func orgAccessCreatedText(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func commandOrgPermissionList(ctx *CommandContext, _ *cmd.OrgPermissionListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	catalog, err := cl.API().GetAccessPermissions(ctx)
	if err != nil {
		return fmt.Errorf("listing permissions: %w", err)
	}
	if ctx.JSON {
		catalog.Categories = normalizeJSONSlice(catalog.Categories)
		catalog.Permissions = normalizeJSONSlice(catalog.Permissions)
		ctx.OutputJSON(catalog)
		return nil
	}
	rows := make([][]string, 0, len(catalog.Permissions))
	for _, p := range catalog.Permissions {
		kinds := make([]string, 0, len(p.RoleKinds))
		for _, kind := range p.RoleKinds {
			kinds = append(kinds, strings.ToLower(string(kind)))
		}
		rows = append(rows, []string{
			p.Name, p.DisplayName, strings.Join(kinds, ","), orgAccessYesNo(p.Available && !p.Reserved),
		})
	}
	ctx.OutputTable(TableOutput{Headers: []string{"NAME", "LABEL", "ROLE KINDS", "ASSIGNABLE"}, Rows: rows})
	return nil
}

func commandOrgRoleList(ctx *CommandContext, flags *cmd.OrgRoleListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	teamID, err := orgResolveTeamID(ctx, cl.API(), flags.Team)
	if err != nil {
		return err
	}
	roles, err := orgListRoles(ctx, cl.API(), managementapi.GetV1AccessRolesParams{TeamId: teamID})
	if err != nil {
		return fmt.Errorf("listing roles: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.OrgRoleList{Items: normalizeJSONSlice(roles)})
		return nil
	}
	if len(roles) == 0 {
		ctx.LogLine("No roles found.")
		return nil
	}
	rows := make([][]string, 0, len(roles))
	for _, r := range roles {
		rows = append(rows, []string{
			r.Id, r.Name, strings.ToLower(string(r.Kind)), orgAccessOrDash(r.TeamName), orgAccessYesNo(r.BuiltIn),
			strconv.Itoa(len(r.Permissions)),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"ID", "NAME", "KIND", "TEAM", "BUILT-IN", "PERMISSIONS"},
		Rows:                rows,
		RightAlignedColumns: []int{5},
	})
	return nil
}

func commandOrgRoleDescribe(ctx *CommandContext, flags *cmd.OrgRoleDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	teamID, err := orgResolveTeamID(ctx, cl.API(), flags.Team)
	if err != nil {
		return err
	}
	id, err := orgRoleID(ctx, cl.API(), flags.ID, flags.Name, teamID)
	if err != nil {
		return err
	}
	role, err := cl.API().GetAccessRolesRoleId(ctx, id, managementapi.GetV1AccessRolesRoleIdParams{TeamId: teamID})
	if err != nil {
		return fmt.Errorf("describing role %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(role)
		return nil
	}
	permissions, includes := "-", "-"
	if len(role.Permissions) > 0 {
		permissions = strings.Join(role.Permissions, ", ")
	}
	if len(role.IncludeRoleIds) > 0 {
		includes = strings.Join(role.IncludeRoleIds, ", ")
	}
	ctx.Outputf("ID:          %s\n", role.Id)
	ctx.Outputf("Name:        %s\n", role.Name)
	ctx.Outputf("Description: %s\n", role.Description)
	ctx.Outputf("Kind:        %s\n", strings.ToLower(string(role.Kind)))
	ctx.Outputf("Team:        %s\n", orgAccessOrDash(role.TeamName))
	ctx.Outputf("Built-in:    %s\n", orgAccessYesNo(role.BuiltIn))
	ctx.Outputf("Permissions: %s\n", permissions)
	ctx.Outputf("Includes:    %s\n", includes)
	return nil
}

func commandOrgRoleCreate(ctx *CommandContext, flags *cmd.OrgRoleCreateFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	teamID, err := orgResolveTeamID(ctx, cl.API(), flags.Team)
	if err != nil {
		return err
	}
	permissions, includes := normalizeJSONSlice(flags.Permissions), normalizeJSONSlice(flags.Includes)
	role, err := cl.API().PostAccessRoles(ctx, managementapi.CreateAccessRoleRequest{
		Name:           flags.Name,
		Kind:           managementapi.AccessRoleKind(strings.ToUpper(flags.Kind)),
		Description:    &flags.Description,
		Permissions:    &permissions,
		IncludeRoleIds: &includes,
		TeamId:         teamID,
	})
	if err != nil {
		return fmt.Errorf("creating role: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(role)
		return nil
	}
	ctx.Logf("Created role %s\n", role.Name)
	ctx.OutputLine(role.Id)
	return nil
}

func commandOrgRoleUpdate(ctx *CommandContext, flags *cmd.OrgRoleUpdateFlags) error {
	nonEmpty := func(values []string) *[]string {
		if len(values) == 0 {
			return nil
		}
		return &values
	}
	req := managementapi.UpdateAccessRoleRequest{
		Description:            flags.Description.Pointer(),
		Permissions:            nonEmpty(flags.AddPermissions),
		PermissionsToDelete:    nonEmpty(flags.RemovePermissions),
		IncludeRoleIds:         nonEmpty(flags.AddIncludes),
		IncludeRoleIdsToDelete: nonEmpty(flags.RemoveIncludes),
	}
	if flags.NewName != "" {
		req.Name = &flags.NewName
	}
	if req == (managementapi.UpdateAccessRoleRequest{}) {
		return cmd.NewErrUsagef("pass at least one of --new-name, --description, --add-permission, " +
			"--remove-permission, --add-include and --remove-include")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := orgRoleID(ctx, cl.API(), flags.ID, flags.Name, nil)
	if err != nil {
		return err
	}
	role, err := cl.API().PatchAccessRoles(ctx, id, req)
	if err != nil {
		return fmt.Errorf("updating role %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(role)
		return nil
	}
	ctx.Logf("Updated role %s\n", role.Name)
	return nil
}

func commandOrgRoleDelete(ctx *CommandContext, flags *cmd.OrgRoleDeleteFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := orgRoleID(ctx, cl.API(), flags.ID, flags.Name, nil)
	if err != nil {
		return err
	}
	deleted, err := cl.API().DeleteAccessRoles(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting role %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(deleted)
		return nil
	}
	ctx.Logf("Deleted role %s\n", deleted.Id)
	return nil
}

func commandOrgRoleAssignmentCreate(ctx *CommandContext, flags *cmd.OrgRoleAssignmentCreateFlags) error {
	resourceSets := make([]managementapi.AccessResourceSetRequest, 0, len(flags.ResourceSets))
	for _, value := range flags.ResourceSets {
		set, err := orgAccessParseResourceSet(value)
		if err != nil {
			return err
		}
		resourceSets = append(resourceSets, managementapi.AccessResourceSetRequest{AllOf: set})
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	subject, err := orgAccessSubjectFromFlags(ctx, cl.API(), cmd.OrgRoleAssignmentHolderFilterFlags(flags.OrgRoleAssignmentHolderFlags))
	if err != nil {
		return err
	}
	if subject == nil {
		return cmd.NewErrUsagef("pass one of --user-id, --user-email, --service-account-id, " +
			"--service-account-name and --directory-group-id")
	}
	teamID, err := orgResolveTeamID(ctx, cl.API(), flags.Team)
	if err != nil {
		return err
	}
	roleID, err := orgRoleID(ctx, cl.API(), flags.RoleID, flags.RoleName, teamID)
	if err != nil {
		return err
	}
	req := managementapi.CreateAccessAssignmentRequest{
		RoleId: roleID, SubjectType: subject.Type, SubjectId: subject.ID, TeamId: teamID,
	}
	if len(resourceSets) > 0 {
		req.ResourceSets = &resourceSets
	}
	assignment, err := cl.API().PostAccessAssignments(ctx, req)
	if err != nil {
		return fmt.Errorf("assigning role %s: %w", roleID, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(assignment)
		return nil
	}
	ctx.Logf("Assigned role %s\n", assignment.RoleName)
	ctx.OutputLine(orgAccessOrDash(assignment.Id))
	return nil
}

func commandOrgRoleAssignmentList(ctx *CommandContext, flags *cmd.OrgRoleAssignmentListFlags) error {
	if flags.RoleID != "" && flags.RoleName != "" {
		return cmd.NewErrUsagef("pass --role-id or --role-name, not both")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	subject, err := orgAccessSubjectFromFlags(ctx, cl.API(), flags.OrgRoleAssignmentHolderFilterFlags)
	if err != nil {
		return err
	}
	teamID, err := orgResolveTeamID(ctx, cl.API(), flags.Team)
	if err != nil {
		return err
	}
	params := managementapi.GetV1AccessAssignmentsParams{TeamId: teamID}
	if flags.RoleID != "" || flags.RoleName != "" {
		roleID, err := orgRoleID(ctx, cl.API(), flags.RoleID, flags.RoleName, teamID)
		if err != nil {
			return err
		}
		params.RoleId = &roleID
	}
	if subject != nil {
		params.SubjectType = &subject.Type
		params.SubjectId = &subject.ID
	}
	var assignments []managementapi.AccessAssignment
	for {
		page, err := cl.API().GetAccessAssignments(ctx, params)
		if err != nil {
			return fmt.Errorf("listing role assignments: %w", err)
		}
		assignments = append(assignments, page.Items...)
		if !page.Pagination.HasMore || page.Pagination.Cursor == nil {
			break
		}
		params.Cursor = page.Pagination.Cursor
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.OrgRoleAssignmentList{Items: normalizeJSONSlice(assignments)})
		return nil
	}
	if len(assignments) == 0 {
		ctx.LogLine("No role assignments found.")
		return nil
	}
	rows := make([][]string, 0, len(assignments))
	for _, as := range assignments {
		rows = append(rows, []string{
			orgAccessOrDash(as.Id), as.RoleName, orgAccessSubjectText(as.SubjectType, as.SubjectName),
			orgAccessOrDash(as.TeamName), orgAccessResourceSetsText(as.ResourceSets), orgAccessCreatedText(as.CreatedAt),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "ROLE", "HOLDER", "TEAM", "RESOURCE SETS", "CREATED"},
		Rows:    rows,
	})
	return nil
}

func commandOrgRoleAssignmentDescribe(ctx *CommandContext, flags *cmd.OrgRoleAssignmentRefFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	as, err := cl.API().GetAccessAssignmentsAssignmentId(ctx, flags.ID)
	if err != nil {
		return fmt.Errorf("describing role assignment %s: %w", flags.ID, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(as)
		return nil
	}
	ctx.Outputf("ID:            %s\n", orgAccessOrDash(as.Id))
	ctx.Outputf("Role:          %s (%s)\n", as.RoleName, as.RoleId)
	ctx.Outputf("Holder:        %s (%s)\n", orgAccessSubjectText(as.SubjectType, as.SubjectName), as.SubjectId)
	ctx.Outputf("Team:          %s\n", orgAccessOrDash(as.TeamName))
	ctx.Outputf("Resource sets: %s\n", orgAccessResourceSetsText(as.ResourceSets))
	ctx.Outputf("Created by:    %s\n", orgAccessOrDash(as.CreatedBy))
	ctx.Outputf("Created:       %s\n", orgAccessCreatedText(as.CreatedAt))
	return nil
}

func commandOrgRoleAssignmentDelete(ctx *CommandContext, flags *cmd.OrgRoleAssignmentRefFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	deleted, err := cl.API().DeleteAccessAssignments(ctx, flags.ID)
	if err != nil {
		return fmt.Errorf("removing role assignment %s: %w", flags.ID, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(deleted)
		return nil
	}
	ctx.Logf("Removed role assignment %s\n", deleted.Id)
	return nil
}

func commandOrgUserAccess(ctx *CommandContext, flags *cmd.OrgUserAccessFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	userID := flags.UserID
	if flags.UserEmail != "" {
		user, err := orgUserByEmail(ctx, cl.API(), flags.UserEmail)
		if err != nil {
			return err
		}
		userID = user.UserId
	}
	access, err := cl.API().GetUsersAccess(ctx, userID)
	if err != nil {
		return fmt.Errorf("describing access: %w", err)
	}
	return orgAccessDescribe(ctx, access)
}

func commandOrgServiceAccountAccess(ctx *CommandContext, flags *cmd.OrgServiceAccountAccessFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveOrgServiceAccountRef(ctx, cl.API(), flags.OrgServiceAccountRefFlags)
	if err != nil {
		return err
	}
	access, err := cl.API().GetServiceAccountsAccess(ctx, id)
	if err != nil {
		return fmt.Errorf("describing access: %w", err)
	}
	return orgAccessDescribe(ctx, access)
}

// orgAccessDescribe prints the roles and permissions a principal holds.
func orgAccessDescribe(ctx *CommandContext, access *managementapi.EffectiveAccess) error {
	if ctx.JSON {
		access.Assignments = normalizeJSONSlice(access.Assignments)
		ctx.OutputJSON(access)
		return nil
	}
	ctx.Outputf("%s (%s)\n", orgAccessSubjectText(access.SubjectType, access.SubjectName), access.SubjectId)
	rows := make([][]string, 0, len(access.Assignments))
	for _, held := range access.Assignments {
		via := strings.ToLower(string(held.GrantedVia))
		if held.SubjectType == managementapi.AccessSubjectType_DIRECTORY_GROUP {
			via += " to group " + held.SubjectName
		}
		rows = append(rows, []string{
			held.RoleName, via, orgAccessOrDash(held.TeamName), orgAccessResourceSetsText(held.ResourceSets),
			orgAccessOrDash(held.Id),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ROLE", "HELD VIA", "TEAM", "RESOURCE SETS", "ASSIGNMENT"},
		Rows:    rows,
	})
	if access.Permissions == nil {
		ctx.LogLine("Effective permissions aren't available yet.")
		return nil
	}
	var grants [][]string
	for _, permission := range *access.Permissions {
		for _, grant := range permission.Grants {
			team := "organization"
			if grant.TeamName != nil {
				team = *grant.TeamName
			}
			grants = append(grants, []string{permission.Name, team, orgAccessResourceSetText(grant.ResourceSet, "all")})
		}
	}
	ctx.Outputf("\n")
	ctx.OutputTable(TableOutput{Headers: []string{"PERMISSION", "TEAM", "RESOURCE SET"}, Rows: grants})
	return nil
}

func commandOrgAccessCheck(ctx *CommandContext, flags *cmd.OrgAccessCheckFlags) error {
	var resourceSet *[]managementapi.AccessResourceRequest
	if flags.ResourceSet != "" {
		set, err := orgAccessParseResourceSet(flags.ResourceSet)
		if err != nil {
			return err
		}
		resourceSet = &set
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	teamID, err := orgResolveTeamID(ctx, cl.API(), flags.Team)
	if err != nil {
		return err
	}
	checks := make([]managementapi.AccessCheck, 0, len(flags.Permissions))
	for _, permission := range flags.Permissions {
		checks = append(checks, managementapi.AccessCheck{Permission: permission, TeamId: teamID, ResourceSet: resourceSet})
	}
	results, err := cl.API().PostAccessCheck(ctx, managementapi.CheckAccessRequest{Checks: checks})
	if err != nil {
		return fmt.Errorf("checking access: %w", err)
	}
	if ctx.JSON {
		results.Results = normalizeJSONSlice(results.Results)
		ctx.OutputJSON(results)
		return nil
	}
	rows := make([][]string, 0, len(results.Results))
	for _, r := range results.Results {
		rows = append(rows, []string{r.Permission, orgAccessResourceSetText(r.ResourceSet, "-"), orgAccessYesNo(r.Allowed)})
	}
	ctx.OutputTable(TableOutput{Headers: []string{"PERMISSION", "RESOURCE SET", "ALLOWED"}, Rows: rows})
	return nil
}
