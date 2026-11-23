package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("org access assignment create", commandOrgAccessAssignmentCreate)
	Register("org access assignment delete", commandOrgAccessAssignmentDelete)
	Register("org access assignment describe", commandOrgAccessAssignmentDescribe)
	Register("org access assignment list", commandOrgAccessAssignmentList)
	Register("org access check", commandOrgAccessCheck)
	Register("org access describe", commandOrgAccessDescribe)
	Register("org access permission list", commandOrgAccessPermissionList)
	Register("org access role create", commandOrgAccessRoleCreate)
	Register("org access role delete", commandOrgAccessRoleDelete)
	Register("org access role describe", commandOrgAccessRoleDescribe)
	Register("org access role list", commandOrgAccessRoleList)
	Register("org access role update", commandOrgAccessRoleUpdate)
	Register("org access settings describe", commandOrgAccessSettingsDescribe)
	Register("org access settings update", commandOrgAccessSettingsUpdate)
}

// accessAPI calls the /v1/access role endpoints, which the generated client
// doesn't cover while they are pre-release.
type accessAPI struct {
	api *managementapi.Client
}

func newAccessAPI(ctx *CommandContext) (accessAPI, error) {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return accessAPI{}, err
	}
	return accessAPI{api: cl.API()}, nil
}

// call sends body (when not nil) as JSON and decodes a 2xx response into out.
// Any other status returns a *managementapi.ResponseError, so failures exit
// like every other management API command.
func (a accessAPI) call(ctx context.Context, method, path string, query url.Values, body, out any) error {
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

type accessPage[T any] struct {
	Items      []T                              `json:"items"`
	Pagination managementapi.PaginationResponse `json:"pagination"`
}

// listAll follows a paginated access listing to its end.
func listAll[T any](ctx context.Context, a accessAPI, path string, query url.Values) ([]T, error) {
	query = cloneQuery(query)
	var items []T
	for {
		var page accessPage[T]
		if err := a.call(ctx, http.MethodGet, path, query, nil, &page); err != nil {
			return nil, err
		}
		items = append(items, page.Items...)
		if !page.Pagination.HasMore || page.Pagination.Cursor == nil {
			return items, nil
		}
		query.Set("cursor", *page.Pagination.Cursor)
	}
}

func cloneQuery(query url.Values) url.Values {
	out := url.Values{}
	for key, values := range query {
		out[key] = append([]string(nil), values...)
	}
	return out
}

// teamQuery resolves --team, when given, into a team_id query parameter.
func (a accessAPI) teamQuery(ctx context.Context, team string) (url.Values, string, error) {
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
func (a accessAPI) roleID(ctx context.Context, id, name, teamID string) (string, error) {
	if id != "" {
		return id, nil
	}
	query := url.Values{"name": {name}}
	if teamID != "" {
		query.Set("team_id", teamID)
	}
	roles, err := listAll[cmd.AccessRole](ctx, a, "/v1/access/roles", query)
	if err != nil {
		return "", fmt.Errorf("looking up role %q: %w", name, err)
	}
	for _, role := range roles {
		if role.Name == name {
			return role.ID, nil
		}
	}
	return "", cmd.NewErrNotFound(fmt.Errorf("no role named %q", name))
}

// userIDByEmail looks up the ID of the organization's user with this email.
func userIDByEmail(ctx context.Context, api *managementapi.Client, email string) (string, error) {
	resp, err := api.GetUsers(ctx, managementapi.GetV1UsersParams{Email: &email})
	if err != nil {
		return "", fmt.Errorf("looking up user %q: %w", email, err)
	}
	switch len(resp.Items) {
	case 0:
		return "", cmd.NewErrNotFound(fmt.Errorf("no user with email %q", email))
	case 1:
		return resp.Items[0].UserId, nil
	}
	return "", fmt.Errorf("multiple users with email %q", email)
}

// accessSubject is who holds a role, as the access API names it.
type accessSubject struct {
	Type string
	ID   string
}

// subject resolves the holder flags into an assignment subject. Returns nil
// when no flag is set.
func (a accessAPI) subject(ctx context.Context, f cmd.OrgAccessHolderFilterFlags) (*accessSubject, error) {
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
		return &accessSubject{Type: "USER", ID: f.UserID}, nil
	case f.UserEmail != "":
		id, err := userIDByEmail(ctx, a.api, f.UserEmail)
		if err != nil {
			return nil, err
		}
		return &accessSubject{Type: "USER", ID: id}, nil
	case f.ServiceAccountID != "":
		return &accessSubject{Type: "SERVICE_ACCOUNT", ID: f.ServiceAccountID}, nil
	case f.ServiceAccountName != "":
		id, err := orgServiceAccountIDByName(ctx, a.api, f.ServiceAccountName)
		if err != nil {
			return nil, err
		}
		return &accessSubject{Type: "SERVICE_ACCOUNT", ID: id}, nil
	case f.DirectoryGroupID != "":
		return &accessSubject{Type: "DIRECTORY_GROUP", ID: f.DirectoryGroupID}, nil
	}
	return nil, nil
}

// parseResourceSet reads TYPE:ID entries joined by commas, such as
// model:abc,environment:staging. Types are case-insensitive; the API checks
// the rest.
func parseResourceSet(value string) ([]cmd.AccessResourceSetEntry, error) {
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

func resourceSetText(set []cmd.AccessResourceSetEntry) string {
	entries := make([]string, 0, len(set))
	for _, entry := range set {
		entries = append(entries, strings.ToLower(entry.Type)+":"+entry.Identifier)
	}
	return strings.Join(entries, ",")
}

func resourceSetsText(sets []cmd.AccessResourceSet) string {
	if len(sets) == 0 {
		return "all"
	}
	texts := make([]string, 0, len(sets))
	for _, set := range sets {
		texts = append(texts, resourceSetText(set.AllOf))
	}
	return strings.Join(texts, "; ")
}

func commandOrgAccessPermissionList(ctx *CommandContext, _ *cmd.OrgAccessPermissionListFlags) error {
	a, err := newAccessAPI(ctx)
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
			p.Name, p.DisplayName, strings.ToLower(strings.Join(p.RoleKinds, ",")), yesNo(p.Available && !p.Reserved),
		})
	}
	ctx.OutputTable(TableOutput{Headers: []string{"NAME", "LABEL", "ROLE KINDS", "ASSIGNABLE"}, Rows: rows})
	return nil
}

func commandOrgAccessRoleList(ctx *CommandContext, flags *cmd.OrgAccessRoleListFlags) error {
	a, err := newAccessAPI(ctx)
	if err != nil {
		return err
	}
	query, _, err := a.teamQuery(ctx, flags.Team)
	if err != nil {
		return err
	}
	roles, err := listAll[cmd.AccessRole](ctx, a, "/v1/access/roles", query)
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
			r.ID, r.Name, strings.ToLower(r.Kind), orDash(r.TeamName), yesNo(r.BuiltIn), strings.Join(r.Permissions, ","),
		})
	}
	ctx.OutputTable(TableOutput{Headers: []string{"ID", "NAME", "KIND", "TEAM", "BUILT-IN", "PERMISSIONS"}, Rows: rows})
	return nil
}

func commandOrgAccessRoleDescribe(ctx *CommandContext, flags *cmd.OrgAccessRoleDescribeFlags) error {
	a, err := newAccessAPI(ctx)
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
	ctx.Outputf("ID:          %s\n", role.ID)
	ctx.Outputf("Name:        %s\n", role.Name)
	ctx.Outputf("Description: %s\n", role.Description)
	ctx.Outputf("Kind:        %s\n", strings.ToLower(role.Kind))
	ctx.Outputf("Team:        %s\n", orDash(role.TeamName))
	ctx.Outputf("Built-in:    %s\n", yesNo(role.BuiltIn))
	ctx.Outputf("Permissions: %s\n", joinOrDash(role.Permissions))
	ctx.Outputf("Includes:    %s\n", joinOrDash(role.IncludeRoleIDs))
	return nil
}

func commandOrgAccessRoleCreate(ctx *CommandContext, flags *cmd.OrgAccessRoleCreateFlags) error {
	a, err := newAccessAPI(ctx)
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

func commandOrgAccessRoleUpdate(ctx *CommandContext, flags *cmd.OrgAccessRoleUpdateFlags) error {
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
	a, err := newAccessAPI(ctx)
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

func commandOrgAccessRoleDelete(ctx *CommandContext, flags *cmd.OrgAccessRoleDeleteFlags) error {
	a, err := newAccessAPI(ctx)
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

func commandOrgAccessAssignmentCreate(ctx *CommandContext, flags *cmd.OrgAccessAssignmentCreateFlags) error {
	resourceSets := make([]cmd.AccessResourceSet, 0, len(flags.ResourceSets))
	for _, value := range flags.ResourceSets {
		set, err := parseResourceSet(value)
		if err != nil {
			return err
		}
		resourceSets = append(resourceSets, cmd.AccessResourceSet{AllOf: set})
	}
	a, err := newAccessAPI(ctx)
	if err != nil {
		return err
	}
	subject, err := a.subject(ctx, cmd.OrgAccessHolderFilterFlags(flags.OrgAccessSubjectFlags))
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
	ctx.OutputLine(orDash(assignment.ID))
	return nil
}

func commandOrgAccessAssignmentList(ctx *CommandContext, flags *cmd.OrgAccessAssignmentListFlags) error {
	if flags.RoleID != "" && flags.RoleName != "" {
		return cmd.NewErrUsagef("pass --role-id or --role-name, not both")
	}
	a, err := newAccessAPI(ctx)
	if err != nil {
		return err
	}
	subject, err := a.subject(ctx, flags.OrgAccessHolderFilterFlags)
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
	assignments, err := listAll[cmd.AccessAssignment](ctx, a, "/v1/access/assignments", query)
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
		rows = append(rows, []string{
			orDash(as.ID), as.RoleName, holderText(as), orDash(as.TeamName), resourceSetsText(as.ResourceSets),
			timeOrDash(as.CreatedAt),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "ROLE", "HOLDER", "TEAM", "RESOURCE SETS", "CREATED"},
		Rows:    rows,
	})
	return nil
}

func holderText(as cmd.AccessAssignment) string {
	return strings.ReplaceAll(strings.ToLower(as.SubjectType), "_", " ") + " " + as.SubjectName
}

func timeOrDash(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func commandOrgAccessAssignmentDescribe(ctx *CommandContext, flags *cmd.OrgAccessAssignmentRefFlags) error {
	a, err := newAccessAPI(ctx)
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
	ctx.Outputf("ID:            %s\n", orDash(as.ID))
	ctx.Outputf("Role:          %s (%s)\n", as.RoleName, as.RoleID)
	ctx.Outputf("Holder:        %s (%s)\n", holderText(as), as.SubjectID)
	ctx.Outputf("Team:          %s\n", orDash(as.TeamName))
	ctx.Outputf("Resource sets: %s\n", resourceSetsText(as.ResourceSets))
	ctx.Outputf("Created by:    %s\n", orDash(as.CreatedBy))
	ctx.Outputf("Created:       %s\n", timeOrDash(as.CreatedAt))
	return nil
}

func commandOrgAccessAssignmentDelete(ctx *CommandContext, flags *cmd.OrgAccessAssignmentRefFlags) error {
	a, err := newAccessAPI(ctx)
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

func commandOrgAccessDescribe(ctx *CommandContext, flags *cmd.OrgAccessDescribeFlags) error {
	a, err := newAccessAPI(ctx)
	if err != nil {
		return err
	}
	subject, err := a.subject(ctx, cmd.OrgAccessHolderFilterFlags{
		UserID:             flags.UserID,
		UserEmail:          flags.UserEmail,
		ServiceAccountID:   flags.ServiceAccountID,
		ServiceAccountName: flags.ServiceAccountName,
	})
	if err != nil {
		return err
	}
	path := "/v1/users/me/access"
	if subject != nil {
		switch subject.Type {
		case "SERVICE_ACCOUNT":
			path = "/v1/service_accounts/" + url.PathEscape(subject.ID) + "/access"
		case "USER":
			path = "/v1/users/" + url.PathEscape(subject.ID) + "/access"
		default:
			return fmt.Errorf("unexpected subject type %q", subject.Type)
		}
	}
	var access cmd.Access
	if err := a.call(ctx, http.MethodGet, path, nil, nil, &access); err != nil {
		return fmt.Errorf("describing access: %w", err)
	}
	if ctx.JSON {
		access.Assignments = normalizeJSONSlice(access.Assignments)
		ctx.OutputJSON(access)
		return nil
	}
	ctx.Outputf("%s %s (%s)\n", strings.ReplaceAll(strings.ToLower(access.SubjectType), "_", " "),
		access.SubjectName, access.SubjectID)
	rows := make([][]string, 0, len(access.Assignments))
	for _, held := range access.Assignments {
		via := strings.ToLower(held.GrantedVia)
		if held.SubjectType == "DIRECTORY_GROUP" {
			via += " to group " + held.SubjectName
		}
		rows = append(rows, []string{
			held.RoleName, via, orDash(held.TeamName), resourceSetsText(held.ResourceSets), orDash(held.ID),
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
			set := resourceSetText(grant.ResourceSet)
			if set == "" {
				set = "all"
			}
			grants = append(grants, []string{permission.Name, team, set})
		}
	}
	ctx.Outputf("\n")
	ctx.OutputTable(TableOutput{Headers: []string{"PERMISSION", "TEAM", "RESOURCE SET"}, Rows: grants})
	return nil
}

func commandOrgAccessCheck(ctx *CommandContext, flags *cmd.OrgAccessCheckFlags) error {
	var resourceSet []cmd.AccessResourceSetEntry
	if flags.ResourceSet != "" {
		set, err := parseResourceSet(flags.ResourceSet)
		if err != nil {
			return err
		}
		resourceSet = set
	}
	a, err := newAccessAPI(ctx)
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
		set := resourceSetText(r.ResourceSet)
		if set == "" {
			set = "-"
		}
		rows = append(rows, []string{r.Permission, orDash(r.TeamID), set, yesNo(r.Allowed)})
	}
	ctx.OutputTable(TableOutput{Headers: []string{"PERMISSION", "TEAM", "RESOURCE SET", "ALLOWED"}, Rows: rows})
	return nil
}

func commandOrgAccessSettingsDescribe(ctx *CommandContext, _ *cmd.OrgAccessSettingsDescribeFlags) error {
	a, err := newAccessAPI(ctx)
	if err != nil {
		return err
	}
	var settings cmd.AccessSettings
	if err := a.call(ctx, http.MethodGet, "/v1/organizations/me", nil, nil, &settings); err != nil {
		return fmt.Errorf("reading role settings: %w", err)
	}
	if settings.AllowTeamAdminsToManageRoles == nil {
		return errRoleSettingsUnavailable
	}
	if ctx.JSON {
		ctx.OutputJSON(settings)
		return nil
	}
	ctx.Outputf("Team admins manage roles: %s\n", yesNo(*settings.AllowTeamAdminsToManageRoles))
	return nil
}

func commandOrgAccessSettingsUpdate(ctx *CommandContext, flags *cmd.OrgAccessSettingsUpdateFlags) error {
	a, err := newAccessAPI(ctx)
	if err != nil {
		return err
	}
	body := cmd.AccessSettings{AllowTeamAdminsToManageRoles: flags.AllowTeamAdminsToManageRoles.Pointer()}
	var settings cmd.AccessSettings
	if err := a.call(ctx, http.MethodPatch, "/v1/organizations/me", nil, body, &settings); err != nil {
		return fmt.Errorf("updating role settings: %w", err)
	}
	if settings.AllowTeamAdminsToManageRoles == nil {
		return errRoleSettingsUnavailable
	}
	if ctx.JSON {
		ctx.OutputJSON(settings)
		return nil
	}
	ctx.LogLine("Updated role settings")
	return nil
}

// errRoleSettingsUnavailable is the organization's answer without a role
// setting: custom roles aren't available to it, or the caller can't read its roles.
var errRoleSettingsUnavailable = cmd.NewErrNotFound(errors.New("role settings aren't available: " +
	"your organization doesn't have custom roles, or you can't read its roles"))

func orDash(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

func joinOrDash(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ", ")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
