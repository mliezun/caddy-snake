package apps

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// newTestPlatform starts a platform with its own built-in Postgres in a temp
// folder. It skips when Postgres or Python are not installed.
func newTestPlatform(t *testing.T) *Platform {
	t.Helper()
	if testing.Short() {
		t.Skip("starts Postgres and builds Python environments")
	}
	if findPostgresBin() == "" {
		t.Skip("Postgres is not installed")
	}
	if _, err := exec.LookPath(defaultPythonFor(runtime.GOOS)); err != nil {
		t.Skip("Python is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	p, err := Open(ctx, Config{DataDir: t.TempDir(), PublicURL: "http://apps.test"}, zap.NewNop())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// mustOK returns a checker for the (result, error) pair publish calls return.
func mustOK(t *testing.T) func(*PublishResult, error) *PublishResult {
	return func(res *PublishResult, err error) *PublishResult {
		t.Helper()
		if err != nil {
			t.Fatalf("publish: %v", err)
		}
		if !res.OK {
			t.Fatalf("publish failed: %s\nsteps: %v", res.Error, res.Steps)
		}
		return res
	}
}

// plainApp is a dependency-free WSGI app, so tests do not need the network.
const plainApp = `import os


def app(environ, start_response):
    body = ("v%s " + os.environ["APP_SLUG"]).encode()
    start_response("200 OK", [("Content-Type", "text/plain")])
    return [body]
`

func plainFiles(version string) map[string][]byte {
	return map[string][]byte{
		"main.py":          []byte(strings.Replace(plainApp, "%s", version, 1)),
		"requirements.txt": nil, // remove the starter's flask requirement
		"static/style.css": nil,
	}
}

func currentRelease(t *testing.T, p *Platform, slug string) string {
	t.Helper()
	target, err := filepath.EvalSymlinks(filepath.Join(p.appDir(slug), "current"))
	if err != nil {
		t.Fatalf("current link for %s: %v", slug, err)
	}
	return target
}

func TestPlatformLifecycle(t *testing.T) {
	p := newTestPlatform(t)
	ctx := context.Background()

	// Creating an app saves the starter template; publishing it needs Flask from
	// the network, so replace the files with a dependency-free app first.
	app, first, err := p.CreateApp(ctx, "Team Lunch Orders!", "who wants what", "ana")
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if app.Slug != "team-lunch-orders" {
		t.Fatalf("slug = %q", app.Slug)
	}
	if exists, _ := p.Known(app.Slug); !exists {
		t.Fatal("new app is not known to the gate")
	}
	t.Logf("starter publish ok=%v (needs network for flask): %s", first.OK, first.Error)

	if _, _, err := p.CreateApp(ctx, "team lunch orders", "", "ana"); err == nil {
		t.Fatal("expected an error creating a second app with the same name")
	}

	v1, err := p.SaveFiles(ctx, app.Slug, "", plainFiles("1"), "First real version", "ana")
	if err != nil {
		t.Fatalf("SaveFiles: %v", err)
	}
	mustOK(t)(p.Publish(ctx, app.Slug, "", "ana"))
	if _, published := p.Known(app.Slug); !published {
		t.Fatal("app is not marked published")
	}
	rel1 := currentRelease(t, p, app.Slug)
	if _, err := os.Stat(venvPython(rel1)); err != nil {
		t.Fatalf("release has no python environment: %v", err)
	}
	settings, err := os.ReadFile(p.settingsPath(app.Slug))
	if err != nil || !strings.Contains(string(settings), `APP_BASE_PATH="/team-lunch-orders"`) || !strings.Contains(string(settings), "DATABASE_URL=") {
		t.Fatalf("settings file: %v\n%s", err, settings)
	}

	// A migration is applied once, and data survives later publishes.
	files := map[string][]byte{
		"migrations/001_orders.sql": []byte("CREATE TABLE orders (id serial PRIMARY KEY, dish text NOT NULL);\nINSERT INTO orders (dish) VALUES ('soup');"),
	}
	if _, err := p.SaveFiles(ctx, app.Slug, "", files, "Add orders", "ana"); err != nil {
		t.Fatal(err)
	}
	res := mustOK(t)(p.Publish(ctx, app.Slug, "", "ana"))
	if !strings.Contains(strings.Join(res.Steps, "\n"), "001_orders.sql") {
		t.Fatalf("steps do not mention the migration: %v", res.Steps)
	}
	res = mustOK(t)(p.Publish(ctx, app.Slug, "", "ana"))
	if strings.Contains(strings.Join(res.Steps, "\n"), "001_orders.sql") {
		t.Fatalf("migration was applied twice: %v", res.Steps)
	}
	if got := countRows(t, app.DatabaseURL, "orders"); got != 1 {
		t.Fatalf("orders rows = %d, want 1", got)
	}

	// A version that does not start is refused and the old one stays live.
	before := currentRelease(t, p, app.Slug)
	if _, err := p.SaveFiles(ctx, app.Slug, "", map[string][]byte{"main.py": []byte("import nonexistent_module_xyz\n")}, "Broken", "ana"); err != nil {
		t.Fatal(err)
	}
	bad, err := p.Publish(ctx, app.Slug, "", "ana")
	if err != nil {
		t.Fatal(err)
	}
	if bad.OK || !strings.Contains(bad.Error, "nonexistent_module_xyz") {
		t.Fatalf("expected a failed publish naming the missing module, got ok=%v error=%q", bad.OK, bad.Error)
	}
	if after := currentRelease(t, p, app.Slug); after != before {
		t.Fatal("a failed publish changed the live version")
	}

	// A broken migration is refused too.
	if _, err := p.SaveFiles(ctx, app.Slug, "", map[string][]byte{
		"main.py":                []byte(strings.Replace(plainApp, "%s", "3", 1)),
		"migrations/002_bad.sql": []byte("ALTER TABLE no_such_table ADD COLUMN x int;"),
	}, "Bad migration", "ana"); err != nil {
		t.Fatal(err)
	}
	bad, _ = p.Publish(ctx, app.Slug, "", "ana")
	if bad.OK || !strings.Contains(bad.Error, "002_bad.sql") {
		t.Fatalf("expected the bad migration to fail the publish, got ok=%v error=%q", bad.OK, bad.Error)
	}
	if after := currentRelease(t, p, app.Slug); after != before {
		t.Fatal("a failed migration changed the live version")
	}

	// Going back restores the old files as a new version and publishes it.
	back := mustOK(t)(p.RollBack(ctx, app.Slug, v1, "ana"))
	_, got, err := p.ReadFiles(ctx, app.Slug, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, has := got["migrations/002_bad.sql"]; has || !strings.Contains(string(got["main.py"]), `"v1 "`) {
		t.Fatalf("roll back did not restore version 1 files: %v", keys(got))
	}
	versions, err := p.Versions(ctx, app.Slug, 50)
	if err != nil || len(versions) < 5 || versions[0].ID != back.Version {
		t.Fatalf("versions = %+v, err %v", versions, err)
	}

	// Settings reach the settings file; reserved names are refused.
	token := "s3cret value"
	if err := p.SetSetting(ctx, app.Slug, "API_TOKEN", &token); err != nil {
		t.Fatal(err)
	}
	if err := p.SetSetting(ctx, app.Slug, "DATABASE_URL", &token); err == nil {
		t.Fatal("expected DATABASE_URL to be refused as a setting")
	}
	settings, _ = os.ReadFile(p.settingsPath(app.Slug))
	if !strings.Contains(string(settings), `API_TOKEN="s3cret value"`) {
		t.Fatalf("setting missing from file:\n%s", settings)
	}

	// A draft is a copy with its own data.
	draft, err := p.CreateDraft(ctx, app.Slug, "New Form", "ana")
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if draft.Draft.Slug != "team-lunch-orders--new-form" || !draft.Publish.OK {
		t.Fatalf("draft = %+v publish = %+v", draft.Draft, draft.Publish)
	}
	if draft.DataCopied {
		if got := countRows(t, draft.Draft.DatabaseURL, "orders"); got != 1 {
			t.Fatalf("draft orders rows = %d, want the copied 1", got)
		}
	}
	draftSettings, _ := os.ReadFile(p.settingsPath(draft.Draft.Slug))
	if !strings.Contains(string(draftSettings), `APP_IS_DRAFT="1"`) || !strings.Contains(string(draftSettings), "API_TOKEN=") {
		t.Fatalf("draft settings:\n%s", draftSettings)
	}

	// Changes in the draft do not touch the live app until it is published.
	if _, err := p.SaveFiles(ctx, app.Slug, "new-form", map[string][]byte{
		"main.py":                     []byte(strings.Replace(plainApp, "%s", "9", 1)),
		"migrations/003_due_date.sql": []byte("ALTER TABLE orders ADD COLUMN due date;"),
	}, "Draft change", "ana"); err != nil {
		t.Fatal(err)
	}
	mustOK(t)(p.Publish(ctx, draft.Draft.Slug, "", "ana"))
	_, live, _ := p.ReadFiles(ctx, app.Slug, "", "", nil)
	if strings.Contains(string(live["main.py"]), `"v9 "`) {
		t.Fatal("draft change leaked into the live app")
	}

	final := mustOK(t)(p.PublishDraft(ctx, app.Slug, "new-form", "ana"))
	if !strings.Contains(strings.Join(final.Steps, "\n"), "003_due_date.sql") {
		t.Fatalf("publishing the draft did not apply its migration to live data: %v", final.Steps)
	}
	_, live, _ = p.ReadFiles(ctx, app.Slug, "", "", nil)
	if !strings.Contains(string(live["main.py"]), `"v9 "`) {
		t.Fatal("live app does not have the draft's files after publish_draft")
	}
	if exists, _ := p.Known("team-lunch-orders--new-form"); exists {
		t.Fatal("draft still exists after being published")
	}

	// Deleting removes everything.
	if err := p.DeleteApp(ctx, app.Slug); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}
	if exists, _ := p.Known(app.Slug); exists {
		t.Fatal("app still known after delete")
	}
	for _, path := range []string{p.appDir(app.Slug), p.repoDir(app.Slug)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after delete", path)
		}
	}
	if list, _ := p.store.ListAll(ctx); len(list) != 0 {
		t.Fatalf("records left after delete: %+v", list)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func countRows(t *testing.T, databaseURL, table string) int {
	t.Helper()
	conn, err := openAppDB(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("connecting to app database: %v", err)
	}
	defer conn.Close()
	var n int
	// Test-only query on a fixed table name.
	if err := conn.QueryRow("SELECT count(*) FROM " + quoteIdent(table)).Scan(&n); err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return n
}

func TestSlugs(t *testing.T) {
	cases := map[string]string{
		"Team Lunch Orders!":  "team-lunch-orders",
		"  --Hello__World-- ": "hello-world",
		"2024 budget":         "app-2024-budget",
		"çà et là":            "et-l",
		"???":                 "",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "Has-Caps", "double--hyphen", "_apps", "static", strings.Repeat("a", 31), "1abc", "trailing-"} {
		if err := ValidateAppSlug(bad); err == nil {
			t.Errorf("ValidateAppSlug(%q) expected an error", bad)
		}
	}
	if err := ValidateServingSlug("inventory--new-form"); err != nil {
		t.Errorf("draft slug rejected: %v", err)
	}
	if app, draft := SplitSlug("inventory--new-form"); app != "inventory" || draft != "new-form" {
		t.Errorf("SplitSlug = %q, %q", app, draft)
	}
	if got := dbName("inventory--new-form"); got != "app_inventory__new_form" {
		t.Errorf("dbName = %q", got)
	}
}

func TestCleanPath(t *testing.T) {
	for _, ok := range []string{"main.py", "templates/index.html", "./static/app.js"} {
		if _, err := cleanPath(ok); err != nil {
			t.Errorf("cleanPath(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "/etc/passwd", "../x", "a/../../x", ".git/config", "x/.venv/y", "a//b"} {
		if got, err := cleanPath(bad); err == nil {
			t.Errorf("cleanPath(%q) = %q, expected an error", bad, got)
		}
	}
}
