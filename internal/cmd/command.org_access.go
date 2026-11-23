package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
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

// orgAccessAPI calls the custom role endpoints, and the organization's role
// setting, which the generated client doesn't cover while they are pre-release.
type orgAccessAPI struct {
	api *managementapi.Client
}

func newOrgAccessAPI(ctx *CommandContext) (orgAccessAPI, error) {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return orgAccessAPI{}, err
	}
	return orgAccessAPI{api: cl.API()}, nil
}

// call sends body (when not nil) as JSON and decodes a 2xx response into out.
// Any other status returns a *managementapi.ResponseError, so failures exit
// like every other management API command.
func (a orgAccessAPI) call(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.api.BaseURL, "/")+path, reader)
	if err != nil {
		return err
	}
	if len(query) > 0 {
		req.URL.RawQuery = query.Encode()
	}
	for key, values := range a.api.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.api.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return &managementapi.ResponseError{StatusCode: resp.StatusCode, Body: string(raw)}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding %s %s response: %w", method, path, err)
	}
	return nil
}

type orgAccessPage[T any] struct {
	Items      []T                              `json:"items"`
	Pagination managementapi.PaginationResponse `json:"pagination"`
}

// orgAccessListAll follows a paginated listing to its end.
func orgAccessListAll[T any](ctx context.Context, a orgAccessAPI, path string, query url.Values) ([]T, error) {
	pageQuery := url.Values{}
	maps.Copy(pageQuery, query)
	var items []T
	for {
		var page orgAccessPage[T]
		if err := a.call(ctx, http.MethodGet, path, pageQuery, nil, &page); err != nil {
			return nil, err
		}
		items = append(items, page.Items...)
		if !page.Pagination.HasMore || page.Pagination.Cursor == nil {
			return items, nil
		}
		pageQuery.Set("cursor", *page.Pagination.Cursor)
	}
}

// teamQuery resolves --team, when given, into a team_id query parameter.
func (a orgAccessAPI) teamQuery(ctx context.Context, team string) (url.Values, string, error) {
	query := url.Values{}
	teamID, err := ResolveTeam(ctx, a.api, team)
	if err != nil {
		return nil, "", err
	}
	if teamID != "" {
		query.Set("team_id", teamID)
	}
	return query, teamID, nil
}

// roleID resolves a role reference. Names are unique in the organization. A
// name is looked up in teamID's role list when given, which a team admin can
// read without organization-wide access to roles.
func (a orgAccessAPI) roleID(ctx context.Context, id, name, teamID string) (string, error) {
	if id != "" {
		return id, nil
	}
	query := url.Values{"name": {name}}
	if teamID != "" {
		query.Set("team_id", teamID)
	}
	roles, err := orgAccessListAll[cmd.AccessRole](ctx, a, "/v1/access/roles", query)
	if err != nil {
		return "", fmt.Errorf("looking up role %q: %w", name, err)
	}
	var ids []string
	for _, role := range roles {
		if role.Name == name {
			ids = append(ids, role.ID)
		}
	}
	switch len(ids) {
	case 0:
		return "", cmd.NewErrNotFound(fmt.Errorf("no role named %q", name))
	case 1:
		return ids[0], nil
	}
	// A custom role can share a built-in role's name.
	return "", cmd.NewErrUsagef("more than one role is named %q (%s); pass its ID instead", name, strings.Join(ids, ", "))
}

// orgAccessSubject is who holds a role, as the access API names it.
type orgAccessSubject struct {
	Type string
	ID   string
}

// subject resolves the holder flags into an assignment subject. Returns nil
// when no flag is set.
func (a orgAccessAPI) subject(ctx context.Context, f cmd.OrgRoleAssignmentHolderFilterFlags) (*orgAccessSubject, error) {
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
		return &orgAccessSubject{Type: "USER", ID: f.UserID}, nil
	case f.UserEmail != "":
		user, err := orgUserByEmail(ctx, a.api, f.UserEmail)
		if err != nil {
			return nil, err
		}
		return &orgAccessSubject{Type: "USER", ID: user.UserId}, nil
	case f.ServiceAccountID != "":
		return &orgAccessSubject{Type: "SERVICE_ACCOUNT", ID: f.ServiceAccountID}, nil
	case f.ServiceAccountName != "":
		id, err := orgServiceAccountIDByName(ctx, a.api, f.ServiceAccountName)
		if err != nil {
			return nil, err
		}
		return &orgAccessSubject{Type: "SERVICE_ACCOUNT", ID: id}, nil
	case f.DirectoryGroupID != "":
		return &orgAccessSubject{Type: "DIRECTORY_GROUP", ID: f.DirectoryGroupID}, nil
	}
	return nil, nil
}

// orgAccessParseResourceSet reads TYPE:ID entries joined by commas, such as
// model:abc,environment:staging. Types are case-insensitive; the API checks
// the rest.
func orgAccessParseResourceSet(value string) ([]cmd.AccessResourceSetEntry, error) {
	var entries []cmd.AccessResourceSetEntry
	for _, part := range strings.Split(value, ",") {
		resourceType, id, ok := strings.Cut(strings.TrimSpace(part), ":")
		if !ok || resourceType == "" || id == "" {
			return nil, cmd.NewErrUsagef("resource set entry %q isn't TYPE:ID, such as model:<model-id> or environment:staging", part)
		}
		entries = append(entries, cmd.AccessResourceSetEntry{Type: strings.ToUpper(resourceType), Identifier: id})
	}
	return entries, nil
}

// orgAccessResourceSetText renders a resource set as the --resource-set
// syntax, or fallback when it's empty.
func orgAccessResourceSetText(set []cmd.AccessResourceSetEntry, fallback string) string {
	if len(set) == 0 {
		return fallback
	}
	entries := make([]string, 0, len(set))
	for _, entry := range set {
		entries = append(entries, strings.ToLower(entry.Type)+":"+entry.Identifier)
	}
	return strings.Join(entries, ",")
}

func orgAccessResourceSetsText(sets []cmd.AccessResourceSet) string {
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
func orgAccessSubjectText(subjectType, name string) string {
	return strings.ReplaceAll(strings.ToLower(subjectType), "_", " ") + " " + name
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

func commandOrgPermissionList(ctx *CommandContext, _ *cmd.OrgPermissionListFlags) error {
	a, err := newOrgAccessAPI(ctx)
	if err != nil {
		return err
	}
	var catalog cmd.AccessPermissionCatalog
	if err := a.call(ctx, http.MethodGet, "/v1/access/permissions", nil, nil, &catalog); err != nil {
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
		rows = append(rows, []string{
			p.Name, p.DisplayName, strings.ToLower(strings.Join(p.RoleKinds, ",")), orgAccessYesNo(p.Available && !p.Reserved),
		})
	}
	ctx.OutputTable(TableOutput{Headers: []string{"NAME", "LABEL", "ROLE KINDS", "ASSIGNABLE"}, Rows: rows})
	return nil
}

func commandOrgRoleList(ctx *CommandContext, flags *cmd.OrgRoleListFlags) error {
	a, err := newOrgAccessAPI(ctx)
	if err != nil {
		return err
	}
	query, _, err := a.teamQuery(ctx, flags.Team)
	if err != nil {
		return err
	}
	roles, err := orgAccessListAll[cmd.AccessRole](ctx, a, "/v1/access/roles", query)
	if err != nil {
		return fmt.Errorf("listing roles: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.AccessRoleList{Items: normalizeJSONSlice(roles)})
		return nil
	}
	if len(roles) == 0 {
		ctx.LogLine("No roles found.")
		return nil
	}
	rows := make([][]string, 0, len(roles))
	for _, r := range roles {
		rows = append(rows, []string{
			r.ID, r.Name, strings.ToLower(r.Kind), orgAccessOrDash(r.TeamName), orgAccessYesNo(r.BuiltIn),
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
	a, err := newOrgAccessAPI(ctx)
	if err != nil {
		return err
	}
	query, teamID, err := a.teamQuery(ctx, flags.Team)
	if err != nil {
		return err
	}
	id, err := a.roleID(ctx, flags.ID, flags.Name, teamID)
	if err != nil {
		return err
	}
	var role cmd.AccessRole
	if err := a.call(ctx, http.MethodGet, "/v1/access/roles/"+url.PathEscape(id), query, nil, &role); err != nil {
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
	if len(role.IncludeRoleIDs) > 0 {
		includes = strings.Join(role.IncludeRoleIDs, ", ")
	}
	ctx.Outputf("ID:          %s\n", role.ID)
	ctx.Outputf("Name:        %s\n", role.Name)
	ctx.Outputf("Description: %s\n", role.Description)
	ctx.Outputf("Kind:        %s\n", strings.ToLower(role.Kind))
	ctx.Outputf("Team:        %s\n", orgAccessOrDash(role.TeamName))
	ctx.Outputf("Built-in:    %s\n", orgAccessYesNo(role.BuiltIn))
	ctx.Outputf("Permissions: %s\n", permissions)
	ctx.Outputf("Includes:    %s\n", includes)
	return nil
}

func commandOrgRoleCreate(ctx *CommandContext, flags *cmd.OrgRoleCreateFlags) error {
	a, err := newOrgAccessAPI(ctx)
	if err != nil {
		return err
	}
	body := map[string]any{
		"name":             flags.Name,
		"kind":             strings.ToUpper(flags.Kind),
		"description":      flags.Description,
		"permissions":      normalizeJSONSlice(flags.Permissions),
		"include_role_ids": normalizeJSONSlice(flags.Includes),
	}
	if flags.Team != "" {
		teamID, err := ResolveTeam(ctx, a.api, flags.Team)
		if err != nil {
			return err
		}
		body["team_id"] = teamID
	}
	var role cmd.AccessRole
	if err := a.call(ctx, http.MethodPost, "/v1/access/roles", nil, body, &role); err != nil {
		return fmt.Errorf("creating role: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(role)
		return nil
	}
	ctx.Logf("Created role %s\n", role.Name)
	ctx.OutputLine(role.ID)
	return nil
}

func commandOrgRoleUpdate(ctx *CommandContext, flags *cmd.OrgRoleUpdateFlags) error {
	body := map[string]any{}
	if flags.NewName != "" {
		body["name"] = flags.NewName
	}
	if flags.Description.IsSet() {
		body["description"] = *flags.Description.Pointer()
	}
	for key, values := range map[string][]string{
		"permissions":                flags.AddPermissions,
		"permissions_to_delete":      flags.RemovePermissions,
		"include_role_ids":           flags.AddIncludes,
		"include_role_ids_to_delete": flags.RemoveIncludes,
	} {
		if len(values) > 0 {
			body[key] = values
		}
	}
	if len(body) == 0 {
		return cmd.NewErrUsagef("pass at least one of --new-name, --description, --add-permission, " +
			"--remove-permission, --add-include and --remove-include")
	}
	a, err := newOrgAccessAPI(ctx)
	if err != nil {
		return err
	}
	id, err := a.roleID(ctx, flags.ID, flags.Name, "")
	if err != nil {
		return err
	}
	var role cmd.AccessRole
	if err := a.call(ctx, http.MethodPatch, "/v1/access/roles/"+url.PathEscape(id), nil, body, &role); err != nil {
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
	a, err := newOrgAccessAPI(ctx)
	if err != nil {
		return err
	}
	id, err := a.roleID(ctx, flags.ID, flags.Name, "")
	if err != nil {
		return err
	}
	var deleted cmd.AccessDeleted
	if err := a.call(ctx, http.MethodDelete, "/v1/access/roles/"+url.PathEscape(id), nil, nil, &deleted); err != nil {
		return fmt.Errorf("deleting role %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(deleted)
		return nil
	}
	ctx.Logf("Deleted role %s\n", deleted.ID)
	return nil
}

func commandOrgRoleAssignmentCreate(ctx *CommandContext, flags *cmd.OrgRoleAssignmentCreateFlags) error {
	resourceSets := make([]cmd.AccessResourceSet, 0, len(flags.ResourceSets))
	for _, value := range flags.ResourceSets {
		set, err := orgAccessParseResourceSet(value)
		if err != nil {
			return err
		}
		resourceSets = append(resourceSets, cmd.AccessResourceSet{AllOf: set})
	}
	a, err := newOrgAccessAPI(ctx)
	if err != nil {
		return err
	}
	subject, err := a.subject(ctx, cmd.OrgRoleAssignmentHolderFilterFlags(flags.OrgRoleAssignmentHolderFlags))
	if err != nil {
		return err
	}
	if subject == nil {
		return cmd.NewErrUsagef("pass one of --user-id, --user-email, --service-account-id, " +
			"--service-account-name and --directory-group-id")
	}
	teamID, err := ResolveTeam(ctx, a.api, flags.Team)
	if err != nil {
		return err
	}
	roleID, err := a.roleID(ctx, flags.RoleID, flags.RoleName, teamID)
	if err != nil {
		return err
	}
	body := map[string]any{"role_id": roleID, "subject_type": subject.Type, "subject_id": subject.ID}
	if len(resourceSets) > 0 {
		body["resource_sets"] = resourceSets
	}
	if teamID != "" {
		body["team_id"] = teamID
	}
	var assignment cmd.AccessAssignment
	if err := a.call(ctx, http.MethodPost, "/v1/access/assignments", nil, body, &assignment); err != nil {
		return fmt.Errorf("assigning role %s: %w", roleID, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(assignment)
		return nil
	}
	ctx.Logf("Assigned role %s\n", assignment.RoleName)
	ctx.OutputLine(orgAccessOrDash(assignment.ID))
	return nil
}

func commandOrgRoleAssignmentList(ctx *CommandContext, flags *cmd.OrgRoleAssignmentListFlags) error {
	if flags.RoleID != "" && flags.RoleName != "" {
		return cmd.NewErrUsagef("pass --role-id or --role-name, not both")
	}
	a, err := newOrgAccessAPI(ctx)
	if err != nil {
		return err
	}
	subject, err := a.subject(ctx, flags.OrgRoleAssignmentHolderFilterFlags)
	if err != nil {
		return err
	}
	query, teamID, err := a.teamQuery(ctx, flags.Team)
	if err != nil {
		return err
	}
	if flags.RoleID != "" || flags.RoleName != "" {
		roleID, err := a.roleID(ctx, flags.RoleID, flags.RoleName, teamID)
		if err != nil {
			return err
		}
		query.Set("role_id", roleID)
	}
	if subject != nil {
		query.Set("subject_type", subject.Type)
		query.Set("subject_id", subject.ID)
	}
	assignments, err := orgAccessListAll[cmd.AccessAssignment](ctx, a, "/v1/access/assignments", query)
	if err != nil {
		return fmt.Errorf("listing role assignments: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.AccessAssignmentList{Items: normalizeJSONSlice(assignments)})
		return nil
	}
	if len(assignments) == 0 {
		ctx.LogLine("No role assignments found.")
		return nil
	}
	rows := make([][]string, 0, len(assignments))
	for _, as := range assignments {
		created := "-"
		if as.CreatedAt != nil {
			created = as.CreatedAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, []string{
			orgAccessOrDash(as.ID), as.RoleName, orgAccessSubjectText(as.SubjectType, as.SubjectName),
			orgAccessOrDash(as.TeamName), orgAccessResourceSetsText(as.ResourceSets), created,
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "ROLE", "HOLDER", "TEAM", "RESOURCE SETS", "CREATED"},
		Rows:    rows,
	})
	return nil
}

func commandOrgRoleAssignmentDescribe(ctx *CommandContext, flags *cmd.OrgRoleAssignmentRefFlags) error {
	a, err := newOrgAccessAPI(ctx)
	if err != nil {
		return err
	}
	var as cmd.AccessAssignment
	if err := a.call(ctx, http.MethodGet, "/v1/access/assignments/"+url.PathEscape(flags.ID), nil, nil, &as); err != nil {
		return fmt.Errorf("describing role assignment %s: %w", flags.ID, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(as)
		return nil
	}
	created := "-"
	if as.CreatedAt != nil {
		created = as.CreatedAt.UTC().Format(time.RFC3339)
	}
	ctx.Outputf("ID:            %s\n", orgAccessOrDash(as.ID))
	ctx.Outputf("Role:          %s (%s)\n", as.RoleName, as.RoleID)
	ctx.Outputf("Holder:        %s (%s)\n", orgAccessSubjectText(as.SubjectType, as.SubjectName), as.SubjectID)
	ctx.Outputf("Team:          %s\n", orgAccessOrDash(as.TeamName))
	ctx.Outputf("Resource sets: %s\n", orgAccessResourceSetsText(as.ResourceSets))
	ctx.Outputf("Created by:    %s\n", orgAccessOrDash(as.CreatedBy))
	ctx.Outputf("Created:       %s\n", created)
	return nil
}

func commandOrgRoleAssignmentDelete(ctx *CommandContext, flags *cmd.OrgRoleAssignmentRefFlags) error {
	a, err := newOrgAccessAPI(ctx)
	if err != nil {
		return err
	}
	var deleted cmd.AccessDeleted
	if err := a.call(ctx, http.MethodDelete, "/v1/access/assignments/"+url.PathEscape(flags.ID), nil, nil, &deleted); err != nil {
		return fmt.Errorf("removing role assignment %s: %w", flags.ID, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(deleted)
		return nil
	}
	ctx.Logf("Removed role assignment %s\n", deleted.ID)
	return nil
}

func commandOrgUserAccess(ctx *CommandContext, flags *cmd.OrgUserAccessFlags) error {
	a, err := newOrgAccessAPI(ctx)
	if err != nil {
		return err
	}
	userID := flags.UserID
	if flags.UserEmail != "" {
		user, err := orgUserByEmail(ctx, a.api, flags.UserEmail)
		if err != nil {
			return err
		}
		userID = user.UserId
	}
	return orgAccessDescribe(ctx, a, "/v1/users/"+url.PathEscape(userID)+"/access")
}

func commandOrgServiceAccountAccess(ctx *CommandContext, flags *cmd.OrgServiceAccountAccessFlags) error {
	a, err := newOrgAccessAPI(ctx)
	if err != nil {
		return err
	}
	id, err := resolveOrgServiceAccountRef(ctx, a.api, flags.OrgServiceAccountRefFlags)
	if err != nil {
		return err
	}
	return orgAccessDescribe(ctx, a, "/v1/service_accounts/"+url.PathEscape(id)+"/access")
}

// orgAccessDescribe prints the roles and permissions a principal's access
// endpoint returns.
func orgAccessDescribe(ctx *CommandContext, a orgAccessAPI, path string) error {
	var access cmd.Access
	if err := a.call(ctx, http.MethodGet, path, nil, nil, &access); err != nil {
		return fmt.Errorf("describing access: %w", err)
	}
	if ctx.JSON {
		access.Assignments = normalizeJSONSlice(access.Assignments)
		ctx.OutputJSON(access)
		return nil
	}
	ctx.Outputf("%s (%s)\n", orgAccessSubjectText(access.SubjectType, access.SubjectName), access.SubjectID)
	rows := make([][]string, 0, len(access.Assignments))
	for _, held := range access.Assignments {
		via := strings.ToLower(held.GrantedVia)
		if held.SubjectType == "DIRECTORY_GROUP" {
			via += " to group " + held.SubjectName
		}
		rows = append(rows, []string{
			held.RoleName, via, orgAccessOrDash(held.TeamName), orgAccessResourceSetsText(held.ResourceSets),
			orgAccessOrDash(held.ID),
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
	for _, permission := range access.Permissions {
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
	var resourceSet []cmd.AccessResourceSetEntry
	if flags.ResourceSet != "" {
		set, err := orgAccessParseResourceSet(flags.ResourceSet)
		if err != nil {
			return err
		}
		resourceSet = set
	}
	a, err := newOrgAccessAPI(ctx)
	if err != nil {
		return err
	}
	var teamID *string
	if flags.Team != "" {
		id, err := ResolveTeam(ctx, a.api, flags.Team)
		if err != nil {
			return err
		}
		teamID = &id
	}
	checks := make([]cmd.AccessCheck, 0, len(flags.Permissions))
	for _, permission := range flags.Permissions {
		checks = append(checks, cmd.AccessCheck{Permission: permission, TeamID: teamID, ResourceSet: resourceSet})
	}
	var results cmd.AccessCheckResults
	if err := a.call(ctx, http.MethodPost, "/v1/access/check", nil, map[string]any{"checks": checks}, &results); err != nil {
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
