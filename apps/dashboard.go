package apps

import (
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	caddysnake "github.com/mliezun/caddy-snake"
)

var pages = func() *template.Template {
	t := template.Must(template.New("layout").Funcs(template.FuncMap{"icon": icon}).Parse(layoutTemplates))
	for name, body := range map[string]string{
		"message": messagePage, "login": loginPage, "apps": appsPage,
		"app": appPage, "connect": connectPage, "people": peoplePage,
	} {
		template.Must(t.New(name).Parse(body))
	}
	return t
}()

// icon returns one of the inline icons by name.
func icon(name string) template.HTML {
	switch name {
	case "logo":
		return template.HTML(logoSVG)
	case "ok":
		return template.HTML(okSVG)
	case "warn":
		return template.HTML(warnSVG)
	default:
		return template.HTML(infoSVG)
	}
}

// crumb is one step of the breadcrumb trail; the last one has no URL.
type crumb struct{ Label, URL string }

// pageData is what every page template receives; pages add their own keys.
func pageData(title, nav string, id *Identity, requireLogin bool) map[string]any {
	data := map[string]any{
		"Title":        title,
		"Nav":          nav,
		"CSS":          template.CSS(styles),
		"JS":           template.JS(script),
		"Version":      ServerVersion,
		"Crumbs":       []crumb{{Label: title}},
		"User":         "",
		"Admin":        false,
		"RequireLogin": requireLogin,
	}
	if id != nil {
		data["User"] = id.Username
		data["Admin"] = id.Admin
	}
	return data
}

func render(w http.ResponseWriter, status int, name string, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.WriteHeader(status)
	_ = pages.ExecuteTemplate(w, name, data)
}

func renderMessage(w http.ResponseWriter, status int, title, text, linkURL, linkText string) {
	data := pageData(title, "", nil, false)
	data["Heading"], data["Text"], data["LinkURL"], data["LinkText"] = title, text, linkURL, linkText
	render(w, status, "message", data)
}

func renderLogin(w http.ResponseWriter, status int, next, errMsg string) {
	data := pageData("Sign in", "", nil, true)
	data["Next"], data["Error"] = next, errMsg
	render(w, status, "login", data)
}

// looksLikeError picks out the lines worth a person's attention; ordinary
// output is shown plainly even when the app wrote it to stderr.
func looksLikeError(line string) bool {
	for _, mark := range []string{"Traceback", "Error", "Exception", "failed", "  File \""} {
		if strings.Contains(line, mark) {
			return true
		}
	}
	return false
}

// ago describes a time the way a person would say it.
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < 2*time.Minute:
		return "a minute ago"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 2*time.Hour:
		return "an hour ago"
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	default:
		return t.Local().Format("2 Jan 2006")
	}
}

type dashboardDraft struct {
	Slug string
	Name string
	URL  string
}

type dashboardApp struct {
	Slug        string
	Name        string
	Description string
	URL         string
	Published   bool
	PublishedAt string
	Awake       bool
	Memory      string
	Drafts      []dashboardDraft
}

// awakeApps maps serving slug to memory in bytes (-1 if unknown) for apps
// that are loaded right now.
func (p *Platform) awakeApps() map[string]int64 {
	out := map[string]int64{}
	for _, info := range caddysnake.LoadedDynamicApps() {
		if slug := p.slugFromDir(info.WorkingDir); slug != "" {
			out[slug] = info.MemoryBytes
		}
	}
	return out
}

func megabytes(n int64) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("%d MB", (n+(1<<19))>>20)
}

func (h *Handler) serveDashboard(w http.ResponseWriter, r *http.Request, id *Identity) {
	p := h.platform
	all, err := p.store.ListAll(r.Context())
	if err != nil {
		renderMessage(w, http.StatusInternalServerError, "Something went wrong", "The list of apps could not be loaded. Try again in a moment.", "", "")
		return
	}
	drafts := map[string][]dashboardDraft{}
	for _, a := range all {
		if a.IsDraft() {
			_, name := SplitSlug(a.Slug)
			drafts[a.Parent] = append(drafts[a.Parent], dashboardDraft{Slug: a.Slug, Name: name, URL: "/" + a.Slug + "/"})
		}
	}
	awake := p.awakeApps()
	apps := []dashboardApp{}
	for _, a := range all {
		if a.IsDraft() {
			continue
		}
		item := dashboardApp{
			Slug: a.Slug, Name: a.Name, Description: a.Description,
			URL: "/" + a.Slug + "/", Published: a.LiveVersion != "", Drafts: drafts[a.Slug],
		}
		if a.PublishedAt != nil {
			item.PublishedAt = ago(*a.PublishedAt)
		}
		if mem, ok := awake[a.Slug]; ok {
			item.Awake, item.Memory = true, megabytes(mem)
		}
		apps = append(apps, item)
	}
	data := pageData("Apps", "apps", id, p.cfg.RequireLogin)
	data["Apps"] = apps
	data["MCPURL"] = p.cfg.PublicURL + platformPrefix + "/mcp"
	render(w, http.StatusOK, "apps", data)
}

type versionRow struct {
	ID      string
	When    string
	Message string
	Author  string
	Live    bool
}

type activityRow struct {
	When  string
	Class string
	Line  string
}

func (h *Handler) serveAppPage(w http.ResponseWriter, r *http.Request, id *Identity, slug string) {
	p := h.platform
	ctx := r.Context()
	a, err := p.requireApp(ctx, slug)
	if err != nil {
		renderMessage(w, http.StatusNotFound, "Nothing here", "There is no app or draft with that name.", "/", "See all apps")
		return
	}
	appSlug, draftName := SplitSlug(a.Slug)
	data := pageData(a.Name, "apps", id, p.cfg.RequireLogin)
	data["App"] = a
	data["AppSlug"], data["DraftName"] = appSlug, draftName
	data["IsDraft"] = a.IsDraft()
	data["ParentSlug"] = a.Parent
	data["Published"] = a.LiveVersion != ""
	data["URL"] = "/" + a.Slug + "/"
	data["FullURL"] = p.URL(a.Slug)
	data["CreatedAt"] = ago(a.CreatedAt)
	data["PublishedAt"] = ""
	if a.PublishedAt != nil {
		data["PublishedAt"] = ago(*a.PublishedAt)
	}
	crumbs := []crumb{{Label: "Apps", URL: "/"}}
	if a.IsDraft() {
		crumbs = append(crumbs, crumb{Label: appSlug, URL: platformPrefix + "/app/" + appSlug})
		crumbs = append(crumbs, crumb{Label: draftName})
	} else {
		crumbs = append(crumbs, crumb{Label: a.Name})
	}
	data["Crumbs"] = crumbs
	data["Awake"], data["Memory"] = false, ""
	if mem, ok := p.awakeApps()[a.Slug]; ok {
		data["Awake"], data["Memory"] = true, megabytes(mem)
	}

	versions, _ := p.Versions(ctx, a.Slug, 30)
	rows := make([]versionRow, 0, len(versions))
	for _, v := range versions {
		rows = append(rows, versionRow{ID: v.ID, When: ago(v.Time), Message: v.Message, Author: v.Author, Live: v.ID == a.LiveVersion})
	}
	data["Versions"] = rows

	draftList := []dashboardDraft{}
	if !a.IsDraft() {
		drafts, _ := p.store.ListDrafts(ctx, a.Slug)
		for _, d := range drafts {
			_, name := SplitSlug(d.Slug)
			draftList = append(draftList, dashboardDraft{Slug: d.Slug, Name: name, URL: "/" + d.Slug + "/"})
		}
		sort.Slice(draftList, func(i, j int) bool { return draftList[i].Name < draftList[j].Name })
	}
	data["Drafts"] = draftList

	settings, _ := p.SettingNames(ctx, a.Slug)
	data["Settings"] = settings

	activity := []activityRow{}
	for _, line := range p.Logs(a.Slug, 80) {
		class := ""
		if line.Source == "caddy-snake" {
			class = "note"
		} else if looksLikeError(line.Line) {
			class = "err"
		}
		activity = append(activity, activityRow{When: line.Time.Local().Format("15:04:05"), Class: class, Line: line.Line})
	}
	data["Activity"] = activity
	render(w, http.StatusOK, "app", data)
}

func (h *Handler) serveConnect(w http.ResponseWriter, r *http.Request, id *Identity) {
	p := h.platform
	data := pageData("Connect", "connect", id, p.cfg.RequireLogin)
	data["MCPURL"] = p.cfg.PublicURL + platformPrefix + "/mcp"
	type keyRow struct{ Name, Created, LastUsed string }
	rows := []keyRow{}
	if id != nil {
		keys, _ := p.AccessKeys(r.Context(), id.Username)
		for _, k := range keys {
			row := keyRow{Name: k.Name, Created: ago(k.CreatedAt), LastUsed: "never"}
			if k.LastUsed != nil {
				row.LastUsed = ago(*k.LastUsed)
			}
			rows = append(rows, row)
		}
	}
	data["Keys"] = rows
	render(w, http.StatusOK, "connect", data)
}

func (h *Handler) servePeople(w http.ResponseWriter, r *http.Request, id *Identity) {
	p := h.platform
	data := pageData("People", "people", id, p.cfg.RequireLogin)
	type personRow struct {
		Username string
		Admin    bool
		You      bool
	}
	rows := []personRow{}
	if p.cfg.RequireLogin && id != nil && id.Admin {
		users, _ := p.Users(r.Context())
		for _, u := range users {
			rows = append(rows, personRow{Username: u.Username, Admin: u.Admin, You: u.Username == id.Username})
		}
	}
	data["People"] = rows
	render(w, http.StatusOK, "people", data)
}
