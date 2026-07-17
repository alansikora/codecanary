package review

import (
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
)

// matchesAnyGlob reports whether path matches any of the glob patterns. Each
// pattern is tried against the full path first and then against just the base
// filename, so both "src/**/*.go" and "*.go" work. Uses doublestar to support
// ** recursive globs (e.g. "dist/**", "src/**/*.test.*").
func matchesAnyGlob(path string, patterns []string) bool {
	for _, pat := range patterns {
		if matched, _ := doublestar.Match(pat, path); matched {
			return true
		}
		// Also try matching against just the filename.
		if matched, _ := doublestar.Match(pat, filepath.Base(path)); matched {
			return true
		}
	}
	return false
}
