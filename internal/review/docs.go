package review

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skipDirs are directories to ignore when walking the repo.
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, ".git": true,
	"dist": true, "build": true, ".next": true, ".nuxt": true,
	"target": true, "__pycache__": true, ".venv": true, "venv": true,
	".claude": true, ".codecanary": true,
}

// maxDocBytes caps the size of a single CLAUDE.md file included in the prompt.
const maxDocBytes = 16384

// maxTotalDocBytes caps the total size of all CLAUDE.md files combined.
const maxTotalDocBytes = 49152

// maxDocs caps the number of CLAUDE.md files loaded per review.
const maxDocs = 10

// ReadProjectDocs reads CLAUDE.md files from the repo root and every ancestor
// directory of the PR's changed files. Root docs come first; more-specific
// ancestor docs come later so LLM recency bias favors the most relevant
// guidance. Per-file and overall size caps apply. When prFiles is empty only
// root docs are loaded.
func ReadProjectDocs(prFiles []string) map[string]string {
	root, err := os.Getwd()
	if err != nil {
		return map[string]string{}
	}
	return readProjectDocsFrom(root, prFiles)
}

// readProjectDocsFrom is the testable core of ReadProjectDocs. It resolves all
// candidate paths relative to root, which must be an absolute path.
func readProjectDocsFrom(root string, prFiles []string) map[string]string {
	docs := make(map[string]string)

	// Candidate paths in load order: repo root first, then ancestors from
	// shallowest to deepest. writeProjectDocs ultimately sorts docs
	// alphabetically before emitting them to the prompt, which coincidentally
	// preserves this root-then-deeper order for typical repo layouts.
	paths := []string{"CLAUDE.md", filepath.Join(".claude", "CLAUDE.md")}
	for _, dir := range ancestorDirs(prFiles) {
		paths = append(paths, filepath.Join(dir, "CLAUDE.md"))
		paths = append(paths, filepath.Join(dir, ".claude", "CLAUDE.md"))
	}

	totalBytes := 0
	for _, relPath := range paths {
		if _, exists := docs[relPath]; exists {
			continue
		}
		if len(docs) >= maxDocs {
			break
		}
		data, err := os.ReadFile(filepath.Join(root, relPath))
		if err != nil {
			continue
		}
		content := string(data)
		if len(content) > maxDocBytes {
			content = content[:maxDocBytes] + "\n... (truncated)"
		}
		if totalBytes+len(content) > maxTotalDocBytes {
			continue
		}
		docs[relPath] = content
		totalBytes += len(content)
	}

	return docs
}

// ancestorDirs returns the unique set of ancestor directories for the given
// files, sorted shallowest-first. Files inside skipDirs (node_modules, vendor,
// .git, dotfile dirs, etc.) are excluded — no CLAUDE.md lookup happens for
// their ancestors. The repo root itself is excluded; root docs are handled
// separately by the caller.
func ancestorDirs(files []string) []string {
	seen := make(map[string]bool)
	for _, f := range files {
		dir := filepath.Dir(filepath.Clean(f))
		if dir == "." || dir == "/" || dir == "" {
			continue
		}
		if pathHasSkippedComponent(dir) {
			continue
		}
		for dir != "." && dir != "/" && dir != "" {
			seen[dir] = true
			dir = filepath.Dir(dir)
		}
	}

	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		di := strings.Count(out[i], string(filepath.Separator))
		dj := strings.Count(out[j], string(filepath.Separator))
		if di != dj {
			return di < dj
		}
		return out[i] < out[j]
	})
	return out
}

// pathHasSkippedComponent reports whether any component of dir is in skipDirs
// or starts with a dot. Used to exclude build artifacts, VCS metadata, and
// hidden config directories from the ancestor walk.
func pathHasSkippedComponent(dir string) bool {
	for _, p := range strings.Split(filepath.Clean(dir), string(filepath.Separator)) {
		if skipDirs[p] || strings.HasPrefix(p, ".") {
			return true
		}
	}
	return false
}
