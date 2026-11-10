package apps

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// DraftResult describes a newly created draft.
type DraftResult struct {
	Draft      *App           `json:"draft"`
	URL        string         `json:"url"`
	DataCopied bool           `json:"data_copied"`
	Note       string         `json:"note,omitempty"`
	Publish    *PublishResult `json:"publish"`
}

// CreateDraft makes a private copy of an app: its files at the current version
// and a copy of its data, served at /<app>--<name>/.
func (p *Platform) CreateDraft(ctx context.Context, app, name, by string) (*DraftResult, error) {
	parent, err := p.requireApp(ctx, app)
	if err != nil {
		return nil, err
	}
	if parent.IsDraft() {
		return nil, errors.New("a draft cannot have drafts of its own; create it from the app")
	}
	draft := Slugify(name)
	if err := validSlugPart(draft, maxDraftSlugLen, "draft"); err != nil {
		return nil, err
	}
	slug := DraftSlug(app, draft)
	defer p.lock(app)()
	if _, err := p.store.GetApp(ctx, slug); err == nil {
		return nil, fmt.Errorf("draft %q already exists at %s", draft, p.URL(slug))
	} else if !errors.Is(err, errNotFound) {
		return nil, err
	}
	r := p.repo(app)
	if err := r.CreateBranch(branchFor(draft)); err != nil {
		return nil, err
	}
	cleanup := func() {
		_ = r.DeleteBranch(branchFor(draft))
		_ = p.pg.DropAppDatabase(context.Background(), slug)
		_ = os.RemoveAll(p.appDir(slug))
		_ = p.store.DeleteApp(context.Background(), slug)
		p.forget(slug)
	}
	dbURL, err := p.pg.CreateAppDatabase(ctx, slug)
	if err != nil {
		cleanup()
		return nil, err
	}
	res := &DraftResult{DataCopied: true}
	if err := p.pg.CloneDatabase(ctx, app, slug, dbURL); err != nil {
		if !errors.Is(err, errNoCloneTools) {
			cleanup()
			return nil, err
		}
		res.DataCopied = false
		res.Note = "The draft starts with empty data: the Postgres tools pg_dump and psql are not installed on the server, so the app's data could not be copied."
	}
	d := &App{Slug: slug, Parent: app, Name: parent.Name + " (draft: " + draft + ")", Description: parent.Description, DatabaseURL: dbURL, CreatedBy: by}
	if err := p.store.InsertApp(ctx, d); err != nil {
		cleanup()
		return nil, err
	}
	p.setKnown(slug, false)
	if err := p.store.CopySettings(ctx, app, slug); err != nil {
		cleanup()
		return nil, err
	}
	if err := p.writeSettings(ctx, d); err != nil {
		cleanup()
		return nil, err
	}
	res.Publish = p.publishLocked(ctx, d, "", by)
	fresh, err := p.store.GetApp(ctx, slug)
	if err != nil {
		return nil, err
	}
	res.Draft = fresh
	res.URL = p.URL(slug)
	return res, nil
}

// PublishDraft makes a draft's files the live app and removes the draft. The
// draft's data is discarded; database changes reach the live app through the
// draft's migration files, which run against the live data on publish.
func (p *Platform) PublishDraft(ctx context.Context, app, name, by string) (*PublishResult, error) {
	slug := DraftSlug(app, Slugify(name))
	d, err := p.requireApp(ctx, slug)
	if err != nil {
		return nil, err
	}
	if !d.IsDraft() {
		return nil, fmt.Errorf("%q is not a draft", slug)
	}
	parent, err := p.requireApp(ctx, app)
	if err != nil {
		return nil, err
	}
	_, draft := SplitSlug(slug)
	defer p.lock(app)()
	r := p.repo(app)
	previous, err := r.Head(mainBranch)
	if err != nil {
		return nil, err
	}
	version, err := r.FastForwardMain(branchFor(draft))
	if err != nil {
		return nil, err
	}
	res := p.publishLocked(ctx, parent, version, by)
	if !res.OK {
		// Put the app's saved history back so the failed draft is not "latest".
		if previous != "" {
			_ = r.resetMain(previous)
		}
		res.Steps = append(res.Steps, "The draft was kept so it can be fixed and published again.")
		return res, nil
	}
	if err := p.deleteLocked(ctx, d, app, draft); err != nil {
		res.Steps = append(res.Steps, "The app was published, but the draft could not be removed: "+err.Error())
		return res, nil
	}
	res.Steps = append(res.Steps, "Removed the draft.")
	return res, nil
}
