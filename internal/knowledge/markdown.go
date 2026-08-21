package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"cairn/internal/protocol"
	"cairn/internal/storage"
)

const maxArticleBytes = 2 << 20

type citation struct {
	SourceID string `json:"source_id"`
	Locator  string `json:"locator"`
}

type parsedArticle struct {
	ArticleID   string
	Title       string
	Slug        string
	Summary     string
	Sensitivity string
	Version     int
	Tags        []string
	SourceIDs   []string
	Citations   []citation
	Body        string
}

type articleRow struct {
	ArticleID   string
	Slug        string
	Title       string
	Path        string
	Sensitivity string
	Version     int
	Hash        string
	CreatedAt   string
	UpdatedAt   string
}

func managedArticlePath(store *storage.Storage, row articleRow) (string, *protocol.CodedError) {
	path := row.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(store.Paths.Root, filepath.FromSlash(path))
	}
	path = filepath.Clean(path)
	root := filepath.Join(store.Paths.Wiki, "articles")
	if !within(path, root) || path == root {
		return "", protocol.NewCodedError("PATH_INVALID", "managed article path is outside the Wiki", false, nil)
	}
	return path, nil
}

func readManagedArticle(store *storage.Storage, row articleRow) (parsedArticle, []byte, string, *protocol.CodedError) {
	path, codedErr := managedArticlePath(store, row)
	if codedErr != nil {
		return parsedArticle{}, nil, "", codedErr
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return parsedArticle{}, nil, path, protocol.NewCodedError("WIKI_DRIFT", "managed article file is missing", false, map[string]any{"path": path})
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return parsedArticle{}, nil, path, protocol.NewCodedError("WIKI_DRIFT", "managed article file is unavailable", false, map[string]any{"path": path})
	}
	if info.Size() > maxArticleBytes {
		return parsedArticle{}, nil, path, protocol.NewCodedError("ARTICLE_TOO_LARGE", "managed article exceeds the retrieval size limit", false, nil)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return parsedArticle{}, nil, path, protocol.NewCodedError("WIKI_DRIFT", "managed article file cannot be read", false, map[string]any{"path": path})
	}
	actualHash := hashBytes(contents)
	if actualHash != row.Hash {
		return parsedArticle{}, nil, path, protocol.NewCodedError("WIKI_DRIFT", "managed article file differs from its recorded hash", false, map[string]any{"path": path, "expected_hash": row.Hash, "actual_hash": actualHash})
	}
	parsed, err := parseManagedMarkdown(contents)
	if err != nil || parsed.ArticleID != row.ArticleID || parsed.Slug != row.Slug || parsed.Sensitivity != row.Sensitivity || parsed.Version != row.Version {
		return parsedArticle{}, nil, path, protocol.NewCodedError("WIKI_DRIFT", "managed article metadata does not match its recorded version", false, map[string]any{"path": path})
	}
	return parsed, contents, path, nil
}

func parseManagedMarkdown(contents []byte) (parsedArticle, error) {
	text := string(contents)
	if !strings.HasPrefix(text, "---\n") {
		return parsedArticle{}, errors.New("frontmatter start is missing")
	}
	rest := text[len("---\n"):]
	closeAt := strings.Index(rest, "\n---\n")
	if closeAt < 0 {
		return parsedArticle{}, errors.New("frontmatter end is missing")
	}
	frontmatter := rest[:closeAt]
	body := rest[closeAt+len("\n---\n"):]
	body = strings.TrimPrefix(body, "\n")
	var result parsedArticle
	section := ""
	var currentCitation *citation
	flushCitation := func() error {
		if currentCitation != nil {
			if currentCitation.SourceID == "" || currentCitation.Locator == "" {
				return errors.New("citation is incomplete")
			}
			result.Citations = append(result.Citations, *currentCitation)
			currentCitation = nil
		}
		return nil
	}
	for _, line := range strings.Split(frontmatter, "\n") {
		if line == "tags:" || line == "sources:" || line == "citations:" {
			if err := flushCitation(); err != nil {
				return parsedArticle{}, err
			}
			section = strings.TrimSuffix(line, ":")
			continue
		}
		if strings.HasPrefix(line, "  - source_id: ") {
			if err := flushCitation(); err != nil {
				return parsedArticle{}, err
			}
			value, err := parseFrontmatterValue(strings.TrimPrefix(line, "  - source_id: "))
			if err != nil {
				return parsedArticle{}, err
			}
			currentCitation = &citation{SourceID: value}
			section = "citations"
			continue
		}
		if strings.HasPrefix(line, "    locator: ") {
			if currentCitation == nil {
				return parsedArticle{}, errors.New("citation locator has no source")
			}
			value, err := parseFrontmatterValue(strings.TrimPrefix(line, "    locator: "))
			if err != nil {
				return parsedArticle{}, err
			}
			currentCitation.Locator = value
			continue
		}
		if strings.HasPrefix(line, "  - ") {
			value, err := parseFrontmatterValue(strings.TrimPrefix(line, "  - "))
			if err != nil {
				return parsedArticle{}, err
			}
			switch section {
			case "tags":
				result.Tags = append(result.Tags, value)
			case "sources":
				result.SourceIDs = append(result.SourceIDs, value)
			default:
				return parsedArticle{}, errors.New("unknown list section")
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) != 2 {
			return parsedArticle{}, fmt.Errorf("invalid frontmatter line %q", line)
		}
		section = ""
		value, err := parseFrontmatterValue(parts[1])
		if err != nil {
			return parsedArticle{}, err
		}
		switch parts[0] {
		case "cairn_article_id":
			result.ArticleID = value
		case "title":
			result.Title = value
		case "slug":
			result.Slug = value
		case "summary":
			result.Summary = value
		case "sensitivity":
			result.Sensitivity = value
		case "version":
			result.Version, err = strconv.Atoi(value)
			if err != nil {
				return parsedArticle{}, errors.New("article version is invalid")
			}
		default:
			return parsedArticle{}, fmt.Errorf("unknown frontmatter field %q", parts[0])
		}
	}
	if err := flushCitation(); err != nil {
		return parsedArticle{}, err
	}
	if result.ArticleID == "" || result.Title == "" || result.Slug == "" || result.Sensitivity == "" || result.Version < 1 {
		return parsedArticle{}, errors.New("required frontmatter is missing")
	}
	if result.Sensitivity != "normal" && result.Sensitivity != "sensitive" && result.Sensitivity != "restricted" {
		return parsedArticle{}, errors.New("article sensitivity is invalid")
	}
	result.Body = body
	result.Tags = uniqueStrings(result.Tags)
	result.SourceIDs = uniqueStrings(result.SourceIDs)
	result.Citations = uniqueCitations(result.Citations)
	return result, nil
}

func parseFrontmatterValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' {
		var result string
		if err := jsonUnquote(value, &result); err != nil {
			return "", err
		}
		return result, nil
	}
	if value == "" {
		return "", errors.New("frontmatter value is empty")
	}
	return value, nil
}

func jsonUnquote(value string, output *string) error {
	decoded, err := strconv.Unquote(value)
	if err != nil {
		return err
	}
	*output = decoded
	return nil
}

func tokenize(value string) []string {
	var result []string
	for _, token := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if token != "" {
			result = append(result, token)
		}
	}
	return result
}

func snippet(body, query string, limit int) string {
	body = strings.Join(strings.Fields(body), " ")
	if body == "" {
		return ""
	}
	if query != "" {
		lowerBody := strings.ToLower(body)
		for _, token := range tokenize(query) {
			if at := strings.Index(lowerBody, strings.ToLower(token)); at >= 0 {
				start := at - limit/3
				if start < 0 {
					start = 0
				}
				end := start + limit
				if end > len(body) {
					end = len(body)
				}
				return body[start:end]
			}
		}
	}
	if len(body) > limit {
		return body[:limit]
	}
	return body
}

func hashBytes(contents []byte) string {
	// Kept local to avoid making the compile package part of retrieval's API.
	hash := sha256.Sum256(contents)
	return "sha256:" + hex.EncodeToString(hash[:])
}

func within(path, root string) bool {
	path, _ = filepath.Abs(path)
	root, _ = filepath.Abs(root)
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "."
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func uniqueCitations(values []citation) []citation {
	seen := make(map[string]struct{}, len(values))
	result := make([]citation, 0, len(values))
	for _, value := range values {
		key := value.SourceID + "\x00" + value.Locator
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SourceID == result[j].SourceID {
			return result[i].Locator < result[j].Locator
		}
		return result[i].SourceID < result[j].SourceID
	})
	return result
}
