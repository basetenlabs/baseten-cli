package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
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

// roleID resolves a role reference. Names are unique in the organization.
func (a accessAPI) roleID(ctx context.Context, id, name string) (string, error) {
	if id != "" {
		return id, nil
	}
	roles, err := listAll[cmd.AccessRole](ctx, a, "/v1/access/roles", nil)
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

// subject resolves the holder flags into an assignment subject. Service
// accounts are users to role assignments. Returns nil when no flag is set.
func (a accessAPI) subject(ctx context.Context, f cmd.OrgAccessHolderFilterFlags) (*cmd.AccessSubject, error) {
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
		return &cmd.AccessSubject{Type: "user", ID: f.UserID}, nil
	case f.UserEmail != "":
		id, err := userIDByEmail(ctx, a.api, f.UserEmail)
		if err != nil {
			return nil, err
		}
		return &cmd.AccessSubject{Type: "user", ID: id}, nil
	case f.ServiceAccountID != "":
		return &cmd.AccessSubject{Type: "user", ID: f.ServiceAccountID}, nil
	case f.ServiceAccountName != "":
		id, err := orgServiceAccountIDByName(ctx, a.api, f.ServiceAccountName)
		if err != nil {
			return nil, err
		}
		return &cmd.AccessSubject{Type: "user", ID: id}, nil
	case f.DirectoryGroupID != "":
		return &cmd.AccessSubject{Type: "directory_group", ID: f.DirectoryGroupID}, nil
	}
	return nil, nil
}

func commandOrgAccessPermissionList(ctx *CommandContext, flags *cmd.OrgAccessPermissionListFlags) error {
	a, err := newAccessAPI(ctx)
	if err != nil {
		return err
	}
	query, _, err := a.teamQuery(ctx, flags.Team)
	if err != nil {
		return err
	}
	var catalog cmd.AccessPermissionCatalog
	if err := a.call(ctx, http.MethodGet, "/v1/access/permissions", query, nil, &catalog); err != nil {
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
		rows = append(rows, []string{p.Name, p.Label, strings.Join(p.RoleKinds, ","), yesNo(p.Selectable)})
	}
	ctx.OutputTable(TableOutput{Headers: []string{"NAME", "LABEL", "ROLE KINDS", "SELECTABLE"}, Rows: rows})
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
			r.ID, r.Name, orDash(r.Kind), orDash(r.TeamID), yesNo(r.BuiltIn), strings.Join(r.Permissions, ","),
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
	query, _, err := a.teamQuery(ctx, flags.Team)
	if err != nil {
		return err
	}
	id, err := a.roleID(ctx, flags.ID, flags.Name)
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
	writeAccessRole(ctx, role)
	return nil
}

func writeAccessRole(ctx *CommandContext, r cmd.AccessRole) {
	ctx.Outputf("ID:          %s\n", r.ID)
	ctx.Outputf("Name:        %s\n", r.Name)
	ctx.Outputf("Description: %s\n", r.Description)
	ctx.Outputf("Kind:        %s\n", orDash(r.Kind))
	ctx.Outputf("Team:        %s\n", orDash(r.TeamID))
	ctx.Outputf("Built-in:    %s\n", yesNo(r.BuiltIn))
	ctx.Outputf("Permissions: %s\n", joinOrDash(r.Permissions))
	ctx.Outputf("Includes:    %s\n", joinOrDash(r.Includes))
}

func commandOrgAccessRoleCreate(ctx *CommandContext, flags *cmd.OrgAccessRoleCreateFlags) error {
	a, err := newAccessAPI(ctx)
	if err != nil {
		return err
	}
	body := map[string]any{
		"name":        flags.Name,
		"kind":        flags.Kind,
		"description": flags.Description,
		"permissions": normalizeJSONSlice(flags.Permissions),
		"includes":    normalizeJSONSlice(flags.Includes),
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
	if len(flags.Permissions) > 0 {
		body["permissions"] = flags.Permissions
	}
	if len(flags.Includes) > 0 {
		body["includes"] = flags.Includes
	}
	if len(body) == 0 {
		return cmd.NewErrUsagef("pass at least one of --new-name, --description, --permission and --include")
	}
	a, err := newAccessAPI(ctx)
	if err != nil {
		return err
	}
	id, err := a.roleID(ctx, flags.ID, flags.Name)
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
	id, err := a.roleID(ctx, flags.ID, flags.Name)
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
	if (len(flags.ModelIDs) > 0 || len(flags.ModelNames) > 0) && flags.Team == "" {
		return cmd.NewErrUsagef("--model-id and --model-name need --team")
	}
	a, err := newAccessAPI(ctx)
	if err != nil {
		return err
	}
	subject, err := a.subject(ctx, cmd.OrgAccessHolderFilterFlags(flags.OrgAccessSubjectFlags))
	if err != nil {
		return err
	}
	roleID, err := a.roleID(ctx, flags.RoleID, flags.RoleName)
	if err != nil {
		return err
	}
	resources := make([]cmd.AccessResource, 0, len(flags.ModelIDs)+len(flags.ModelNames))
	for _, id := range flags.ModelIDs {
		resources = append(resources, cmd.AccessResource{Type: "model", ID: id})
	}
	for _, name := range flags.ModelNames {
		resources = append(resources, cmd.AccessResource{Type: "model", Name: name})
	}
	selection := cmd.AccessResourceSelection{Mode: "all", Resources: []cmd.AccessResource{}}
	if len(resources) > 0 {
		selection = cmd.AccessResourceSelection{Mode: "selected", Resources: resources}
	}
	scope := map[string]any{
		"resource_selection":    selection,
		"environment_selection": normalizeJSONSlice(flags.EnvironmentIDs),
	}
	if flags.Team != "" {
		teamID, err := ResolveTeam(ctx, a.api, flags.Team)
		if err != nil {
			return err
		}
		scope["team_id"] = teamID
	}
	body := map[string]any{"subject": subject, "role_id": roleID, "scope": scope}
	var assignment cmd.AccessAssignment
	if err := a.call(ctx, http.MethodPost, "/v1/access/assignments", nil, body, &assignment); err != nil {
		return fmt.Errorf("assigning role %s: %w", roleID, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(assignment)
		return nil
	}
	ctx.Logf("Assigned role %s\n", assignment.RoleID)
	ctx.OutputLine(assignment.ID)
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
	query, _, err := a.teamQuery(ctx, flags.Team)
	if err != nil {
		return err
	}
	if flags.RoleID != "" || flags.RoleName != "" {
		roleID, err := a.roleID(ctx, flags.RoleID, flags.RoleName)
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
			as.ID, as.RoleID, as.Subject.Type + " " + as.Subject.ID, orDash(as.Scope.TeamID),
			resourcesText(as.Scope.ResourceSelection), joinOrDash(as.Scope.EnvironmentSelection),
			as.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "ROLE", "HOLDER", "TEAM", "RESOURCES", "ENVIRONMENTS", "CREATED"},
		Rows:    rows,
	})
	return nil
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
	ctx.Outputf("ID:           %s\n", as.ID)
	ctx.Outputf("Role:         %s\n", as.RoleID)
	ctx.Outputf("Holder:       %s %s\n", as.Subject.Type, as.Subject.ID)
	ctx.Outputf("Team:         %s\n", orDash(as.Scope.TeamID))
	ctx.Outputf("Resources:    %s\n", resourcesText(as.Scope.ResourceSelection))
	ctx.Outputf("Environments: %s\n", joinOrDash(as.Scope.EnvironmentSelection))
	ctx.Outputf("Created by:   %s\n", orDash(as.CreatedBy))
	ctx.Outputf("Created:      %s\n", as.CreatedAt.UTC().Format(time.RFC3339))
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
	path := "/v1/access/me"
	switch {
	case flags.ServiceAccountID != "" || flags.ServiceAccountName != "":
		path = "/v1/access/service_accounts/" + url.PathEscape(subject.ID)
	case subject != nil:
		path = "/v1/access/users/" + url.PathEscape(subject.ID)
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
	ctx.Outputf("%s %s\n", access.Subject.Type, access.Subject.ID)
	rows := make([][]string, 0, len(access.Assignments))
	for _, held := range access.Assignments {
		via := "membership"
		if held.ID != nil {
			via = "assignment"
		}
		if held.Subject.Type == "directory_group" {
			via += " to group " + held.Subject.ID
		}
		rows = append(rows, []string{
			held.Role.Name, via, orDash(held.Scope.TeamID), resourcesText(held.Scope.ResourceSelection),
			joinOrDash(held.Scope.EnvironmentSelection), orDash(held.ID),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ROLE", "HELD VIA", "TEAM", "RESOURCES", "ENVIRONMENTS", "ASSIGNMENT"},
		Rows:    rows,
	})
	if access.Effective == nil {
		ctx.LogLine("Effective permissions aren't available yet.")
		return nil
	}
	e := access.Effective
	ctx.Outputf("\nOrganization: %s\n", joinOrDash(e.OrgWide))
	for _, team := range sortedKeys(e.Teams) {
		ctx.Outputf("Team %s: %s\n", team, strings.Join(e.Teams[team], ", "))
	}
	for _, resourceType := range sortedKeys(e.Resources) {
		for _, id := range sortedKeys(e.Resources[resourceType]) {
			ctx.Outputf("%s %s: %s\n", resourceType, id, strings.Join(e.Resources[resourceType][id], ", "))
		}
	}
	for _, environment := range sortedKeys(e.Environments) {
		ctx.Outputf("Environment %s: %s\n", environment, strings.Join(e.Environments[environment], ", "))
	}
	return nil
}

func commandOrgAccessCheck(ctx *CommandContext, flags *cmd.OrgAccessCheckFlags) error {
	if flags.Team != "" && flags.ModelID != "" {
		return cmd.NewErrUsagef("pass --team or --model-id, not both")
	}
	a, err := newAccessAPI(ctx)
	if err != nil {
		return err
	}
	var resource *cmd.AccessResource
	switch {
	case flags.Team != "":
		teamID, err := ResolveTeam(ctx, a.api, flags.Team)
		if err != nil {
			return err
		}
		resource = &cmd.AccessResource{Type: "team", ID: teamID}
	case flags.ModelID != "":
		resource = &cmd.AccessResource{Type: "model", ID: flags.ModelID}
	}
	checks := make([]cmd.AccessCheck, 0, len(flags.Permissions))
	for _, permission := range flags.Permissions {
		checks = append(checks, cmd.AccessCheck{Permission: permission, Resource: resource})
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
		on := "organization"
		if r.Check.Resource != nil {
			on = r.Check.Resource.Type + " " + r.Check.Resource.ID
		}
		rows = append(rows, []string{r.Check.Permission, on, yesNo(r.Allow)})
	}
	ctx.OutputTable(TableOutput{Headers: []string{"PERMISSION", "ON", "ALLOWED"}, Rows: rows})
	return nil
}

func commandOrgAccessSettingsDescribe(ctx *CommandContext, _ *cmd.OrgAccessSettingsDescribeFlags) error {
	a, err := newAccessAPI(ctx)
	if err != nil {
		return err
	}
	var settings cmd.AccessSettings
	if err := a.call(ctx, http.MethodGet, "/v1/access/settings", nil, nil, &settings); err != nil {
		return fmt.Errorf("reading role settings: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(settings)
		return nil
	}
	ctx.Outputf("Team admins manage roles: %s\n", yesNo(settings.AllowTeamAdminsToManageRoles))
	return nil
}

func commandOrgAccessSettingsUpdate(ctx *CommandContext, flags *cmd.OrgAccessSettingsUpdateFlags) error {
	a, err := newAccessAPI(ctx)
	if err != nil {
		return err
	}
	body := cmd.AccessSettings{AllowTeamAdminsToManageRoles: *flags.AllowTeamAdminsToManageRoles.Pointer()}
	var settings cmd.AccessSettings
	if err := a.call(ctx, http.MethodPatch, "/v1/access/settings", nil, body, &settings); err != nil {
		return fmt.Errorf("updating role settings: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(settings)
		return nil
	}
	ctx.LogLine("Updated role settings")
	return nil
}

func resourcesText(selection cmd.AccessResourceSelection) string {
	if selection.Mode != "selected" {
		return "all"
	}
	names := make([]string, 0, len(selection.Resources))
	for _, r := range selection.Resources {
		names = append(names, r.Type+" "+r.ID)
	}
	return strings.Join(names, ", ")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

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
