package apps

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the boundaries the platform has to hold: what a signed-in person,
// an app, another website and someone who is not signed in must not be able
// to do. Each one fails without the check it names.

// The SQL tool and an app's migration files reach that app's data only: not
// the platform's records, not another app's data, not any other file.
func TestAppDataConnectionsCannotReachOtherFiles(t *testing.T) {
	p := newTestPlatform(t)
	ctx := context.Background()
	app, _, err := p.CreateApp(ctx, "Mallory", "", "mallory")
	if err != nil {
		t.Fatal(err)
	}
	data := p.appDataPath(app.Slug)
	platformDB := filepath.Join(p.cfg.DataDir, platformDBName)

	if _, err := runSQL(ctx, data, "CREATE TABLE notes (body varchar(20))"); err != nil {
		t.Fatalf("an ordinary statement was refused: %v", err)
	}
	if _, err := runSQL(ctx, data, "SELECT count(*) FROM notes"); err != nil {
		t.Fatalf("an ordinary query was refused: %v", err)
	}

	for _, statement := range []string{
		"ATTACH DATABASE '" + platformDB + "' AS p",
		"ATTACH DATABASE '" + platformDB + "' AS p; SELECT username, password_hash FROM p.users",
		"ATTACH DATABASE '" + platformDB + "' AS p; UPDATE p.users SET is_admin = 1 RETURNING username",
		"DETACH DATABASE main",
	} {
		if _, err := runSQL(ctx, data, statement); err == nil {
			t.Errorf("run_sql accepted %q", statement)
		}
	}

	// A copy of the data written wherever the caller likes.
	stolen := filepath.Join(t.TempDir(), "copy.db")
	if _, err := runSQL(ctx, data, "VACUUM INTO '"+stolen+"'"); err == nil {
		t.Error("run_sql accepted VACUUM INTO")
	}
	if _, err := os.Stat(stolen); err == nil {
		t.Error("VACUUM INTO wrote a file")
	}

	// A migration file that ends the transaction it runs in, to do the same.
	files := plainFiles("1")
	files["migrations/001_escape.sql"] = []byte("COMMIT; ATTACH DATABASE '" + platformDB + "' AS p; UPDATE p.users SET is_admin = 1; BEGIN;")
	if _, err := p.SaveFiles(ctx, app.Slug, "", files, "escape", "mallory"); err != nil {
		t.Fatal(err)
	}
	res, err := p.Publish(ctx, app.Slug, "", "mallory")
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("a migration that attaches the platform's records was applied")
	}

	// The platform itself can still copy an app's data for a draft.
	if _, err := p.CreateDraft(ctx, app.Slug, "try", "mallory"); err != nil && !strings.Contains(err.Error(), "version") {
		t.Fatalf("CreateDraft: %v", err)
	}
	if _, err := os.Stat(p.appDataPath(DraftSlug(app.Slug, "try"))); err != nil {
		t.Fatalf("the draft's copy of the data was not made: %v", err)
	}
}

// An app is told who is using it, and is not handed how they signed in.
func TestAppsDoNotReceiveCredentials(t *testing.T) {
	h, serve, passed := newTestHandler(t, true)
	p := h.platform
	ctx := context.Background()
	if err := p.AddUser(ctx, "ana", "correct horse battery", true); err != nil {
		t.Fatal(err)
	}
	app, _, err := p.CreateApp(ctx, "Inventory", "", "ana")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.SaveFiles(ctx, app.Slug, "", plainFiles("1"), "plain", "ana"); err != nil {
		t.Fatal(err)
	}
	mustOK(t)(p.Publish(ctx, app.Slug, "", "ana"))
	session := signIn(t, p, "ana", "correct horse battery")
	key, err := p.CreateAccessKey(ctx, "ana", "assistant")
	if err != nil {
		t.Fatal(err)
	}

	// Through the browser: a session cookie next to a cookie of the app's own.
	r := request(http.MethodGet, "/"+app.Slug+"/", "")
	r.AddCookie(session)
	r.Header.Set("Cookie", r.Header.Get("Cookie")+"; theme=dark")
	r.Header.Set("X-Forwarded-User", "boss")
	r.Header["X_Forwarded_User"] = []string{"boss"}
	r.Header["x_forwarded_user"] = []string{"boss"}
	if rec := serve(r); rec.Code != http.StatusOK {
		t.Fatalf("signed-in request: %d %s", rec.Code, rec.Body.String())
	}
	got := (*passed)[len(*passed)-1]
	if cookie := got.Header.Get("Cookie"); strings.Contains(cookie, sessionCookie) || strings.Contains(cookie, session.Value) || cookie != "theme=dark" {
		t.Errorf("the app received Cookie %q", cookie)
	}
	for name, values := range got.Header {
		if strings.EqualFold(strings.ReplaceAll(name, "_", "-"), userHeader) && (name != userHeader || len(values) != 1 || values[0] != "ana") {
			t.Errorf("the app received %s: %v", name, values)
		}
	}
	if got.Header.Get(userHeader) != "ana" {
		t.Errorf("the app was not told who is using it: %q", got.Header.Get(userHeader))
	}

	// Through an access key.
	r = request(http.MethodGet, "/"+app.Slug+"/", "")
	r.Header.Set("Authorization", "Bearer "+key)
	if rec := serve(r); rec.Code != http.StatusOK {
		t.Fatalf("request with an access key: %d", rec.Code)
	}
	got = (*passed)[len(*passed)-1]
	if auth := got.Header.Get("Authorization"); auth != "" {
		t.Errorf("the app received Authorization %q", auth)
	}

	// An app's own way of signing in is left alone.
	r = request(http.MethodGet, "/"+app.Slug+"/", "")
	r.AddCookie(session)
	r.Header.Set("Authorization", "Bearer the-apps-own-token")
	serve(r)
	got = (*passed)[len(*passed)-1]
	if auth := got.Header.Get("Authorization"); auth != "Bearer the-apps-own-token" {
		t.Errorf("an Authorization header that is not ours was changed to %q", auth)
	}
}

// With sign-in off nobody is named, whatever the visitor sends.
func TestVisitorsCannotNameThemselves(t *testing.T) {
	h, serve, passed := newTestHandler(t, false)
	p := h.platform
	ctx := context.Background()
	app, _, err := p.CreateApp(ctx, "Inventory", "", "someone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.SaveFiles(ctx, app.Slug, "", plainFiles("1"), "plain", "someone"); err != nil {
		t.Fatal(err)
	}
	mustOK(t)(p.Publish(ctx, app.Slug, "", "someone"))
	r := request(http.MethodGet, "/"+app.Slug+"/", "")
	r.Header.Set("X-Forwarded-User", "boss")
	r.Header["X_Forwarded_User"] = []string{"boss"}
	r.Header["X_FORWARDED_USER"] = []string{"boss"}
	if rec := serve(r); rec.Code != http.StatusOK {
		t.Fatalf("request: %d", rec.Code)
	}
	for name, values := range (*passed)[len(*passed)-1].Header {
		if strings.EqualFold(strings.ReplaceAll(name, "_", "-"), userHeader) {
			t.Errorf("the app received %s: %v", name, values)
		}
	}

	// Apps answer only under the names the server is known by.
	r = request(http.MethodGet, "/"+app.Slug+"/", "")
	r.Host = "evil.example"
	if rec := serve(r); rec.Code != http.StatusMisdirectedRequest {
		t.Errorf("an app answered under another host name: %d", rec.Code)
	}
}

// Someone who is not signed in cannot tell which apps exist.
func TestSignedOutVisitorsLearnNothing(t *testing.T) {
	h, serve, _ := newTestHandler(t, true)
	p := h.platform
	ctx := context.Background()
	if _, _, err := p.CreateApp(ctx, "Payroll", "", "ana"); err != nil {
		t.Fatal(err)
	}
	real := serve(request(http.MethodGet, "/payroll/", ""))
	unknown := serve(request(http.MethodGet, "/no-such-app/", ""))
	if real.Code != http.StatusSeeOther || unknown.Code != http.StatusSeeOther {
		t.Fatalf("existing app: %d, unknown app: %d; both must send to sign-in", real.Code, unknown.Code)
	}
}

func TestVersionFilesStayInTheirFolder(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"main.py", "templates/index.html", "static/css/app.css"} {
		target, err := exportTarget(dir, name)
		if err != nil || !strings.HasPrefix(target, dir+string(filepath.Separator)) {
			t.Errorf("exportTarget(%q) = %q, %v", name, target, err)
		}
	}
	for _, name := range []string{
		"../platform.db", "a/../../platform.db", "../../../../etc/passwd", "/etc/passwd", "..", ".",
		"a//b", "./main.py", ".git/hooks/pre-receive", ".GIT/config", ".venv/bin/python", ".VENV/x", `..\platform.db`, "",
	} {
		if target, err := exportTarget(dir, name); err == nil {
			t.Errorf("exportTarget accepted %q -> %q", name, target)
		}
	}
}

func TestOnlyVersionIDsArePublished(t *testing.T) {
	p := newTestPlatform(t)
	ctx := context.Background()
	app, _, err := p.CreateApp(ctx, "Inventory", "", "ana")
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"main", "main~1", "HEAD", "main^{/../../../../tmp/x}", "../../x", "3f2a9c1", "ZZZZZZZZZZZZ"} {
		res, err := p.Publish(ctx, app.Slug, version, "ana")
		if err == nil && res.OK {
			t.Errorf("published %q", version)
		}
		if version != "3f2a9c1" && err == nil && !strings.Contains(res.Error, "is not a version id") {
			t.Errorf("%q: %q", version, res.Error)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(p.appDir(app.Slug), "releases"))
	for _, e := range entries {
		if !versionIDRE.MatchString(strings.SplitN(e.Name(), "-", 2)[0]) {
			t.Errorf("a release folder was made for something that is not a version id: %q", e.Name())
		}
	}
}

func TestAppNamesArePlainText(t *testing.T) {
	p := newTestPlatform(t)
	ctx := context.Background()
	for _, name := range []string{"x\nPYTHONPATH=\"/tmp/e", "x\rLD_PRELOAD=/tmp/e.so", "tab\tname", "bell\x07"} {
		if _, _, err := p.CreateApp(ctx, name, "", "ana"); err == nil {
			t.Errorf("CreateApp accepted the name %q", name)
		}
	}
	if _, _, err := p.CreateApp(ctx, "fine", "first line\nCADDYSNAKE_X=1", "ana"); err == nil {
		t.Error("CreateApp accepted a description with a line break")
	}
	// Quotes are ordinary characters in a name.
	app, _, err := p.CreateApp(ctx, `Bob's "best" app`, "", "ana")
	if err != nil {
		t.Fatalf("a name with quotes was refused: %v", err)
	}
	settings, err := os.ReadFile(p.settingsPath(app.Slug))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(settings)), "\n") {
		if !strings.HasPrefix(line, "#") && !strings.Contains(line, "=") {
			t.Errorf("settings file has a line that is not a setting: %q", line)
		}
	}
}

func TestAssistantEndpointTakesJSONOnly(t *testing.T) {
	_, serve, _ := newTestHandler(t, false)
	r := request(http.MethodPost, platformPrefix+"/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	r.Header.Set("Content-Type", "text/plain")
	if rec := serve(r); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("MCP accepted text/plain: %d", rec.Code)
	}
	r = request(http.MethodPost, platformPrefix+"/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rec := serve(r); rec.Code != http.StatusOK {
		t.Errorf("MCP refused JSON: %d %s", rec.Code, rec.Body.String())
	}
	page := serve(request(http.MethodGet, "/", ""))
	if page.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(page.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("dashboard pages are missing their protective headers: %v", page.Header())
	}
}

// A new password ends every way in that the old one could have made.
func TestNewPasswordEndsSessionsAndKeys(t *testing.T) {
	p := newTestPlatform(t)
	ctx := context.Background()
	if err := p.AddUser(ctx, "ana", "correct horse battery", false); err != nil {
		t.Fatal(err)
	}
	session := signIn(t, p, "ana", "correct horse battery")
	key, err := p.CreateAccessKey(ctx, "ana", "assistant")
	if err != nil {
		t.Fatal(err)
	}
	if p.SessionIdentity(ctx, session.Value) == nil || p.KeyIdentity(ctx, key) == nil {
		t.Fatal("the session and the key should work before the change")
	}
	if err := p.SetPassword(ctx, "ana", "a brand new password"); err != nil {
		t.Fatal(err)
	}
	if p.SessionIdentity(ctx, session.Value) != nil {
		t.Error("the old session still works")
	}
	if p.KeyIdentity(ctx, key) != nil {
		t.Error("the old access key still works")
	}
}
