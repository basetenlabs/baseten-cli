package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("org role-assignment create", commandOrgRoleAssignmentCreate)
	Register("org role-assignment delete", commandOrgRoleAssignmentDelete)
	Register("org role-assignment describe", commandOrgRoleAssignmentDescribe)
	Register("org role-assignment list", commandOrgRoleAssignmentList)
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
	req := managementapi.CreateAccessAssignmentRequest{SubjectType: subject.Type, SubjectId: subject.ID}
	if flags.Team != "" {
		teamID, err := ResolveTeam(ctx, cl.API(), flags.Team)
		if err != nil {
			return err
		}
		req.TeamId = &teamID
	}
	req.RoleId, err = orgRoleID(ctx, cl.API(), flags.RoleID, flags.RoleName, req.TeamId)
	if err != nil {
		return err
	}
	if len(resourceSets) > 0 {
		req.ResourceSets = &resourceSets
	}
	assignment, err := cl.API().PostAccessAssignments(ctx, req)
	if err != nil {
		return fmt.Errorf("assigning role %s: %w", req.RoleId, err)
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
	params := managementapi.GetV1AccessAssignmentsParams{}
	if flags.Team != "" {
		teamID, err := ResolveTeam(ctx, cl.API(), flags.Team)
		if err != nil {
			return err
		}
		params.TeamId = &teamID
	}
	if flags.RoleID != "" || flags.RoleName != "" {
		roleID, err := orgRoleID(ctx, cl.API(), flags.RoleID, flags.RoleName, params.TeamId)
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
		created := "-"
		if as.CreatedAt != nil {
			created = as.CreatedAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, []string{
			orgAccessOrDash(as.Id), as.RoleName, orgAccessSubjectText(as.SubjectType, as.SubjectName),
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
	created := "-"
	if as.CreatedAt != nil {
		created = as.CreatedAt.UTC().Format(time.RFC3339)
	}
	ctx.Outputf("ID:            %s\n", orgAccessOrDash(as.Id))
	ctx.Outputf("Role:          %s (%s)\n", as.RoleName, as.RoleId)
	ctx.Outputf("Holder:        %s (%s)\n", orgAccessSubjectText(as.SubjectType, as.SubjectName), as.SubjectId)
	ctx.Outputf("Team:          %s\n", orgAccessOrDash(as.TeamName))
	ctx.Outputf("Resource sets: %s\n", orgAccessResourceSetsText(as.ResourceSets))
	ctx.Outputf("Created by:    %s\n", orgAccessOrDash(as.CreatedBy))
	ctx.Outputf("Created:       %s\n", created)
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
