package apps

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	caddysnake "github.com/mliezun/caddy-snake"
)

// Tool is one action an agent (through MCP), the HTTP API or the command line
// can take. All three run the same code.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	// Destructive marks tools that remove things, for clients that ask first.
	Destructive bool `json:"-"`
	run         func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error)
}

type prop map[string]any

func schema(required []string, props map[string]prop) map[string]any {
	properties := map[string]any{}
	for name, p := range props {
		properties[name] = map[string]any(p)
	}
	s := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) prop { return prop{"type": "string", "description": desc} }

var (
	appProp   = str("The app's name as it appears in its address (for example \"team-lunch-orders\").")
	draftProp = str("Name of a draft of the app. Leave out to work on the app itself.")
)

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, fmt.Errorf("the arguments are not valid: %w", err)
	}
	return v, nil
}

func servingSlug(app, draft string) string {
	if draft == "" {
		return app
	}
	return DraftSlug(app, Slugify(draft))
}

// fileValue is how one file travels in tool arguments and results: plain text,
// or {"base64": "..."} for files that are not text.
func encodeFile(data []byte) any {
	if utf8.Valid(data) {
		return string(data)
	}
	return map[string]string{"base64": base64.StdEncoding.EncodeToString(data)}
}

func decodeFiles(in map[string]json.RawMessage) (map[string][]byte, error) {
	out := make(map[string][]byte, len(in))
	for name, raw := range in {
		if string(raw) == "null" {
			out[name] = nil
			continue
		}
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			out[name] = []byte(text)
			continue
		}
		var obj struct {
			Base64 string `json:"base64"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil || obj.Base64 == "" {
			return nil, fmt.Errorf("file %q must be text, null (to delete it) or {\"base64\": \"...\"}", name)
		}
		data, err := base64.StdEncoding.DecodeString(obj.Base64)
		if err != nil {
			return nil, fmt.Errorf("file %q is not valid base64", name)
		}
		out[name] = data
	}
	return out, nil
}

type appView struct {
	*App
	URL       string   `json:"url"`
	Published bool     `json:"published"`
	Drafts    []string `json:"drafts,omitempty"`
}

func (p *Platform) view(a *App) appView {
	return appView{App: a, URL: p.URL(a.Slug), Published: a.LiveVersion != ""}
}

// Tools returns every tool, in the order they are listed to agents.
func Tools() []Tool {
	return []Tool{
		{
			Name:        "guide",
			Description: "Read this first. How apps on this server must be built (Python, plain HTML, relative links, database changes) and how to talk to the person you are helping.",
			InputSchema: schema(nil, nil),
			run: func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error) {
				return map[string]string{"guide": Guide}, nil
			},
		},
		{
			Name:        "list_apps",
			Description: "List the apps on this server with their addresses and drafts.",
			InputSchema: schema(nil, nil),
			run: func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error) {
				all, err := p.store.ListAll(ctx)
				if err != nil {
					return nil, err
				}
				drafts := map[string][]string{}
				for _, a := range all {
					if a.IsDraft() {
						_, d := SplitSlug(a.Slug)
						drafts[a.Parent] = append(drafts[a.Parent], d)
					}
				}
				out := []appView{}
				for _, a := range all {
					if !a.IsDraft() {
						v := p.view(a)
						v.Drafts = drafts[a.Slug]
						out = append(out, v)
					}
				}
				return map[string]any{"apps": out}, nil
			},
		},
		{
			Name:        "create_app",
			Description: "Create a new app from a small working starter and publish it. Returns its address. The name becomes part of the address.",
			InputSchema: schema([]string{"name"}, map[string]prop{
				"name":        str("A short human name, for example \"Team lunch orders\"."),
				"description": str("One sentence on what the app is for."),
			}),
			run: func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error) {
				args, err := decode[struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				}](raw)
				if err != nil {
					return nil, err
				}
				a, res, err := p.CreateApp(ctx, args.Name, args.Description, who)
				if err != nil {
					return nil, err
				}
				return map[string]any{"app": p.view(a), "publish": res}, nil
			},
		},
		{
			Name:        "get_app",
			Description: "Details of one app: address, whether it is published, its drafts, recent versions and the names of its settings.",
			InputSchema: schema([]string{"app"}, map[string]prop{"app": appProp, "draft": draftProp}),
			run: func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error) {
				args, err := decode[struct{ App, Draft string }](raw)
				if err != nil {
					return nil, err
				}
				slug := servingSlug(args.App, args.Draft)
				a, err := p.requireApp(ctx, slug)
				if err != nil {
					return nil, err
				}
				v := p.view(a)
				if !a.IsDraft() {
					drafts, err := p.store.ListDrafts(ctx, a.Slug)
					if err != nil {
						return nil, err
					}
					for _, d := range drafts {
						_, name := SplitSlug(d.Slug)
						v.Drafts = append(v.Drafts, name)
					}
				}
				versions, err := p.Versions(ctx, slug, 10)
				if err != nil {
					return nil, err
				}
				settings, err := p.SettingNames(ctx, slug)
				if err != nil {
					return nil, err
				}
				return map[string]any{"app": v, "versions": versions, "settings": settings}, nil
			},
		},
		{
			Name:        "read_files",
			Description: "Read the app's files as of its latest saved version (or a given version). Give paths to read only some files.",
			InputSchema: schema([]string{"app"}, map[string]prop{
				"app": appProp, "draft": draftProp,
				"version": str("A version id from list_versions. Leave out for the latest."),
				"paths":   {"type": "array", "items": map[string]any{"type": "string"}, "description": "Only these files. Leave out for all."},
			}),
			run: func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error) {
				args, err := decode[struct {
					App, Draft, Version string
					Paths               []string
				}](raw)
				if err != nil {
					return nil, err
				}
				version, files, err := p.ReadFiles(ctx, args.App, Slugify(args.Draft), args.Version, args.Paths)
				if err != nil {
					return nil, err
				}
				out := make(map[string]any, len(files))
				for name, data := range files {
					out[name] = encodeFile(data)
				}
				return map[string]any{"version": version, "files": out}, nil
			},
		},
		{
			Name:        "save_files",
			Description: "Save changed files as a new version and (unless publish is false) put it live. Send each file's full content; use null to delete a file. If publishing fails, nothing changes for people using the app and the error says what to fix.",
			InputSchema: schema([]string{"app", "files"}, map[string]prop{
				"app": appProp, "draft": draftProp,
				"files": {
					"type":                 "object",
					"description":          "Map of file path to content. Text as a string, null to delete, or {\"base64\": \"...\"} for non-text files.",
					"additionalProperties": true,
				},
				"message": str("One short sentence describing the change, in plain language."),
				"publish": {"type": "boolean", "description": "Put the new version live straight away. Default true."},
			}),
			run: func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error) {
				args, err := decode[struct {
					App, Draft, Message string
					Files               map[string]json.RawMessage
					Publish             *bool
				}](raw)
				if err != nil {
					return nil, err
				}
				changes, err := decodeFiles(args.Files)
				if err != nil {
					return nil, err
				}
				draft := Slugify(args.Draft)
				version, err := p.SaveFiles(ctx, args.App, draft, changes, args.Message, who)
				if err != nil {
					return nil, err
				}
				out := map[string]any{"version": version}
				if args.Publish == nil || *args.Publish {
					res, err := p.Publish(ctx, servingSlug(args.App, draft), version, who)
					if err != nil {
						return nil, err
					}
					out["publish"] = res
				}
				return out, nil
			},
		},
		{
			Name:        "publish",
			Description: "Put a saved version live. Without a version, the latest saved one.",
			InputSchema: schema([]string{"app"}, map[string]prop{"app": appProp, "draft": draftProp, "version": str("A version id from list_versions.")}),
			run: func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error) {
				args, err := decode[struct{ App, Draft, Version string }](raw)
				if err != nil {
					return nil, err
				}
				return p.Publish(ctx, servingSlug(args.App, args.Draft), args.Version, who)
			},
		},
		{
			Name:        "list_versions",
			Description: "The app's saved versions, newest first.",
			InputSchema: schema([]string{"app"}, map[string]prop{"app": appProp, "draft": draftProp}),
			run: func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error) {
				args, err := decode[struct{ App, Draft string }](raw)
				if err != nil {
					return nil, err
				}
				slug := servingSlug(args.App, args.Draft)
				versions, err := p.Versions(ctx, slug, 50)
				if err != nil {
					return nil, err
				}
				a, err := p.requireApp(ctx, slug)
				if err != nil {
					return nil, err
				}
				return map[string]any{"live_version": a.LiveVersion, "versions": versions}, nil
			},
		},
		{
			Name:        "roll_back",
			Description: "Go back to an earlier version: its files become the latest version and are published. Database changes made since are not undone.",
			InputSchema: schema([]string{"app", "version"}, map[string]prop{"app": appProp, "draft": draftProp, "version": str("The version id to go back to.")}),
			run: func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error) {
				args, err := decode[struct{ App, Draft, Version string }](raw)
				if err != nil {
					return nil, err
				}
				return p.RollBack(ctx, servingSlug(args.App, args.Draft), args.Version, who)
			},
		},
		{
			Name:        "create_draft",
			Description: "Make a draft: a private copy of the app with a copy of its data at its own address, for trying changes safely. Save to it by passing its name as draft.",
			InputSchema: schema([]string{"app", "name"}, map[string]prop{"app": appProp, "name": str("A short name for the draft, for example \"new form\".")}),
			run: func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error) {
				args, err := decode[struct{ App, Name string }](raw)
				if err != nil {
					return nil, err
				}
				return p.CreateDraft(ctx, args.App, args.Name, who)
			},
		},
		{
			Name:        "publish_draft",
			Description: "Make a draft's files the live app and remove the draft. The draft's data is discarded; its database changes (migrations) are applied to the live data.",
			InputSchema: schema([]string{"app", "draft"}, map[string]prop{"app": appProp, "draft": str("The draft's name.")}),
			run: func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error) {
				args, err := decode[struct{ App, Draft string }](raw)
				if err != nil {
					return nil, err
				}
				return p.PublishDraft(ctx, args.App, args.Draft, who)
			},
		},
		{
			Name:        "delete_draft",
			Description: "Remove a draft and its copy of the data. The app itself is not affected.",
			Destructive: true,
			InputSchema: schema([]string{"app", "draft"}, map[string]prop{"app": appProp, "draft": str("The draft's name.")}),
			run: func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error) {
				args, err := decode[struct{ App, Draft string }](raw)
				if err != nil {
					return nil, err
				}
				if Slugify(args.Draft) == "" {
					return nil, errors.New("say which draft to remove")
				}
				slug := servingSlug(args.App, args.Draft)
				if err := p.DeleteApp(ctx, slug); err != nil {
					return nil, err
				}
				return map[string]any{"removed": slug}, nil
			},
		},
		{
			Name:        "get_logs",
			Description: "Recent output of the app: errors with tracebacks, anything it printed, and publish results. Check this after publishing or when something does not work.",
			InputSchema: schema([]string{"app"}, map[string]prop{
				"app": appProp, "draft": draftProp,
				"lines": {"type": "integer", "description": "How many lines, newest last. Default 100."},
			}),
			run: func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error) {
				args, err := decode[struct {
					App, Draft string
					Lines      int
				}](raw)
				if err != nil {
					return nil, err
				}
				slug := servingSlug(args.App, args.Draft)
				if _, err := p.requireApp(ctx, slug); err != nil {
					return nil, err
				}
				if args.Lines <= 0 {
					args.Lines = 100
				}
				return map[string]any{"lines": p.Logs(slug, args.Lines)}, nil
			},
		},
		{
			Name:        "set_setting",
			Description: "Store a setting (an API token, a password, an address) the app reads as an environment variable. Use null as the value to remove it. The app restarts to pick it up. Values are never shown again.",
			InputSchema: schema([]string{"app", "name"}, map[string]prop{
				"app": appProp, "draft": draftProp,
				"name":  str("Uppercase with underscores, for example API_TOKEN."),
				"value": {"type": []string{"string", "null"}, "description": "The value, or null to remove the setting."},
			}),
			run: func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error) {
				args, err := decode[struct {
					App, Draft, Name string
					Value            *string
				}](raw)
				if err != nil {
					return nil, err
				}
				slug := servingSlug(args.App, args.Draft)
				if err := p.SetSetting(ctx, slug, args.Name, args.Value); err != nil {
					return nil, err
				}
				names, err := p.SettingNames(ctx, slug)
				return map[string]any{"settings": names}, err
			},
		},
		{
			Name:        "run_sql",
			Description: "Run one SQL statement against the app's own database, to look at data or check the tables. At most 200 rows come back. Change the shape of the data with migration files, not with this.",
			InputSchema: schema([]string{"app", "sql"}, map[string]prop{"app": appProp, "draft": draftProp, "sql": str("One SQL statement.")}),
			run: func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error) {
				args, err := decode[struct{ App, Draft, SQL string }](raw)
				if err != nil {
					return nil, err
				}
				a, err := p.requireApp(ctx, servingSlug(args.App, args.Draft))
				if err != nil {
					return nil, err
				}
				return runSQL(ctx, a.DatabaseURL, args.SQL)
			},
		},
		{
			Name:        "get_status",
			Description: "Which apps are awake right now and how much memory each uses.",
			InputSchema: schema(nil, nil),
			run: func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error) {
				type awake struct {
					App            string `json:"app"`
					MemoryMB       int64  `json:"memory_mb"`
					ActiveRequests int    `json:"active_requests"`
				}
				out := []awake{}
				for _, info := range caddysnake.LoadedDynamicApps() {
					if slug := p.slugFromDir(info.WorkingDir); slug != "" {
						mb := int64(-1)
						if info.MemoryBytes >= 0 {
							mb = info.MemoryBytes >> 20
						}
						out = append(out, awake{App: slug, MemoryMB: mb, ActiveRequests: info.ActiveRequests})
					}
				}
				sort.Slice(out, func(i, j int) bool { return out[i].App < out[j].App })
				return map[string]any{"awake": out}, nil
			},
		},
		{
			Name:        "delete_app",
			Description: "Permanently remove an app with its drafts, all its versions and all its data. This cannot be undone. Only do this when the person has clearly asked for it.",
			Destructive: true,
			InputSchema: schema([]string{"app", "confirm"}, map[string]prop{"app": appProp, "confirm": str("Repeat the app's name exactly to confirm.")}),
			run: func(ctx context.Context, p *Platform, who string, raw json.RawMessage) (any, error) {
				args, err := decode[struct{ App, Confirm string }](raw)
				if err != nil {
					return nil, err
				}
				if args.Confirm != args.App {
					return nil, errors.New("to remove an app, pass its exact name again as confirm")
				}
				if _, draft := SplitSlug(args.App); draft != "" {
					return nil, errors.New("that is a draft; use delete_draft")
				}
				if err := p.DeleteApp(ctx, args.App); err != nil {
					return nil, err
				}
				return map[string]any{"removed": args.App}, nil
			},
		},
	}
}

// Call runs a tool by name.
func (p *Platform) Call(ctx context.Context, name, who string, args json.RawMessage) (any, error) {
	for _, t := range Tools() {
		if t.Name == name {
			return t.run(ctx, p, who, args)
		}
	}
	return nil, fmt.Errorf("there is no tool called %q", name)
}

const (
	maxSQLRows    = 200
	sqlTimeout    = 30 * time.Second
	maxSQLCellLen = 2000
)

// runSQL executes one statement supplied by the caller in an app's own
// database, as that app's login.
func runSQL(ctx context.Context, databaseURL, statement string) (any, error) {
	if strings.TrimSpace(statement) == "" {
		return nil, errors.New("the SQL statement is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, sqlTimeout)
	defer cancel()
	conn, err := openAppDB(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connecting to the app's database: %w", err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	truncated := false
	for rows.Next() {
		if len(out) >= maxSQLRows {
			truncated = true
			break
		}
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = jsonValue(values[i])
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return map[string]any{"columns": cols, "rows": out, "truncated": truncated}, nil
}

func jsonValue(v any) any {
	switch x := v.(type) {
	case []byte:
		s := string(x)
		if !utf8.ValidString(s) {
			return map[string]string{"base64": base64.StdEncoding.EncodeToString(x)}
		}
		return clip(s)
	case string:
		return clip(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	default:
		return v
	}
}

func clip(s string) string {
	if len(s) > maxSQLCellLen {
		return s[:maxSQLCellLen] + "…"
	}
	return s
}
