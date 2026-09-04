// Package evalcorpus defines the on-disk format for frozen review inputs and
// the helpers to read and write them.
//
// A fixture is everything a review needs in order to build its prompt: the PR
// metadata and diff, the file contents the reviewer would have read, the
// project docs it would have picked up, and the review config in force. Once
// captured, rendering a prompt from a fixture touches neither the network nor
// a working tree, so the same input produces the same prompt on any machine.
//
// The package deliberately declares its own plain structs rather than reusing
// the review package's types. Keeping it dependency-free means the review
// package's own tests can import it without an import cycle, and it pins the
// serialised format so a refactor inside review can't silently invalidate a
// corpus captured months earlier.
//
// # Corpus location
//
// A small corpus captured from this repository's own public pull requests is
// committed under internal/review/testdata/corpus, so the harness runs in CI
// and anyone reading the tests can see the shape of a fixture without having
// to capture one first.
//
// Richer corpora are usually captured from private repositories, and those
// must not be committed here. $CODECANARY_EVAL_CORPUS points the harness at
// one kept outside this repository; see Dir.
package evalcorpus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FormatVersion is bumped whenever Fixture changes shape in a way that makes
// previously captured fixtures unreadable. Load refuses a fixture from a newer
// format rather than silently misreading it.
const FormatVersion = 1

// EnvCorpusDir is the environment variable naming the directory that holds the
// corpus. See Dir.
const EnvCorpusDir = "CODECANARY_EVAL_CORPUS"

// Fixture is one frozen review input.
type Fixture struct {
	FormatVersion int    `json:"format_version"`
	Name          string `json:"name"`
	Repo          string `json:"repo"`
	PRNumber      int    `json:"pr_number"`
	HeadSHA       string `json:"head_sha"`
	CapturedAt    string `json:"captured_at"`

	PR          PRInput           `json:"pr"`
	Config      *ConfigInput      `json:"config,omitempty"`
	ProjectDocs map[string]string `json:"project_docs,omitempty"`
}

// PRInput mirrors the fields of review.PRData that feed prompt construction.
type PRInput struct {
	Number       int               `json:"number"`
	Title        string            `json:"title"`
	Body         string            `json:"body"`
	Author       string            `json:"author"`
	BaseBranch   string            `json:"base_branch"`
	HeadBranch   string            `json:"head_branch"`
	Diff         string            `json:"diff"`
	Files        []string          `json:"files"`
	FileContents map[string]string `json:"file_contents,omitempty"`
}

// ConfigInput mirrors the review config fields that reach the prompt. Model,
// provider and budget settings are deliberately absent: they steer how the
// review runs, not what the prompt says, and capturing them would invite
// fixtures that drift with unrelated config changes.
type ConfigInput struct {
	Rules   []RuleInput `json:"rules,omitempty"`
	Context string      `json:"context,omitempty"`
	Ignore  []string    `json:"ignore,omitempty"`
}

// RuleInput mirrors review.Rule.
type RuleInput struct {
	ID           string   `json:"id"`
	Description  string   `json:"description"`
	Severity     string   `json:"severity"`
	Paths        []string `json:"paths,omitempty"`
	ExcludePaths []string `json:"exclude_paths,omitempty"`
}

// Dir resolves the corpus directory, preferring $CODECANARY_EVAL_CORPUS over
// the committed corpus at fallback.
//
// The override exists so a larger corpus captured from a private repository
// can drive the same harness from outside this repository. When the variable
// is set but does not name a directory, Dir reports it as not-ok rather than
// quietly falling back: someone who pointed the harness somewhere specific
// should hear that the path is wrong, not silently get different results.
//
// ok=false means no corpus is readable. For the committed fallback that
// should not happen in a normal checkout, so callers are better off failing
// than skipping — a corpus that vanished is a broken checkout, not a
// legitimate state.
func Dir(fallback string) (dir string, ok bool) {
	if env := strings.TrimSpace(os.Getenv(EnvCorpusDir)); env != "" {
		info, err := os.Stat(env)
		return env, err == nil && info.IsDir()
	}
	info, err := os.Stat(fallback)
	return fallback, err == nil && info.IsDir()
}

// Save writes a fixture as indented JSON under dir, named after f.Name.
func Save(dir string, f *Fixture) (string, error) {
	if f.Name == "" {
		return "", fmt.Errorf("fixture has no name")
	}
	f.FormatVersion = FormatVersion
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating corpus dir: %w", err)
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding fixture: %w", err)
	}
	path := filepath.Join(dir, f.Name+".json")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("writing fixture: %w", err)
	}
	return path, nil
}

// Load reads one fixture from disk.
func Load(path string) (*Fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f Fixture
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", filepath.Base(path), err)
	}
	if f.FormatVersion > FormatVersion {
		return nil, fmt.Errorf("%s: fixture format v%d is newer than this build understands (v%d); update codecanary or re-capture the corpus",
			filepath.Base(path), f.FormatVersion, FormatVersion)
	}
	return &f, nil
}

// List loads every fixture in dir, sorted by name so callers iterate in a
// stable order.
func List(dir string) ([]*Fixture, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	out := make([]*Fixture, 0, len(matches))
	for _, m := range matches {
		f, err := Load(m)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}
