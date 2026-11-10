package apps

import (
	"context"
	"net/http"
	"net/http/cgi" // #nosec G504 -- the Httpoxy fix (Go 1.6.3) is in every Go this builds with
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

const gitPrefix = platformPrefix + "/git/"

// branchHeads returns the latest version of every branch of an app.
func (p *Platform) branchHeads(app string) map[string]string {
	r := p.repo(app)
	out := map[string]string{}
	branches, err := r.Branches()
	if err != nil {
		return out
	}
	for _, b := range branches {
		if head, err := r.Head(b); err == nil {
			out[b] = head
		}
	}
	return out
}

// serveGit exposes each app's history as a normal git remote at
// <server>/_apps/git/<app>.git, for assistants that work in a terminal. It
// hands the request to `git http-backend`, so it is only available when git is
// installed on the server. Pushing main publishes the app; pushing draft/<name>
// publishes that draft if it exists.
func (h *Handler) serveGit(w http.ResponseWriter, r *http.Request, who string) {
	p := h.platform
	gitBin, err := exec.LookPath("git")
	if err != nil {
		http.Error(w, "git is not installed on this server; use the save_files tool instead", http.StatusNotFound)
		return
	}
	repoName, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, gitPrefix), "/")
	app := strings.TrimSuffix(repoName, ".git")
	if !strings.HasSuffix(repoName, ".git") || ValidateAppSlug(app) != nil {
		http.NotFound(w, r)
		return
	}
	if exists, _ := p.Known(app); !exists {
		http.NotFound(w, r)
		return
	}
	isPush := r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/git-receive-pack")
	var before map[string]string
	if isPush {
		// Keep saves and publishes out of the repository while it is written.
		unlock := p.lock(app)
		before = p.branchHeads(app)
		defer func() {
			after := p.branchHeads(app)
			unlock()
			go p.afterPush(app, before, after, who)
		}()
	}
	reposDir := filepath.Join(p.cfg.DataDir, "repos")
	(&cgi.Handler{
		Path: gitBin,
		Args: []string{"http-backend"},
		Root: strings.TrimSuffix(gitPrefix, "/"),
		Dir:  reposDir,
		Env: []string{
			"GIT_PROJECT_ROOT=" + reposDir,
			"GIT_HTTP_EXPORT_ALL=1",
			// http-backend only accepts pushes from a named user.
			"REMOTE_USER=" + who,
		},
	}).ServeHTTP(w, r)
}

// afterPush publishes whatever a git push changed.
func (p *Platform) afterPush(app string, before, after map[string]string, who string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	for branch, head := range after {
		if before[branch] == head {
			continue
		}
		slug := app
		if branch != mainBranch {
			if !strings.HasPrefix(branch, draftBranchPrefix) {
				continue
			}
			slug = DraftSlug(app, strings.TrimPrefix(branch, draftBranchPrefix))
			if exists, _ := p.Known(slug); !exists {
				p.logs.Add(app, time.Now(), "caddy-snake", "pushed branch "+branch+" has no draft; create it with create_draft first")
				continue
			}
		}
		res, err := p.Publish(ctx, slug, head, who)
		if err != nil {
			p.logger.Warn("publishing after a git push", zap.String("app", logSafe(slug)), zap.String("error", logSafe(err.Error())))
			continue
		}
		if !res.OK {
			p.logger.Info("a pushed version was not published", zap.String("app", logSafe(slug)), zap.String("error", logSafe(firstLine(res.Error))))
		}
	}
}
