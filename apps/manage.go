package apps

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// serveManage handles the dashboard's own actions on people and access keys.
// These are deliberately not tools: an assistant can build and change apps,
// but only a signed-in person decides who gets in.
//
//	POST /_apps/api/manage/people/add       administrators
//	POST /_apps/api/manage/people/password  administrators
//	POST /_apps/api/manage/people/remove    administrators
//	POST /_apps/api/manage/keys/create      any signed-in person, for themselves
//	POST /_apps/api/manage/keys/revoke      any signed-in person, for themselves
func (h *Handler) serveManage(w http.ResponseWriter, r *http.Request, id *Identity, action string) {
	p := h.platform
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST with a JSON body"})
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "send the request as application/json"})
		return
	}
	// On an open server there are no people to manage over the web: otherwise
	// anyone who can reach it could create an administrator ahead of sign-in
	// being turned on. Use the command line there.
	if !p.cfg.RequireLogin || id == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Sign-in is not turned on for this server. People are managed with the caddysnake apps user command."})
		return
	}
	var args struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Admin    bool   `json:"admin"`
		Name     string `json:"name"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err == nil {
		err = json.Unmarshal(body, &args)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the request could not be read"})
		return
	}
	ctx := r.Context()
	fail := func(err error) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
	}

	if strings.HasPrefix(action, "people/") {
		if !id.Admin {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Only administrators can manage people."})
			return
		}
		switch action {
		case "people/add":
			err = p.AddUser(ctx, args.Username, args.Password, args.Admin)
		case "people/password":
			err = p.SetPassword(ctx, args.Username, args.Password)
		case "people/remove":
			if args.Username == id.Username {
				err = errors.New("you cannot remove yourself; ask another administrator")
			} else {
				err = p.RemoveUser(ctx, args.Username)
			}
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown action"})
			return
		}
		if err != nil {
			fail(err)
			return
		}
		// Sessions and keys of the affected person must stop working at once.
		h.forgetIdentities()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	switch action {
	case "keys/create":
		name := strings.TrimSpace(args.Name)
		if name == "" || len(name) > 60 {
			fail(errors.New("give the key a short name"))
			return
		}
		key, err := p.CreateAccessKey(ctx, id.Username, name)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": key})
	case "keys/revoke":
		if _, err := p.RevokeAccessKeys(ctx, id.Username, args.Name); err != nil {
			fail(err)
			return
		}
		h.forgetIdentities()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown action"})
	}
}
