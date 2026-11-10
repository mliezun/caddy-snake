package apps

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-billy/v6/memfs"
	"github.com/go-git/go-billy/v6/osfs"
	"github.com/go-git/go-billy/v6/util"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/cache"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/storage/filesystem"
)

const (
	mainBranch        = "main"
	draftBranchPrefix = "draft/"
	maxFileBytes      = 2 << 20
	maxFilesPerSave   = 500
)

// Version is one saved state of an app's files (a git commit).
type Version struct {
	ID      string    `json:"version"`
	Message string    `json:"message"`
	Author  string    `json:"saved_by,omitempty"`
	Time    time.Time `json:"saved_at"`
}

// repo stores an app's files and their history. It is an ordinary git
// repository on disk, written with go-git so no git program is needed.
// Callers serialize access per app.
type repo struct{ dir string }

func branchFor(draft string) string {
	if draft == "" {
		return mainBranch
	}
	return draftBranchPrefix + draft
}

func shortID(h plumbing.Hash) string { return h.String()[:12] }

func (r *repo) storage() *filesystem.Storage {
	return filesystem.NewStorage(osfs.New(r.dir), cache.NewObjectLRUDefault())
}

// initRepo creates an empty repository whose default branch is main.
func initRepo(dir string) (*repo, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	r := &repo{dir: dir}
	g, err := git.Init(r.storage(), git.WithWorkTree(memfs.New()), git.WithDefaultBranch(plumbing.NewBranchReferenceName(mainBranch)))
	if err != nil {
		return nil, err
	}
	// Mark it bare so `git push` over HTTP is accepted for the default branch.
	cfg, err := g.Config()
	if err != nil {
		return nil, err
	}
	cfg.Core.IsBare = true
	// The in-memory checkout is not a folder git could use; a path left here
	// makes git warn on every push.
	cfg.Core.Worktree = ""
	cfg.Raw.Section("core").RemoveOption("worktree")
	if err := g.SetConfig(cfg); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *repo) open() (*git.Repository, error) {
	// A stale index would make a fresh in-memory checkout skip files it thinks
	// are already present, so always start from an empty one.
	_ = os.Remove(filepath.Join(r.dir, "index"))
	return git.Open(r.storage(), memfs.New())
}

// cleanPath validates a file path inside an app and returns its canonical form.
func cleanPath(p string) (string, error) {
	orig := p
	p = strings.TrimPrefix(strings.ReplaceAll(p, "\\", "/"), "./")
	if p == "" || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("file path %q must be relative to the app folder", orig)
	}
	clean := path.Clean(p)
	if clean != p || clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("file path %q is not a plain relative path", orig)
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".git" || seg == ".venv" {
			return "", fmt.Errorf("file path %q uses a reserved folder name", orig)
		}
	}
	return clean, nil
}

// Head returns the latest version on a branch, or "" if the branch is empty.
func (r *repo) Head(branch string) (string, error) {
	g, err := r.open()
	if err != nil {
		return "", err
	}
	ref, err := g.Reference(plumbing.NewBranchReferenceName(branch), true)
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return shortID(ref.Hash()), nil
}

// Save applies file changes on a branch and records them as a new version.
// A nil value deletes the file. It returns the new version id, or the current
// one when nothing changed.
func (r *repo) Save(branch string, changes map[string][]byte, message, author string) (string, error) {
	if len(changes) == 0 {
		return "", errors.New("no files were given")
	}
	if len(changes) > maxFilesPerSave {
		return "", fmt.Errorf("too many files in one save (%d, at most %d)", len(changes), maxFilesPerSave)
	}
	g, err := r.open()
	if err != nil {
		return "", err
	}
	wt, err := g.Worktree()
	if err != nil {
		return "", err
	}
	refName := plumbing.NewBranchReferenceName(branch)
	if _, err := g.Reference(refName, true); err == nil {
		if err := wt.Checkout(&git.CheckoutOptions{Branch: refName, Force: true}); err != nil {
			return "", fmt.Errorf("opening the current files: %w", err)
		}
	} else if !errors.Is(err, plumbing.ErrReferenceNotFound) {
		return "", err
	} else if branch != mainBranch {
		return "", fmt.Errorf("draft %q does not exist", strings.TrimPrefix(branch, draftBranchPrefix))
	}

	for name, content := range changes {
		clean, err := cleanPath(name)
		if err != nil {
			return "", err
		}
		if content == nil {
			if _, statErr := wt.Filesystem().Stat(clean); statErr == nil {
				if _, err := wt.Remove(clean); err != nil {
					return "", err
				}
			}
			continue
		}
		if len(content) > maxFileBytes {
			return "", fmt.Errorf("file %q is too large (%d bytes, at most %d)", clean, len(content), maxFileBytes)
		}
		if err := util.WriteFile(wt.Filesystem(), clean, content, 0o644); err != nil {
			return "", err
		}
		if _, err := wt.Add(clean); err != nil {
			return "", err
		}
	}
	status, err := wt.Status()
	if err != nil {
		return "", err
	}
	if status.IsClean() {
		return r.Head(branch)
	}
	if message == "" {
		message = "Save changes"
	}
	if author == "" {
		author = "unknown"
	}
	hash, err := wt.Commit(message, &git.CommitOptions{
		Author: &object.Signature{Name: author, Email: author + "@caddy-snake.local", When: time.Now()},
	})
	if err != nil {
		return "", err
	}
	return shortID(hash), nil
}

func (r *repo) commit(g *git.Repository, version string) (*object.Commit, error) {
	hash, err := g.ResolveRevision(plumbing.Revision(version))
	if err != nil {
		return nil, fmt.Errorf("version %q was not found", version)
	}
	return g.CommitObject(*hash)
}

// Files returns the content of every file in a version (or only the named ones).
func (r *repo) Files(version string, only []string) (map[string][]byte, error) {
	g, err := r.open()
	if err != nil {
		return nil, err
	}
	c, err := r.commit(g, version)
	if err != nil {
		return nil, err
	}
	tree, err := c.Tree()
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, p := range only {
		want[strings.TrimPrefix(p, "./")] = true
	}
	out := map[string][]byte{}
	err = tree.Files().ForEach(func(f *object.File) error {
		if len(want) > 0 && !want[f.Name] {
			return nil
		}
		rd, err := f.Reader()
		if err != nil {
			return err
		}
		defer rd.Close()
		data, err := io.ReadAll(rd)
		if err != nil {
			return err
		}
		out[f.Name] = data
		return nil
	})
	return out, err
}

// Export writes every file of a version into dir.
func (r *repo) Export(version, dir string) error {
	files, err := r.Files(version, nil)
	if err != nil {
		return err
	}
	for name, data := range files {
		target := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o640); err != nil {
			return err
		}
	}
	return nil
}

// Versions lists the most recent versions on a branch, newest first.
func (r *repo) Versions(branch string, limit int) ([]Version, error) {
	g, err := r.open()
	if err != nil {
		return nil, err
	}
	ref, err := g.Reference(plumbing.NewBranchReferenceName(branch), true)
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	iter, err := g.Log(&git.LogOptions{From: ref.Hash()})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var out []Version
	for len(out) < limit {
		c, err := iter.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		out = append(out, Version{
			ID:      shortID(c.Hash),
			Message: strings.TrimSpace(c.Message),
			Author:  c.Author.Name,
			Time:    c.Author.When,
		})
	}
	return out, nil
}

// CreateBranch starts a draft branch at the current main version.
func (r *repo) CreateBranch(branch string) error {
	g, err := r.open()
	if err != nil {
		return err
	}
	main, err := g.Reference(plumbing.NewBranchReferenceName(mainBranch), true)
	if err != nil {
		return errors.New("the app has no saved version yet")
	}
	name := plumbing.NewBranchReferenceName(branch)
	if _, err := g.Reference(name, true); err == nil {
		return fmt.Errorf("draft %q already exists", strings.TrimPrefix(branch, draftBranchPrefix))
	}
	return g.Storer.SetReference(plumbing.NewHashReference(name, main.Hash()))
}

// DeleteBranch removes a draft branch.
func (r *repo) DeleteBranch(branch string) error {
	g, err := r.open()
	if err != nil {
		return err
	}
	return g.Storer.RemoveReference(plumbing.NewBranchReferenceName(branch))
}

// FastForwardMain moves main to the head of a draft branch. It fails when main
// has versions the draft does not include, so nothing published is lost.
func (r *repo) FastForwardMain(branch string) (string, error) {
	g, err := r.open()
	if err != nil {
		return "", err
	}
	mainRef, err := g.Reference(plumbing.NewBranchReferenceName(mainBranch), true)
	if err != nil {
		return "", err
	}
	draftRef, err := g.Reference(plumbing.NewBranchReferenceName(branch), true)
	if err != nil {
		return "", fmt.Errorf("draft %q does not exist", strings.TrimPrefix(branch, draftBranchPrefix))
	}
	mainCommit, err := g.CommitObject(mainRef.Hash())
	if err != nil {
		return "", err
	}
	draftCommit, err := g.CommitObject(draftRef.Hash())
	if err != nil {
		return "", err
	}
	if mainRef.Hash() != draftRef.Hash() {
		ok, err := mainCommit.IsAncestor(draftCommit)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", errors.New("the live app has changed since this draft was started; start a new draft from the current version and re-apply the changes")
		}
	}
	if err := g.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName(mainBranch), draftRef.Hash())); err != nil {
		return "", err
	}
	return shortID(draftRef.Hash()), nil
}

// resetMain points main back at an earlier version (used to undo a failed
// draft publish).
func (r *repo) resetMain(version string) error {
	g, err := r.open()
	if err != nil {
		return err
	}
	c, err := r.commit(g, version)
	if err != nil {
		return err
	}
	return g.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName(mainBranch), c.Hash))
}

// Branches lists branch names, sorted.
func (r *repo) Branches() ([]string, error) {
	g, err := r.open()
	if err != nil {
		return nil, err
	}
	iter, err := g.Branches()
	if err != nil {
		return nil, err
	}
	var out []string
	_ = iter.ForEach(func(ref *plumbing.Reference) error {
		out = append(out, ref.Name().Short())
		return nil
	})
	sort.Strings(out)
	return out, nil
}
