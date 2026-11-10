package apps

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// signIn returns a session cookie for a person, without going through the form.
func signIn(t *testing.T, p *Platform, username, password string) *http.Cookie {
	t.Helper()
	token, _, err := p.Login(context.Background(), username, password)
	if err != nil {
		t.Fatalf("Login(%s): %v", username, err)
	}
	return &http.Cookie{Name: sessionCookie, Value: token}
}

func TestPeopleKeysAndPages(t *testing.T) {
	h, serve, _ := newTestHandler(t, true)
	p := h.platform
	ctx := context.Background()
	for name, admin := range map[string]bool{"ana": true, "bob": false} {
		if err := p.AddUser(ctx, name, "password for "+name, admin); err != nil {
			t.Fatal(err)
		}
	}
	ana := signIn(t, p, "ana", "password for ana")
	bob := signIn(t, p, "bob", "password for bob")

	as := func(c *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
		r := request(method, path, body)
		if c != nil {
			r.AddCookie(c)
		}
		return serve(r)
	}
	manage := func(c *http.Cookie, action string, body any) (int, map[string]any) {
		raw, _ := json.Marshal(body)
		rec := as(c, http.MethodPost, "/_apps/api/manage/"+action, string(raw))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// The people page: administrators see everyone, others are told why not.
	page := as(ana, http.MethodGet, "/_apps/people", "").Body.String()
	if !strings.Contains(page, "bob") || !strings.Contains(page, `onclick="addPerson(this)"`) || !strings.Contains(page, "Administrator") {
		t.Fatalf("administrator's people page is missing content")
	}
	page = as(bob, http.MethodGet, "/_apps/people", "").Body.String()
	if !strings.Contains(page, "Administrators only") || strings.Contains(page, `onclick="addPerson(this)"`) {
		t.Fatalf("non-administrator's people page is wrong")
	}

	// Only administrators manage people.
	if code, _ := manage(bob, "people/add", map[string]any{"username": "eve", "password": "long enough pw", "admin": true}); code != http.StatusForbidden {
		t.Fatalf("non-administrator adding a person = %d, want 403", code)
	}
	if code, _ := manage(nil, "people/add", map[string]any{"username": "eve", "password": "long enough pw"}); code != http.StatusUnauthorized {
		t.Fatalf("anonymous adding a person = %d, want 401", code)
	}
	if code, out := manage(ana, "people/add", map[string]any{"username": "carol", "password": "short"}); code != http.StatusUnprocessableEntity || !strings.Contains(out["error"].(string), "at least 8") {
		t.Fatalf("short password = %d %v", code, out)
	}
	if code, out := manage(ana, "people/add", map[string]any{"username": "carol", "password": "long enough pw"}); code != http.StatusOK {
		t.Fatalf("adding a person = %d %v", code, out)
	}
	if code, _ := manage(ana, "people/remove", map[string]any{"username": "ana"}); code != http.StatusUnprocessableEntity {
		t.Fatalf("removing yourself = %d, want 422", code)
	}

	// A new password signs the person out everywhere.
	if rec := as(bob, http.MethodGet, "/", ""); rec.Code != http.StatusOK {
		t.Fatalf("bob before password change = %d", rec.Code)
	}
	if code, out := manage(ana, "people/password", map[string]any{"username": "bob", "password": "a different password"}); code != http.StatusOK {
		t.Fatalf("setting a password = %d %v", code, out)
	}
	if rec := as(bob, http.MethodGet, "/", ""); rec.Code != http.StatusSeeOther {
		t.Fatalf("bob's old session still works after a password change: %d", rec.Code)
	}
	bob = signIn(t, p, "bob", "a different password")

	// Access keys: created for yourself, shown once, listed by name, revocable.
	code, out := manage(bob, "keys/create", map[string]any{"name": "laptop"})
	key, _ := out["key"].(string)
	if code != http.StatusOK || !strings.HasPrefix(key, accessKeyPrefix) {
		t.Fatalf("creating a key = %d %v", code, out)
	}
	withKey := request(http.MethodPost, "/_apps/api/tools/list_apps", "{}")
	withKey.Header.Set("Authorization", "Bearer "+key)
	if rec := serve(withKey); rec.Code != http.StatusOK {
		t.Fatalf("new key refused: %d", rec.Code)
	}
	connect := as(bob, http.MethodGet, "/_apps/connect", "").Body.String()
	if !strings.Contains(connect, "laptop") || strings.Contains(connect, key) {
		t.Fatalf("connect page should list the key by name and never show it again")
	}
	if strings.Contains(as(ana, http.MethodGet, "/_apps/connect", "").Body.String(), "laptop") {
		t.Fatal("one person's keys are shown to another")
	}
	if code, _ := manage(bob, "keys/revoke", map[string]any{"name": "laptop"}); code != http.StatusOK {
		t.Fatalf("revoking a key = %d", code)
	}
	withKey = request(http.MethodPost, "/_apps/api/tools/list_apps", "{}")
	withKey.Header.Set("Authorization", "Bearer "+key)
	if rec := serve(withKey); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key still works: %d", rec.Code)
	}

	// Removing a person stops their session.
	if code, _ := manage(ana, "people/remove", map[string]any{"username": "bob"}); code != http.StatusOK {
		t.Fatalf("removing a person = %d", code)
	}
	if rec := as(bob, http.MethodGet, "/", ""); rec.Code != http.StatusSeeOther {
		t.Fatalf("removed person's session still works: %d", rec.Code)
	}

	// The page for one app.
	app, _, err := p.CreateApp(ctx, "Notes", "Team notes", "ana")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.SaveFiles(ctx, app.Slug, "", plainFiles("1"), "First real version", "ana"); err != nil {
		t.Fatal(err)
	}
	if res, err := p.Publish(ctx, app.Slug, "", "ana"); err != nil || !res.OK {
		t.Fatalf("publish: %v %+v", err, res)
	}
	token := "secret-value-123"
	if err := p.SetSetting(ctx, app.Slug, "API_TOKEN", &token); err != nil {
		t.Fatal(err)
	}
	draft, err := p.CreateDraft(ctx, app.Slug, "new look", "ana")
	if err != nil {
		t.Fatal(err)
	}
	rec := as(ana, http.MethodGet, "/_apps/app/notes", "")
	body := rec.Body.String()
	for _, want := range []string{"First real version", "Start from the template", "API_TOKEN", "new-look", "published version", `onclick="removeThis(this)"`, "Details", `onclick="goBack(this)"`, `onclick="newDraft(this)"`} {
		if !strings.Contains(body, want) {
			t.Errorf("app page is missing %q", want)
		}
	}
	if strings.Contains(body, token) {
		t.Error("app page shows a setting's value")
	}
	draftPage := as(ana, http.MethodGet, "/_apps/app/"+draft.Draft.Slug, "").Body.String()
	if !strings.Contains(draftPage, `onclick="publishDraft(this)"`) || !strings.Contains(draftPage, "Draft of") || strings.Contains(draftPage, `onclick="newDraft(this)"`) {
		t.Error("draft page has the wrong actions")
	}
	if rec := as(ana, http.MethodGet, "/_apps/app/does-not-exist", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown app page = %d, want 404", rec.Code)
	}
	if rec := as(nil, http.MethodGet, "/_apps/app/notes", ""); rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous app page = %d, want a redirect to sign in", rec.Code)
	}

	// No page uses the middle dot character (maintainer's rule, see AGENTS.md).
	for _, path := range []string{"/", "/_apps/app/notes", "/_apps/connect", "/_apps/people"} {
		if strings.Contains(as(ana, http.MethodGet, path, "").Body.String(), string(rune(0xB7))) {
			t.Errorf("%s contains the middle dot character", path)
		}
	}
}

func TestManageIsClosedOnAnOpenServer(t *testing.T) {
	_, serve, _ := newTestHandler(t, false)
	rec := serve(request(http.MethodPost, "/_apps/api/manage/people/add", `{"username":"eve","password":"long enough pw","admin":true}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("an open server let someone create a person over the web: %d", rec.Code)
	}
	page := serve(request(http.MethodGet, "/_apps/people", "")).Body.String()
	if !strings.Contains(page, "Sign-in is off") {
		t.Fatal("people page does not say that sign-in is off")
	}
	if rec := serve(request(http.MethodGet, "/_apps/connect", "")); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/_apps/mcp") {
		t.Fatalf("connect page = %d", rec.Code)
	}
}

func TestAgo(t *testing.T) {
	now := time.Now()
	cases := map[time.Duration]string{
		10 * time.Second: "just now",
		90 * time.Second: "a minute ago",
		10 * time.Minute: "10 minutes ago",
		90 * time.Minute: "an hour ago",
		5 * time.Hour:    "5 hours ago",
		30 * time.Hour:   "yesterday",
		72 * time.Hour:   "3 days ago",
	}
	for d, want := range cases {
		if got := ago(now.Add(-d)); got != want {
			t.Errorf("ago(%v) = %q, want %q", d, got, want)
		}
	}
	if !looksLikeError("Traceback (most recent call last):") || looksLikeError("hello from the app") {
		t.Error("looksLikeError misclassifies lines")
	}
}
