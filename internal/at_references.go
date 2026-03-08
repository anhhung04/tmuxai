package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/anhhung04/tmuxai/system"
)

const (
	atRefFileMaxBytes = 200_000 // 200 KB per file
	atRefDirMaxFiles  = 50      // max files read inline from a directory
)

// ResolvedRef carries metadata about a single resolved @path reference.
type ResolvedRef struct {
	Original  string // the raw "@./foo.go" token as the user typed it
	Path      string // absolute path after resolution
	IsDir     bool
	FileCount int    // number of files whose content was injected
	Tokens    int    // estimated token count of injected content
	Content   string // formatted block ready to inject (empty on error)
	Err       error
}

// resolveAtReferences scans input for @path tokens, reads each path from disk,
// and returns the enriched message string plus metadata for each reference.
// cwd is used to resolve relative paths. Individual errors are captured in
// ResolvedRef.Err and never abort the whole call.
func resolveAtReferences(input string, cwd string) (string, []ResolvedRef, error) {
	tokens := parseAtTokens(input)
	if len(tokens) == 0 {
		return input, nil, nil
	}

	var refs []ResolvedRef
	var appendix strings.Builder

	for _, tok := range tokens {
		ref := resolveOnePath(tok, cwd)
		refs = append(refs, ref)
		if ref.Err != nil {
			// Leave the original token in the message; warn via the summary.
			continue
		}
		appendix.WriteString(ref.Path)
		appendix.WriteString("\n")
		appendix.WriteString(ref.Content)
		appendix.WriteString("\n")
	}

	result := input
	if appendix.Len() > 0 {
		result = input + "\n\n" + strings.TrimSpace(appendix.String())
	}
	return result, refs, nil
}

// parseAtTokens extracts unique @path tokens from input using a simple regex.
// It matches @<non-whitespace> sequences and deduplicates them.
func parseAtTokens(input string) []string {
	re := regexp.MustCompile(`@([^\s@,;'"()]+)`)
	var seen = make(map[string]bool)
	var result []string
	for _, m := range re.FindAllString(input, -1) {
		if !seen[m] {
			seen[m] = true
			result = append(result, m)
		}
	}
	return result
}

// resolveOnePath resolves a single @token to a ResolvedRef.
func resolveOnePath(tok string, cwd string) ResolvedRef {
	ref := ResolvedRef{Original: tok}

	// Strip the leading "@".
	rawPath := strings.TrimPrefix(tok, "@")
	if rawPath == "" {
		ref.Err = fmt.Errorf("empty path")
		return ref
	}

	absPath := rawPath
	if !filepath.IsAbs(rawPath) {
		absPath = filepath.Join(cwd, rawPath)
	}
	ref.Path = absPath

	info, err := os.Stat(absPath)
	if err != nil {
		ref.Err = err
		return ref
	}
	ref.IsDir = info.IsDir()

	if ref.IsDir {
		ref.Content, ref.FileCount, ref.Err = readDirBlock(absPath)
	} else {
		ref.Content, ref.Err = readFileBlock(absPath)
		if ref.Err == nil {
			ref.FileCount = 1
		}
	}
	ref.Tokens = system.EstimateTokenCount(ref.Content)
	return ref
}

// readFileBlock reads a file and wraps it in a fenced block with a header.
// Files larger than atRefFileMaxBytes are truncated.
func readFileBlock(absPath string) (string, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	content := string(data)
	truncated := false
	if len(data) > atRefFileMaxBytes {
		content = string(data[:atRefFileMaxBytes])
		truncated = true
	}

	lang := langFromPath(absPath)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== File: %s ===\n", absPath))
	sb.WriteString(fmt.Sprintf("```%s\n", lang))
	sb.WriteString(content)
	if truncated {
		sb.WriteString("\n[file truncated at 200 KB]")
	}
	sb.WriteString("\n```")
	return sb.String(), nil
}

// readDirBlock lists a directory. If the directory has ≤atRefDirMaxFiles files,
// each file's content is also injected. Otherwise only a listing is produced.
func readDirBlock(absPath string) (string, int, error) {
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return "", 0, err
	}

	// Collect regular files (non-recursive).
	var files []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e)
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Directory: %s ===\n", absPath))

	if len(files) > atRefDirMaxFiles {
		// Listing only.
		for _, e := range entries {
			if e.IsDir() {
				sb.WriteString(fmt.Sprintf("  %s/\n", e.Name()))
			} else {
				sb.WriteString(fmt.Sprintf("  %s\n", e.Name()))
			}
		}
		sb.WriteString(fmt.Sprintf("[%d files — content omitted, too many files]\n", len(files)))
		return sb.String(), 0, nil
	}

	// Inline content for each file.
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			sb.WriteString(fmt.Sprintf("  %s/\n", e.Name()))
			continue
		}
		filePath := filepath.Join(absPath, e.Name())
		block, err := readFileBlock(filePath)
		if err != nil {
			sb.WriteString(fmt.Sprintf("  [error reading %s: %v]\n", e.Name(), err))
			continue
		}
		sb.WriteString(block)
		sb.WriteString("\n\n")
		count++
	}
	return strings.TrimRight(sb.String(), "\n"), count, nil
}

// langFromPath infers a markdown code-fence language hint from the file extension.
func langFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".mjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".sh", ".bash", ".zsh":
		return "bash"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".toml":
		return "toml"
	case ".md":
		return "markdown"
	case ".rs":
		return "rust"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".hpp":
		return "cpp"
	case ".java":
		return "java"
	case ".rb":
		return "ruby"
	case ".sql":
		return "sql"
	case ".html", ".htm":
		return "html"
	case ".css":
		return "css"
	case ".xml":
		return "xml"
	case ".tf":
		return "hcl"
	case ".dockerfile", "":
		name := strings.ToLower(filepath.Base(path))
		if name == "dockerfile" || strings.HasPrefix(name, "dockerfile.") {
			return "dockerfile"
		}
		return ""
	default:
		return ""
	}
}

// atPathCandidates returns tab-completion candidates for a partial @path token.
// partialToken includes the leading "@" (e.g. "@./src/").
func atPathCandidates(partialToken string, cwd string) []string {
	rawPath := strings.TrimPrefix(partialToken, "@")

	// Determine the directory to list and the prefix to match.
	var dir, prefix string
	if strings.HasSuffix(rawPath, "/") || rawPath == "" {
		dir = rawPath
		prefix = ""
	} else {
		dir = filepath.Dir(rawPath)
		prefix = filepath.Base(rawPath)
	}

	absDir := dir
	if !filepath.IsAbs(dir) {
		absDir = filepath.Join(cwd, dir)
	}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil
	}

	var candidates []string
	for _, e := range entries {
		name := e.Name()
		// Skip hidden files unless the user started typing one.
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		// Skip entries with spaces — they break tokenization.
		if strings.ContainsRune(name, ' ') {
			continue
		}
		rel := filepath.Join(dir, name)
		if e.IsDir() {
			rel += "/"
		}
		candidates = append(candidates, "@"+rel)
	}
	return candidates
}
