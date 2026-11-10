package apps

import (
	"sync"
	"time"
)

// LogLine is one line of an app's output, or a platform notice about it.
type LogLine struct {
	Time   time.Time `json:"time"`
	Source string    `json:"source"` // "stdout", "stderr" or "caddy-snake"
	Line   string    `json:"line"`
}

// logBook keeps the most recent output lines of each app in memory.
type logBook struct {
	mu    sync.Mutex
	limit int
	lines map[string][]LogLine
}

func newLogBook(limit int) *logBook {
	return &logBook{limit: limit, lines: map[string][]LogLine{}}
}

func (b *logBook) Add(slug string, at time.Time, source, line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := append(b.lines[slug], LogLine{Time: at, Source: source, Line: line})
	if len(lines) > b.limit {
		lines = append([]LogLine(nil), lines[len(lines)-b.limit:]...)
	}
	b.lines[slug] = lines
}

func (b *logBook) Tail(slug string, n int) []LogLine {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := b.lines[slug]
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return append([]LogLine{}, lines...)
}

func (b *logBook) Drop(slug string) {
	b.mu.Lock()
	delete(b.lines, slug)
	b.mu.Unlock()
}
