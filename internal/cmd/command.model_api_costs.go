package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("model-api costs", commandModelAPICosts)
}

func commandModelAPICosts(ctx *CommandContext, flags *cmd.ModelAPICostsFlags) error {
	query, dims, err := modelAPICostsQuery(ctx, flags)
	if err != nil {
		return err
	}
	client, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	var writer *JSONArrayWriter
	if ctx.JSON {
		writer = ctx.NewJSONArrayWriter()
		defer writer.Close()
	}
	table := modelAPICostsTable{dims: dims, scale: 2}
	count := 0
	hitLimit := false
	seenCursors := map[string]bool{}
pages:
	for {
		page, err := getModelAPICosts(ctx, client.API(), query)
		if err != nil {
			return fmt.Errorf("getting Model APIs costs: %w", err)
		}
		for i, bucket := range page.Items {
			if ctx.JSON {
				writer.Write(bucket)
			} else if err := table.addBucket(bucket); err != nil {
				return err
			}
			count++
			if flags.Limit > 0 && count == flags.Limit {
				hitLimit = page.Pagination.HasMore || i < len(page.Items)-1
				break pages
			}
		}
		if !page.Pagination.HasMore {
			break
		}
		cursor := page.Pagination.Cursor
		if cursor == nil || *cursor == "" || seenCursors[*cursor] {
			return fmt.Errorf("getting Model APIs costs: invalid pagination cursor in response")
		}
		seenCursors[*cursor] = true
		query = url.Values{"cursor": {*cursor}}
	}
	if !ctx.JSON {
		if table.dataRows == 0 {
			ctx.LogLine("No usage in the selected window.")
		} else {
			ctx.Logf("Window: %s through %s UTC · %d daily buckets · grouped by %s\n",
				table.firstDate, table.lastDate, count, strings.Join(dims, ", "))
			ctx.LogLine("Costs are in USD and may differ from finalized invoice amounts.")
			if table.sawNull {
				ctx.LogLine("A '-' dimension means attribution is unavailable.")
			}
			ctx.OutputTable(table.render())
		}
	}
	if hitLimit {
		ctx.Logf("Reached the --limit of %d buckets; more exist. Increase --limit or use --limit 0 for no limit.\n", flags.Limit)
	}
	return nil
}

func modelAPICostsQuery(ctx *CommandContext, flags *cmd.ModelAPICostsFlags) (url.Values, []string, error) {
	if flags.Limit < 0 {
		return nil, nil, cmd.NewErrUsagef("--limit must be zero (no limit) or a positive number")
	}
	hasSince := ctx.Command.Flags().Changed("since")
	if hasSince && (flags.Start != "" || flags.End != "") {
		return nil, nil, cmd.NewErrUsagef("--since cannot be combined with --start or --end")
	}
	const day = 24 * time.Hour
	window := 7 * day
	if hasSince {
		if flags.Since <= 0 || flags.Since%day != 0 {
			return nil, nil, cmd.NewErrUsagef("--since must be a positive whole number of days (e.g. 7d)")
		}
		window = flags.Since
	}
	end := ctx.Now().UTC().Truncate(day).Add(day)
	if flags.End != "" {
		var err error
		end, err = time.Parse(time.DateOnly, flags.End)
		if err != nil {
			return nil, nil, cmd.NewErrUsagef("--end must be a UTC date in YYYY-MM-DD format")
		}
	}
	start := end.Add(-window)
	if flags.Start != "" {
		var err error
		start, err = time.Parse(time.DateOnly, flags.Start)
		if err != nil {
			return nil, nil, cmd.NewErrUsagef("--start must be a UTC date in YYYY-MM-DD format")
		}
	}
	if !start.Before(end) {
		return nil, nil, cmd.NewErrUsagef("--start must be earlier than --end")
	}
	if end.Sub(start) > 90*day {
		return nil, nil, cmd.NewErrUsagef("cost window must be at most 90 days")
	}
	if start.Format(time.DateOnly) < "2026-08-05" {
		return nil, nil, cmd.NewErrUsagef("costs are not available before 2026-08-05 UTC")
	}
	dims := slices.Clone(flags.GroupBy)
	if len(dims) == 0 {
		dims = []string{"model"}
	}
	query := url.Values{
		"start_date":       {start.Format(time.DateOnly)},
		"end_date":         {end.Format(time.DateOnly)},
		"api_key_prefixes": flags.APIKeyPrefixes,
		"user_ids":         flags.UserIDs,
		"models":           flags.Models,
		"service_tiers":    flags.ServiceTiers,
	}
	uniqueDims := make([]string, 0, len(dims))
	for _, dim := range dims {
		var wire string
		switch dim {
		case "api-key":
			wire = "api_key_prefix"
		case "user", "model", "service-tier":
			wire = strings.ReplaceAll(dim, "-", "_")
		default:
			return nil, nil, cmd.NewErrUsagef("invalid --group-by %q; must be one of: api-key, user, model, service-tier", dim)
		}
		if !slices.Contains(uniqueDims, dim) {
			uniqueDims = append(uniqueDims, dim)
			query.Add("group_by", wire)
		}
	}
	pageSize := 31
	if flags.Limit > 0 {
		pageSize = min(pageSize, flags.Limit)
	}
	query.Set("limit", strconv.Itoa(pageSize))
	return query, uniqueDims, nil
}

type modelAPICostsPage struct {
	Items      []cmd.ModelAPICostBucket          `json:"items"`
	Pagination *managementapi.PaginationResponse `json:"pagination"`
}

func getModelAPICosts(ctx *CommandContext, client *managementapi.Client, query url.Values) (*modelAPICostsPage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(client.BaseURL, "/")+"/v1/billing/model_apis?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header = client.Headers.Clone()
	resp, err := client.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return nil, fmt.Errorf("reading cost error response: %w", err)
		}
		return nil, &managementapi.ResponseError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	var page modelAPICostsPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("decoding daily costs: %w", err)
	}
	if page.Items == nil || page.Pagination == nil {
		return nil, fmt.Errorf("cost response is missing items or pagination")
	}
	return &page, nil
}

type modelAPICostsTable struct {
	dims                []string
	rows                [][]string
	firstDate, lastDate string
	dataRows            int
	sawNull             bool
	total               big.Rat
	scale               int
}

var modelAPICostDecimal = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)

func (t *modelAPICostsTable) addBucket(bucket cmd.ModelAPICostBucket) error {
	if t.firstDate == "" {
		t.firstDate = bucket.Date
	}
	t.lastDate = bucket.Date
	stamp := bucket.Date
	if len(bucket.Results) == 0 {
		row := make([]string, len(t.dims)+2)
		row[0], row[1] = stamp, "(no usage)"
		t.rows = append(t.rows, row)
	}
	for _, result := range bucket.Results {
		if !modelAPICostDecimal.MatchString(result.Subtotal) {
			return fmt.Errorf("invalid cost subtotal %q for %s", result.Subtotal, bucket.Date)
		}
		value, ok := new(big.Rat).SetString(result.Subtotal)
		if !ok {
			return fmt.Errorf("invalid cost subtotal %q for %s", result.Subtotal, bucket.Date)
		}
		_, fractional, _ := strings.Cut(result.Subtotal, ".")
		t.scale = max(t.scale, len(fractional))
		t.total.Add(&t.total, value)
		row := []string{stamp}
		for _, dim := range t.dims {
			var cell string
			switch dim {
			case "api-key":
				cell = strings.Join(result.APIKeyPrefixes, ", ")
			case "user":
				cell = modelAPIUsageCell(result.UserID)
			case "model":
				cell = modelAPIUsageCell(result.Model)
			case "service-tier":
				cell = modelAPIUsageCell(result.ServiceTier)
			default:
				return fmt.Errorf("unhandled cost dimension %q", dim)
			}
			if cell == "" || cell == "-" {
				cell = "-"
				t.sawNull = true
			}
			row = append(row, cell)
		}
		t.rows = append(t.rows, append(row, modelAPICostMoney(value, len(fractional))))
		t.dataRows++
		stamp = ""
	}
	return nil
}

func modelAPICostMoney(value *big.Rat, scale int) string {
	amount := value.FloatString(max(2, scale))
	whole, fraction, _ := strings.Cut(amount, ".")
	fraction = strings.TrimRight(fraction, "0")
	for len(fraction) < 2 {
		fraction += "0"
	}
	sign := ""
	if strings.HasPrefix(whole, "-") {
		sign, whole = "-", strings.TrimPrefix(whole, "-")
	}
	return sign + "$" + billingGroupDigits(whole) + "." + fraction
}

func (t *modelAPICostsTable) render() TableOutput {
	headers := []string{"DATE"}
	for _, dim := range t.dims {
		headers = append(headers, strings.ToUpper(strings.ReplaceAll(dim, "-", " ")))
	}
	headers = append(headers, "SUBTOTAL (USD)")
	if t.dataRows > 1 {
		row := make([]string, len(headers))
		row[0], row[len(row)-1] = "ALL", modelAPICostMoney(&t.total, t.scale)
		t.rows = append(t.rows, row)
	}
	return TableOutput{Headers: headers, Rows: t.rows, RightAlignedColumns: []int{len(headers) - 1}}
}
