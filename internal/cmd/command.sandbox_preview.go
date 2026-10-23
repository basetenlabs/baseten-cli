package cmd

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// sandboxPreviewCreateTokenTTL is the --with-token link lifetime when the preview has no expiry.
const sandboxPreviewCreateTokenTTL = 7 * 24 * time.Hour

// sandboxPreviewWithToken is a preview plus the share link minted for it.
type sandboxPreviewWithToken struct {
	managementapi.SandboxPreview
	ShareURL string `json:"share_url"`
}

func init() {
	Register("sandbox preview list", commandSandboxPreviewList)
	Register("sandbox preview describe", commandSandboxPreviewDescribe)
	Register("sandbox preview create", commandSandboxPreviewCreate)
	Register("sandbox preview update", commandSandboxPreviewUpdate)
	Register("sandbox preview delete", commandSandboxPreviewDelete)
	Register("sandbox preview token create", commandSandboxPreviewTokenCreate)
	Register("sandbox preview token list", commandSandboxPreviewTokenList)
	Register("sandbox preview token delete", commandSandboxPreviewTokenDelete)
}

func commandSandboxPreviewList(ctx *CommandContext, flags *cmd.SandboxPreviewListFlags) error {
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	params := managementapi.ListSandboxPreviewsParams{TeamId: teamID}
	items, truncated, err := sandboxCollectPages(flags.Limit, func(cursor *string, pageLimit int) ([]managementapi.SandboxPreview, managementapi.SandboxApiPagination, error) {
		params.Cursor, params.Limit = cursor, &pageLimit
		page, err := client.API().ListSandboxPreviews(ctx, flags.Name, params)
		if err != nil {
			return nil, managementapi.SandboxApiPagination{}, err
		}
		return page.Items, page.Pagination, nil
	})
	if err != nil {
		return fmt.Errorf("listing previews of sandbox %s: %w", flags.Name, err)
	}
	if truncated {
		ctx.Logf("Reached the --limit of %d; more exist. Increase --limit or use --limit 0 for no limit.\n", flags.Limit)
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.SandboxPreviewList{Items: normalizeJSONSlice(items)})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No previews found.")
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		var port, status string
		if item.Port != nil {
			port = strconv.Itoa(*item.Port)
		}
		if item.Status != nil {
			status = string(*item.Status)
		}
		rows = append(rows, []string{
			deref(item.Name), port, strconv.FormatBool(item.Public), status, deref(item.Url), sandboxFormatTime(item.ExpiresAt),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"NAME", "PORT", "PUBLIC", "STATUS", "URL", "EXPIRES"},
		Rows:    rows,
	})
	return nil
}

func commandSandboxPreviewDescribe(ctx *CommandContext, flags *cmd.SandboxPreviewDescribeFlags) error {
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	params := managementapi.GetSandboxPreviewParams{TeamId: teamID}
	if flags.ShowSecrets {
		params.ShowSecrets = &flags.ShowSecrets
	}
	record, err := client.API().GetSandboxPreview(ctx, flags.Name, flags.PreviewName, params)
	if err != nil {
		return fmt.Errorf("describing preview %s of sandbox %s: %w", flags.PreviewName, flags.Name, err)
	}
	sandboxPreviewOutputRecord(ctx, record)
	return nil
}

func commandSandboxPreviewCreate(ctx *CommandContext, flags *cmd.SandboxPreviewCreateFlags) error {
	if flags.Public && flags.WithToken {
		return cmd.NewErrUsagef("--public and --with-token cannot be combined")
	}
	responseHeaders, err := sandboxPreviewHeaders("response-header", flags.ResponseHeader)
	if err != nil {
		return err
	}
	requestHeaders, err := sandboxPreviewHeaders("request-header", flags.RequestHeader)
	if err != nil {
		return err
	}
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	ttl, expiresAt := sandboxPreviewExpiry(ctx, flags.SandboxPreviewExpiryFlags)
	request := managementapi.CreateSandboxPreviewRequest{
		Port:            flags.Port,
		Name:            sandboxOptionalString(flags.PreviewName),
		ResponseHeaders: responseHeaders,
		RequestHeaders:  requestHeaders,
		Ttl:             ttl,
		ExpiresAt:       expiresAt,
	}
	if flags.Public {
		request.Public = &flags.Public
	}
	if flags.IfNotExists {
		request.CreateIfNotExists = &flags.IfNotExists
	}
	record, err := client.API().CreateSandboxPreview(ctx, flags.Name, managementapi.CreateSandboxPreviewParams{TeamId: teamID}, request)
	if err != nil {
		return fmt.Errorf("creating preview of sandbox %s: %w", flags.Name, err)
	}
	if !flags.WithToken {
		sandboxPreviewOutputRecord(ctx, record)
		return nil
	}
	// The token dies with the preview when the preview has the shorter life.
	tokenExpiresAt := time.Now().Add(sandboxPreviewCreateTokenTTL)
	if expiresAt != nil && expiresAt.Before(tokenExpiresAt) {
		tokenExpiresAt = *expiresAt
	}
	token, tokenErr := client.API().CreateSandboxPreviewToken(ctx, flags.Name, deref(record.Name), managementapi.CreateSandboxPreviewTokenParams{TeamId: teamID}, managementapi.CreateSandboxPreviewTokenRequest{ExpiresAt: tokenExpiresAt})
	if tokenErr != nil {
		return fmt.Errorf("creating a token for preview %s of sandbox %s: %w", deref(record.Name), flags.Name, tokenErr)
	}
	shareURL := deref(record.Url) + "?preview_token=" + url.QueryEscape(token.Token)
	if ctx.JSON {
		ctx.OutputJSON(sandboxPreviewWithToken{SandboxPreview: *record, ShareURL: shareURL})
		return nil
	}
	sandboxPreviewOutputRecord(ctx, record)
	ctx.Outputf("Share link: %s\n", shareURL)
	ctx.Outputf("Link good until: %s; the first open sets a cookie, and the link also works with curl.\n", sandboxFormatTime(&token.ExpiresAt))
	return nil
}

func commandSandboxPreviewUpdate(ctx *CommandContext, flags *cmd.SandboxPreviewUpdateFlags) error {
	if flags.Public && flags.Private {
		return cmd.NewErrUsagef("--public and --private cannot be combined")
	}
	responseHeaders, err := sandboxPreviewHeaders("response-header", flags.ResponseHeader)
	if err != nil {
		return err
	}
	requestHeaders, err := sandboxPreviewHeaders("request-header", flags.RequestHeader)
	if err != nil {
		return err
	}
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	ttl, expiresAt := sandboxPreviewExpiry(ctx, flags.SandboxPreviewExpiryFlags)
	request := managementapi.UpdateSandboxPreviewRequest{
		ResponseHeaders: responseHeaders,
		RequestHeaders:  requestHeaders,
		Ttl:             ttl,
		ExpiresAt:       expiresAt,
	}
	if flags.Public || flags.Private {
		request.Public = &flags.Public
	}
	record, err := client.API().UpdateSandboxPreview(ctx, flags.Name, flags.PreviewName, managementapi.UpdateSandboxPreviewParams{TeamId: teamID}, request)
	if err != nil {
		return fmt.Errorf("updating preview %s of sandbox %s: %w", flags.PreviewName, flags.Name, err)
	}
	sandboxPreviewOutputRecord(ctx, record)
	return nil
}

func commandSandboxPreviewDelete(ctx *CommandContext, flags *cmd.SandboxPreviewDeleteFlags) error {
	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Delete preview %s of sandbox %s and its tokens? Its URL stops serving at once.", flags.PreviewName, flags.Name)); err != nil {
			return err
		}
	}
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	record, err := client.API().DeleteSandboxPreview(ctx, flags.Name, flags.PreviewName, managementapi.DeleteSandboxPreviewParams{TeamId: teamID})
	if err != nil {
		return fmt.Errorf("deleting preview %s of sandbox %s: %w", flags.PreviewName, flags.Name, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(record)
		return nil
	}
	ctx.Logf("Deleted preview %s.\n", flags.PreviewName)
	return nil
}

func commandSandboxPreviewTokenCreate(ctx *CommandContext, flags *cmd.SandboxPreviewTokenCreateFlags) error {
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	request := managementapi.CreateSandboxPreviewTokenRequest{ExpiresAt: flags.ExpiresAt}
	if flags.IfNotExists {
		request.CreateIfNotExists = &flags.IfNotExists
	}
	token, err := client.API().CreateSandboxPreviewToken(ctx, flags.Name, flags.PreviewName, managementapi.CreateSandboxPreviewTokenParams{TeamId: teamID}, request)
	if err != nil {
		return fmt.Errorf("creating token for preview %s of sandbox %s: %w", flags.PreviewName, flags.Name, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(token)
		return nil
	}
	ctx.Outputf("Name:     %s\n", token.Name)
	ctx.Outputf("Token:    %s\n", token.Token)
	ctx.Outputf("Expires:  %s\n", sandboxFormatTime(&token.ExpiresAt))
	// The token record has no URL, so the preview's is fetched to print one
	// ready to open.
	preview, err := client.API().GetSandboxPreview(ctx, flags.Name, flags.PreviewName, managementapi.GetSandboxPreviewParams{TeamId: teamID})
	if err != nil {
		return fmt.Errorf("getting the URL of preview %s of sandbox %s: %w", flags.PreviewName, flags.Name, err)
	}
	ctx.Outputf("URL:      %s?preview_token=%s\n", deref(preview.Url), url.QueryEscape(token.Token))
	return nil
}

func commandSandboxPreviewTokenList(ctx *CommandContext, flags *cmd.SandboxPreviewTokenListFlags) error {
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	params := managementapi.ListSandboxPreviewTokensParams{TeamId: teamID}
	items, _, err := sandboxCollectPages(0, func(cursor *string, pageLimit int) ([]managementapi.SandboxPreviewToken, managementapi.SandboxApiPagination, error) {
		params.Cursor, params.Limit = cursor, &pageLimit
		page, err := client.API().ListSandboxPreviewTokens(ctx, flags.Name, flags.PreviewName, params)
		if err != nil {
			return nil, managementapi.SandboxApiPagination{}, err
		}
		return page.Items, page.Pagination, nil
	})
	if err != nil {
		return fmt.Errorf("listing tokens of preview %s of sandbox %s: %w", flags.PreviewName, flags.Name, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.SandboxPreviewTokenList{Items: normalizeJSONSlice(items)})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No tokens found.")
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{item.Name, sandboxFormatTime(&item.ExpiresAt), strconv.FormatBool(item.Expired)})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"NAME", "EXPIRES", "EXPIRED"},
		Rows:    rows,
	})
	return nil
}

func commandSandboxPreviewTokenDelete(ctx *CommandContext, flags *cmd.SandboxPreviewTokenDeleteFlags) error {
	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Delete token %s of preview %s? Requests using it are rejected at once.", flags.TokenName, flags.PreviewName)); err != nil {
			return err
		}
	}
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	if err := client.API().DeleteSandboxPreviewToken(ctx, flags.Name, flags.PreviewName, flags.TokenName, managementapi.DeleteSandboxPreviewTokenParams{TeamId: teamID}); err != nil {
		return fmt.Errorf("deleting token %s of preview %s of sandbox %s: %w", flags.TokenName, flags.PreviewName, flags.Name, err)
	}
	ctx.Logf("Deleted token %s.\n", flags.TokenName)
	return nil
}

func sandboxPreviewOutputRecord(ctx *CommandContext, record *managementapi.SandboxPreview) {
	if ctx.JSON {
		ctx.OutputJSON(record)
		return
	}
	ctx.Outputf("Name:              %s\n", deref(record.Name))
	if record.Status != nil {
		ctx.Outputf("Status:            %s\n", *record.Status)
	}
	sandboxOutputField(ctx, "URL:               ", deref(record.Url))
	if record.Port != nil {
		ctx.Outputf("Port:              %d\n", *record.Port)
	}
	ctx.Outputf("Public:            %t\n", record.Public)
	sandboxOutputField(ctx, "Region:            ", deref(record.Region))
	sandboxOutputField(ctx, "Response headers:  ", sandboxPreviewFormatHeaders(record.ResponseHeaders))
	sandboxOutputField(ctx, "Request headers:   ", sandboxPreviewFormatHeaders(record.RequestHeaders))
	sandboxOutputField(ctx, "Expires at:        ", sandboxFormatTime(record.ExpiresAt))
	sandboxOutputField(ctx, "Created:           ", sandboxFormatTime(record.CreatedAt))
	sandboxOutputField(ctx, "Updated:           ", sandboxFormatTime(record.UpdatedAt))
}

// sandboxPreviewFormatHeaders formats headers as NAME=VALUE pairs, sorted so
// the same headers always print the same way.
func sandboxPreviewFormatHeaders(headers *managementapi.SandboxPreviewHeaders) string {
	if headers == nil {
		return ""
	}
	pairs := make([]string, 0, len(*headers))
	for _, name := range slices.Sorted(maps.Keys(*headers)) {
		pairs = append(pairs, name+"="+(*headers)[name])
	}
	return strings.Join(pairs, ", ")
}

// sandboxPreviewHeaders turns KEY=VALUE flag repeats into headers, nil when
// there are none.
func sandboxPreviewHeaders(flagName string, values []string) (*managementapi.SandboxPreviewHeaders, error) {
	parsed, err := sandboxParseKeyValues(flagName, values)
	if err != nil || parsed == nil {
		return nil, err
	}
	headers := managementapi.SandboxPreviewHeaders(parsed)
	return &headers, nil
}

// sandboxPreviewExpiry builds a preview's ttl and expires_at, each nil when its
// flag is unset. Both set is left for the server to reject. The TTL goes out
// in Go syntax, which the server accepts, and whenever passed, 0 included, so
// the server judges what was typed rather than it being dropped as unset.
func sandboxPreviewExpiry(ctx *CommandContext, flags cmd.SandboxPreviewExpiryFlags) (*string, *time.Time) {
	var ttl *string
	if ctx.Command.Flags().Changed("ttl") {
		ttl = new(flags.TTL.String())
	}
	var expiresAt *time.Time
	if !flags.ExpiresAt.IsZero() {
		expiresAt = &flags.ExpiresAt
	}
	return ttl, expiresAt
}
