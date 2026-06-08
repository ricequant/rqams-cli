package app

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"rqams-cli/internal/asyncpoll"
	"rqams-cli/internal/client"
	"rqams-cli/internal/config"
	"rqams-cli/internal/output"
	"rqams-cli/internal/payload"
)

const (
	// Name is the executable command name shown in help and version output.
	Name = "rqamsc"
)

// Version is the package version. Release builds may override this with ldflags.
var Version = "0.0.1"

type handler func(context) (any, map[string]any, error)

type context struct {
	command string
	payload map[string]any
	config  config.Config
	client  client.Client
}

type route struct {
	method string
	path   string
	run    handler
	ndjson ndjsonExtractor
}

type ndjsonExtractor func(any) ([]any, error)

type paperTradingCreateTarget struct {
	template string
	version  string
	path     string
}

type paperTradingInfo struct {
	version   string
	productID string
	channelID string
	config    map[string]any
}

type productLike struct {
	resourceType string
	resourceID   string
}

// Run executes the rqamsc demo CLI.
func Run(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if handled, code := handleUtilityArgs(args, stdout); handled {
		return code
	}

	command, payloadArg, err := parseArgs(args)
	if err != nil {
		writeFailure(stdout, command, "invalid_arguments", err.Error())
		return 2
	}
	if isSchemaCommand(command) {
		return runSchemaCommand(command, payloadArg, stdin, stdout, stderr)
	}

	doc, err := payload.Parse(payloadArg, stdin)
	if err != nil {
		writeFailure(stdout, command, "invalid_payload", err.Error())
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		writeFailure(stdout, command, "config_error", err.Error())
		return 2
	}
	cfg = config.SelectProfile(cfg, firstString(doc["profile"]))
	routes := routes()
	selected, ok := routes[command]
	if !ok {
		writeFailure(stdout, command, "unknown_command", fmt.Sprintf("unsupported command %q", command))
		return 2
	}
	cfg, err = refreshAuthIfNeeded(command, cfg)
	if err != nil {
		writeFailure(stdout, command, classifyError(err), err.Error())
		return 1
	}

	ctx := context{
		command: command,
		payload: doc,
		config:  cfg,
		client:  client.New(cfg),
	}
	data, meta, err := selected.run(ctx)
	if err != nil {
		refreshed, refreshErr := refreshAuthAfterFailure(command, cfg, err)
		if refreshErr == nil {
			cfg = refreshed
			ctx.config = cfg
			ctx.client = client.New(cfg)
			data, meta, err = selected.run(ctx)
		} else if !errors.Is(refreshErr, errAuthRefreshNotNeeded) {
			err = refreshErr
		}
		if err != nil {
			writeFailure(stdout, command, classifyError(err), err.Error())
			return 1
		}
	}
	if meta == nil {
		meta = map[string]any{}
	}
	if selected.method != "" {
		meta["method"] = selected.method
	}
	if selected.path != "" {
		meta["path"] = selected.path
	}
	if shouldWriteNDJSON(doc) {
		if selected.ndjson == nil {
			writeFailure(stdout, command, "invalid_payload", fmt.Sprintf("command %q does not support ndjson output", command))
			return 2
		}
		items, err := selected.ndjson(data)
		if err != nil {
			writeFailure(stdout, command, "runtime_error", err.Error())
			return 1
		}
		if err := output.WriteNDJSON(stdout, items); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if err := output.Write(stdout, output.Success(command, data, meta)); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

var errAuthRefreshNotNeeded = errors.New("auth refresh not needed")

func refreshAuthIfNeeded(command string, cfg config.Config) (config.Config, error) {
	if command == "auth" || strings.TrimSpace(cfg.SID) != "" {
		return cfg, nil
	}
	if !hasStoredPassword(cfg) {
		return cfg, nil
	}
	return refreshAuth(cfg)
}

func refreshAuthAfterFailure(command string, cfg config.Config, cause error) (config.Config, error) {
	if command == "auth" || !isAuthExpired(cause) || !hasStoredPassword(cfg) {
		return cfg, errAuthRefreshNotNeeded
	}
	return refreshAuth(cfg)
}

func refreshAuth(cfg config.Config) (config.Config, error) {
	loginClient := client.New(cfg)
	login, err := loginClient.Login(cfg.Username, cfg.Password)
	if err != nil {
		return cfg, err
	}
	cfg.UserID = login.UserID
	cfg.SID = login.SID
	cfg.Plaintext = true
	if err := config.Save(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func hasStoredPassword(cfg config.Config) bool {
	return strings.TrimSpace(cfg.BaseURL) != "" && strings.TrimSpace(cfg.Username) != "" && strings.TrimSpace(cfg.Password) != ""
}

func isAuthExpired(err error) bool {
	var httpErr client.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.Status == 401 || httpErr.Status == 403
}

func handleUtilityArgs(args []string, stdout io.Writer) (bool, int) {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stdout, helpText())
		return true, 0
	}
	if len(args) != 1 {
		return false, 0
	}
	switch args[0] {
	case "-h", "--help", "help":
		_, _ = fmt.Fprint(stdout, helpText())
		return true, 0
	case "-v", "--version", "version":
		_, _ = fmt.Fprintf(stdout, "%s version %s\n", Name, Version)
		return true, 0
	default:
		return false, 0
	}
}

func helpText() string {
	return `RQAMS CLI - RQAMS tools for AI agents and terminal workflows

Usage:
  rqamsc <verb> <resource> --payload <json|@file|->
  rqamsc auth --payload '{"base_url":"https://www.ricequant.com","username":"...","password":"..."}'
  rqamsc schema list
  rqamsc schema get --payload '{"command":"get product-list"}'

Available Utility Commands:
  schema list     List supported commands and route metadata
  schema get      Show payload guidance for one command
  help            Show this help message
  version         Show CLI version

Examples:
  rqamsc auth --payload '{"base_url":"https://www.ricequant.com","username":"...","password":"..."}'
  rqamsc get product-list --payload '{}'
  rqamsc get product --payload '{"product_id_or_name":"demo"}'
  rqamsc get trade-list --payload '{"product_id_or_name":"...","start_date":"2026-01-01","end_date":"2026-01-31"}'

Flags:
  -h, --help      help for rqamsc
  -v, --version   version for rqamsc
`
}

func isSchemaCommand(command string) bool {
	return command == "schema list" || command == "schema get"
}

func runSchemaCommand(command string, payloadArg string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	doc := map[string]any{}
	if strings.TrimSpace(payloadArg) != "" {
		parsed, err := payload.Parse(payloadArg, stdin)
		if err != nil {
			writeFailure(stdout, command, "invalid_payload", err.Error())
			return 2
		}
		doc = parsed
	}

	var data any
	var err error
	switch command {
	case "schema list":
		data = schemaList()
	case "schema get":
		data, err = schemaGet(doc)
	default:
		err = fmt.Errorf("unsupported command %q", command)
	}
	if err != nil {
		writeFailure(stdout, command, "invalid_arguments", err.Error())
		return 2
	}
	if err := output.Write(stdout, output.Success(command, data, nil)); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func schemaList() []map[string]any {
	allRoutes := routes()
	commands := make([]string, 0, len(allRoutes))
	for command := range allRoutes {
		commands = append(commands, command)
	}
	sort.Strings(commands)

	items := make([]map[string]any, 0, len(commands))
	for _, command := range commands {
		item := schemaItem(command, allRoutes[command])
		if guidance, ok := commandPayloadGuidance()[command]; ok {
			guidance = withGlobalPayloadGuidance(guidance)
			item["required_payload"] = guidance.required
			item["optional_payload"] = guidance.optional
		}
		items = append(items, item)
	}
	return items
}

func schemaGet(doc map[string]any) (map[string]any, error) {
	command, err := payload.String(doc, "command")
	if err != nil {
		return nil, err
	}
	allRoutes := routes()
	selected, ok := allRoutes[command]
	if !ok {
		return nil, fmt.Errorf("unsupported command %q", command)
	}
	item := schemaItem(command, selected)
	if guidance, ok := commandPayloadGuidance()[command]; ok {
		guidance = withGlobalPayloadGuidance(guidance)
		item["required_payload"] = guidance.required
		item["optional_payload"] = guidance.optional
		item["examples"] = guidance.examples
		if guidance.parameters != nil {
			item["parameters"] = guidance.parameters
		}
		if guidance.returns != "" {
			item["returns"] = guidance.returns
		}
	} else {
		item["required_payload"] = []string{}
		item["optional_payload"] = []string{"raw endpoint payload; see docs/rqams_cli_manual.md"}
	}
	return item, nil
}

func withGlobalPayloadGuidance(guidance payloadGuidance) payloadGuidance {
	if !stringSliceContains(guidance.optional, "profile") {
		guidance.optional = append(append([]string(nil), guidance.optional...), "profile")
	}
	if guidance.parameters == nil {
		guidance.parameters = map[string]parameterSchema{}
	}
	if _, ok := guidance.parameters["profile"]; !ok {
		parameters := map[string]parameterSchema{}
		for key, value := range guidance.parameters {
			parameters[key] = value
		}
		parameters["profile"] = parameterSchema{Type: "string", Required: false, Description: "Optional local config profile for isolating accounts or workspaces"}
		guidance.parameters = parameters
	}
	return guidance
}

func stringSliceContains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func schemaItem(command string, selected route) map[string]any {
	item := map[string]any{
		"command": command,
		"usage":   fmt.Sprintf("rqamsc %s --payload '<json>'", command),
	}
	if selected.method != "" {
		item["method"] = selected.method
	}
	if selected.path != "" {
		item["path"] = selected.path
	}
	if selected.ndjson != nil {
		item["supports_ndjson"] = true
	}
	return item
}

type payloadGuidance struct {
	required   []string
	optional   []string
	examples   []map[string]any
	parameters map[string]parameterSchema
	returns    string
}

type parameterSchema struct {
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Description string   `json:"description,omitempty"`
	Default     any      `json:"default,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
}

func commandPayloadGuidance() map[string]payloadGuidance {
	return map[string]payloadGuidance{
		"auth": {
			required: []string{"base_url", "username", "password"},
			optional: []string{"profile"},
			examples: []map[string]any{{"base_url": "https://www.ricequant.com", "username": "...", "password": "...", "profile": "default"}},
			parameters: map[string]parameterSchema{
				"base_url": {Type: "string", Required: true, Description: "AMS base URL, for example https://www.ricequant.com"},
				"username": {Type: "string", Required: true, Description: "RQAMS username"},
				"password": {Type: "string", Required: true, Description: "RQAMS password"},
				"profile":  {Type: "string", Required: false, Description: "Optional local config profile for isolating accounts or workspaces"},
			},
			returns: "data.authenticated, data.user_id, data.profile, and saved local session config",
		},
		"get workspace-list": {
			required:   []string{},
			optional:   []string{},
			examples:   []map[string]any{{}},
			parameters: map[string]parameterSchema{},
			returns:    "data contains the raw /api/user/v1/workspaces response; common shape is data.data[]",
		},
		"use workspace": {
			required: []string{"workspace_name_or_id"},
			optional: []string{"profile"},
			examples: []map[string]any{{"workspace_name_or_id": "default"}},
			parameters: map[string]parameterSchema{
				"workspace_name_or_id": {Type: "string", Required: true, Description: "Workspace id or workspace display name to persist locally", Aliases: []string{"workspace_id"}},
				"profile":              {Type: "string", Required: false, Description: "Optional local config profile to update"},
			},
			returns: "data.workspace_id, data.workspace_name, and data.display for the selected workspace",
		},
		"get current-workspace": {
			required:   []string{},
			optional:   []string{},
			examples:   []map[string]any{{}},
			parameters: map[string]parameterSchema{},
			returns:    "data.workspace_id, data.workspace_name, and data.display for the current local workspace",
		},
		"get product-list": {
			required: []string{},
			optional: []string{"fields", "limit", "raw", "format"},
			examples: []map[string]any{{"fields": []string{"id", "name", "start_date"}, "limit": 20, "format": "ndjson"}},
			parameters: map[string]parameterSchema{
				"fields": {Type: "array<string> | string", Required: false, Description: "Returned product fields; defaults to id,name,start_date,label"},
				"limit":  {Type: "integer", Required: false, Description: "Maximum number of products returned by the CLI"},
				"raw":    {Type: "boolean", Required: false, Description: "Keep raw server fields instead of applying default projection"},
				"format": {Type: "string", Required: false, Description: "Output format", Default: "json", Enum: []string{"json", "ndjson"}},
			},
			returns: "data.products[] and data.total from the product list response",
		},
		"get product": {
			required: []string{"product_id_or_name"},
			optional: []string{},
			examples: []map[string]any{{"product_id_or_name": "demo"}},
			parameters: map[string]parameterSchema{
				"product_id_or_name": {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
			},
			returns: "data contains the product detail object",
		},
		"insert product": {
			required: []string{"top-level product fields"},
			optional: []string{},
			examples: []map[string]any{{"name": "demo", "start_date": "2026-01-01"}},
		},
		"update product": {
			required: []string{"product_id_or_name", "update_fields"},
			optional: []string{},
			examples: []map[string]any{{"product_id_or_name": "demo", "update_fields": map[string]any{"description": "updated"}}},
			parameters: map[string]parameterSchema{
				"product_id_or_name": {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
				"update_fields":      {Type: "object", Required: true, Description: "Product fields to update"},
			},
		},
		"delete product": {
			required: []string{"product_id_or_name"},
			optional: []string{},
			examples: []map[string]any{{"product_id_or_name": "demo"}},
			parameters: map[string]parameterSchema{
				"product_id_or_name": {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
			},
		},
		"get product-group-list": {
			required: []string{},
			optional: []string{"fields", "limit", "raw", "format"},
			examples: []map[string]any{{"fields": []string{"id", "name"}, "format": "ndjson"}},
			parameters: map[string]parameterSchema{
				"fields": {Type: "array<string> | string", Required: false, Description: "Returned product group fields; defaults to id,name,start_date,label"},
				"limit":  {Type: "integer", Required: false, Description: "Maximum number of product groups returned by the CLI"},
				"raw":    {Type: "boolean", Required: false, Description: "Keep raw server fields instead of applying default projection"},
				"format": {Type: "string", Required: false, Description: "Output format", Default: "json", Enum: []string{"json", "ndjson"}},
			},
			returns: "data.product_groups[] and data.total from the product group list response",
		},
		"get product-group": {
			required: []string{"product_group_id_or_name"},
			optional: []string{},
			examples: []map[string]any{{"product_group_id_or_name": "demo-group"}},
			parameters: map[string]parameterSchema{
				"product_group_id_or_name": {Type: "string", Required: true, Description: "Product group id or name"},
			},
		},
		"get permission-list": {
			required: []string{"resource_type", "resource_id"},
			optional: []string{"product_id_or_name", "product_group_id_or_name", "fields", "limit", "format"},
			examples: []map[string]any{{"resource_type": "products", "resource_id": "...", "fields": []string{"id", "user_id", "permission"}, "format": "ndjson"}},
			parameters: map[string]parameterSchema{
				"resource_type":            {Type: "string", Required: true, Description: "Permission resource type", Enum: []string{"products", "product_groups"}, Aliases: []string{"product", "product_group"}},
				"resource_id":              {Type: "string", Required: true, Description: "Resource id"},
				"product_id_or_name":       {Type: "string", Required: false, Description: "Convenience alias for products; resolves product name to resource_id"},
				"product_group_id_or_name": {Type: "string", Required: false, Description: "Convenience alias for product_groups; resolves product group name to resource_id"},
				"fields":                   {Type: "array<string> | string", Required: false, Description: "Returned permission fields"},
				"limit":                    {Type: "integer", Required: false, Description: "Maximum number of permission rows returned by the CLI"},
				"format":                   {Type: "string", Required: false, Description: "Output format", Default: "json", Enum: []string{"json", "ndjson"}},
			},
			returns: "data.permissions[] contains resource permission rows",
		},
		"update permission": {
			required: []string{"resource_type", "resource_id", "permissions"},
			optional: []string{"product_id_or_name", "product_group_id_or_name"},
			examples: []map[string]any{{"resource_type": "products", "resource_id": "...", "permissions": []map[string]any{{"user_id": 123, "permission": "read_import_data"}}}},
			parameters: map[string]parameterSchema{
				"resource_type":            {Type: "string", Required: true, Description: "Permission resource type", Enum: []string{"products", "product_groups"}, Aliases: []string{"product", "product_group"}},
				"resource_id":              {Type: "string", Required: true, Description: "Resource id"},
				"product_id_or_name":       {Type: "string", Required: false, Description: "Convenience alias for products; resolves product name to resource_id"},
				"product_group_id_or_name": {Type: "string", Required: false, Description: "Convenience alias for product_groups; resolves product group name to resource_id"},
				"permissions":              {Type: "array<object> | object", Required: true, Description: "Permission rows to add or modify; each row requires user_id and permission, and may include permission_id"},
			},
			returns: "data.effect_count and data.error_messages from AMS",
		},
		"delete permission": {
			required: []string{"resource_type", "resource_id", "permission_ids"},
			optional: []string{"product_id_or_name", "product_group_id_or_name"},
			examples: []map[string]any{{"resource_type": "products", "resource_id": "...", "permission_ids": []string{"..."}}},
			parameters: map[string]parameterSchema{
				"resource_type":            {Type: "string", Required: true, Description: "Permission resource type", Enum: []string{"products", "product_groups"}, Aliases: []string{"product", "product_group"}},
				"resource_id":              {Type: "string", Required: true, Description: "Resource id"},
				"product_id_or_name":       {Type: "string", Required: false, Description: "Convenience alias for products; resolves product name to resource_id"},
				"product_group_id_or_name": {Type: "string", Required: false, Description: "Convenience alias for product_groups; resolves product group name to resource_id"},
				"permission_ids":           {Type: "array<string> | string", Required: true, Description: "Permission record ids to delete", Aliases: []string{"permission_id", "ids"}},
			},
			returns: "data.effect_count from AMS",
		},
		"update permission-batch": {
			required: []string{"resource_type", "resource_ids", "permissions"},
			optional: []string{},
			examples: []map[string]any{{"resource_type": "products", "product_ids_or_names": []string{"demo1", "demo2"}, "permissions": []map[string]any{{"user_id": 123, "permission": "read_net_value"}}}},
			parameters: map[string]parameterSchema{
				"resource_type":              {Type: "string", Required: true, Description: "Permission resource type", Enum: []string{"products", "product_groups"}, Aliases: []string{"product", "product_group"}},
				"resource_ids":               {Type: "array<string> | string", Required: true, Description: "Resource ids; use product_ids_or_names or product_group_ids_or_names to resolve names", Aliases: []string{"ids", "product_ids", "product_group_ids"}},
				"permissions":                {Type: "array<object> | object", Required: true, Description: "Permission rows to apply to every resource; each row requires user_id and permission"},
				"product_ids_or_names":       {Type: "array<string> | string", Required: false, Description: "Product ids or names; implies resource_type=products"},
				"product_group_ids_or_names": {Type: "array<string> | string", Required: false, Description: "Product group ids or names; implies resource_type=product_groups"},
			},
			returns: "data.effect_count and data.error_messages from AMS",
		},
		"update product-group": {
			required: []string{"product_group_id_or_name", "update_fields"},
			optional: []string{},
			examples: []map[string]any{{"product_group_id_or_name": "demo-group", "update_fields": map[string]any{"description": "updated"}}},
			parameters: map[string]parameterSchema{
				"product_group_id_or_name": {Type: "string", Required: true, Description: "Product group id or name"},
				"update_fields":            {Type: "object", Required: true, Description: "Product group fields to update"},
			},
		},
		"delete product-group": {
			required: []string{"product_group_id_or_name"},
			optional: []string{},
			examples: []map[string]any{{"product_group_id_or_name": "demo-group"}},
			parameters: map[string]parameterSchema{
				"product_group_id_or_name": {Type: "string", Required: true, Description: "Product group id or name"},
			},
		},
		"get trade-list": {
			required: []string{"product_id_or_name"},
			optional: []string{"start_date", "end_date", "sources", "order_book_id", "symbol", "asset_transaction_types", "account_names", "asset_unit_ids", "key_words", "group_by", "remarks", "limit", "format"},
			examples: []map[string]any{{"product_id_or_name": "...", "start_date": "2026-01-01", "end_date": "2026-01-31", "limit": 20}},
			parameters: map[string]parameterSchema{
				"product_id_or_name":      {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
				"start_date":              {Type: "string", Required: false, Description: "Inclusive start date"},
				"end_date":                {Type: "string", Required: false, Description: "Inclusive end date"},
				"sources":                 {Type: "array<string> | string", Required: false, Description: "Trade source filter"},
				"order_book_id":           {Type: "string", Required: false, Description: "Order book id filter"},
				"symbol":                  {Type: "string", Required: false, Description: "Symbol keyword filter"},
				"asset_transaction_types": {Type: "array<string> | string", Required: false, Description: "Asset transaction type filter"},
				"account_names":           {Type: "array<string> | string", Required: false, Description: "Account name filter"},
				"asset_unit_ids":          {Type: "array<string> | string", Required: false, Description: "Asset unit id filter"},
				"key_words":               {Type: "string", Required: false, Description: "Keyword filter"},
				"group_by":                {Type: "string", Required: false, Description: "Server-side grouping option"},
				"remarks":                 {Type: "string", Required: false, Description: "Remarks filter"},
				"limit":                   {Type: "integer", Required: false, Description: "Maximum number of trades returned by the CLI"},
				"format":                  {Type: "string", Required: false, Description: "Output format", Default: "json", Enum: []string{"json", "ndjson"}},
			},
			returns: "data.trades[] with optional data.total and related server fields",
		},
		"insert trade": {
			required: []string{"product_id_or_name", "trades"},
			optional: []string{"chunk_size"},
			examples: []map[string]any{{"product_id_or_name": "...", "trades": []map[string]any{{"transaction_type": "buy", "datetime": "2026-01-05 09:31:00", "order_book_id": "000001.XSHE", "symbol": "平安银行", "quantity": 100, "price": 10.5}}}},
			parameters: map[string]parameterSchema{
				"product_id_or_name": {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
				"trades":             {Type: "array<object>", Required: true, Description: "Trade rows. When used to fix reconciliation differences, confirm product, date, transaction_type, instrument, direction, quantity, price/amount, account, asset_unit_id, and expected impact with the user before inserting"},
				"chunk_size":         {Type: "integer", Required: false, Description: "Client-side batch size"},
			},
			returns: "data contains the batch insert result returned by AMS",
		},
		"insert settlement-trade": {
			required: []string{"product_id_or_name", "account_name", "file_paths"},
			optional: []string{},
			examples: []map[string]any{{"product_id_or_name": "...", "account_name": "stock", "file_paths": []string{"D:/tmp/settlement.csv"}}},
			parameters: map[string]parameterSchema{
				"product_id_or_name": {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
				"account_name":       {Type: "string", Required: true, Description: "Product account name"},
				"file_paths":         {Type: "array<string>", Required: true, Description: "Settlement file paths to upload"},
			},
			returns: "data contains the upload and parse result returned by AMS",
		},
		"delete trade": {
			required: []string{"product_id_or_name"},
			optional: []string{"trade_ids", "start_date", "end_date", "sources"},
			examples: []map[string]any{{"product_id_or_name": "...", "trade_ids": []string{"..."}}},
		},
		"get balance": {
			required: []string{"product_like_id_or_name"},
			optional: []string{"date", "fields"},
			examples: []map[string]any{{"product_like_id_or_name": "...", "date": "2026-01-05", "fields": []string{"total_equity", "unit_net_value"}}},
			parameters: map[string]parameterSchema{
				"product_like_id_or_name": {Type: "string", Required: true, Description: "Product or product group id/name; resolved before request"},
				"date":                    {Type: "string", Required: false, Description: "Balance date; omitted or future dates request realtime balance"},
				"fields":                  {Type: "array<string> | string", Required: false, Description: "Top-level balance fields to return; also forwarded to AMS"},
			},
			returns: "data contains the product or product group balance object",
		},
		"get balance-series": {
			required: []string{"product_like_id_or_name", "start_date", "end_date"},
			optional: []string{"fields", "limit", "format"},
			examples: []map[string]any{{"product_like_id_or_name": "...", "start_date": "2026-01-01", "end_date": "2026-01-31", "format": "ndjson"}},
			parameters: map[string]parameterSchema{
				"product_like_id_or_name": {Type: "string", Required: true, Description: "Product or product group id/name; resolved before request"},
				"start_date":              {Type: "string", Required: true, Description: "Inclusive start date"},
				"end_date":                {Type: "string", Required: true, Description: "Inclusive end date"},
				"fields":                  {Type: "array<string> | string", Required: false, Description: "Balance position fields requested from AMS"},
				"limit":                   {Type: "integer", Required: false, Description: "Maximum number of rows returned by the CLI"},
				"format":                  {Type: "string", Required: false, Description: "Output format", Default: "json", Enum: []string{"json", "ndjson"}},
			},
			returns: "data[] or data.data[] balance series rows, depending on AMS response shape",
		},
		"get asset-snapshot": {
			required: []string{"product_like_id_or_name"},
			optional: []string{"fields", "flatten_positions", "classifier"},
			examples: []map[string]any{{"product_like_id_or_name": "...", "fields": []string{"risk_exposure", "net_risk_exposure", "excess_returns"}}},
			parameters: map[string]parameterSchema{
				"product_like_id_or_name": {Type: "string", Required: true, Description: "Product or product group id/name; resolved before request"},
				"fields":                  {Type: "array<string> | string", Required: false, Description: "Additional top-level realtime snapshot fields requested from AMS"},
				"flatten_positions":       {Type: "boolean", Required: false, Description: "Flatten positions; defaults to true"},
				"classifier":              {Type: "string", Required: false, Description: "Position classifier when flatten_positions is false"},
			},
			returns: "data contains the realtime product or product group balance snapshot",
		},
		"insert paper-trading": {
			required: []string{"template for new paper trading, or product_id_or_name for existing product config", "template-specific fields"},
			optional: []string{"strategy_model", "name", "benchmark", "start_date", "init_amount", "algo", "start_time", "end_time", "commission_rate", "min_fee", "stock_commission_rate", "stock_min_fee", "loan_rate", "margin_rate", "strategy_category", "futures_float_rate", "futures_float_amount", "slippage_rate", "slippage_ticks", "tag_ids", "description", "file_paths", "config"},
			examples: []map[string]any{
				{"template": "equity_long", "name": "demo", "benchmark": "index,000300.XSHG", "start_date": "2026-01-01", "init_amount": 1000000, "algo": "open"},
				{"template": "conventional", "name": "demo", "benchmark": "index,000300.XSHG", "start_date": "2026-01-01", "init_amount": 1000000, "stock_min_fee": 5, "stock_commission_rate": 0.0003, "loan_rate": 0.06, "margin_rate": 0.5, "strategy_category": "index_enhanced"},
				{"product_id_or_name": "demo", "stock_min_fee": 5, "stock_commission_rate": 0.0003},
			},
		},
		"get paper-trading-list": {
			required: []string{},
			optional: []string{"fields", "limit", "format"},
			examples: []map[string]any{{"fields": []string{"product_id", "name", "status", "strategy_model"}, "format": "ndjson"}},
			parameters: map[string]parameterSchema{
				"fields": {Type: "array<string> | string", Required: false, Description: "Returned paper trading fields"},
				"limit":  {Type: "integer", Required: false, Description: "Maximum number of paper trading configs returned by the CLI"},
				"format": {Type: "string", Required: false, Description: "Output format", Default: "json", Enum: []string{"json", "ndjson"}},
			},
			returns: "data[] paper trading configs aggregated by product_id and strategy_model",
		},
		"recompute balance": {
			required: []string{"product_like_ids_or_names or explicit product/group ids"},
			optional: []string{"start_date"},
			examples: []map[string]any{{"product_like_ids_or_names": []string{"..."}, "start_date": "2026-01-01"}},
		},
		"get reconciliation-list": {
			required: []string{"start_date", "end_date", "product_ids or product_ids_or_names"},
			optional: []string{"format"},
			examples: []map[string]any{{"product_ids_or_names": []string{"demo"}, "start_date": "2026-01-01", "end_date": "2026-01-31"}},
		},
		"get reconciliation-diff": {
			required: []string{"product_id_or_name", "date", "fields"},
			optional: []string{},
			examples: []map[string]any{{"product_id_or_name": "...", "date": "2026-01-31", "fields": []string{"positions", "prices", "net_asset"}}},
		},
		"update reconciliation": {
			required: []string{"product_id_or_name", "date"},
			optional: []string{"action", "done", "description"},
			examples: []map[string]any{
				{"product_id_or_name": "...", "date": "2026-01-31", "action": "mark", "done": true, "description": "checked"},
				{"product_id_or_name": "...", "date": "2026-01-31", "action": "undo_auto"},
			},
			parameters: map[string]parameterSchema{
				"product_id_or_name": {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
				"date":               {Type: "string", Required: true, Description: "Reconciliation date"},
				"action":             {Type: "string", Required: false, Description: "mark writes manual status. auto overwrites the day's trade/position result with the valuation report and should only be used after explicit user confirmation. undo_auto reverts auto reconciliation", Default: "mark", Enum: []string{"mark", "auto", "undo_auto"}},
				"done":               {Type: "boolean", Required: false, Description: "Manual reconciliation status for action=mark"},
				"description":        {Type: "string", Required: false, Description: "Manual reconciliation note"},
			},
		},
		"get reconciliation-asset-unit-diff": {
			required: []string{"product_id_or_name", "asset_unit_id", "date"},
			optional: []string{},
			examples: []map[string]any{{"product_id_or_name": "...", "asset_unit_id": "...", "date": "2026-01-31"}},
		},
		"get reconciliation-position-statement": {
			required: []string{"product_id_or_name", "asset_unit_id", "date"},
			optional: []string{},
			examples: []map[string]any{{"product_id_or_name": "...", "asset_unit_id": "...", "date": "2026-01-31"}},
		},
		"get position-statement-latest-list": {
			required: []string{},
			optional: []string{},
			examples: []map[string]any{{}},
		},
		"get valuation-report-list": {
			required: []string{"product_id_or_name"},
			optional: []string{"start_date", "end_date", "fields", "limit", "format"},
			examples: []map[string]any{{"product_id_or_name": "...", "fields": []string{"valuation_report_id", "file_name", "date"}, "format": "ndjson"}},
			parameters: map[string]parameterSchema{
				"product_id_or_name": {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
				"start_date":         {Type: "string", Required: false, Description: "Inclusive start date"},
				"end_date":           {Type: "string", Required: false, Description: "Inclusive end date"},
				"fields":             {Type: "array<string> | string", Required: false, Description: "Returned valuation report metadata fields"},
				"limit":              {Type: "integer", Required: false, Description: "Maximum number of valuation reports returned by the CLI"},
				"format":             {Type: "string", Required: false, Description: "Output format", Default: "json", Enum: []string{"json", "ndjson"}},
			},
			returns: "data.valuation_reports[] metadata items, commonly valuation_report_id, date, file_name, and source",
		},
		"insert valuation-report": {
			required: []string{"product_id_or_name", "file_paths or valuation_reports"},
			optional: []string{"replace_dates"},
			examples: []map[string]any{{"product_id_or_name": "...", "file_paths": []string{"D:/tmp/valuation.xlsx"}}},
			parameters: map[string]parameterSchema{
				"product_id_or_name": {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
				"file_paths":         {Type: "array<string>", Required: false, Description: "Local .xls/.xlsx file paths or directories containing valuation report files"},
				"valuation_reports":  {Type: "array<object> | object", Required: false, Description: "Valuation report objects for JSON insert; each object follows the valuation report balance schema"},
				"replace_dates":      {Type: "array<string> | string", Required: false, Description: "Dates allowed to be overwritten during insert"},
			},
			returns: "data[] upload or JSON insert results; file upload entries include file and result/err_msg",
		},
		"delete valuation-report": {
			required: []string{"product_id_or_name", "dates"},
			optional: []string{},
			examples: []map[string]any{{"product_id_or_name": "...", "dates": []string{"2026-01-31"}}},
			parameters: map[string]parameterSchema{
				"product_id_or_name": {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
				"dates":              {Type: "array<string> | string", Required: true, Description: "Valuation report dates to delete", Aliases: []string{"deleted_dates"}},
			},
			returns: "data contains the server delete result, commonly effect_count",
		},
		"get valuation-report-file": {
			required: []string{"product_id_or_name", "save_path"},
			optional: []string{"valuation_report_id", "file_name", "start_date", "end_date", "fields", "limit"},
			examples: []map[string]any{{"product_id_or_name": "...", "valuation_report_id": "...", "save_path": "D:/tmp/reports", "file_name": "valuation.xlsx"}},
			parameters: map[string]parameterSchema{
				"product_id_or_name":  {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
				"save_path":           {Type: "string", Required: true, Description: "Output directory or file path"},
				"valuation_report_id": {Type: "string", Required: false, Description: "Valuation report id for single-file download", Aliases: []string{"report_id"}},
				"file_name":           {Type: "string", Required: false, Description: "Output file name for single-file download"},
				"start_date":          {Type: "string", Required: false, Description: "Inclusive start date used when valuation_report_id is omitted"},
				"end_date":            {Type: "string", Required: false, Description: "Inclusive end date used when valuation_report_id is omitted"},
				"fields":              {Type: "array<string> | string", Required: false, Description: "List fields used before batch download when valuation_report_id is omitted"},
				"limit":               {Type: "integer", Required: false, Description: "Maximum number of files to download when valuation_report_id is omitted"},
			},
			returns: "single-file download returns data.path, data.content_type, and data.bytes; batch download returns data.successful[] and data.failed[]",
		},
		"get customized-instrument-price": {
			required: []string{"customized_ins_id"},
			optional: []string{"raw", "limit"},
			examples: []map[string]any{{"customized_ins_id": "...", "raw": false, "limit": 200}},
			parameters: map[string]parameterSchema{
				"customized_ins_id": {Type: "string", Required: true, Description: "Customized instrument id"},
				"raw":               {Type: "boolean", Required: false, Description: "Return the full customized instrument object instead of only fair_values"},
				"limit":             {Type: "integer", Required: false, Description: "Maximum number of fair_value rows returned by the CLI"},
			},
			returns: "data is the fair_values array by default; raw:true returns the full customized instrument object",
		},
		"insert customized-instrument-price": {
			required: []string{"customized_ins_id", "file_paths"},
			optional: []string{},
			examples: []map[string]any{{"customized_ins_id": "...", "file_paths": []string{"D:/tmp/fair_values.xlsx"}}},
			parameters: map[string]parameterSchema{
				"customized_ins_id": {Type: "string", Required: true, Description: "Customized instrument id"},
				"file_paths":        {Type: "array<string>", Required: true, Description: "Local price files or directories containing price files"},
			},
			returns: "data contains one upload result per file path",
		},
		"get custodian-event-list": {
			required: []string{"product_id_or_name"},
			optional: []string{"start_date", "end_date", "custodian_event_type", "adjust_target", "fields", "limit", "format"},
			examples: []map[string]any{{"product_id_or_name": "...", "fields": []string{"id", "date", "custodian_event_type", "amount"}, "limit": 20}},
			parameters: map[string]parameterSchema{
				"product_id_or_name":   {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
				"start_date":           {Type: "string", Required: false, Description: "Inclusive start date"},
				"end_date":             {Type: "string", Required: false, Description: "Inclusive end date"},
				"custodian_event_type": {Type: "array<string> | string", Required: false, Description: "Custodian event type filter"},
				"adjust_target":        {Type: "string", Required: false, Description: "Subject adjustment target filter"},
				"fields":               {Type: "array<string> | string", Required: false, Description: "Returned custodian event fields"},
				"limit":                {Type: "integer", Required: false, Description: "Maximum number of custodian events returned by the CLI"},
				"format":               {Type: "string", Required: false, Description: "Output format", Default: "json", Enum: []string{"json", "ndjson"}},
			},
			returns: "data.custodian_events[] and data.total",
		},
		"insert custodian-event": {
			required: []string{"product_id_or_name", "custodian_events"},
			optional: []string{},
			examples: []map[string]any{{"product_id_or_name": "...", "custodian_events": []map[string]any{{"date": "2026-01-31", "custodian_event_type": "product_dividend_paid", "amount": 1000}}}},
			parameters: map[string]parameterSchema{
				"product_id_or_name": {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
				"custodian_events":   {Type: "array<object> | object", Required: true, Description: "Custodian event objects. When used to fix reconciliation differences, confirm product, date/effective_date, event type, amount, subject target, and expected impact with the user before inserting"},
			},
			returns: "data contains the server batch insert result, commonly effect_count",
		},
		"update custodian-event": {
			required: []string{"product_id_or_name", "event_id", "custodian_event"},
			optional: []string{},
			examples: []map[string]any{{"product_id_or_name": "...", "event_id": "...", "custodian_event": map[string]any{"date": "2026-01-31", "custodian_event_type": "product_dividend_paid", "amount": 1000}}},
			parameters: map[string]parameterSchema{
				"product_id_or_name": {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
				"event_id":           {Type: "string", Required: true, Description: "Custodian event id; may also be supplied as id inside custodian_event"},
				"custodian_event":    {Type: "object", Required: true, Description: "Full custodian event object to replace"},
			},
			returns: "data contains the server update result, commonly effect_count",
		},
		"delete custodian-event": {
			required: []string{"product_id_or_name", "event_ids"},
			optional: []string{},
			examples: []map[string]any{{"product_id_or_name": "...", "event_ids": []string{"..."}}},
			parameters: map[string]parameterSchema{
				"product_id_or_name": {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
				"event_ids":          {Type: "array<string> | string", Required: true, Description: "Custodian event ids to delete", Aliases: []string{"event_id", "ids"}},
			},
			returns: "data contains the server delete result, commonly effect_count",
		},
		"get unit-event-list": {
			required: []string{"product_id_or_name"},
			optional: []string{"start_date", "end_date", "include_auto_units", "fields", "limit", "format"},
			examples: []map[string]any{{"product_id_or_name": "...", "fields": []string{"id", "date", "subscription_units", "redemption_units", "source"}, "limit": 20}},
			parameters: map[string]parameterSchema{
				"product_id_or_name": {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
				"start_date":         {Type: "string", Required: false, Description: "Inclusive start date"},
				"end_date":           {Type: "string", Required: false, Description: "Inclusive end date"},
				"include_auto_units": {Type: "boolean", Required: false, Description: "Keep auto-generated daily_units entries; defaults to false"},
				"fields":             {Type: "array<string> | string", Required: false, Description: "Returned daily_units fields"},
				"limit":              {Type: "integer", Required: false, Description: "Maximum number of daily_units rows returned by the CLI"},
				"format":             {Type: "string", Required: false, Description: "Output format", Default: "json", Enum: []string{"json", "ndjson"}},
			},
			returns: "data.daily_units[] and data.unit_changes[]",
		},
		"insert unit-event": {
			required: []string{"product_id_or_name", "unit_events"},
			optional: []string{},
			examples: []map[string]any{{"product_id_or_name": "...", "unit_events": []map[string]any{{"date": "2026-01-31", "subscription_units": 1000}}}},
			parameters: map[string]parameterSchema{
				"product_id_or_name": {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
				"unit_events":        {Type: "array<object> | object", Required: true, Description: "Unit event objects"},
			},
			returns: "data contains the server batch insert result, commonly effect_count and err_msg",
		},
		"update unit-event": {
			required: []string{"product_id_or_name", "event_id", "unit_event"},
			optional: []string{},
			examples: []map[string]any{{"product_id_or_name": "...", "event_id": "...", "unit_event": map[string]any{"subscription_units": 1000, "redemption_units": nil}}},
			parameters: map[string]parameterSchema{
				"product_id_or_name": {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
				"event_id":           {Type: "string", Required: true, Description: "Unit event id; may also be supplied as id inside unit_event"},
				"unit_event":         {Type: "object", Required: true, Description: "Unit event fields to update; include subscription_units and redemption_units"},
			},
			returns: "data contains the server update result, commonly effect_count",
		},
		"delete unit-event": {
			required: []string{"product_id_or_name", "event_ids"},
			optional: []string{},
			examples: []map[string]any{{"product_id_or_name": "...", "event_ids": []string{"..."}}},
			parameters: map[string]parameterSchema{
				"product_id_or_name": {Type: "string", Required: true, Description: "Product id or product name; resolved before request"},
				"event_ids":          {Type: "array<string> | string", Required: true, Description: "Unit event ids to delete", Aliases: []string{"event_id", "ids"}},
			},
			returns: "data contains the server delete result, commonly effect_count",
		},
	}
}

func shouldWriteNDJSON(doc map[string]any) bool {
	if !strings.EqualFold(queryValue(doc["format"]), "ndjson") {
		return false
	}
	return true
}

func ndjsonTopLevelItems(data any) ([]any, error) {
	if items, ok := data.([]any); ok {
		return items, nil
	}
	return nil, fmt.Errorf("ndjson output requires list data")
}

func ndjsonFieldItems(field string) ndjsonExtractor {
	return func(data any) ([]any, error) {
		root, ok := data.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("ndjson output requires object data")
		}
		items, ok := root[field].([]any)
		if !ok {
			return nil, fmt.Errorf("ndjson output requires field %q to be a list", field)
		}
		return items, nil
	}
}

func ndjsonDataItems(data any) ([]any, error) {
	if items, ok := data.([]any); ok {
		return items, nil
	}
	root, ok := data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ndjson output requires object data")
	}
	items, ok := root["data"].([]any)
	if !ok {
		return nil, fmt.Errorf("ndjson output requires field %q to be a list", "data")
	}
	return items, nil
}

func parseArgs(args []string) (string, string, error) {
	if len(args) < 1 {
		return "", "", fmt.Errorf("usage: rqamsc auth --payload <json|@file|-> or rqamsc <verb> <resource> --payload <json|@file|->")
	}
	command := args[0]
	payloadStart := 1
	if args[0] != "auth" {
		if len(args) < 2 {
			return "", "", fmt.Errorf("usage: rqamsc auth --payload <json|@file|-> or rqamsc <verb> <resource> --payload <json|@file|->")
		}
		command = args[0] + " " + args[1]
		payloadStart = 2
	}
	payloadArg := ""
	for i := payloadStart; i < len(args); i++ {
		if args[i] != "--payload" {
			return command, "", fmt.Errorf("unknown argument %q", args[i])
		}
		if i+1 >= len(args) {
			return command, "", fmt.Errorf("--payload requires a value")
		}
		payloadArg = args[i+1]
		i++
	}
	return command, payloadArg, nil
}

func routes() map[string]route {
	return map[string]route{
		"auth": {
			method: "POST",
			path:   "login",
			run:    openAuth,
		},
		"get workspace-list": {
			method: "GET",
			path:   "v1/workspaces",
			run: func(ctx context) (any, map[string]any, error) {
				data, err := ctx.client.UserRequest("GET", "v1/workspaces", nil)
				return data, nil, err
			},
		},
		"get current-workspace": {
			run: getCurrentWorkspace,
		},
		"use workspace": {
			run: useWorkspace,
		},
		"get product-list": {
			method: "GET",
			path:   "products",
			run:    getProductList,
			ndjson: ndjsonFieldItems("products"),
		},
		"get product": {
			method: "GET",
			path:   "products/${product_id}",
			run: func(ctx context) (any, map[string]any, error) {
				productID, err := productID(ctx.payload)
				if err != nil {
					return nil, nil, err
				}
				data, err := ctx.client.AMSRequest("GET", "products/"+productID, nil)
				return data, map[string]any{"resolved_path": "products/" + productID}, err
			},
		},
		"insert product": {
			method: "POST",
			path:   "products",
			run:    insertProduct,
		},
		"get product-group-list": {
			method: "GET",
			path:   "product_groups",
			run:    getProductGroupList,
			ndjson: ndjsonFieldItems("product_groups"),
		},
		"get product-group": {
			method: "GET",
			path:   "product_groups/${group_id}",
			run:    getProductGroup,
		},
		"get permission-list": {
			method: "GET",
			path:   "${resource_type}/${resource_id}/permissions",
			run:    getPermissionList,
			ndjson: ndjsonFieldItems("permissions"),
		},
		"update permission": {
			method: "POST",
			path:   "${resource_type}/${resource_id}/permissions",
			run:    updatePermission,
		},
		"delete permission": {
			method: "DELETE",
			path:   "${resource_type}/${resource_id}/permissions",
			run:    deletePermission,
		},
		"update permission-batch": {
			method: "POST",
			path:   "${resource_type}/permissions",
			run:    updatePermissionBatch,
		},
		"get trade-list": {
			method: "GET",
			path:   "products/${product_id}/trades",
			run:    getTradeList,
			ndjson: ndjsonFieldItems("trades"),
		},
		"insert trade": {
			method: "POST",
			path:   "products/${product_id}/trades:batch_insert",
			run:    insertTrade,
		},
		"insert settlement-trade": {
			method: "POST",
			path:   "products/${product_id}:upload_settlement_trade_file",
			run:    insertSettlementTrade,
		},
		"delete trade": {
			method: "GET",
			path:   "products/${product_id}/trades:batch_delete_by_date",
			run:    deleteTrade,
		},
		"get balance": {
			method: "GET",
			path:   "${resource_type}/${resource_id}/balance",
			run:    getBalance,
		},
		"get balance-series": {
			method: "GET",
			path:   "${resource_type}/${resource_id}/balance_series",
			run:    getBalanceSeries,
			ndjson: ndjsonDataItems,
		},
		"get asset-snapshot": {
			method: "GET",
			path:   "${resource_type}/${resource_id}/asset_snapshot_summary",
			run:    getAssetSnapshot,
		},
		"recompute balance": {
			method: "POST",
			path:   "products:recompute + product_groups:recompute",
			run:    recomputeBalance,
		},
		"get reconciliation-list": {
			method: "POST",
			path:   "products:batch_get_reconciliation_list",
			run:    getReconciliationList,
			ndjson: ndjsonTopLevelItems,
		},
		"get reconciliation-diff": {
			method: "POST",
			path:   "products/${product_id}:get_reconciliation_diff",
			run:    getReconciliationDiff,
		},
		"update reconciliation": {
			method: "POST",
			path:   "products/${product_id}:reconciliation",
			run:    updateReconciliation,
		},
		"get reconciliation-asset-unit-diff": {
			method: "POST",
			path:   "products/${product_id}/asset_units/${asset_unit_id}:get_reconciliation_diff",
			run:    getReconciliationAssetUnitDiff,
		},
		"get reconciliation-position-statement": {
			method: "GET",
			path:   "products/${product_id}/asset_units/${asset_unit_id}:reconciliation_positions_statement",
			run:    getReconciliationPositionStatement,
		},
		"get position-statement-latest-list": {
			method: "GET",
			path:   "products/asset_units/positions_statement:get_latest",
			run:    getPositionStatementLatestList,
			ndjson: ndjsonTopLevelItems,
		},
		"get position-statement": {
			method: "GET",
			path:   "products/${product_id}/asset_units/${asset_unit_id}/positions_statement",
			run:    getPositionStatement,
		},
		"insert position-statement": {
			method: "POST",
			path:   "products/${product_id}/asset_units/${asset_unit_id}:upload_positions_statement",
			run:    insertPositionStatement,
		},
		"delete position-statement": {
			method: "DELETE",
			path:   "products/${product_id}/asset_units/${asset_unit_id}/positions_statement",
			run:    deletePositionStatement,
		},
		"get valuation-report-list": {
			method: "GET",
			path:   "products/${product_id}/valuation_reports",
			run:    getValuationReportList,
			ndjson: ndjsonFieldItems("valuation_reports"),
		},
		"insert valuation-report": {
			method: "POST",
			path:   "products/${product_id}/valuation_reports",
			run:    insertValuationReport,
		},
		"delete valuation-report": {
			method: "POST",
			path:   "products/${product_id}/valuation_reports:batch_delete",
			run:    deleteValuationReport,
		},
		"get valuation-report-file": {
			method: "GET",
			path:   "products/${product_id}/valuation_reports/${valuation_report_id}/src_file",
			run:    getValuationReportFile,
		},
		"get custodian-event-list": {
			method: "GET",
			path:   "products/${product_id}/custodian_events",
			run:    listProductEvents("custodian_events", "custodian_events"),
			ndjson: ndjsonFieldItems("custodian_events"),
		},
		"insert custodian-event": {
			method: "POST",
			path:   "products/${product_id}/custodian_events:batch_insert",
			run:    insertProductEvents("custodian_events", "custodian_events"),
		},
		"update custodian-event": {
			method: "PUT",
			path:   "products/${product_id}/custodian_events/${event_id}",
			run:    updateProductEvent("custodian_events"),
		},
		"delete custodian-event": {
			method: "POST",
			path:   "products/${product_id}/custodian_events:batch_delete",
			run:    deleteProductEvents("custodian_events"),
		},
		"get unit-event-list": {
			method: "GET",
			path:   "products/${product_id}/unit_events",
			run:    listProductEvents("unit_events", "daily_units"),
			ndjson: ndjsonFieldItems("daily_units"),
		},
		"insert unit-event": {
			method: "POST",
			path:   "products/${product_id}/unit_events:batch_insert",
			run:    insertProductEvents("unit_events", "unit_events"),
		},
		"update unit-event": {
			method: "PUT",
			path:   "products/${product_id}/unit_events/${event_id}",
			run:    updateProductEvent("unit_events"),
		},
		"delete unit-event": {
			method: "POST",
			path:   "products/${product_id}/unit_events:batch_delete",
			run:    deleteProductEvents("unit_events"),
		},
		"insert customized-instrument": {
			method: "POST",
			path:   "customized_instruments",
			run:    insertCustomizedInstrument,
		},
		"get customized-instrument-list": {
			method: "GET",
			path:   "customized_instruments",
			run:    getCustomizedInstrumentList,
			ndjson: ndjsonFieldItems("customized_instruments"),
		},
		"get customized-instrument-price": {
			method: "GET",
			path:   "customized_instruments/${customized_ins_id}",
			run:    getCustomizedInstrumentPrice,
		},
		"insert customized-instrument-price": {
			method: "POST",
			path:   "customized_instruments/${customized_ins_id}:upload_fair_price_file",
			run:    insertCustomizedInstrumentPrice,
		},
		"delete customized-instrument": {
			method: "POST",
			path:   "customized_instruments:batch_delete",
			run:    deleteCustomizedInstrument,
		},
		"get customized-benchmark-list": {
			method: "GET",
			path:   "customized_benchmarks",
			run:    getCustomizedBenchmarkList,
			ndjson: ndjsonDataItems,
		},
		"get customized-benchmark": {
			method: "GET",
			path:   "customized_benchmarks/${customized_benchmark_id}",
			run:    getCustomizedBenchmark,
		},
		"insert customized-benchmark": {
			method: "POST",
			path:   "customized_benchmarks",
			run:    insertCustomizedBenchmark,
		},
		"update customized-benchmark": {
			method: "PUT",
			path:   "customized_benchmarks/${customized_benchmark_id}",
			run:    updateCustomizedBenchmark,
		},
		"delete customized-benchmark": {
			method: "DELETE",
			path:   "customized_benchmarks/${customized_benchmark_id}",
			run:    deleteCustomizedBenchmark,
		},
		"get weekly-net-value-report": {
			method: "GET",
			path:   "${resource_type}/${resource_id}/weekly_net_value_report",
			run:    getWeeklyNetValueReport,
		},
		"get indicator": {
			method: "GET",
			path:   "${resource_type}/${resource_id}/indicators",
			run:    getIndicator,
		},
		"get indicator-series": {
			method: "GET",
			path:   "${resource_type}/${resource_id}/indicators_series",
			run:    getIndicatorSeries,
		},
		"get customized-indicator": {
			method: "GET",
			path:   "${resource_type}/${resource_id}/customized_indicators",
			run:    getCustomizedIndicator,
		},
		"insert customized-indicator": {
			method: "POST",
			path:   "${resource_type}/${resource_id}/customized_indicators",
			run:    insertCustomizedIndicator,
		},
		"update customized-indicator": {
			method: "PATCH",
			path:   "${resource_type}/${resource_id}/customized_indicators",
			run:    updateCustomizedIndicator,
		},
		"delete customized-indicator": {
			method: "DELETE",
			path:   "${resource_type}/${resource_id}/customized_indicators",
			run:    deleteCustomizedIndicator,
		},
		"get investment-overview-summary-indicator": {
			method: "GET",
			path:   "product_group_overview/indicators_v2",
			run:    getInvestmentOverviewSummaryIndicator,
		},
		"get investment-overview-returns-series": {
			method: "POST",
			path:   "product_group_overview/returns_v2",
			run:    investmentOverviewHandler("returns", true, true),
		},
		"get investment-overview-asset-capital-size": {
			method: "POST",
			path:   "product_group_overview/asset_capital_size_v2",
			run:    investmentOverviewHandler("asset_capital_size", true, false),
		},
		"get investment-overview-asset-allocation": {
			method: "POST",
			path:   "product_group_overview/asset_allocation_v2",
			run:    investmentOverviewHandler("asset_allocation", true, false),
		},
		"get investment-overview-excess-correlation": {
			method: "POST",
			path:   "product_group_overview/excess_returns_correlation_v2",
			run:    investmentOverviewHandler("excess_returns_correlation", true, true),
		},
		"get investment-overview-returns-correlation": {
			method: "POST",
			path:   "product_group_overview/returns_correlation_v2",
			run:    investmentOverviewHandler("returns_correlation", true, false),
		},
		"get performance-attribution": {
			method: "POST",
			path:   "${resource_type}/${resource_id}/performance_attributions",
			run:    getPerformanceAttribution,
		},
		"get returns-decomposition": {
			method: "POST",
			path:   "${resource_type}/${resource_id}/performance_attributions",
			run:    getReturnsDecomposition,
		},
		"get trading-analysis-list": {
			method: "GET",
			path:   "${resource_type}/${resource_id}/trading_analysis_list",
			run:    getTradingAnalysisList,
		},
		"get trading-analysis": {
			method: "GET",
			path:   "${resource_type}/${resource_id}/single_trading_analysis",
			run:    getTradingAnalysis,
		},
		"get paper-trading-list": {
			method: "GET",
			path:   "auto paper_trading list",
			run:    getPaperTradingList,
			ndjson: ndjsonTopLevelItems,
		},
		"get paper-trading": {
			method: "GET",
			path:   "auto paper_trading detail",
			run:    getPaperTrading,
		},
		"insert paper-trading": {
			method: "POST",
			path:   "auto paper_trading create/write",
			run:    insertPaperTrading,
		},
		"get paper-trading-signal-list": {
			method: "GET",
			path:   "auto paper_trading_signals",
			run:    getUnifiedPaperTradingSignalList,
			ndjson: ndjsonFieldItems("signals"),
		},
		"get paper-trading-signal": {
			method: "GET",
			path:   "auto paper_trading_signal detail",
			run:    getUnifiedPaperTradingSignal,
		},
		"insert paper-trading-signal": {
			method: "POST",
			path:   "auto paper_trading signal upload",
			run:    insertPaperTradingSignal,
		},
		"update paper-trading": {
			method: "PATCH",
			path:   "auto paper_trading update",
			run:    updatePaperTrading,
		},
		"delete paper-trading": {
			method: "DELETE",
			path:   "auto paper_trading delete",
			run:    deletePaperTrading,
		},
		"delete paper-trading-signal": {
			method: "DELETE",
			path:   "auto paper_trading signal delete",
			run:    deletePaperTradingSignal,
		},
		"recompute paper-trading": {
			method: "POST",
			path:   "auto paper_trading recompute",
			run:    recomputePaperTrading,
		},
		"update product": {
			method: "PATCH",
			path:   "products/${product_id}",
			run: func(ctx context) (any, map[string]any, error) {
				productID, err := productID(ctx.payload)
				if err != nil {
					return nil, nil, err
				}
				updateFields, err := payload.Object(ctx.payload, "update_fields")
				if err != nil {
					return nil, nil, err
				}
				data, err := ctx.client.AMSRequest("PATCH", "products/"+productID, updateFields)
				return data, map[string]any{"resolved_path": "products/" + productID}, err
			},
		},
		"delete product": {
			method: "DELETE",
			path:   "products/${product_id}",
			run: func(ctx context) (any, map[string]any, error) {
				productID, err := productID(ctx.payload)
				if err != nil {
					return nil, nil, err
				}
				data, err := ctx.client.AMSRequest("DELETE", "products/"+productID, nil)
				return data, map[string]any{"resolved_path": "products/" + productID}, err
			},
		},
		"update product-group": {
			method: "PATCH",
			path:   "product_groups/${group_id}",
			run:    updateProductGroup,
		},
		"delete product-group": {
			method: "DELETE",
			path:   "product_groups/${group_id}",
			run:    deleteProductGroup,
		},
	}
}

func getProductList(ctx context) (any, map[string]any, error) {
	params := url.Values{}
	raw := boolValue(ctx.payload["raw"])
	fields := ""
	limit := intValue(ctx.payload["limit"])
	if !raw {
		fields = listFieldsWithDefault(ctx.payload, "id,name,start_date,label")
		params.Set("fields", fields)
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}

	data, err := ctx.client.AMSRequestWithParams("GET", "products", nil, params)
	if err != nil {
		return nil, nil, err
	}
	if !raw {
		if root, ok := data.(map[string]any); ok {
			delete(root, "permissions")
			projectProducts(root, splitFields(fields))
		}
	}
	if limit > 0 {
		if root, ok := data.(map[string]any); ok {
			limitProducts(root, limit)
		}
	}
	meta := map[string]any{}
	if len(params) > 0 {
		meta["query"] = params.Encode()
	}
	return data, meta, nil
}

func insertProduct(ctx context) (any, map[string]any, error) {
	body := productCreatePayload(ctx.payload)
	data, err := ctx.client.AMSRequest("POST", "products", body)
	return data, nil, err
}

func productCreatePayload(doc map[string]any) map[string]any {
	if product, ok := doc["product"].(map[string]any); ok {
		return product
	}
	body := map[string]any{}
	for key, value := range doc {
		if key == "format" || key == "profile" {
			continue
		}
		body[key] = value
	}
	return body
}

func getProductGroupList(ctx context) (any, map[string]any, error) {
	params := url.Values{}
	raw := boolValue(ctx.payload["raw"])
	fields := ""
	limit := intValue(ctx.payload["limit"])
	if !raw {
		fields = listFieldsWithDefault(ctx.payload, "id,name,start_date,label")
		params.Set("fields", fields)
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}

	data, err := ctx.client.AMSRequestWithParams("GET", "product_groups", nil, params)
	if err != nil {
		return nil, nil, err
	}
	if !raw {
		if root, ok := data.(map[string]any); ok {
			projectListField(root, "product_groups", splitFields(fields))
		}
	}
	limitList(data, "product_groups", limit)
	meta := map[string]any{}
	if len(params) > 0 {
		meta["query"] = params.Encode()
	}
	return data, meta, nil
}

func getProductGroup(ctx context) (any, map[string]any, error) {
	groupID, err := resolveProductGroupIDFromPayload(ctx)
	if err != nil {
		return nil, nil, err
	}
	path := "product_groups/" + groupID
	data, err := ctx.client.AMSRequest("GET", path, nil)
	return data, map[string]any{"resolved_path": path}, err
}

func updateProductGroup(ctx context) (any, map[string]any, error) {
	groupID, err := resolveProductGroupIDFromPayload(ctx)
	if err != nil {
		return nil, nil, err
	}
	updateFields, err := payload.Object(ctx.payload, "update_fields")
	if err != nil {
		return nil, nil, err
	}
	path := "product_groups/" + groupID
	data, err := ctx.client.AMSRequest("PATCH", path, productGroupUpdateFields(updateFields))
	return data, map[string]any{"resolved_path": path}, err
}

func deleteProductGroup(ctx context) (any, map[string]any, error) {
	groupID, err := resolveProductGroupIDFromPayload(ctx)
	if err != nil {
		return nil, nil, err
	}
	path := "product_groups/" + groupID
	data, err := ctx.client.AMSRequest("DELETE", path, nil)
	return data, map[string]any{"resolved_path": path}, err
}

func getPermissionList(ctx context) (any, map[string]any, error) {
	resource, err := resolvePermissionResource(ctx)
	if err != nil {
		return nil, nil, err
	}
	path := resource.resourceType + "/" + resource.resourceID + "/permissions"
	data, err := ctx.client.AMSRequest("GET", path, nil)
	if err != nil {
		return nil, nil, err
	}
	permissions := normalizePermissionItems(extractTopLevelList(data))
	if fields := splitFields(listFields(ctx.payload)); len(fields) > 0 {
		permissions = projectItems(permissions, fields)
	}
	limit := intValue(ctx.payload["limit"])
	if limit > 0 && len(permissions) > limit {
		permissions = permissions[:limit]
	}
	result := map[string]any{
		"permissions": permissions,
		"total":       len(permissions),
	}
	if limit > 0 {
		result["returned"] = len(permissions)
	}
	return result, permissionMeta(path, resource), nil
}

func updatePermission(ctx context) (any, map[string]any, error) {
	resource, err := resolvePermissionResource(ctx)
	if err != nil {
		return nil, nil, err
	}
	permissions, err := permissionPayload(ctx.payload, true)
	if err != nil {
		return nil, nil, err
	}
	path := resource.resourceType + "/" + resource.resourceID + "/permissions"
	data, err := ctx.client.AMSRequest("POST", path, permissions)
	return data, permissionMeta(path, resource), err
}

func deletePermission(ctx context) (any, map[string]any, error) {
	resource, err := resolvePermissionResource(ctx)
	if err != nil {
		return nil, nil, err
	}
	ids, err := requiredStringList(ctx.payload, "permission_ids", "permission_id", "ids")
	if err != nil {
		return nil, nil, err
	}
	path := resource.resourceType + "/" + resource.resourceID + "/permissions"
	data, err := ctx.client.AMSRequest("DELETE", path, ids)
	return data, permissionMeta(path, resource), err
}

func updatePermissionBatch(ctx context) (any, map[string]any, error) {
	resourceType, resourceIDs, err := resolvePermissionBatchResources(ctx)
	if err != nil {
		return nil, nil, err
	}
	permissions, err := permissionPayload(ctx.payload, false)
	if err != nil {
		return nil, nil, err
	}
	path := resourceType + "/permissions"
	body := map[string]any{
		"resource_ids":   resourceIDs,
		"perm_info_list": permissions,
	}
	data, err := ctx.client.AMSRequest("POST", path, body)
	meta := queryMeta(url.Values{}, path)
	meta["resolved_resource_type"] = resourceType
	meta["resolved_resource_ids"] = resourceIDs
	return data, meta, err
}

func getTradeList(ctx context) (any, map[string]any, error) {
	productID, err := productID(ctx.payload)
	if err != nil {
		return nil, nil, err
	}
	params := tradeQueryParams(ctx.payload, true)
	data, err := ctx.client.AMSRequestWithParams("GET", "products/"+productID+"/trades", nil, params)
	if err != nil {
		return nil, nil, err
	}
	limitList(data, "trades", intValue(ctx.payload["limit"]))
	return data, queryMeta(params, "products/"+productID+"/trades"), nil
}

func insertTrade(ctx context) (any, map[string]any, error) {
	productID, err := resolveProductID(ctx)
	if err != nil {
		return nil, nil, err
	}
	trades, err := objectList(ctx.payload, "trades")
	if err != nil {
		return nil, nil, err
	}
	chunkSize := boundedChunkSize(intValue(ctx.payload["chunk_size"]))
	chunks := chunkMaps(trades, chunkSize)
	path := "products/" + productID + "/trades:batch_insert"
	results := make([]any, 0)
	for _, chunk := range chunks {
		body := make([]map[string]any, 0, len(chunk))
		for _, trade := range chunk {
			copy := cloneMap(trade)
			copy["source"] = "open_api"
			body = append(body, copy)
		}
		data, err := ctx.client.AMSRequest("POST", path, body)
		if err != nil {
			return nil, nil, err
		}
		results = append(results, extractBatchResult(data)...)
	}
	return results, map[string]any{"resolved_path": path, "chunk_size": chunkSize, "chunks": len(chunks)}, nil
}

func insertSettlementTrade(ctx context) (any, map[string]any, error) {
	productID, err := resolveProductID(ctx)
	if err != nil {
		return nil, nil, err
	}
	accountName, err := payload.String(ctx.payload, "account_name")
	if err != nil {
		return nil, nil, err
	}
	files, err := uploadFiles(ctx.payload)
	if err != nil {
		return nil, nil, err
	}
	fields := map[string]string{"account": accountName}
	if assetUnitID := firstString(ctx.payload["asset_unit_id"], ctx.payload["asset_unit"]); assetUnitID != "" {
		fields["asset_unit_id"] = assetUnitID
	}
	path := "products/" + productID + ":upload_settlement_trade_file"
	result := map[string]any{}
	for _, file := range files {
		upload := file
		upload.FieldName = "file"
		data, err := ctx.client.AMSMultipartRequest("POST", path, fields, []client.UploadFile{upload})
		if err != nil {
			return nil, nil, err
		}
		taskID := firstStringFromMap(data, "task_id")
		if taskID == "" {
			result[upload.Path] = dataFromEnvelope(data)
			continue
		}
		finalData, err := pollTaskIDStatus(ctx, path, taskID)
		if err != nil {
			return nil, nil, err
		}
		result[upload.Path] = responseDataOrMessage(finalData)
	}
	return result, map[string]any{"resolved_path": path, "files": len(files)}, nil
}

func deleteTrade(ctx context) (any, map[string]any, error) {
	productID, err := resolveProductID(ctx)
	if err != nil {
		return nil, nil, err
	}
	if tradeIDs, ok := stringList(ctx.payload["trade_ids"]); ok {
		path := "products/" + productID + "/trades:batch_delete"
		data, err := ctx.client.AMSRequest("POST", path, tradeIDs)
		if err != nil {
			return nil, nil, err
		}
		return data, map[string]any{"resolved_path": path, "delete_mode": "trade_ids"}, nil
	}
	path := "products/" + productID + "/trades:batch_delete_by_date"
	params := tradeQueryParams(ctx.payload, false)
	data, err := ctx.client.AMSRequestWithParams("GET", path, nil, params)
	if err != nil {
		return nil, nil, err
	}
	return responseDataOrMessage(data), queryMeta(params, path), nil
}

func getBalance(ctx context) (any, map[string]any, error) {
	productLike, err := resolveProductLike(ctx)
	if err != nil {
		return nil, nil, err
	}
	params := queryParams(ctx.payload, []string{"date"})
	fieldsText := listFields(ctx.payload)
	if fieldsText != "" {
		params.Set("fields", fieldsText)
	}
	params.Set("flat_position", "true")
	path := productLike.resourceType + "/" + productLike.resourceID + "/balance"
	data, err := ctx.client.AMSRequestWithParams("GET", path, nil, params)
	if err != nil {
		return nil, nil, err
	}
	if fields := splitFields(fieldsText); len(fields) > 0 {
		if object, ok := data.(map[string]any); ok {
			data = pickFields(object, fields)
		}
	}
	return data, productLikeMeta(params, path, productLike), err
}

func getBalanceSeries(ctx context) (any, map[string]any, error) {
	productLike, err := resolveProductLike(ctx)
	if err != nil {
		return nil, nil, err
	}
	params := queryParams(ctx.payload, []string{"start_date", "end_date"})
	if text := listOrStringValue(ctx.payload["fields"]); text != "" {
		params.Set("position_fields", text)
	}
	params.Set("flat_position", "true")
	path := productLike.resourceType + "/" + productLike.resourceID + "/balance_series"
	data, err := ctx.client.AMSRequestWithParams("GET", path, nil, params)
	limitTopLevelList(&data, intValue(ctx.payload["limit"]))
	return data, productLikeMeta(params, path, productLike), err
}

func getAssetSnapshot(ctx context) (any, map[string]any, error) {
	productLike, err := resolveProductLike(ctx)
	if err != nil {
		return nil, nil, err
	}
	params := url.Values{}
	if text := listOrStringValue(ctx.payload["fields"]); text != "" {
		params.Set("fields", text+",positions")
	}
	if flatten, ok := ctx.payload["flatten_positions"].(bool); !ok || flatten {
		params.Set("flat_position", "true")
	} else if classifier := queryValue(ctx.payload["classifier"]); classifier != "" {
		params.Set("classifier", classifier)
	}
	path := productLike.resourceType + "/" + productLike.resourceID + "/asset_snapshot_summary"
	data, err := ctx.client.AMSRequestWithParams("GET", path, nil, params)
	return data, productLikeMeta(params, path, productLike), err
}

func recomputeBalance(ctx context) (any, map[string]any, error) {
	productLikes, err := resolveProductLikeList(ctx)
	if err != nil {
		return nil, nil, err
	}
	productIDs := make([]string, 0)
	groupIDs := make([]string, 0)
	for _, item := range productLikes {
		if item.resourceType == "products" {
			productIDs = append(productIDs, item.resourceID)
		} else if item.resourceType == "product_groups" {
			groupIDs = append(groupIDs, item.resourceID)
		}
	}
	result := map[string]any{"submit_recompute_count": 0}
	if len(productIDs) > 0 {
		body := map[string]any{"product_ids": productIDs}
		copyDateField(body, ctx.payload, "start_date")
		data, err := ctx.client.AMSRequest("POST", "products:recompute", body)
		if err != nil {
			return nil, nil, err
		}
		result["products"] = data
		result["submit_recompute_count"] = intValueFromAny(result["submit_recompute_count"]) + intValueFromAny(firstExisting(data, "submit_recompute_count"))
	}
	if len(groupIDs) > 0 {
		body := map[string]any{"product_group_ids": groupIDs}
		copyDateField(body, ctx.payload, "start_date")
		data, err := ctx.client.AMSRequest("POST", "product_groups:recompute", body)
		if err != nil {
			return nil, nil, err
		}
		result["product_groups"] = data
		result["submit_recompute_count"] = intValueFromAny(result["submit_recompute_count"]) + intValueFromAny(firstExisting(data, "submit_recompute_count"))
	}
	return result, map[string]any{"resolved_product_ids": productIDs, "resolved_product_group_ids": groupIDs}, nil
}

func getReconciliationList(ctx context) (any, map[string]any, error) {
	productIDs, err := resolveProductIDs(ctx)
	if err != nil {
		return nil, nil, err
	}
	startDate, err := payload.String(ctx.payload, "start_date")
	if err != nil {
		return nil, nil, err
	}
	endDate, err := payload.String(ctx.payload, "end_date")
	if err != nil {
		return nil, nil, err
	}
	body := map[string]any{
		"product_ids": productIDs,
		"start_date":  startDate,
		"end_date":    endDate,
	}
	path := "products:batch_get_reconciliation_list"
	data, err := ctx.client.AMSRequest("POST", path, body)
	if err != nil {
		return nil, nil, err
	}
	limitTopLevelList(&data, intValue(ctx.payload["limit"]))
	return data, map[string]any{"resolved_path": path, "resolved_product_ids": productIDs}, nil
}

func getReconciliationDiff(ctx context) (any, map[string]any, error) {
	productID, err := resolveProductID(ctx)
	if err != nil {
		return nil, nil, err
	}
	body, err := reconciliationDateBody(ctx.payload)
	if err != nil {
		return nil, nil, err
	}
	fields, err := requiredStringList(ctx.payload, "fields")
	if err != nil {
		return nil, nil, err
	}
	body["fields"] = fields
	path := "products/" + productID + ":get_reconciliation_diff"
	data, err := ctx.client.AMSRequest("POST", path, body)
	return data, map[string]any{"resolved_path": path, "resolved_product_id": productID}, err
}

func updateReconciliation(ctx context) (any, map[string]any, error) {
	productID, err := resolveProductID(ctx)
	if err != nil {
		return nil, nil, err
	}
	body, err := reconciliationDateBody(ctx.payload)
	if err != nil {
		return nil, nil, err
	}
	action := strings.ToLower(stringDefault(ctx.payload["action"], "mark"))
	switch action {
	case "mark", "manual", "status":
	case "auto", "auto_reconciliation":
		return submitReconciliationDateActionForProduct(ctx, productID, "auto_reconciliation")
	case "undo_auto", "undo", "undo_auto_reconciliation":
		return submitReconciliationDateActionForProduct(ctx, productID, "undo_auto_reconciliation")
	default:
		return nil, nil, fmt.Errorf("payload field %q must be one of mark, auto, undo_auto", "action")
	}
	for _, field := range []string{"done", "description"} {
		if value, ok := ctx.payload[field]; ok {
			body[field] = value
		}
	}
	path := "products/" + productID + ":reconciliation"
	data, err := ctx.client.AMSRequest("POST", path, body)
	return data, map[string]any{"resolved_path": path, "resolved_product_id": productID, "resolved_action": "mark"}, err
}

func getReconciliationAssetUnitDiff(ctx context) (any, map[string]any, error) {
	productID, assetUnitID, err := productAndAssetUnitID(ctx)
	if err != nil {
		return nil, nil, err
	}
	body, err := reconciliationDateBody(ctx.payload)
	if err != nil {
		return nil, nil, err
	}
	path := "products/" + productID + "/asset_units/" + assetUnitID + ":get_reconciliation_diff"
	data, err := ctx.client.AMSRequest("POST", path, body)
	return data, map[string]any{"resolved_path": path, "resolved_product_id": productID, "resolved_asset_unit_id": assetUnitID}, err
}

func getReconciliationPositionStatement(ctx context) (any, map[string]any, error) {
	productID, assetUnitID, err := productAndAssetUnitID(ctx)
	if err != nil {
		return nil, nil, err
	}
	params := queryParams(ctx.payload, []string{"date"})
	path := "products/" + productID + "/asset_units/" + assetUnitID + ":reconciliation_positions_statement"
	data, err := ctx.client.AMSRequestWithParams("GET", path, nil, params)
	meta := queryMeta(params, path)
	meta["resolved_product_id"] = productID
	meta["resolved_asset_unit_id"] = assetUnitID
	return data, meta, err
}

func getPositionStatementLatestList(ctx context) (any, map[string]any, error) {
	path := "products/asset_units/positions_statement:get_latest"
	data, err := ctx.client.AMSRequest("GET", path, nil)
	if err != nil {
		return nil, nil, err
	}
	limitTopLevelList(&data, intValue(ctx.payload["limit"]))
	return data, map[string]any{"resolved_path": path}, nil
}

func getPositionStatement(ctx context) (any, map[string]any, error) {
	productID, assetUnitID, err := productAndAssetUnitID(ctx)
	if err != nil {
		return nil, nil, err
	}
	params := queryParams(ctx.payload, []string{"start_date", "end_date"})
	params.Set("is_detail", "true")
	path := "products/" + productID + "/asset_units/" + assetUnitID + "/positions_statement"
	data, err := ctx.client.AMSRequestWithParams("GET", path, nil, params)
	limitTopLevelList(&data, intValue(ctx.payload["limit"]))
	return data, queryMeta(params, path), err
}

func insertPositionStatement(ctx context) (any, map[string]any, error) {
	productID, assetUnitID, err := productAndAssetUnitID(ctx)
	if err != nil {
		return nil, nil, err
	}
	files, err := uploadFiles(ctx.payload)
	if err != nil {
		return nil, nil, err
	}
	fields := map[string]string{"broker": stringDefault(ctx.payload["broker"], "ricequant")}
	path := "products/" + productID + "/asset_units/" + assetUnitID + ":upload_positions_statement"
	result := map[string]any{}
	for _, file := range files {
		upload := file
		upload.FieldName = "file"
		data, err := ctx.client.AMSMultipartRequest("POST", path, fields, []client.UploadFile{upload})
		if err != nil {
			return nil, nil, err
		}
		if taskID := firstStringFromMap(data, "task_id"); taskID != "" {
			finalData, err := pollTaskIDStatus(ctx, path, taskID)
			if err != nil {
				return nil, nil, err
			}
			result[upload.Path] = responseDataOrMessage(finalData)
			continue
		}
		result[upload.Path] = dataFromEnvelope(data)
	}
	return result, map[string]any{"resolved_path": path, "files": len(files)}, nil
}

func deletePositionStatement(ctx context) (any, map[string]any, error) {
	productID, assetUnitID, err := productAndAssetUnitID(ctx)
	if err != nil {
		return nil, nil, err
	}
	ids, err := requiredStringList(ctx.payload, "positions_statement_ids", "position_statement_ids")
	if err != nil {
		return nil, nil, err
	}
	path := "products/" + productID + "/asset_units/" + assetUnitID + "/positions_statement"
	data, err := ctx.client.AMSRequest("DELETE", path, ids)
	return data, map[string]any{"resolved_path": path}, err
}

func getValuationReportList(ctx context) (any, map[string]any, error) {
	productID, err := resolveProductID(ctx)
	if err != nil {
		return nil, nil, err
	}
	params := queryParams(ctx.payload, []string{"start_date", "end_date"})
	path := "products/" + productID + "/valuation_reports"
	data, err := ctx.client.AMSRequestWithParams("GET", path, nil, params)
	if err != nil {
		return nil, nil, err
	}
	if items, ok := data.([]any); ok {
		data = map[string]any{"valuation_reports": items, "total": len(items)}
	}
	projectAndLimitList(data, "valuation_reports", ctx.payload)
	return data, queryMeta(params, path), nil
}

func insertValuationReport(ctx context) (any, map[string]any, error) {
	productID, err := resolveProductID(ctx)
	if err != nil {
		return nil, nil, err
	}
	path := "products/" + productID + "/valuation_reports"
	results := make([]any, 0)
	if reports, ok := optionalObjectList(ctx.payload, "valuation_reports"); ok {
		for _, report := range reports {
			body := map[string]any{"source": "open_api", "valuation_report": report}
			if replaceDates, ok := stringList(ctx.payload["replace_dates"]); ok {
				body["replace_dates"] = replaceDates
			}
			data, err := ctx.client.AMSRequest("POST", path, body)
			if err != nil {
				return nil, nil, err
			}
			results = append(results, data)
		}
		return results, map[string]any{"resolved_path": path, "reports": len(reports)}, nil
	}
	files, err := valuationReportFiles(ctx.payload)
	if err != nil {
		return nil, nil, err
	}
	fields := map[string]string{"source": "open_api"}
	if replaceDates := listOrStringValue(ctx.payload["replace_dates"]); replaceDates != "" {
		fields["replace_dates"] = replaceDates
	}
	for _, file := range files {
		upload := file
		upload.FieldName = "file"
		data, err := ctx.client.AMSMultipartRequest("POST", path, fields, []client.UploadFile{upload})
		if err != nil {
			results = append(results, map[string]any{"file": upload.Path, "err_msg": err.Error()})
			continue
		}
		results = append(results, map[string]any{"file": upload.Path, "result": data})
	}
	return results, map[string]any{"resolved_path": path, "files": len(files)}, nil
}

func deleteValuationReport(ctx context) (any, map[string]any, error) {
	productID, err := resolveProductID(ctx)
	if err != nil {
		return nil, nil, err
	}
	dates, err := requiredStringList(ctx.payload, "dates", "deleted_dates")
	if err != nil {
		return nil, nil, err
	}
	path := "products/" + productID + "/valuation_reports:batch_delete"
	data, err := ctx.client.AMSRequest("POST", path, map[string]any{"dates": dates})
	return data, map[string]any{"resolved_path": path}, err
}

func getValuationReportFile(ctx context) (any, map[string]any, error) {
	productID, err := resolveProductID(ctx)
	if err != nil {
		return nil, nil, err
	}
	savePath, err := payload.String(ctx.payload, "save_path")
	if err != nil {
		return nil, nil, err
	}
	if reportID := firstString(ctx.payload["valuation_report_id"], ctx.payload["report_id"]); reportID != "" {
		fileName := stringDefault(ctx.payload["file_name"], reportID+".xlsx")
		destination := downloadDestination(savePath, fileName)
		path := "products/" + productID + "/valuation_reports/" + reportID + "/src_file"
		result, err := ctx.client.AMSDownloadToFile(path, nil, destination)
		return downloadResultMap(result), map[string]any{"resolved_path": path}, err
	}
	listData, _, err := getValuationReportList(ctx)
	if err != nil {
		return nil, nil, err
	}
	return downloadValuationReportFiles(ctx, productID, savePath, listData)
}

func listProductEvents(collection string, listField string) handler {
	return func(ctx context) (any, map[string]any, error) {
		productID, err := resolveProductID(ctx)
		if err != nil {
			return nil, nil, err
		}
		queryFields := []string{"start_date", "end_date"}
		if collection == "custodian_events" {
			queryFields = append(queryFields, "custodian_event_type", "adjust_target")
		}
		params := queryParams(ctx.payload, queryFields)
		if collection == "custodian_events" {
			params.Set("limit", "1073741824")
		}
		path := "products/" + productID + "/" + collection
		data, err := ctx.client.AMSRequestWithParams("GET", path, nil, params)
		if err != nil {
			return nil, nil, err
		}
		if collection == "unit_events" && !boolValue(ctx.payload["include_auto_units"]) {
			filterAutoUnitEvents(data)
		}
		projectAndLimitList(data, listField, ctx.payload)
		return data, queryMeta(params, path), nil
	}
}

func insertProductEvents(collection string, payloadField string) handler {
	return func(ctx context) (any, map[string]any, error) {
		productID, err := resolveProductID(ctx)
		if err != nil {
			return nil, nil, err
		}
		events, err := eventPayloadList(ctx.payload, payloadField)
		if err != nil {
			return nil, nil, err
		}
		body := make([]map[string]any, 0, len(events))
		for _, event := range events {
			copy := cloneMap(event)
			copy["product_id"] = productID
			body = append(body, copy)
		}
		path := "products/" + productID + "/" + collection + ":batch_insert"
		data, err := ctx.client.AMSRequest("POST", path, body)
		return data, map[string]any{"resolved_path": path}, err
	}
}

func updateProductEvent(collection string) handler {
	return func(ctx context) (any, map[string]any, error) {
		productID, err := resolveProductID(ctx)
		if err != nil {
			return nil, nil, err
		}
		event, err := eventPayloadObject(ctx.payload, collection)
		if err != nil {
			return nil, nil, err
		}
		body := cloneMap(event)
		eventID := firstString(body["id"], ctx.payload["event_id"])
		if eventID == "" {
			return nil, nil, fmt.Errorf("payload event requires id")
		}
		if collection == "unit_events" {
			delete(body, "id")
		}
		path := "products/" + productID + "/" + collection + "/" + eventID
		data, err := ctx.client.AMSRequest("PUT", path, body)
		return data, map[string]any{"resolved_path": path}, err
	}
}

func deleteProductEvents(collection string) handler {
	return func(ctx context) (any, map[string]any, error) {
		productID, err := resolveProductID(ctx)
		if err != nil {
			return nil, nil, err
		}
		ids, err := requiredStringList(ctx.payload, "event_ids", "event_id", "ids")
		if err != nil {
			return nil, nil, err
		}
		path := "products/" + productID + "/" + collection + ":batch_delete"
		data, err := ctx.client.AMSRequest("POST", path, ids)
		return data, map[string]any{"resolved_path": path}, err
	}
}

func insertCustomizedInstrument(ctx context) (any, map[string]any, error) {
	body, err := payload.Object(ctx.payload, "customized_instrument")
	if err != nil {
		return nil, nil, err
	}
	data, err := ctx.client.AMSRequest("POST", "customized_instruments", body)
	return data, map[string]any{"resolved_path": "customized_instruments"}, err
}

func getCustomizedInstrumentList(ctx context) (any, map[string]any, error) {
	data, err := ctx.client.AMSRequest("GET", "customized_instruments", nil)
	if err != nil {
		return nil, nil, err
	}
	projectAndLimitList(data, "customized_instruments", ctx.payload)
	return data, map[string]any{"resolved_path": "customized_instruments"}, nil
}

func getCustomizedInstrumentPrice(ctx context) (any, map[string]any, error) {
	id, err := payload.String(ctx.payload, "customized_ins_id")
	if err != nil {
		return nil, nil, err
	}
	path := "customized_instruments/" + id
	data, err := ctx.client.AMSRequest("GET", path, nil)
	if root, ok := data.(map[string]any); ok {
		if fairValues, ok := root["fair_values"]; ok && !boolValue(ctx.payload["raw"]) {
			data = fairValues
		}
	}
	limitTopLevelList(&data, intValue(ctx.payload["limit"]))
	return data, map[string]any{"resolved_path": path}, err
}

func insertCustomizedInstrumentPrice(ctx context) (any, map[string]any, error) {
	id, err := payload.String(ctx.payload, "customized_ins_id")
	if err != nil {
		return nil, nil, err
	}
	files, err := uploadFiles(ctx.payload)
	if err != nil {
		return nil, nil, err
	}
	path := "customized_instruments/" + id + ":upload_fair_price_file"
	results := map[string]any{}
	for _, file := range files {
		upload := file
		upload.FieldName = "file"
		data, err := ctx.client.AMSMultipartRequest("POST", path, nil, []client.UploadFile{upload})
		if err != nil {
			return nil, nil, err
		}
		results[upload.Path] = data
	}
	return results, map[string]any{"resolved_path": path, "files": len(files)}, nil
}

func deleteCustomizedInstrument(ctx context) (any, map[string]any, error) {
	ids, err := requiredStringList(ctx.payload, "customized_ins_ids", "customized_ins_id", "ids")
	if err != nil {
		return nil, nil, err
	}
	data, err := ctx.client.AMSRequest("POST", "customized_instruments:batch_delete", ids)
	return data, map[string]any{"resolved_path": "customized_instruments:batch_delete"}, err
}

func getCustomizedBenchmarkList(ctx context) (any, map[string]any, error) {
	data, err := ctx.client.AMSRequest("GET", "customized_benchmarks", nil)
	if err != nil {
		return nil, nil, err
	}
	limitTopLevelList(&data, intValue(ctx.payload["limit"]))
	if fields := splitFields(listFields(ctx.payload)); len(fields) > 0 {
		if items, ok := data.([]any); ok {
			data = projectItems(items, fields)
		}
	}
	return data, map[string]any{"resolved_path": "customized_benchmarks"}, nil
}

func getCustomizedBenchmark(ctx context) (any, map[string]any, error) {
	id, err := payload.String(ctx.payload, "customized_benchmark_id")
	if err != nil {
		return nil, nil, err
	}
	path := "customized_benchmarks/" + id
	data, err := ctx.client.AMSRequest("GET", path, nil)
	return data, map[string]any{"resolved_path": path}, err
}

func insertCustomizedBenchmark(ctx context) (any, map[string]any, error) {
	body, err := payload.Object(ctx.payload, "customized_benchmark")
	if err != nil {
		return nil, nil, err
	}
	data, err := ctx.client.AMSRequest("POST", "customized_benchmarks", body)
	if err != nil {
		return nil, nil, err
	}
	if id := firstStringFromMap(data, "inserted_id", "id"); id != "" && !boolValue(ctx.payload["raw"]) {
		path := "customized_benchmarks/" + id
		created, err := ctx.client.AMSRequest("GET", path, nil)
		return created, map[string]any{"resolved_path": path, "insert_response": data}, err
	}
	return data, map[string]any{"resolved_path": "customized_benchmarks"}, nil
}

func updateCustomizedBenchmark(ctx context) (any, map[string]any, error) {
	id, err := payload.String(ctx.payload, "customized_benchmark_id")
	if err != nil {
		return nil, nil, err
	}
	body, err := payload.Object(ctx.payload, "customized_benchmark")
	if err != nil {
		return nil, nil, err
	}
	path := "customized_benchmarks/" + id
	data, err := ctx.client.AMSRequest("PUT", path, body)
	if err != nil || boolValue(ctx.payload["raw"]) {
		return data, map[string]any{"resolved_path": path}, err
	}
	updated, err := ctx.client.AMSRequest("GET", path, nil)
	return map[string]any{"update": data, "benchmark": updated}, map[string]any{"resolved_path": path}, err
}

func deleteCustomizedBenchmark(ctx context) (any, map[string]any, error) {
	id, err := payload.String(ctx.payload, "customized_benchmark_id")
	if err != nil {
		return nil, nil, err
	}
	path := "customized_benchmarks/" + id
	data, err := ctx.client.AMSRequest("DELETE", path, nil)
	if err != nil {
		return nil, nil, err
	}
	root, ok := data.(map[string]any)
	if ok {
		if deleted, ok := root["deleted"].(bool); ok {
			return map[string]any{"effect_count": boolToInt(deleted), "raw": data}, map[string]any{"resolved_path": path}, nil
		}
	}
	return data, map[string]any{"resolved_path": path}, nil
}

func getWeeklyNetValueReport(ctx context) (any, map[string]any, error) {
	productLike, err := resolveProductLike(ctx)
	if err != nil {
		return nil, nil, err
	}
	savePath, err := payload.String(ctx.payload, "save_path")
	if err != nil {
		return nil, nil, err
	}
	params := queryParams(ctx.payload, []string{"start_date", "end_date"})
	addNestedParams(params, ctx.payload)
	fileName := stringDefault(ctx.payload["file_name"], productLike.resourceID+"_weekly_net_value_report.xlsx")
	destination := downloadDestination(savePath, fileName)
	path := productLike.resourceType + "/" + productLike.resourceID + "/weekly_net_value_report"
	result, err := ctx.client.AMSDownloadToFile(path, params, destination)
	return downloadResultMap(result), productLikeMeta(params, path, productLike), err
}

func downloadValuationReportFiles(ctx context, productID string, savePath string, listData any) (any, map[string]any, error) {
	items := extractList(listData, "valuation_reports")
	results := map[string]any{"successful": []any{}, "failed": []any{}}
	for _, item := range items {
		report, ok := item.(map[string]any)
		if !ok {
			continue
		}
		reportID := firstString(report["valuation_report_id"], report["id"])
		fileName := firstString(report["file_name"], report["name"])
		if reportID == "" || fileName == "" {
			results["failed"] = appendAny(results["failed"], map[string]any{"item": report, "reason": "missing valuation_report_id or file_name"})
			continue
		}
		path := "products/" + productID + "/valuation_reports/" + reportID + "/src_file"
		result, err := ctx.client.AMSDownloadToFile(path, nil, downloadDestination(savePath, fileName))
		if err != nil {
			results["failed"] = appendAny(results["failed"], map[string]any{"file_name": fileName, "reason": err.Error()})
			continue
		}
		if result.Path == "" {
			results["failed"] = appendAny(results["failed"], map[string]any{"file_name": fileName, "reason": "response did not contain file content", "response": result.Data})
			continue
		}
		results["successful"] = appendAny(results["successful"], fileName)
	}
	return results, map[string]any{"resolved_path": "products/" + productID + "/valuation_reports/*/src_file"}, nil
}

func getIndicator(ctx context) (any, map[string]any, error) {
	productLike, err := resolveProductLike(ctx)
	if err != nil {
		return nil, nil, err
	}
	params := queryParams(ctx.payload, []string{"start_date", "end_date", "fields"})
	addExtraParams(params, ctx.payload)
	path := productLike.resourceType + "/" + productLike.resourceID + "/indicators"
	data, err := ctx.client.AMSRequestWithParams("GET", path, nil, params)
	return data, productLikeMeta(params, path, productLike), err
}

func getIndicatorSeries(ctx context) (any, map[string]any, error) {
	productLike, err := resolveProductLike(ctx)
	if err != nil {
		return nil, nil, err
	}
	params := queryParams(ctx.payload, []string{"start_date", "end_date", "indicators"})
	addExtraParams(params, ctx.payload)
	path := productLike.resourceType + "/" + productLike.resourceID + "/indicators_series"
	data, err := ctx.client.AMSRequestWithParams("GET", path, nil, params)
	return data, productLikeMeta(params, path, productLike), err
}

func getCustomizedIndicator(ctx context) (any, map[string]any, error) {
	return customizedIndicatorRequest(ctx, "GET", nil)
}

func insertCustomizedIndicator(ctx context) (any, map[string]any, error) {
	body, err := payload.Object(ctx.payload, "customized_indicators")
	if err != nil {
		return nil, nil, err
	}
	return customizedIndicatorRequest(ctx, "POST", body)
}

func updateCustomizedIndicator(ctx context) (any, map[string]any, error) {
	body, err := payload.Object(ctx.payload, "customized_indicators")
	if err != nil {
		return nil, nil, err
	}
	return customizedIndicatorRequest(ctx, "PATCH", body)
}

func deleteCustomizedIndicator(ctx context) (any, map[string]any, error) {
	return customizedIndicatorRequest(ctx, "DELETE", nil)
}

func customizedIndicatorRequest(ctx context, method string, body any) (any, map[string]any, error) {
	productLike, err := resolveProductLike(ctx)
	if err != nil {
		return nil, nil, err
	}
	path := productLike.resourceType + "/" + productLike.resourceID + "/customized_indicators"
	data, err := ctx.client.AMSRequest(method, path, body)
	return data, productLikeMeta(nil, path, productLike), err
}

func getInvestmentOverviewSummaryIndicator(ctx context) (any, map[string]any, error) {
	return requestInvestmentOverview(ctx, "indicators", false, false)
}

func investmentOverviewHandler(endpoint string, usePost bool, requireBenchmark bool) handler {
	return func(ctx context) (any, map[string]any, error) {
		return requestInvestmentOverview(ctx, endpoint, usePost, requireBenchmark)
	}
}

func requestInvestmentOverview(ctx context, endpoint string, usePost bool, requireBenchmark bool) (any, map[string]any, error) {
	body, err := investmentOverviewPayload(ctx, requireBenchmark)
	if err != nil {
		return nil, nil, err
	}
	path := "product_group_overview/" + endpoint + "_v2"
	if usePost {
		data, err := ctx.client.AMSRequest("POST", path, body)
		if err != nil {
			return responseDataOrMessage(data), map[string]any{"resolved_path": path}, err
		}
		result, err := responseDataAfterOptionalTask(ctx, path, data)
		return result, map[string]any{"resolved_path": path}, err
	}
	params := mapToValues(body)
	data, err := ctx.client.AMSRequestWithParams("GET", path, nil, params)
	if err != nil {
		return responseDataOrMessage(data), queryMeta(params, path), err
	}
	result, err := responseDataAfterOptionalTask(ctx, path, data)
	return result, queryMeta(params, path), err
}

func getPerformanceAttribution(ctx context) (any, map[string]any, error) {
	return requestPerformanceAttribution(ctx, false)
}

func getReturnsDecomposition(ctx context) (any, map[string]any, error) {
	return requestPerformanceAttribution(ctx, true)
}

func requestPerformanceAttribution(ctx context, onlyReturnsDecomposition bool) (any, map[string]any, error) {
	productLike, err := resolveProductLike(ctx)
	if err != nil {
		return nil, nil, err
	}
	startDate, err := payload.String(ctx.payload, "start_date")
	if err != nil {
		return nil, nil, err
	}
	endDate, err := payload.String(ctx.payload, "end_date")
	if err != nil {
		return nil, nil, err
	}
	body := map[string]any{
		"start_date":                 startDate,
		"end_date":                   endDate,
		"benchmark":                  benchmarkValue(ctx.payload),
		"template":                   stringDefault(ctx.payload["template"], "equity/brinson"),
		"industry_standard":          stringDefault(ctx.payload["industry_standard"], "sws"),
		"drilldown":                  boolValue(ctx.payload["drilldown"]),
		"only_returns_decomposition": onlyReturnsDecomposition || boolValue(ctx.payload["only_returns_decomposition"]),
	}
	path := productLike.resourceType + "/" + productLike.resourceID + "/performance_attributions"
	data, err := ctx.client.AMSRequest("POST", path, body)
	if err != nil {
		return nil, nil, err
	}
	taskID := firstStringFromMap(data, "id")
	if taskID == "" {
		return data, productLikeMeta(nil, path, productLike), nil
	}
	result, err := pollPerformanceAttribution(ctx, productLike, taskID)
	return result, map[string]any{"resolved_path": path, "resolved_task_id": taskID, "resolved_resource_type": productLike.resourceType, "resolved_resource_id": productLike.resourceID}, err
}

func getTradingAnalysisList(ctx context) (any, map[string]any, error) {
	productLike, err := resolveProductLike(ctx)
	if err != nil {
		return nil, nil, err
	}
	params := queryParams(ctx.payload, []string{"start_date", "end_date"})
	addNestedParams(params, ctx.payload)
	path := productLike.resourceType + "/" + productLike.resourceID + "/trading_analysis_list"
	data, err := ctx.client.AMSRequestWithParams("GET", path, nil, params)
	if err != nil {
		return responseDataOrMessage(data), productLikeMeta(params, path, productLike), err
	}
	result, err := responseDataAfterOptionalTask(ctx, path, data)
	return result, productLikeMeta(params, path, productLike), err
}

func getTradingAnalysis(ctx context) (any, map[string]any, error) {
	productLike, err := resolveProductLike(ctx)
	if err != nil {
		return nil, nil, err
	}
	params := queryParams(ctx.payload, []string{"start_date", "end_date", "order_book_id", "asset_class", "direction"})
	addNestedParams(params, ctx.payload)
	path := productLike.resourceType + "/" + productLike.resourceID + "/single_trading_analysis"
	data, err := ctx.client.AMSRequestWithParams("GET", path, nil, params)
	if err != nil {
		return responseDataOrMessage(data), productLikeMeta(params, path, productLike), err
	}
	result, err := responseDataAfterOptionalTask(ctx, path, data)
	return result, productLikeMeta(params, path, productLike), err
}

func getPaperTradingList(ctx context) (any, map[string]any, error) {
	items, err := listUnifiedPaperTrading(ctx)
	if err != nil {
		return nil, nil, err
	}
	items = projectItems(items, splitFields(listFields(ctx.payload)))
	if limit := intValue(ctx.payload["limit"]); limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil, nil
}

func getPaperTrading(ctx context) (any, map[string]any, error) {
	info, err := resolvePaperTrading(ctx)
	if err != nil {
		return nil, nil, err
	}
	return info.config, map[string]any{"resolved_product_id": info.productID}, nil
}

func insertPaperTrading(ctx context) (any, map[string]any, error) {
	route, err := paperTradingCreateRoute(ctx.payload)
	if err != nil {
		return nil, nil, err
	}
	if route.version == "v1" {
		productIDs, err := paperTradingProductIDs(ctx)
		if err != nil {
			return nil, nil, err
		}
		body := paperTradingConfigPayload(ctx.payload)
		body["product_ids"] = productIDs
		body = normalizeV1ExclusivePaperTradingFields(body)
		data, err := ctx.client.AMSRequest("POST", route.path, body)
		return normalizePaperTradingWriteResult(data), nil, err
	}
	fields := paperTradingV2FormFields(ctx.payload, route.template)
	if hasUploadFiles(ctx.payload) {
		files, err := uploadFiles(ctx.payload)
		if err != nil {
			return nil, nil, err
		}
		data, err := ctx.client.AMSMultipartRequest("POST", route.path, fields, files)
		return normalizePaperTradingWriteResult(dataFromEnvelope(data)), nil, err
	}
	data, err := ctx.client.AMSFormRequest("POST", route.path, fields)
	return normalizePaperTradingWriteResult(dataFromEnvelope(data)), nil, err
}

func getUnifiedPaperTradingSignalList(ctx context) (any, map[string]any, error) {
	info, err := resolvePaperTrading(ctx)
	if err != nil {
		return nil, nil, err
	}
	params := queryParams(ctx.payload, []string{"start_date", "end_date"})
	if info.version == "v2" {
		if text := listOrStringValue(ctx.payload["fields"]); text != "" {
			params.Set("fields", text)
		}
	}
	path := paperTradingSignalListPath(info)
	data, err := ctx.client.AMSRequestWithParams("GET", path, nil, params)
	if err != nil {
		return nil, nil, err
	}
	data = normalizePaperTradingSignalList(data, intValue(ctx.payload["limit"]))
	return data, paperTradingMeta(params, path, info), nil
}

func getUnifiedPaperTradingSignal(ctx context) (any, map[string]any, error) {
	info, signalID, err := resolvePaperTradingAndSignal(ctx)
	if err != nil {
		return nil, nil, err
	}
	params := url.Values{}
	if info.version == "v2" {
		if text := listOrStringValue(ctx.payload["fields"]); text != "" {
			params.Set("fields", text)
		}
	}
	path := paperTradingSignalPath(info, signalID)
	data, err := ctx.client.AMSRequestWithParams("GET", path, nil, params)
	return normalizePaperTradingSignalData(data), paperTradingMeta(params, path, info), err
}

func insertPaperTradingSignal(ctx context) (any, map[string]any, error) {
	info, err := resolvePaperTrading(ctx)
	if err != nil {
		return nil, nil, err
	}
	files, err := uploadFiles(ctx.payload)
	if err != nil {
		return nil, nil, err
	}
	if info.version == "v1" {
		if strings.TrimSpace(info.channelID) == "" {
			return nil, nil, fmt.Errorf("paper trading channel id missing for product %q", info.productID)
		}
		path := "products/" + info.productID + "/paper_trading_channels/" + info.channelID + "/batch_paper_trading_file"
		data, err := ctx.client.AMSMultipartRequest("POST", path, nil, files)
		return dataFromEnvelope(data), map[string]any{"resolved_product_id": info.productID}, err
	}
	sessionData, err := ctx.client.AMSRequest("POST", "file_upload_sessions", nil)
	if err != nil {
		return nil, nil, err
	}
	sessionID := fileSessionID(sessionData)
	if sessionID == "" {
		return nil, nil, fmt.Errorf("file upload session response missing file_session_id")
	}
	filesPath := "file_upload_sessions/" + sessionID + "/files"
	if _, err := ctx.client.AMSMultipartRequest("POST", filesPath, nil, files); err != nil {
		return nil, nil, err
	}
	path := "products/" + info.productID + "/paper_trading_v2/signals:batch_upload"
	data, err := ctx.client.AMSFormRequest("POST", path, map[string]string{"file_session_id": sessionID})
	return dataFromEnvelope(data), map[string]any{"file_session_id": sessionID, "resolved_product_id": info.productID}, err
}

func updatePaperTrading(ctx context) (any, map[string]any, error) {
	info, err := resolvePaperTrading(ctx)
	if err != nil {
		return nil, nil, err
	}
	updateFields, err := payload.Object(ctx.payload, "update_fields")
	if err != nil {
		return nil, nil, err
	}
	if info.version == "v1" {
		body := mergedV1PaperTradingConfig(info.config, updateFields)
		body["product_ids"] = []string{info.productID}
		path := "products/paper_trading_channels:batch_upsert"
		data, err := ctx.client.AMSRequest("POST", path, body)
		return data, map[string]any{"resolved_product_id": info.productID}, err
	}
	body := mergedV2PaperTradingConfig(info.config, updateFields)
	path := "products/" + info.productID + "/paper_trading_v2"
	data, err := ctx.client.AMSRequest("PATCH", path, body)
	return data, map[string]any{"resolved_product_id": info.productID}, err
}

func deletePaperTrading(ctx context) (any, map[string]any, error) {
	info, err := resolvePaperTrading(ctx)
	if err != nil {
		return nil, nil, err
	}
	path := "products/" + info.productID + "/paper_trading_v2"
	if info.version == "v1" {
		if strings.TrimSpace(info.channelID) == "" {
			return nil, nil, fmt.Errorf("paper trading channel id missing for product %q", info.productID)
		}
		path = "products/" + info.productID + "/paper_trading_channels/" + info.channelID + ":async_delete"
	}
	data, err := ctx.client.AMSRequest("DELETE", path, nil)
	return dataFromEnvelope(data), map[string]any{"resolved_product_id": info.productID}, err
}

func deletePaperTradingSignal(ctx context) (any, map[string]any, error) {
	info, err := resolvePaperTrading(ctx)
	if err != nil {
		return nil, nil, err
	}
	body := map[string]any{}
	if info.version == "v2" {
		if ids, ok := stringList(ctx.payload["signal_ids"]); ok {
			body["signal_ids"] = ids
		}
	}
	if _, ok := body["signal_ids"]; !ok {
		copyDateField(body, ctx.payload, "start_date")
		copyDateField(body, ctx.payload, "end_date")
	}
	path := "products/" + info.productID + "/paper_trading_channels/paper_trading_signals"
	if info.version == "v2" {
		path = "products/" + info.productID + "/paper_trading_v2/signals:async_delete"
	}
	data, err := ctx.client.AMSRequest("DELETE", path, body)
	return dataFromEnvelope(data), map[string]any{"resolved_product_id": info.productID}, err
}

func recomputePaperTrading(ctx context) (any, map[string]any, error) {
	info, err := resolvePaperTrading(ctx)
	if err != nil {
		return nil, nil, err
	}
	body := map[string]any{}
	copyDateField(body, ctx.payload, "date")
	path := "products/" + info.productID + "/paper_trading_v2:recompute"
	if info.version == "v1" {
		if strings.TrimSpace(info.channelID) == "" {
			return nil, nil, fmt.Errorf("paper trading channel id missing for product %q", info.productID)
		}
		path = "products/" + info.productID + "/paper_trading_channels/" + info.channelID + ":async_recompute"
	}
	data, err := ctx.client.AMSRequest("POST", path, body)
	return dataFromEnvelope(data), map[string]any{"resolved_product_id": info.productID}, err
}

func listUnifiedPaperTrading(ctx context) ([]any, error) {
	channels, err := ctx.client.AMSRequest("GET", "products:list_paper_trading_channels", nil)
	if err != nil {
		return nil, err
	}
	v2List, err := ctx.client.AMSRequest("GET", "products:list_paper_trading_v2", nil)
	if err != nil {
		return nil, err
	}
	items := make([]any, 0)
	for _, item := range extractList(channels, "channels") {
		config, ok := item.(map[string]any)
		if !ok {
			continue
		}
		copy := publicPaperTradingConfig(config)
		copy["strategy_model"] = stringDefault(copy["strategy_model"], "general")
		items = append(items, copy)
	}
	for _, item := range extractList(v2List, "items") {
		config, ok := item.(map[string]any)
		if !ok {
			continue
		}
		copy := publicPaperTradingConfig(config)
		copy["strategy_model"] = stringDefault(copy["strategy_model"], "equity_long")
		items = append(items, copy)
	}
	return items, nil
}

func resolvePaperTrading(ctx context) (paperTradingInfo, error) {
	productID, err := resolveProductID(ctx)
	if err != nil {
		return paperTradingInfo{}, err
	}
	versionMap, err := buildPaperTradingVersionMap(ctx)
	if err != nil {
		return paperTradingInfo{}, err
	}
	info, ok := versionMap[productID]
	if !ok {
		return paperTradingInfo{}, fmt.Errorf("paper trading config not found for product %q", productID)
	}
	return info, nil
}

func resolvePaperTradingAndSignal(ctx context) (paperTradingInfo, string, error) {
	info, err := resolvePaperTrading(ctx)
	if err != nil {
		return paperTradingInfo{}, "", err
	}
	signalID, err := payload.String(ctx.payload, "signal_id")
	if err != nil {
		return paperTradingInfo{}, "", err
	}
	return info, signalID, nil
}

func buildPaperTradingVersionMap(ctx context) (map[string]paperTradingInfo, error) {
	versionMap := map[string]paperTradingInfo{}
	channels, err := ctx.client.AMSRequest("GET", "products:list_paper_trading_channels", nil)
	if err != nil {
		return nil, err
	}
	for _, item := range extractList(channels, "channels") {
		config, ok := item.(map[string]any)
		if !ok {
			continue
		}
		productID := stringifyLocal(config["product_id"])
		if productID == "" {
			continue
		}
		copy := publicPaperTradingConfig(config)
		copy["strategy_model"] = stringDefault(copy["strategy_model"], "general")
		versionMap[productID] = paperTradingInfo{
			version:   "v1",
			productID: productID,
			channelID: firstString(config["_id"], config["id"]),
			config:    copy,
		}
	}
	v2List, err := ctx.client.AMSRequest("GET", "products:list_paper_trading_v2", nil)
	if err != nil {
		return nil, err
	}
	for _, item := range extractList(v2List, "items") {
		config, ok := item.(map[string]any)
		if !ok {
			continue
		}
		productID := firstString(config["product_id"], config["_id"], config["id"])
		if productID == "" {
			continue
		}
		copy := publicPaperTradingConfig(config)
		copy["strategy_model"] = stringDefault(copy["strategy_model"], "equity_long")
		versionMap[productID] = paperTradingInfo{
			version:   "v2",
			productID: productID,
			config:    copy,
		}
	}
	return versionMap, nil
}

func resolveProductID(ctx context) (string, error) {
	if value, ok := ctx.payload["product_id"]; ok {
		text, ok := value.(string)
		if ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text), nil
		}
	}
	value, err := payload.String(ctx.payload, "product_id_or_name")
	if err != nil {
		return "", err
	}
	return resolveProductIDFromValue(ctx, value)
}

func resolveProductLike(ctx context) (productLike, error) {
	if id := firstString(ctx.payload["product_group_id"], ctx.payload["group_id"]); id != "" {
		return productLike{resourceType: "product_groups", resourceID: id}, nil
	}
	if value, ok := ctx.payload["product_group_id_or_name"]; ok {
		id, err := resolveProductGroupID(ctx, stringifyLocal(value))
		if err != nil {
			return productLike{}, err
		}
		return productLike{resourceType: "product_groups", resourceID: id}, nil
	}
	if value, ok := ctx.payload["product_like_id"]; ok {
		text := stringifyLocal(value)
		if text != "" {
			return productLike{resourceType: "products", resourceID: text}, nil
		}
	}
	if id := firstString(ctx.payload["product_id"]); id != "" {
		return productLike{resourceType: "products", resourceID: id}, nil
	}
	value := firstString(ctx.payload["product_like_id_or_name"], ctx.payload["product_id_or_name"])
	if value == "" {
		return productLike{}, fmt.Errorf("payload requires product_like_id_or_name, product_id, product_id_or_name, or product_group_id_or_name")
	}
	productID, err := resolveProductIDFromValue(ctx, value)
	if err == nil {
		return productLike{resourceType: "products", resourceID: productID}, nil
	}
	groupID, groupErr := resolveProductGroupID(ctx, value)
	if groupErr == nil {
		return productLike{resourceType: "product_groups", resourceID: groupID}, nil
	}
	return productLike{}, err
}

func resolveProductIDFromValue(ctx context, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("product value is empty")
	}
	if looksLikeProductID(value) {
		return value, nil
	}
	params := url.Values{}
	params.Set("fields", "id,name")
	data, err := ctx.client.AMSRequestWithParams("GET", "products", nil, params)
	if err != nil {
		return "", err
	}
	for _, item := range extractList(data, "products") {
		product, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := stringifyLocal(product["id"])
		name := stringifyLocal(product["name"])
		if value == id || value == name {
			return id, nil
		}
	}
	return "", fmt.Errorf("product %q does not exist", value)
}

func resolveProductGroupID(ctx context, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("product group value is empty")
	}
	if looksLikeProductID(value) {
		return value, nil
	}
	data, err := ctx.client.AMSRequest("GET", "product_groups", nil)
	if err != nil {
		return "", err
	}
	for _, item := range extractList(data, "product_groups") {
		group, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := stringifyLocal(group["id"])
		name := stringifyLocal(group["name"])
		if value == id || value == name {
			return id, nil
		}
	}
	return "", fmt.Errorf("product group %q does not exist", value)
}

func resolveProductGroupIDFromPayload(ctx context) (string, error) {
	if id := firstString(ctx.payload["group_id"], ctx.payload["product_group_id"]); id != "" {
		return id, nil
	}
	value := firstString(ctx.payload["group_id_or_name"], ctx.payload["product_group_id_or_name"])
	if value == "" {
		return "", fmt.Errorf("payload missing required field %q", "group_id_or_name")
	}
	return resolveProductGroupID(ctx, value)
}

func resolvePermissionResource(ctx context) (productLike, error) {
	if resourceType := normalizePermissionResourceType(firstString(ctx.payload["resource_type"], ctx.payload["permission_resource_type"])); resourceType != "" {
		if resourceID := firstString(ctx.payload["resource_id"], ctx.payload["id"]); resourceID != "" {
			return productLike{resourceType: resourceType, resourceID: resourceID}, nil
		}
		switch resourceType {
		case "products":
			productID, err := resolveProductID(ctx)
			if err != nil {
				return productLike{}, err
			}
			return productLike{resourceType: "products", resourceID: productID}, nil
		case "product_groups":
			groupID, err := resolveProductGroupIDFromPayload(ctx)
			if err != nil {
				return productLike{}, err
			}
			return productLike{resourceType: "product_groups", resourceID: groupID}, nil
		}
	}
	return resolveProductLike(ctx)
}

func resolvePermissionBatchResources(ctx context) (string, []string, error) {
	if items, ok := stringList(ctx.payload["product_ids_or_names"]); ok {
		ids := make([]string, 0, len(items))
		for _, item := range items {
			id, err := resolveProductIDFromValue(ctx, item)
			if err != nil {
				return "", nil, err
			}
			ids = append(ids, id)
		}
		return "products", ids, nil
	}
	if items, ok := stringList(ctx.payload["product_group_ids_or_names"]); ok {
		ids := make([]string, 0, len(items))
		for _, item := range items {
			id, err := resolveProductGroupID(ctx, item)
			if err != nil {
				return "", nil, err
			}
			ids = append(ids, id)
		}
		return "product_groups", ids, nil
	}
	if items, ok := stringList(ctx.payload["product_ids"]); ok {
		return "products", items, nil
	}
	if items, ok := stringList(ctx.payload["product_group_ids"]); ok {
		return "product_groups", items, nil
	}
	resourceType := normalizePermissionResourceType(firstString(ctx.payload["resource_type"], ctx.payload["permission_resource_type"]))
	if resourceType == "" {
		return "", nil, fmt.Errorf("payload missing required field %q", "resource_type")
	}
	ids, err := requiredStringList(ctx.payload, "resource_ids", "resource_id", "ids")
	if err != nil {
		return "", nil, err
	}
	return resourceType, ids, nil
}

func normalizePermissionResourceType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "product", "products":
		return "products"
	case "product_group", "product_groups", "group", "groups":
		return "product_groups"
	default:
		return ""
	}
}

func permissionPayload(doc map[string]any, allowPermissionID bool) ([]map[string]any, error) {
	if items, ok := optionalObjectList(doc, "permissions"); ok {
		return normalizePermissionPayloadItems(items, allowPermissionID), nil
	}
	if item, ok := doc["permission"].(map[string]any); ok {
		return normalizePermissionPayloadItems([]map[string]any{item}, allowPermissionID), nil
	}
	if _, ok := doc["user_id"]; ok {
		item := map[string]any{
			"user_id":    doc["user_id"],
			"permission": doc["permission"],
		}
		if allowPermissionID {
			if permissionID := firstString(doc["permission_id"], doc["id"]); permissionID != "" {
				item["permission_id"] = permissionID
			}
		}
		return normalizePermissionPayloadItems([]map[string]any{item}, allowPermissionID), nil
	}
	return nil, fmt.Errorf("payload missing required field %q", "permissions")
}

func normalizePermissionPayloadItems(items []map[string]any, allowPermissionID bool) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		copy := cloneMap(item)
		if allowPermissionID {
			if permissionID := firstString(copy["permission_id"], copy["id"], copy["_id"]); permissionID != "" {
				copy["permission_id"] = permissionID
			}
		} else {
			delete(copy, "permission_id")
			delete(copy, "id")
			delete(copy, "_id")
		}
		result = append(result, copy)
	}
	return result
}

func permissionMeta(path string, resource productLike) map[string]any {
	meta := queryMeta(url.Values{}, path)
	meta["resolved_resource_type"] = resource.resourceType
	meta["resolved_resource_id"] = resource.resourceID
	return meta
}

func productLikeMeta(params url.Values, path string, productLike productLike) map[string]any {
	meta := queryMeta(params, path)
	meta["resolved_resource_type"] = productLike.resourceType
	meta["resolved_resource_id"] = productLike.resourceID
	return meta
}

func paperTradingSignalListPath(info paperTradingInfo) string {
	if info.version == "v2" {
		return "products/" + info.productID + "/paper_trading_v2/signals"
	}
	return "products/" + info.productID + "/paper_trading_signals"
}

func paperTradingSignalPath(info paperTradingInfo, signalID string) string {
	if info.version == "v2" {
		return "products/" + info.productID + "/paper_trading_v2/signals/" + signalID
	}
	return "products/" + info.productID + "/paper_trading_signals/" + signalID + ":get_details"
}

func paperTradingMeta(params url.Values, _ string, info paperTradingInfo) map[string]any {
	meta := map[string]any{}
	if len(params) > 0 {
		meta["query"] = params.Encode()
	}
	meta["resolved_product_id"] = info.productID
	return meta
}

func publicPaperTradingConfig(config map[string]any) map[string]any {
	copy := cloneMap(config)
	if firstString(copy["id"], copy["_id"]) != "" {
		copy["id"] = firstString(copy["id"], copy["_id"])
	}
	delete(copy, "_id")
	delete(copy, "version")
	return copy
}

func normalizePaperTradingSignalList(data any, limit int) any {
	switch value := data.(type) {
	case []any:
		items := value
		if limit > 0 && len(items) > limit {
			items = items[:limit]
		}
		result := make([]any, 0, len(items))
		for _, item := range items {
			result = append(result, normalizePaperTradingSignalData(item))
		}
		return result
	case map[string]any:
		copy := cloneMap(value)
		if signals, ok := copy["signals"].([]any); ok {
			if limit > 0 && len(signals) > limit {
				signals = signals[:limit]
			}
			result := make([]any, 0, len(signals))
			for _, signal := range signals {
				result = append(result, normalizePaperTradingSignalData(signal))
			}
			copy["signals"] = result
			return copy
		}
		return normalizePaperTradingSignalData(copy)
	default:
		return data
	}
}

func normalizePaperTradingSignalData(data any) any {
	object, ok := data.(map[string]any)
	if !ok {
		return data
	}
	copy := cloneMap(object)
	if id := firstString(copy["id"], copy["_id"], copy["signal_id"]); id != "" {
		copy["id"] = id
	}
	delete(copy, "_id")
	delete(copy, "signal_id")
	delete(copy, "version")
	return copy
}

func normalizePaperTradingWriteResult(data any) any {
	object, ok := data.(map[string]any)
	if !ok {
		return data
	}
	copy := cloneMap(object)
	if id := firstString(copy["id"], copy["paper_trading_id"], copy["channel_id"]); id != "" {
		copy["id"] = id
	}
	delete(copy, "paper_trading_id")
	delete(copy, "channel_id")
	delete(copy, "version")
	return copy
}

func hasPaperTradingProductTarget(doc map[string]any) bool {
	if firstString(doc["product_id"], doc["product_id_or_name"]) != "" {
		return true
	}
	for _, field := range []string{"product_ids", "product_ids_or_names"} {
		if ids, ok := stringList(doc[field]); ok && len(ids) > 0 {
			return true
		}
	}
	return false
}

func mergedV1PaperTradingConfig(current map[string]any, updates map[string]any) map[string]any {
	fields := []string{
		"stock_min_fee",
		"stock_commission_rate",
		"loan_rate",
		"margin_rate",
		"futures_float_rate",
		"futures_float_amount",
		"slippage_rate",
		"slippage_ticks",
	}
	merged := pickFields(current, fields)
	for key, value := range updates {
		merged[key] = value
	}
	if merged["futures_float_rate"] != nil {
		merged["futures_float_amount"] = nil
	} else if merged["futures_float_amount"] != nil {
		merged["futures_float_rate"] = nil
	}
	if merged["slippage_rate"] != nil {
		merged["slippage_ticks"] = nil
	} else if merged["slippage_ticks"] != nil {
		merged["slippage_rate"] = nil
	}
	return merged
}

func normalizeV1ExclusivePaperTradingFields(body map[string]any) map[string]any {
	if body["futures_float_rate"] != nil {
		body["futures_float_amount"] = nil
	} else if body["futures_float_amount"] != nil {
		body["futures_float_rate"] = nil
	}
	if body["slippage_rate"] != nil {
		body["slippage_ticks"] = nil
	} else if body["slippage_ticks"] != nil {
		body["slippage_rate"] = nil
	}
	return body
}

func mergedV2PaperTradingConfig(current map[string]any, updates map[string]any) map[string]any {
	fields := []string{
		"init_amount",
		"algo",
		"start_time",
		"end_time",
		"commission_rate",
		"min_fee",
		"slippage_rate",
		"slippage_ticks",
	}
	merged := pickFields(current, fields)
	for key, value := range updates {
		merged[key] = value
	}
	return merged
}

func paperTradingProductIDs(ctx context) ([]string, error) {
	if ids, ok := stringList(ctx.payload["product_ids"]); ok && len(ids) > 0 {
		return ids, nil
	}
	if ids, ok := stringList(ctx.payload["product_ids_or_names"]); ok && len(ids) > 0 {
		resolved := make([]string, 0, len(ids))
		for _, idOrName := range ids {
			productID, err := resolveProductIDFromValue(ctx, idOrName)
			if err != nil {
				return nil, err
			}
			resolved = append(resolved, productID)
		}
		return resolved, nil
	}
	productID, err := resolveProductID(ctx)
	if err != nil {
		return nil, err
	}
	return []string{productID}, nil
}

func paperTradingConfigPayload(doc map[string]any) map[string]any {
	if config, ok := doc["config"].(map[string]any); ok {
		return copyMap(config)
	}
	body := map[string]any{}
	for key, value := range doc {
		switch key {
		case "version", "profile", "product_id", "product_id_or_name", "product_ids", "product_ids_or_names":
			continue
		default:
			body[key] = value
		}
	}
	return body
}

func paperTradingCreateRoute(doc map[string]any) (paperTradingCreateTarget, error) {
	config := paperTradingConfigPayload(doc)
	template := strings.ToLower(strings.TrimSpace(stringDefault(config["template"], "")))
	if template == "" {
		template = strings.ToLower(strings.TrimSpace(stringDefault(config["strategy_model"], "")))
	}
	if template == "" {
		template = strings.ToLower(strings.TrimSpace(stringDefault(doc["version"], "")))
	}
	if template == "" && hasPaperTradingProductTarget(doc) {
		template = "channel"
	}
	if template == "" {
		template = "equity_long"
	}
	template = strings.ReplaceAll(template, "-", "_")
	switch template {
	case "equity_long", "v2", "paper_trading_v2":
		return paperTradingCreateTarget{
			template: "equity_long",
			version:  "v2",
			path:     "products/paper_trading_v2",
		}, nil
	case "conventional", "v2_conventional", "paper_trading_v2_conventional":
		return paperTradingCreateTarget{
			template: "conventional",
			version:  "v2",
			path:     "products/paper_trading_v2:create_conventional",
		}, nil
	case "v1", "channel", "channels", "paper_trading_channel", "paper_trading_channels":
		return paperTradingCreateTarget{
			template: "channel",
			version:  "v1",
			path:     "products/paper_trading_channels:batch_upsert",
		}, nil
	default:
		return paperTradingCreateTarget{}, fmt.Errorf("payload field %q must be one of equity_long or conventional", "template")
	}
}

func paperTradingV2FormFields(doc map[string]any, template string) map[string]string {
	config := paperTradingConfigPayload(doc)
	fields := map[string]string{}
	keys := []string{"name", "benchmark", "start_date", "init_amount", "slippage_rate", "slippage_ticks", "tag_ids", "remarks", "description"}
	switch template {
	case "conventional":
		keys = append(keys,
			"stock_min_fee",
			"stock_commission_rate",
			"loan_rate",
			"margin_rate",
			"strategy_category",
			"futures_float_rate",
			"futures_float_amount",
		)
	default:
		keys = append(keys,
			"strategy_model",
			"algo",
			"start_time",
			"end_time",
			"commission_rate",
			"min_fee",
		)
	}
	for _, key := range keys {
		value, ok := config[key]
		if !ok || value == nil {
			continue
		}
		if key == "remarks" {
			key = "description"
		}
		if key == "tag_ids" {
			fields[key] = listOrStringValue(value)
			continue
		}
		fields[key] = queryValue(value)
	}
	if template == "equity_long" && strings.TrimSpace(fields["strategy_model"]) == "" {
		fields["strategy_model"] = "equity_long"
	}
	return fields
}

func hasUploadFiles(doc map[string]any) bool {
	for _, key := range []string{"file_paths", "file_path", "files"} {
		if value, ok := doc[key]; ok && value != nil && queryValue(value) != "" {
			return true
		}
	}
	return false
}

func copyMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func pickFields(source map[string]any, fields []string) map[string]any {
	result := map[string]any{}
	for _, field := range fields {
		if value, ok := source[field]; ok {
			result[field] = value
		}
	}
	return result
}

func uploadFiles(doc map[string]any) ([]client.UploadFile, error) {
	rawPaths, ok := doc["file_paths"]
	if !ok {
		rawPaths = doc["file_path"]
	}
	if rawPaths == nil {
		rawPaths = doc["files"]
	}
	paths, ok := stringList(rawPaths)
	if !ok || len(paths) == 0 {
		if text := queryValue(rawPaths); text != "" {
			paths = []string{text}
		} else {
			return nil, fmt.Errorf("payload requires file_paths")
		}
	}
	uploads := make([]client.UploadFile, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			entries, err := os.ReadDir(path)
			if err != nil {
				return nil, err
			}
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				uploads = append(uploads, client.UploadFile{FieldName: "files", Path: filepath.Join(path, entry.Name())})
			}
			continue
		}
		uploads = append(uploads, client.UploadFile{FieldName: "files", Path: path})
	}
	if len(uploads) == 0 {
		return nil, fmt.Errorf("no files found to upload")
	}
	return uploads, nil
}

func valuationReportFiles(doc map[string]any) ([]client.UploadFile, error) {
	files, err := uploadFiles(doc)
	if err != nil {
		return nil, err
	}
	filtered := make([]client.UploadFile, 0, len(files))
	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Path))
		if ext == ".xls" || ext == ".xlsx" {
			filtered = append(filtered, file)
		}
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("no .xls or .xlsx valuation report files found")
	}
	return filtered, nil
}

func objectList(doc map[string]any, field string) ([]map[string]any, error) {
	value, ok := doc[field]
	if !ok {
		return nil, fmt.Errorf("payload missing required field %q", field)
	}
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil, fmt.Errorf("payload field %q must be a non-empty array", field)
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("payload field %q must contain only objects", field)
		}
		result = append(result, object)
	}
	return result, nil
}

func optionalObjectList(doc map[string]any, field string) ([]map[string]any, bool) {
	value, ok := doc[field]
	if !ok {
		return nil, false
	}
	if object, ok := value.(map[string]any); ok {
		return []map[string]any{object}, true
	}
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil, false
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		result = append(result, object)
	}
	return result, true
}

func boundedChunkSize(value int) int {
	if value <= 0 {
		return 1000
	}
	if value < 500 {
		return 500
	}
	if value > 5000 {
		return 5000
	}
	return value
}

func chunkMaps(items []map[string]any, chunkSize int) [][]map[string]any {
	chunks := make([][]map[string]any, 0)
	for start := 0; start < len(items); start += chunkSize {
		end := start + chunkSize
		if end > len(items) {
			end = len(items)
		}
		chunks = append(chunks, items[start:end])
	}
	return chunks
}

func extractBatchResult(data any) []any {
	if items, ok := data.([]any); ok {
		return items
	}
	return []any{data}
}

func pollTaskIDStatus(ctx context, path string, taskID string) (any, error) {
	params := url.Values{}
	params.Set("task_id", taskID)
	return pollHTTP200StatusTask(fmt.Sprintf("task %q", taskID), func() (any, error) {
		return ctx.client.AMSRequestWithParams("GET", path, nil, params)
	})
}

func pollHTTP200StatusTask(taskLabel string, request func() (any, error)) (any, error) {
	for attempt := 0; attempt < asyncpoll.MaxAttempts; attempt++ {
		data, err := request()
		if err != nil {
			return nil, err
		}
		root, ok := data.(map[string]any)
		if !ok || stringifyLocal(root["status"]) != "DOING" {
			return data, nil
		}
		time.Sleep(asyncpoll.Interval)
	}
	return nil, fmt.Errorf("%s did not finish before timeout", taskLabel)
}

func responseDataAfterOptionalTask(ctx context, path string, data any) (any, error) {
	taskID := firstStringFromMap(data, "task_id")
	if taskID == "" {
		return responseDataOrMessage(data), nil
	}
	finalData, err := pollTaskIDStatus(ctx, path, taskID)
	if err != nil {
		return nil, err
	}
	return responseDataOrMessage(finalData), nil
}

func responseDataOrMessage(data any) any {
	root, ok := data.(map[string]any)
	if !ok {
		return data
	}
	if value, ok := root["data"]; ok {
		return value
	}
	if value, ok := root["message"]; ok {
		return value
	}
	return data
}

func firstStringFromMap(data any, fields ...string) string {
	root, ok := data.(map[string]any)
	if !ok {
		return ""
	}
	for _, field := range fields {
		if text := stringifyLocal(root[field]); text != "" {
			return text
		}
	}
	if nested, ok := root["data"].(map[string]any); ok {
		for _, field := range fields {
			if text := stringifyLocal(nested[field]); text != "" {
				return text
			}
		}
	}
	return ""
}

func fileSessionID(data any) string {
	root, ok := data.(map[string]any)
	if !ok {
		return ""
	}
	if id := stringifyLocal(root["file_session_id"]); id != "" {
		return id
	}
	if nested, ok := root["data"].(map[string]any); ok {
		return stringifyLocal(nested["file_session_id"])
	}
	return ""
}

func dataFromEnvelope(data any) any {
	root, ok := data.(map[string]any)
	if !ok {
		return data
	}
	if value, ok := root["data"]; ok {
		return value
	}
	return data
}

func copyDateField(dst map[string]any, src map[string]any, field string) {
	if text := queryValue(src[field]); text != "" {
		dst[field] = text
	}
}

func extractList(data any, preferredField string) []any {
	if items, ok := data.([]any); ok {
		return items
	}
	root, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	if items, ok := root[preferredField].([]any); ok {
		return items
	}
	for _, field := range []string{
		"data", "items", "channels", "products", "product_groups", "signals",
		"valuation_reports", "custodian_events", "daily_units", "customized_instruments",
	} {
		if items, ok := root[field].([]any); ok {
			return items
		}
	}
	return nil
}

func extractTopLevelList(data any) []any {
	if items, ok := data.([]any); ok {
		return items
	}
	root, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	for _, field := range []string{"permissions", "data", "items"} {
		if items, ok := root[field].([]any); ok {
			return items
		}
	}
	return nil
}

func normalizePermissionItems(items []any) []any {
	result := make([]any, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			result = append(result, item)
			continue
		}
		copy := cloneMap(object)
		if id := firstString(copy["id"], copy["_id"], copy["permission_id"]); id != "" {
			copy["id"] = id
		}
		delete(copy, "_id")
		result = append(result, copy)
	}
	return result
}

func projectItems(items []any, fields []string) []any {
	if len(fields) == 0 {
		return items
	}
	projected := make([]any, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			projected = append(projected, item)
			continue
		}
		doc := map[string]any{}
		for _, field := range fields {
			if value, ok := object[field]; ok {
				doc[field] = value
			}
		}
		projected = append(projected, doc)
	}
	return projected
}

func listFields(doc map[string]any) string {
	if value, ok := doc["fields"]; ok {
		if text := listOrStringValue(value); text != "" {
			return text
		}
	}
	return ""
}

func stringList(value any) ([]string, bool) {
	if text := queryValue(value); text != "" {
		return []string{text}, true
	}
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if ok && strings.TrimSpace(text) != "" {
			result = append(result, strings.TrimSpace(text))
		}
	}
	return result, len(result) > 0
}

func requiredStringList(doc map[string]any, fields ...string) ([]string, error) {
	for _, field := range fields {
		if items, ok := stringList(doc[field]); ok {
			return items, nil
		}
	}
	return nil, fmt.Errorf("payload missing required field %q", fields[0])
}

func cloneMap(source map[string]any) map[string]any {
	result := map[string]any{}
	for key, value := range source {
		result[key] = value
	}
	return result
}

func firstString(values ...any) string {
	for _, value := range values {
		if text := stringifyLocal(value); text != "" {
			return text
		}
	}
	return ""
}

func stringifyLocal(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}

func stringDefault(value any, fallback string) string {
	text := stringifyLocal(value)
	if text == "" {
		return fallback
	}
	return text
}

func looksLikeProductID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) == 24 {
		for _, ch := range value {
			if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
				return false
			}
		}
		return true
	}
	return false
}

func queryParams(doc map[string]any, fields []string) url.Values {
	params := url.Values{}
	for _, field := range fields {
		value := doc[field]
		switch field {
		case "fields", "sources", "asset_transaction_types", "account_names", "asset_unit_ids", "key_words", "custodian_event_type":
			if text := listOrStringValue(value); text != "" {
				params.Set(field, text)
			}
		case "is_query_assistant":
			if boolValue(value) {
				params.Set(field, "true")
			}
		default:
			if text := queryValue(value); text != "" {
				params.Set(field, text)
			}
		}
	}
	return params
}

func tradeQueryParams(doc map[string]any, includeGroupBy bool) url.Values {
	fields := []string{
		"start_date",
		"end_date",
		"sources",
		"order_book_id",
		"symbol",
		"asset_transaction_types",
		"account_names",
		"asset_unit_ids",
		"key_words",
		"remarks",
		"is_query_assistant",
	}
	if includeGroupBy {
		fields = append(fields, "group_by")
	}
	return queryParams(doc, fields)
}

func reconciliationDateBody(doc map[string]any) (map[string]any, error) {
	date, err := payload.String(doc, "date")
	if err != nil {
		return nil, err
	}
	return map[string]any{"date": date}, nil
}

func submitReconciliationDateAction(ctx context, action string) (any, map[string]any, error) {
	productID, err := resolveProductID(ctx)
	if err != nil {
		return nil, nil, err
	}
	return submitReconciliationDateActionForProduct(ctx, productID, action)
}

func submitReconciliationDateActionForProduct(ctx context, productID string, action string) (any, map[string]any, error) {
	body, err := reconciliationDateBody(ctx.payload)
	if err != nil {
		return nil, nil, err
	}
	path := "products/" + productID + ":" + action
	data, err := ctx.client.AMSRequest("POST", path, body)
	return data, map[string]any{"resolved_path": path, "resolved_product_id": productID, "resolved_action": reconciliationActionName(action)}, err
}

func reconciliationActionName(action string) string {
	switch action {
	case "auto_reconciliation":
		return "auto"
	case "undo_auto_reconciliation":
		return "undo_auto"
	default:
		return action
	}
}

func addExtraParams(params url.Values, doc map[string]any) {
	if extras, ok := doc["params"].(map[string]any); ok {
		for key, value := range extras {
			if text := listOrStringValue(value); text != "" {
				params.Set(key, text)
				continue
			}
			if text := queryValue(value); text != "" {
				params.Set(key, text)
			}
		}
	}
	for key, value := range doc {
		if reservedPayloadField(key) || params.Has(key) {
			continue
		}
		if text := listOrStringValue(value); text != "" {
			params.Set(key, text)
			continue
		}
		if text := queryValue(value); text != "" {
			params.Set(key, text)
		}
	}
}

func addNestedParams(params url.Values, doc map[string]any) {
	if extras, ok := doc["params"].(map[string]any); ok {
		for key, value := range extras {
			if text := listOrStringValue(value); text != "" {
				params.Set(key, text)
				continue
			}
			if text := queryValue(value); text != "" {
				params.Set(key, text)
			}
		}
	}
}

func reservedPayloadField(field string) bool {
	switch field {
	case "product_like_id", "product_like_id_or_name", "product_id", "product_id_or_name",
		"product_like_ids", "product_like_ids_or_names", "product_ids", "product_group_ids",
		"product_ids_or_names",
		"product_group_id", "group_id", "product_group_id_or_name", "format", "limit", "raw",
		"file_path", "file_paths", "files", "save_path", "file_name", "batch_size",
		"asset_unit_id", "asset_unit", "customized_indicators", "customized_instrument",
		"customized_ins_id", "customized_ins_ids", "customized_benchmark",
		"customized_benchmark_id", "valuation_reports", "deleted_dates", "dates",
		"positions_statement_ids", "position_statement_ids", "event_id", "event_ids",
		"custodian_event", "custodian_events", "unit_event", "unit_events", "params", "profile":
		return true
	default:
		return false
	}
}

func investmentOverviewPayload(ctx context, requireBenchmark bool) (map[string]any, error) {
	ids, err := resolveProductLikeIDs(ctx)
	if err != nil {
		return nil, err
	}
	startDate, err := payload.String(ctx.payload, "start_date")
	if err != nil {
		return nil, err
	}
	endDate, err := payload.String(ctx.payload, "end_date")
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"product_or_group_ids": strings.Join(ids, ","),
		"start_date":           startDate,
		"end_date":             endDate,
	}
	if benchmarkID := firstString(ctx.payload["benchmark_id"], ctx.payload["benchmark"]); benchmarkID != "" {
		body["benchmarks"] = benchmarkID
	} else if requireBenchmark {
		return nil, fmt.Errorf("payload missing required field %q", "benchmark_id")
	}
	if extras, ok := ctx.payload["params"].(map[string]any); ok {
		for key, value := range extras {
			body[key] = value
		}
	}
	return body, nil
}

func resolveProductLikeIDs(ctx context) ([]string, error) {
	if items, ok := stringList(ctx.payload["product_like_ids"]); ok {
		return items, nil
	}
	if items, ok := stringList(ctx.payload["product_ids"]); ok {
		return items, nil
	}
	if items, ok := stringList(ctx.payload["product_group_ids"]); ok {
		return items, nil
	}
	if single := firstString(ctx.payload["product_like_id"], ctx.payload["product_id"], ctx.payload["product_group_id"], ctx.payload["group_id"]); single != "" {
		return []string{single}, nil
	}
	var values []string
	if items, ok := stringList(ctx.payload["product_like_ids_or_names"]); ok {
		values = items
	} else if single := firstString(ctx.payload["product_like_id_or_name"], ctx.payload["product_id_or_name"], ctx.payload["product_group_id_or_name"]); single != "" {
		values = []string{single}
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("payload requires product_like_ids_or_names or product_like_id_or_name")
	}
	ids := make([]string, 0, len(values))
	for _, value := range values {
		productID, err := resolveProductIDFromValue(ctx, value)
		if err == nil {
			ids = append(ids, productID)
			continue
		}
		groupID, groupErr := resolveProductGroupID(ctx, value)
		if groupErr == nil {
			ids = append(ids, groupID)
			continue
		}
		return nil, err
	}
	return ids, nil
}

func resolveProductIDs(ctx context) ([]string, error) {
	if items, ok := stringList(ctx.payload["product_ids"]); ok {
		return items, nil
	}
	if items, ok := stringList(ctx.payload["product_ids_or_names"]); ok {
		ids := make([]string, 0, len(items))
		for _, item := range items {
			productID, err := resolveProductIDFromValue(ctx, item)
			if err != nil {
				return nil, err
			}
			ids = append(ids, productID)
		}
		return ids, nil
	}
	if single := firstString(ctx.payload["product_id"]); single != "" {
		return []string{single}, nil
	}
	if single := firstString(ctx.payload["product_id_or_name"]); single != "" {
		productID, err := resolveProductIDFromValue(ctx, single)
		if err != nil {
			return nil, err
		}
		return []string{productID}, nil
	}
	return nil, fmt.Errorf("payload requires product_ids or product_ids_or_names")
}

func resolveProductLikeList(ctx context) ([]productLike, error) {
	if items, ok := stringList(ctx.payload["product_like_ids"]); ok {
		return productLikeListFromIDs(items, "products"), nil
	}
	if items, ok := stringList(ctx.payload["product_ids"]); ok {
		return productLikeListFromIDs(items, "products"), nil
	}
	if items, ok := stringList(ctx.payload["product_group_ids"]); ok {
		return productLikeListFromIDs(items, "product_groups"), nil
	}
	if single := firstString(ctx.payload["product_like_id"], ctx.payload["product_id"]); single != "" {
		return []productLike{{resourceType: "products", resourceID: single}}, nil
	}
	if single := firstString(ctx.payload["product_group_id"], ctx.payload["group_id"]); single != "" {
		return []productLike{{resourceType: "product_groups", resourceID: single}}, nil
	}
	var values []string
	if items, ok := stringList(ctx.payload["product_like_ids_or_names"]); ok {
		values = items
	} else if single := firstString(ctx.payload["product_like_id_or_name"], ctx.payload["product_id_or_name"], ctx.payload["product_group_id_or_name"]); single != "" {
		values = []string{single}
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("payload requires product_like_ids_or_names or product_like_id_or_name")
	}
	result := make([]productLike, 0, len(values))
	for _, value := range values {
		productID, err := resolveProductIDFromValue(ctx, value)
		if err == nil {
			result = append(result, productLike{resourceType: "products", resourceID: productID})
			continue
		}
		groupID, groupErr := resolveProductGroupID(ctx, value)
		if groupErr == nil {
			result = append(result, productLike{resourceType: "product_groups", resourceID: groupID})
			continue
		}
		return nil, err
	}
	return result, nil
}

func productLikeListFromIDs(ids []string, resourceType string) []productLike {
	result := make([]productLike, 0, len(ids))
	for _, id := range ids {
		result = append(result, productLike{resourceType: resourceType, resourceID: id})
	}
	return result
}

func mapToValues(source map[string]any) url.Values {
	values := url.Values{}
	for key, value := range source {
		if text := listOrStringValue(value); text != "" {
			values.Set(key, text)
			continue
		}
		if text := queryValue(value); text != "" {
			values.Set(key, text)
		}
	}
	return values
}

func benchmarkValue(doc map[string]any) string {
	benchmarkID := firstString(doc["benchmark_id"], doc["benchmark"])
	if benchmarkID == "" {
		benchmarkID = "000300.XSHG"
	}
	if strings.Contains(benchmarkID, ",") {
		return benchmarkID
	}
	switch benchmarkID {
	case "000300.XSHG", "000905.XSHG", "000852.XSHG", "932000.CSI":
		return "index," + benchmarkID
	default:
		return "customized_index," + benchmarkID
	}
}

func pollPerformanceAttribution(ctx context, productLike productLike, taskID string) (any, error) {
	path := productLike.resourceType + "/" + productLike.resourceID + "/performance_attributions/" + taskID
	data, err := pollHTTP200StatusTask(fmt.Sprintf("performance attribution task %q", taskID), func() (any, error) {
		return ctx.client.AMSRequest("GET", path, nil)
	})
	if err != nil {
		return nil, err
	}
	root, ok := data.(map[string]any)
	if !ok {
		return data, nil
	}
	if result, ok := root["result"]; ok {
		return result, nil
	}
	if stringifyLocal(root["status"]) == "FAIL" {
		return nil, fmt.Errorf("performance attribution failed: %s", stringifyLocal(root["message"]))
	}
	return data, nil
}

func queryMeta(params url.Values, resolvedPath string) map[string]any {
	meta := map[string]any{"resolved_path": resolvedPath}
	if len(params) > 0 {
		meta["query"] = params.Encode()
	}
	return meta
}

func limitList(data any, field string, limit int) {
	if limit <= 0 {
		return
	}
	root, ok := data.(map[string]any)
	if !ok {
		return
	}
	items, ok := root[field].([]any)
	if !ok || len(items) <= limit {
		return
	}
	root[field] = items[:limit]
	root["returned"] = limit
}

func limitTopLevelList(data *any, limit int) {
	if limit <= 0 {
		return
	}
	items, ok := (*data).([]any)
	if !ok || len(items) <= limit {
		return
	}
	*data = items[:limit]
}

func listOrStringValue(value any) string {
	if text := queryValue(value); text != "" {
		return text
	}
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if ok && strings.TrimSpace(text) != "" {
			parts = append(parts, strings.TrimSpace(text))
		}
	}
	return strings.Join(parts, ",")
}

func limitProducts(root map[string]any, limit int) {
	items, ok := root["products"].([]any)
	if !ok || limit < 0 || len(items) <= limit {
		return
	}
	root["products"] = items[:limit]
	root["returned"] = limit
}

func projectProducts(root map[string]any, fields []string) {
	projectListField(root, "products", fields)
}

func projectListField(root map[string]any, listField string, fields []string) {
	items, ok := root[listField].([]any)
	if !ok {
		return
	}
	projected := make([]any, 0, len(items))
	for _, item := range items {
		product, ok := item.(map[string]any)
		if !ok {
			projected = append(projected, item)
			continue
		}
		doc := map[string]any{}
		for _, field := range fields {
			if value, ok := product[field]; ok {
				doc[field] = value
			}
		}
		projected = append(projected, doc)
	}
	root[listField] = projected
}

func projectAndLimitList(data any, listField string, doc map[string]any) {
	fields := splitFields(listFields(doc))
	if len(fields) > 0 {
		if root, ok := data.(map[string]any); ok {
			projectListField(root, listField, fields)
		}
	}
	limitList(data, listField, intValue(doc["limit"]))
}

func filterAutoUnitEvents(data any) {
	root, ok := data.(map[string]any)
	if !ok {
		return
	}
	items, ok := root["daily_units"].([]any)
	if !ok {
		return
	}
	filtered := make([]any, 0, len(items))
	for _, item := range items {
		event, ok := item.(map[string]any)
		if !ok || stringifyLocal(event["source"]) != "auto" {
			filtered = append(filtered, item)
		}
	}
	root["daily_units"] = filtered
}

func eventPayloadList(doc map[string]any, field string) ([]map[string]any, error) {
	if items, ok := optionalObjectList(doc, field); ok {
		return items, nil
	}
	singular := strings.TrimSuffix(field, "s")
	if items, ok := optionalObjectList(doc, singular); ok {
		return items, nil
	}
	if items, ok := optionalObjectList(doc, "events"); ok {
		return items, nil
	}
	return nil, fmt.Errorf("payload missing required field %q", field)
}

func eventPayloadObject(doc map[string]any, collection string) (map[string]any, error) {
	for _, field := range []string{strings.TrimSuffix(collection, "s"), collection, "event"} {
		if object, err := payload.Object(doc, field); err == nil {
			return object, nil
		}
	}
	return nil, fmt.Errorf("payload missing required event object")
}

func productGroupUpdateFields(fields map[string]any) map[string]any {
	result := cloneMap(fields)
	for _, field := range []string{"_id", "id", "user_id", "workspace_id"} {
		delete(result, field)
	}
	if products, ok := result["products"].([]any); ok && len(products) > 0 {
		ids := make([]string, 0, len(products))
		for _, product := range products {
			if id := productIDFromProductValue(product); id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			result["product_ids"] = ids
			delete(result, "products")
		}
	}
	if weights, ok := result["product_weights"]; ok && weights == nil {
		result["product_weights"] = map[string]any{}
	}
	return result
}

func productIDFromProductValue(value any) string {
	if text := stringifyLocal(value); text != "" {
		return text
	}
	product, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	return firstString(product["id"], product["_id"], product["product_id"])
}

func splitFields(fields string) []string {
	parts := strings.Split(fields, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		field := strings.TrimSpace(part)
		if field != "" {
			result = append(result, field)
		}
	}
	return result
}

func listFieldsWithDefault(doc map[string]any, defaultFields string) string {
	value, ok := doc["fields"]
	if !ok {
		return defaultFields
	}
	if text := queryValue(value); text != "" {
		return text
	}
	items, ok := value.([]any)
	if !ok {
		return defaultFields
	}
	fields := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if ok && strings.TrimSpace(text) != "" {
			fields = append(fields, strings.TrimSpace(text))
		}
	}
	if len(fields) == 0 {
		return defaultFields
	}
	return strings.Join(fields, ",")
}

func boolValue(value any) bool {
	typed, ok := value.(bool)
	return ok && typed
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func queryValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}

func intValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return 0
}

func intValueFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return intValue(typed)
	}
}

func firstExisting(data any, fields ...string) any {
	root, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	for _, field := range fields {
		if value, ok := root[field]; ok {
			return value
		}
	}
	return nil
}

func downloadDestination(savePath string, fileName string) string {
	info, err := os.Stat(savePath)
	if err == nil && info.IsDir() {
		return filepath.Join(savePath, fileName)
	}
	if strings.HasSuffix(savePath, string(os.PathSeparator)) || strings.HasSuffix(savePath, "/") || strings.HasSuffix(savePath, "\\") {
		return filepath.Join(savePath, fileName)
	}
	return savePath
}

func downloadResultMap(result client.DownloadResult) map[string]any {
	if result.Path == "" {
		return map[string]any{"content_type": result.ContentType, "data": result.Data}
	}
	return map[string]any{"path": result.Path, "content_type": result.ContentType, "bytes": result.Bytes}
}

func appendAny(value any, item any) []any {
	items, ok := value.([]any)
	if !ok {
		items = []any{}
	}
	return append(items, item)
}

func openAuth(ctx context) (any, map[string]any, error) {
	cfg := configWithPayloadProfile(ctx)
	baseURL, err := payload.String(ctx.payload, "base_url")
	if err != nil {
		baseURL = cfg.BaseURL
	}
	username, err := payload.String(ctx.payload, "username")
	if err != nil {
		username = cfg.Username
	}
	if strings.TrimSpace(baseURL) == "" {
		return nil, nil, fmt.Errorf("payload missing required field %q", "base_url")
	}
	if strings.TrimSpace(username) == "" {
		return nil, nil, fmt.Errorf("payload missing required field %q", "username")
	}
	password, err := payload.String(ctx.payload, "password")
	if err != nil {
		password = cfg.Password
	}
	if strings.TrimSpace(password) == "" {
		return nil, nil, fmt.Errorf("payload missing required field %q", "password")
	}
	cfg.BaseURL = baseURL
	cfg.Username = username
	cfg.Password = password
	cfg.Plaintext = true
	loginClient := client.New(cfg)
	login, err := loginClient.Login(username, password)
	if err != nil {
		return nil, nil, err
	}
	cfg.UserID = login.UserID
	cfg.SID = login.SID
	if err := config.Save(cfg); err != nil {
		return nil, nil, err
	}
	return map[string]any{
		"authenticated": true,
		"user_id":       login.UserID,
		"profile":       cfg.Profile,
		"config_saved":  true,
		"plaintext":     cfg.Plaintext,
	}, nil, nil
}

func configWithPayloadProfile(ctx context) config.Config {
	profile := firstString(ctx.payload["profile"])
	if profile == "" {
		return ctx.config
	}
	return config.SelectProfile(ctx.config, profile)
}

func productID(doc map[string]any) (string, error) {
	if value, ok := doc["product_id"]; ok {
		text, ok := value.(string)
		if ok && strings.TrimSpace(text) != "" {
			return text, nil
		}
	}
	return payload.String(doc, "product_id_or_name")
}

func productAndSignalID(doc map[string]any) (string, string, error) {
	productID, err := productID(doc)
	if err != nil {
		return "", "", err
	}
	signalID, err := payload.String(doc, "signal_id")
	if err != nil {
		return "", "", err
	}
	return productID, signalID, nil
}

func productAndAssetUnitID(ctx context) (string, string, error) {
	productID, err := resolveProductID(ctx)
	if err != nil {
		return "", "", err
	}
	assetUnitID := firstString(ctx.payload["asset_unit_id"], ctx.payload["asset_unit"])
	if assetUnitID == "" {
		return "", "", fmt.Errorf("payload missing required field %q", "asset_unit_id")
	}
	return productID, assetUnitID, nil
}

func getCurrentWorkspace(ctx context) (any, map[string]any, error) {
	data := map[string]any{
		"workspace_id": ctx.config.WorkspaceID,
	}
	workspaces, err := ctx.client.Workspaces()
	if err != nil {
		return data, map[string]any{"source": "local_state"}, nil
	}
	if ctx.config.WorkspaceID == "" {
		if len(workspaces) > 0 {
			workspaceID, workspaceName := workspaceIDAndName(workspaces[0])
			data["workspace_id"] = workspaceID
			data["workspace_name"] = workspaceName
			data["display"] = workspaceDisplay(workspaceName, workspaceID)
			data["defaulted"] = true
		}
		return data, map[string]any{"source": "workspace-list"}, nil
	}
	for _, workspace := range workspaces {
		workspaceID, workspaceName := workspaceIDAndName(workspace)
		if workspaceID == ctx.config.WorkspaceID {
			data["workspace_name"] = workspaceName
			data["display"] = workspaceDisplay(workspaceName, workspaceID)
			return data, map[string]any{"source": "workspace-list"}, nil
		}
	}
	return data, map[string]any{"source": "local_state"}, nil
}

func workspaceIDAndName(workspace map[string]any) (string, string) {
	id, _ := workspace["id"].(string)
	name, _ := workspace["name"].(string)
	return id, name
}

func workspaceDisplay(name string, id string) string {
	if strings.TrimSpace(name) == "" {
		return id
	}
	if strings.TrimSpace(id) == "" {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, id)
}

func useWorkspace(ctx context) (any, map[string]any, error) {
	cfg := configWithPayloadProfile(ctx)
	workspaceNameOrID, err := payload.String(ctx.payload, "workspace_id")
	if err != nil {
		workspaceNameOrID, err = payload.String(ctx.payload, "workspace_name_or_id")
		if err != nil {
			return nil, nil, fmt.Errorf("payload requires workspace_id or workspace_name_or_id")
		}
	}
	workspaceClient := client.New(cfg)
	workspaceID, workspaceName, err := resolveWorkspace(workspaceClient, workspaceNameOrID)
	if err != nil {
		return nil, nil, err
	}
	cfg.WorkspaceID = workspaceID
	if err := config.Save(cfg); err != nil {
		return nil, nil, err
	}
	return map[string]any{
		"workspace_id":   workspaceID,
		"workspace_name": workspaceName,
		"profile":        cfg.Profile,
		"config_saved":   true,
	}, map[string]any{"source": "local_state"}, nil
}

func resolveWorkspace(amsClient client.Client, nameOrID string) (string, string, error) {
	workspaces, err := amsClient.Workspaces()
	if err != nil {
		return "", "", err
	}
	for _, workspace := range workspaces {
		id, _ := workspace["id"].(string)
		name, _ := workspace["name"].(string)
		if nameOrID == id || nameOrID == name {
			return id, name, nil
		}
	}
	return "", "", fmt.Errorf("workspace %q does not exist", nameOrID)
}

func classifyError(err error) string {
	var httpErr client.HTTPError
	if asHTTPError(err, &httpErr) {
		return "http_error"
	}
	message := err.Error()
	if strings.Contains(message, "missing AMS base URL") || strings.Contains(message, "missing workspace") {
		return "config_error"
	}
	if strings.Contains(message, "payload") {
		return "invalid_payload"
	}
	return "runtime_error"
}

func asHTTPError(err error, target *client.HTTPError) bool {
	return errors.As(err, target)
}

func writeFailure(stdout io.Writer, command string, code string, message string) {
	_ = output.Write(stdout, output.Failure(command, code, message))
}
