// Package apps turns caddy-snake into a small platform for internal apps:
// people (through their AI agents) create an app, save versions of its files,
// and publish it; each app is served at /<app>/ with its own Postgres database.
package apps

import (
	"fmt"
	"strings"
)

const (
	maxAppSlugLen   = 30
	maxDraftSlugLen = 20
	// draftSeparator joins an app slug and a draft name ("inventory--new-form").
	// Slugify collapses repeated hyphens, so it can never appear inside either part.
	draftSeparator = "--"
)

// reservedSlugs cannot be used as app names because they are paths the
// platform itself serves, or files browsers request from the site root.
var reservedSlugs = map[string]bool{
	"_apps": true, "static": true, "assets": true, "favicon.ico": true,
	"robots.txt": true, "login": true, "logout": true, "api": true, "mcp": true,
}

// Slugify turns a human name ("Team Lunch Orders!") into a URL and directory
// safe slug ("team-lunch-orders"): lowercase letters, digits and single
// hyphens, starting with a letter.
func Slugify(name string) string {
	var b strings.Builder
	lastHyphen := true // suppress leading hyphens
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s != "" && s[0] >= '0' && s[0] <= '9' {
		s = "app-" + s
	}
	return s
}

func validSlugPart(s string, maxLen int, what string) error {
	if s == "" {
		return fmt.Errorf("%s name is empty", what)
	}
	if len(s) > maxLen {
		return fmt.Errorf("%s name %q is too long (at most %d characters)", what, s, maxLen)
	}
	if s != Slugify(s) {
		return fmt.Errorf("%s name %q may only use lowercase letters, digits and single hyphens, and must start with a letter", what, s)
	}
	return nil
}

// ValidateAppSlug checks a slug for a top-level app (not a draft).
func ValidateAppSlug(slug string) error {
	if err := validSlugPart(slug, maxAppSlugLen, "app"); err != nil {
		return err
	}
	if reservedSlugs[slug] {
		return fmt.Errorf("%q is a reserved name; choose another", slug)
	}
	return nil
}

// DraftSlug returns the serving slug of a draft of app.
func DraftSlug(app, draft string) string { return app + draftSeparator + draft }

// SplitSlug splits a serving slug into its app and draft parts; draft is ""
// for a live app.
func SplitSlug(slug string) (app, draft string) {
	if i := strings.Index(slug, draftSeparator); i >= 0 {
		return slug[:i], slug[i+len(draftSeparator):]
	}
	return slug, ""
}

// ValidateServingSlug checks either form: "app" or "app--draft".
func ValidateServingSlug(slug string) error {
	app, draft := SplitSlug(slug)
	if err := ValidateAppSlug(app); err != nil {
		return err
	}
	if strings.Contains(slug, draftSeparator) {
		return validSlugPart(draft, maxDraftSlugLen, "draft")
	}
	return nil
}

// dbName is the Postgres database (and role) name for a serving slug.
func dbName(slug string) string {
	return "app_" + strings.ReplaceAll(slug, "-", "_")
}
