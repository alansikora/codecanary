package review

import (
	"os"
	"path/filepath"
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
const maxDocBytes = 4096

// maxTotalDocBytes caps the total size of all CLAUDE.md files combined.
const maxTotalDocBytes = 12288

// ReadProjectDocs reads CLAUDE.md files from known locations in the working directory.
// It returns a map of path → content, respecting per-file and total size limits.
func ReadProjectDocs() map[string]string {
	docs := make(map[string]string)

	// Check root and .claude/ first.
	docPaths := []string{"CLAUDE.md", ".claude/CLAUDE.md"}

	// Check top-level subdirectories.
	entries, err := os.ReadDir(".")
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() || skipDirs[e.Name()] || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			docPaths = append(docPaths, filepath.Join(e.Name(), "CLAUDE.md"))
		}
	}

	totalBytes := 0
	for _, p := range docPaths {
		if len(docs) >= 5 {
			break
		}
		data, err := os.ReadFile(p)
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
		docs[p] = content
		totalBytes += len(content)
	}

	return docs
}
