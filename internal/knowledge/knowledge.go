package knowledge

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/storage"
)

const (
	defaultCatalogLimit   = 100
	maxCatalogLimit       = 200
	defaultCandidateLimit = 10
	maxCandidateLimit     = 50
	defaultHistoryLimit   = 20
	maxHistoryLimit       = 100
	maxHistoryResponse    = 4 << 20
)

type catalogArguments struct {
	Topic string `json:"topic,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type candidateArguments struct {
	Query string `json:"query,omitempty"`
	Topic string `json:"topic,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type articleSelector struct {
	ArticleID string `json:"article_id,omitempty"`
	Slug      string `json:"slug,omitempty"`
}

type materializeArguments struct {
	ArticleID string `json:"article_id,omitempty"`
	Slug      string `json:"slug,omitempty"`
}

type historyArguments struct {
	ArticleID      string `json:"article_id,omitempty"`
	Slug           string `json:"slug,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	IncludeContent bool   `json:"include_content,omitempty"`
}

type catalogArticle struct {
	ArticleID   string   `json:"article_id"`
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Path        string   `json:"path"`
	Sensitivity string   `json:"sensitivity"`
	Version     int      `json:"version"`
	Hash        string   `json:"hash"`
	Tags        []string `json:"tags,omitempty"`
	SourceIDs   []string `json:"source_ids,omitempty"`
	Drift       bool     `json:"drift,omitempty"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

type topicEntry struct {
	Topic      string   `json:"topic"`
	Count      int      `json:"count"`
	ArticleIDs []string `json:"article_ids"`
}

type catalogData struct {
	Articles []catalogArticle `json:"articles"`
	Topics   []topicEntry     `json:"topics"`
	Count    int              `json:"count"`
}

type candidateData struct {
	ArticleID   string     `json:"article_id"`
	Slug        string     `json:"slug"`
	Title       string     `json:"title"`
	Path        string     `json:"path"`
	Sensitivity string     `json:"sensitivity"`
	Version     int        `json:"version"`
	Hash        string     `json:"hash"`
	Score       int        `json:"score"`
	Summary     string     `json:"summary,omitempty"`
	Snippet     string     `json:"snippet,omitempty"`
	Tags        []string   `json:"tags,omitempty"`
	SourceIDs   []string   `json:"source_ids,omitempty"`
	Citations   []citation `json:"citations,omitempty"`
	Drift       bool       `json:"drift,omitempty"`
}

type candidatesData struct {
	Query      string          `json:"query"`
	Candidates []candidateData `json:"candidates"`
	Count      int             `json:"count"`
}

type reindexData struct {
	Indexed int    `json:"indexed"`
	Skipped int    `json:"skipped"`
	Status  string `json:"status"`
}

type backfillArguments struct {
	SourceIDs []string `json:"source_ids,omitempty"`
	Limit     int      `json:"limit,omitempty"`
}
type backfillData struct {
	BackfillID         string   `json:"backfill_id"`
	State              string   `json:"state"`
	SourceIDs          []string `json:"source_ids"`
	ArticleIDs         []string `json:"article_ids"`
	RequiresModel      bool     `json:"requires_model"`
	AutomaticOnUpgrade bool     `json:"automatic_on_upgrade"`
}

// BackfillPlan creates an explicit legacy-work manifest. It only inventories
// sources without current extraction records; the Skill must start and review
// compile jobs, so upgrades never invoke a model implicitly.
func BackfillPlan(ctx context.Context, store *storage.Storage, req protocol.Request) (any, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[backfillArguments](req)
	if codedErr != nil {
		return nil, codedErr
	}
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
	if codedErr != nil {
		return nil, codedErr
	}
	limit, codedErr := boundedLimit(args.Limit, 100, 200)
	if codedErr != nil {
		return nil, codedErr
	}
	selected := make([]string, 0, len(args.SourceIDs))
	seen := map[string]bool{}
	for _, id := range args.SourceIDs {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			selected = append(selected, id)
		}
	}
	query := `SELECT s.source_id FROM sources s WHERE s.forgotten_at IS NULL AND s.sensitivity = 'normal'`
	params := []any{}
	if allowSensitive {
		query = `SELECT s.source_id FROM sources s WHERE s.forgotten_at IS NULL`
	}
	if len(selected) > 0 {
		query += ` AND s.source_id IN (` + placeholdersAny(len(selected)) + `)`
		for _, id := range selected {
			params = append(params, id)
		}
	}
	query += ` ORDER BY s.created_at, s.source_id LIMIT ?`
	params = append(params, limit)
	rows, err := store.DB.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot inspect legacy backfill sources", true, nil)
	}
	defer rows.Close()
	sourceIDs := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode legacy backfill source", true, nil)
		}
		usable, checkErr := usableActiveExtraction(ctx, store, id)
		if checkErr != nil {
			return nil, checkErr
		}
		if !usable {
			sourceIDs = append(sourceIDs, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish legacy backfill source read", true, nil)
	}
	articleSet := map[string]bool{}
	articleIDs := make([]string, 0)
	if len(sourceIDs) > 0 {
		articleRows, err := store.DB.QueryContext(ctx, `SELECT DISTINCT article_id FROM article_citations WHERE source_id IN (`+placeholdersAny(len(sourceIDs))+`) ORDER BY article_id`, stringSliceAny(sourceIDs)...)
		if err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot inspect cited articles for backfill", true, nil)
		}
		defer articleRows.Close()
		for articleRows.Next() {
			var id string
			if err := articleRows.Scan(&id); err != nil {
				return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode cited article for backfill", true, nil)
			}
			if !articleSet[id] {
				articleSet[id] = true
				articleIDs = append(articleIDs, id)
			}
		}
		if err := articleRows.Err(); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish cited article read", true, nil)
		}
	}
	sourceJSON, _ := json.Marshal(sourceIDs)
	articleJSON, _ := json.Marshal(articleIDs)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	fingerprint, err := req.Fingerprint()
	if err != nil {
		return nil, protocol.NewCodedError("REQUEST_INVALID", "cannot fingerprint request", false, nil)
	}
	backfillID := newBackfillID()
	responseData := backfillData{BackfillID: backfillID, State: "planned", SourceIDs: sourceIDs, ArticleIDs: articleIDs, RequiresModel: true, AutomaticOnUpgrade: false}
	response := protocol.NewSuccessResponse(req, responseData)
	responseBytes, _ := json.Marshal(response)
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin backfill plan", true, nil)
	}
	defer tx.Rollback()
	if stored, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve backfill idempotency", true, nil)
	} else if found {
		var replay protocol.Response
		if err := json.Unmarshal(stored.Response, &replay); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "stored backfill response is invalid", false, nil)
		}
		var data backfillData
		if replay.OK {
			raw, _ := json.Marshal(replay.Data)
			if err := json.Unmarshal(raw, &data); err != nil {
				return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "stored backfill response data is invalid", false, nil)
			}
			return data, nil
		}
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "stored backfill response is not successful", true, nil)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO backfill_jobs(backfill_id, state, source_ids_json, article_ids_json, created_at, updated_at) VALUES (?, 'planned', ?, ?, ?, ?)`, backfillID, sourceJSON, articleJSON, now, now); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot persist backfill plan", true, nil)
	}
	if err := storage.InsertAuditTx(ctx, tx, "aud_"+backfillID, req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"backfill_id": backfillID, "source_count": len(sourceIDs), "requires_model": true}); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot audit backfill plan", true, nil)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot complete backfill idempotency", true, nil)
	}
	if err := tx.Commit(); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot commit backfill plan", true, nil)
	}
	return responseData, nil
}

func usableActiveExtraction(ctx context.Context, store *storage.Storage, sourceID string) (bool, *protocol.CodedError) {
	var path, hash string
	err := store.DB.QueryRowContext(ctx, `SELECT path, content_hash FROM extractions WHERE source_id = ? AND status = 'active' ORDER BY created_at DESC LIMIT 1`, sourceID).Scan(&path, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot inspect source extraction", true, nil)
	}
	managed := filepath.Clean(filepath.Join(store.Paths.Root, filepath.FromSlash(path)))
	if !within(managed, store.Paths.Extractions) {
		return false, nil
	}
	info, err := os.Lstat(managed)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, nil
	}
	contents, err := os.ReadFile(managed)
	return err == nil && hashBytes(contents) == hash, nil
}

func newBackfillID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("backfill_%d", time.Now().UnixNano())
	}
	return "backfill_" + hex.EncodeToString(b)
}

func placeholdersAny(count int) string { return strings.TrimSuffix(strings.Repeat("?,", count), ",") }
func stringSliceAny(values []string) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}

// Reindex rebuilds the disposable article FTS projection from verified
// managed Markdown and SQLite metadata. It never invokes a model and is safe
// to repeat after an interrupted maintenance run.
func Reindex(ctx context.Context, store *storage.Storage, req protocol.Request) (any, *protocol.CodedError) {
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
	if codedErr != nil {
		return nil, codedErr
	}
	fingerprint, err := req.Fingerprint()
	if err != nil {
		return nil, protocol.NewCodedError("REQUEST_INVALID", "cannot fingerprint request", false, nil)
	}
	if record, found, err := store.ReadIdempotency(ctx, req.IdempotencyKey); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read reindex idempotency", true, nil)
	} else if found {
		if record.Fingerprint != fingerprint {
			return nil, protocol.NewCodedError("IDEMPOTENCY_CONFLICT", "idempotency key was used with different arguments", false, nil)
		}
		if record.Status == "processing" {
			return nil, protocol.NewCodedError("IDEMPOTENCY_IN_PROGRESS", "an identical reindex is already being processed", true, nil)
		}
		var cached reindexData
		if err := json.Unmarshal(record.Response, &cached); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "stored reindex response is invalid", false, nil)
		}
		return cached, nil
	}
	rows, codedErr := loadArticles(ctx, store)
	if codedErr != nil {
		return nil, codedErr
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin article index rebuild", true, nil)
	}
	defer tx.Rollback()
	if record, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve reindex idempotency", true, nil)
	} else if found {
		if record.Fingerprint != fingerprint {
			return nil, protocol.NewCodedError("IDEMPOTENCY_CONFLICT", "idempotency key was used with different arguments", false, nil)
		}
		if record.Status == "processing" {
			return nil, protocol.NewCodedError("IDEMPOTENCY_IN_PROGRESS", "an identical reindex is already being processed", true, nil)
		}
		var cached reindexData
		if err := json.Unmarshal(record.Response, &cached); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "stored reindex response is invalid", false, nil)
		}
		return cached, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM article_fts`); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot clear article index", true, nil)
	}
	indexed, skipped := 0, 0
	for _, row := range rows {
		if !sensitivityAllowed(row.Sensitivity, allowSensitive) {
			skipped++
			continue
		}
		parsed, _, _, readErr := readManagedArticle(store, row)
		if readErr != nil {
			if readErr.Code == "WIKI_DRIFT" || readErr.Code == "PATH_INVALID" || readErr.Code == "ARTICLE_TOO_LARGE" {
				skipped++
				continue
			}
			return nil, readErr
		}
		if err := storage.ReplaceArticleIndexTx(ctx, tx, row.ArticleID, row.Version, row.Title, row.Slug, strings.Join(parsed.Tags, " "), parsed.Summary, parsed.Body, strings.Join(parsed.SourceIDs, " ")); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot write article index", true, nil)
		}
		indexed++
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO index_meta(name, state, content_hash, updated_at) VALUES ('article_fts', 'ready', ?, ?) ON CONFLICT(name) DO UPDATE SET state = excluded.state, content_hash = excluded.content_hash, updated_at = excluded.updated_at`, fmt.Sprintf("count:%d", indexed), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot record article index health", true, nil)
	}
	data := reindexData{Indexed: indexed, Skipped: skipped, Status: fmt.Sprintf("indexed-%d", indexed)}
	dataBytes, _ := json.Marshal(data)
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, dataBytes); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot complete reindex idempotency", true, nil)
	}
	if err := tx.Commit(); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot commit article index rebuild", true, nil)
	}
	return data, nil
}

type materializedData struct {
	ArticleID   string     `json:"article_id"`
	Slug        string     `json:"slug"`
	Title       string     `json:"title"`
	Path        string     `json:"path"`
	Sensitivity string     `json:"sensitivity"`
	Version     int        `json:"version"`
	Hash        string     `json:"hash"`
	Bytes       int64      `json:"bytes"`
	Summary     string     `json:"summary"`
	Tags        []string   `json:"tags"`
	SourceIDs   []string   `json:"source_ids"`
	Citations   []citation `json:"citations"`
	Content     string     `json:"content,omitempty"`
	UpdatedAt   string     `json:"updated_at"`
}

type historyVersion struct {
	Version   int        `json:"version"`
	Hash      string     `json:"hash"`
	Path      string     `json:"path"`
	Bytes     int64      `json:"bytes"`
	JobID     string     `json:"job_id,omitempty"`
	CreatedAt string     `json:"created_at"`
	Citations []citation `json:"citations"`
	Content   string     `json:"content,omitempty"`
}

type historyData struct {
	ArticleID   string           `json:"article_id"`
	Slug        string           `json:"slug"`
	Title       string           `json:"title"`
	Sensitivity string           `json:"sensitivity"`
	Versions    []historyVersion `json:"versions"`
	Count       int              `json:"count"`
}

// Catalog returns only metadata and topics. It never uses the article body as a
// transport payload and treats an unverified file as drifted metadata.
func Catalog(ctx context.Context, store *storage.Storage, req protocol.Request) (any, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[catalogArguments](req)
	if codedErr != nil {
		return nil, codedErr
	}
	limit, codedErr := boundedLimit(args.Limit, defaultCatalogLimit, maxCatalogLimit)
	if codedErr != nil {
		return nil, codedErr
	}
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
	if codedErr != nil {
		return nil, codedErr
	}
	rows, codedErr := loadArticles(ctx, store)
	if codedErr != nil {
		return nil, codedErr
	}
	topic := strings.ToLower(strings.TrimSpace(args.Topic))
	result := catalogData{Articles: make([]catalogArticle, 0), Topics: make([]topicEntry, 0)}
	topics := make(map[string][]string)
	for _, row := range rows {
		if !sensitivityAllowed(row.Sensitivity, allowSensitive) {
			continue
		}
		parsed, _, _, readErr := readManagedArticle(store, row)
		article := catalogArticle{ArticleID: row.ArticleID, Slug: row.Slug, Title: row.Title, Path: row.Path, Sensitivity: row.Sensitivity, Version: row.Version, Hash: row.Hash, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
		if readErr != nil {
			article.Drift = readErr.Code == "WIKI_DRIFT" || readErr.Code == "PATH_INVALID" || readErr.Code == "ARTICLE_TOO_LARGE"
			if !article.Drift {
				return nil, readErr
			}
		} else {
			article.Tags = parsed.Tags
			article.SourceIDs = parsed.SourceIDs
		}
		if topic != "" && !containsFold(article.Tags, topic) {
			continue
		}
		if len(result.Articles) < limit {
			result.Articles = append(result.Articles, article)
		}
		for _, tag := range article.Tags {
			tag = strings.ToLower(strings.TrimSpace(tag))
			if tag != "" {
				topics[tag] = append(topics[tag], row.ArticleID)
			}
		}
	}
	result.Count = len(result.Articles)
	topicNames := make([]string, 0, len(topics))
	for name := range topics {
		topicNames = append(topicNames, name)
	}
	sort.Strings(topicNames)
	for _, name := range topicNames {
		ids := append([]string(nil), topics[name]...)
		sort.Strings(ids)
		result.Topics = append(result.Topics, topicEntry{Topic: name, Count: len(ids), ArticleIDs: ids})
	}
	return result, nil
}

// Candidates performs deterministic local ranking and returns only bounded
// excerpts. It does not generate or infer an answer.
func Candidates(ctx context.Context, store *storage.Storage, req protocol.Request) (any, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[candidateArguments](req)
	if codedErr != nil {
		return nil, codedErr
	}
	limit, codedErr := boundedLimit(args.Limit, defaultCandidateLimit, maxCandidateLimit)
	if codedErr != nil {
		return nil, codedErr
	}
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
	if codedErr != nil {
		return nil, codedErr
	}
	rows, codedErr := loadArticles(ctx, store)
	if codedErr != nil {
		return nil, codedErr
	}
	query := strings.TrimSpace(args.Query)
	topic := strings.ToLower(strings.TrimSpace(args.Topic))
	ftsScores := make(map[string]int)
	if query != "" {
		match := ftsMatch(query)
		if match != "" {
			ftsRows, err := store.DB.QueryContext(ctx, `SELECT article_id, bm25(article_fts) FROM article_fts WHERE article_fts MATCH ? ORDER BY bm25(article_fts), article_id LIMIT ?`, match, maxCandidateLimit)
			if err == nil {
				defer ftsRows.Close()
				for ftsRows.Next() {
					var id string
					var rank float64
					if ftsRows.Scan(&id, &rank) == nil {
						score := int(1000 - rank*100)
						if score < 1 {
							score = 1
						}
						ftsScores[id] = score
					}
				}
			}
		}
	}
	results := make([]candidateData, 0)
	for _, row := range rows {
		if !sensitivityAllowed(row.Sensitivity, allowSensitive) {
			continue
		}
		parsed, _, _, readErr := readManagedArticle(store, row)
		if readErr != nil {
			if readErr.Code != "WIKI_DRIFT" && readErr.Code != "PATH_INVALID" && readErr.Code != "ARTICLE_TOO_LARGE" {
				return nil, readErr
			}
			if query != "" && scoreMetadata(row, query) == 0 {
				continue
			}
			results = append(results, candidateData{ArticleID: row.ArticleID, Slug: row.Slug, Title: row.Title, Path: row.Path, Sensitivity: row.Sensitivity, Version: row.Version, Hash: row.Hash, Score: scoreMetadata(row, query), Drift: true})
			continue
		}
		if topic != "" && !containsFold(parsed.Tags, topic) {
			continue
		}
		score := scoreArticle(parsed, query)
		if indexed, ok := ftsScores[row.ArticleID]; ok {
			score = indexed
		}
		if query != "" && score == 0 {
			continue
		}
		citations, codedErr := loadCitations(ctx, store.DB, row.ArticleID, row.Version)
		if codedErr != nil {
			return nil, codedErr
		}
		results = append(results, candidateData{ArticleID: row.ArticleID, Slug: row.Slug, Title: row.Title, Path: row.Path, Sensitivity: row.Sensitivity, Version: row.Version, Hash: row.Hash, Score: score, Summary: parsed.Summary, Snippet: snippet(parsed.Body, query, 240), Tags: parsed.Tags, SourceIDs: parsed.SourceIDs, Citations: citations})
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].Slug != results[j].Slug {
			return results[i].Slug < results[j].Slug
		}
		return results[i].ArticleID < results[j].ArticleID
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return candidatesData{Query: query, Candidates: results, Count: len(results)}, nil
}

func ftsMatch(query string) string {
	tokens := tokenize(query)
	parts := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(token, `"`, ""), "'", ""))
		if token != "" {
			parts = append(parts, `"`+token+`"`)
		}
	}
	return strings.Join(parts, " OR ")
}

// Materialize returns one complete, hash-verified current article.
func Materialize(ctx context.Context, store *storage.Storage, req protocol.Request) (any, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[materializeArguments](req)
	if codedErr != nil {
		return nil, codedErr
	}
	row, codedErr := selectArticle(ctx, store, articleSelector{ArticleID: args.ArticleID, Slug: args.Slug})
	if codedErr != nil {
		return nil, codedErr
	}
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
	if codedErr != nil {
		return nil, codedErr
	}
	if !sensitivityAllowed(row.Sensitivity, allowSensitive) {
		return nil, protocol.NewCodedError("SENSITIVITY_DENIED", "article content requires sensitive-content permission", false, nil)
	}
	parsed, contents, path, codedErr := readManagedArticle(store, row)
	if codedErr != nil {
		return nil, codedErr
	}
	citations, codedErr := loadCitations(ctx, store.DB, row.ArticleID, row.Version)
	if codedErr != nil {
		return nil, codedErr
	}
	inlineContent, codedErr := protocol.OptionBool(req, "inline_content")
	if codedErr != nil {
		return nil, codedErr
	}
	data := materializedData{ArticleID: row.ArticleID, Slug: row.Slug, Title: row.Title, Path: path, Sensitivity: row.Sensitivity, Version: row.Version, Hash: row.Hash, Bytes: int64(len(contents)), Summary: parsed.Summary, Tags: parsed.Tags, SourceIDs: parsed.SourceIDs, Citations: citations, UpdatedAt: row.UpdatedAt}
	if inlineContent || !hasOption(req, "inline_content") {
		data.Content = string(contents)
	}
	return data, nil
}

func hasOption(req protocol.Request, key string) bool {
	_, ok := req.Options[key]
	return ok
}

// History reads version records from SQLite, never an arbitrary filesystem path.
func History(ctx context.Context, store *storage.Storage, req protocol.Request) (any, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[historyArguments](req)
	if codedErr != nil {
		return nil, codedErr
	}
	limit, codedErr := boundedLimit(args.Limit, defaultHistoryLimit, maxHistoryLimit)
	if codedErr != nil {
		return nil, codedErr
	}
	row, codedErr := selectArticle(ctx, store, articleSelector{ArticleID: args.ArticleID, Slug: args.Slug})
	if codedErr != nil {
		return nil, codedErr
	}
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
	if codedErr != nil {
		return nil, codedErr
	}
	if !sensitivityAllowed(row.Sensitivity, allowSensitive) {
		return nil, protocol.NewCodedError("SENSITIVITY_DENIED", "article history requires sensitive-content permission", false, nil)
	}
	rows, err := store.DB.QueryContext(ctx, `SELECT version, content_hash, path, content, COALESCE(job_id, ''), created_at FROM article_versions WHERE article_id = ? ORDER BY version DESC LIMIT ?`, row.ArticleID, limit)
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read article history", true, nil)
	}
	defer rows.Close()
	result := historyData{ArticleID: row.ArticleID, Slug: row.Slug, Title: row.Title, Sensitivity: row.Sensitivity, Versions: make([]historyVersion, 0)}
	contentBytes := 0
	for rows.Next() {
		var version int
		var contentHash, path, content, jobID, createdAt string
		if err := rows.Scan(&version, &contentHash, &path, &content, &jobID, &createdAt); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode article history", true, nil)
		}
		if hashBytes([]byte(content)) != contentHash {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "article history content hash is invalid", false, map[string]any{"article_id": row.ArticleID, "version": version})
		}
		citations, codedErr := loadCitations(ctx, store.DB, row.ArticleID, version)
		if codedErr != nil {
			return nil, codedErr
		}
		item := historyVersion{Version: version, Hash: contentHash, Path: path, Bytes: int64(len(content)), JobID: jobID, CreatedAt: createdAt, Citations: citations}
		if args.IncludeContent {
			if len(content) > maxArticleBytes {
				return nil, protocol.NewCodedError("ARTICLE_TOO_LARGE", "historical article exceeds the retrieval size limit", false, nil)
			}
			contentBytes += len(content)
			if contentBytes > maxHistoryResponse {
				return nil, protocol.NewCodedError("RESPONSE_TOO_LARGE", "requested history content exceeds the response limit", false, nil)
			}
			item.Content = content
		}
		result.Versions = append(result.Versions, item)
	}
	if err := rows.Err(); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish article history", true, nil)
	}
	result.Count = len(result.Versions)
	return result, nil
}

func loadArticles(ctx context.Context, store *storage.Storage) ([]articleRow, *protocol.CodedError) {
	rows, err := store.DB.QueryContext(ctx, `SELECT article_id, slug, title, path, sensitivity, current_version, current_hash, created_at, updated_at FROM articles WHERE forgotten_at IS NULL ORDER BY slug ASC, article_id ASC`)
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read article catalogue", true, nil)
	}
	defer rows.Close()
	result := make([]articleRow, 0)
	for rows.Next() {
		var row articleRow
		if err := rows.Scan(&row.ArticleID, &row.Slug, &row.Title, &row.Path, &row.Sensitivity, &row.Version, &row.Hash, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode article catalogue", true, nil)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish article catalogue", true, nil)
	}
	return result, nil
}

func selectArticle(ctx context.Context, store *storage.Storage, selector articleSelector) (articleRow, *protocol.CodedError) {
	if (strings.TrimSpace(selector.ArticleID) == "") == (strings.TrimSpace(selector.Slug) == "") {
		return articleRow{}, protocol.NewCodedError("REQUEST_INVALID", "exactly one of article_id or slug is required", false, nil)
	}
	var row articleRow
	var err error
	if strings.TrimSpace(selector.ArticleID) != "" {
		err = store.DB.QueryRowContext(ctx, `SELECT article_id, slug, title, path, sensitivity, current_version, current_hash, created_at, updated_at FROM articles WHERE article_id = ? AND forgotten_at IS NULL`, selector.ArticleID).Scan(&row.ArticleID, &row.Slug, &row.Title, &row.Path, &row.Sensitivity, &row.Version, &row.Hash, &row.CreatedAt, &row.UpdatedAt)
	} else {
		err = store.DB.QueryRowContext(ctx, `SELECT article_id, slug, title, path, sensitivity, current_version, current_hash, created_at, updated_at FROM articles WHERE slug = ? AND forgotten_at IS NULL`, selector.Slug).Scan(&row.ArticleID, &row.Slug, &row.Title, &row.Path, &row.Sensitivity, &row.Version, &row.Hash, &row.CreatedAt, &row.UpdatedAt)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return articleRow{}, protocol.NewCodedError("ARTICLE_NOT_FOUND", "article was not found", false, nil)
	}
	if err != nil {
		return articleRow{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read article metadata", true, nil)
	}
	return row, nil
}

func loadCitations(ctx context.Context, db *sql.DB, articleID string, version int) ([]citation, *protocol.CodedError) {
	rows, err := db.QueryContext(ctx, `SELECT source_id, locator FROM article_citations WHERE article_id = ? AND version = ? ORDER BY source_id ASC, locator ASC`, articleID, version)
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read article citations", true, nil)
	}
	defer rows.Close()
	result := make([]citation, 0)
	for rows.Next() {
		var value citation
		if err := rows.Scan(&value.SourceID, &value.Locator); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode article citation", true, nil)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish article citation read", true, nil)
	}
	return result, nil
}

func scoreArticle(article parsedArticle, query string) int {
	tokens := tokenize(query)
	if len(tokens) == 0 {
		return 0
	}
	title := strings.ToLower(article.Title + " " + article.Slug)
	tags := strings.ToLower(strings.Join(article.Tags, " "))
	summary := strings.ToLower(article.Summary)
	body := strings.ToLower(article.Body)
	score := 0
	for _, token := range tokens {
		if strings.Contains(title, token) {
			score += 10
		}
		if strings.Contains(tags, token) {
			score += 6
		}
		if strings.Contains(summary, token) {
			score += 4
		}
		if strings.Contains(body, token) {
			score++
		}
	}
	return score
}

func scoreMetadata(row articleRow, query string) int {
	if query == "" {
		return 0
	}
	tokens := tokenize(query)
	text := strings.ToLower(row.Title + " " + row.Slug)
	score := 0
	for _, token := range tokens {
		if strings.Contains(text, token) {
			score += 10
		}
	}
	return score
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func sensitivityAllowed(value string, allowSensitive bool) bool {
	return allowSensitive || value == "normal"
}

func boundedLimit(value, defaultValue, maximum int) (int, *protocol.CodedError) {
	if value == 0 {
		return defaultValue, nil
	}
	if value < 1 || value > maximum {
		return 0, protocol.NewCodedError("REQUEST_INVALID", "limit is outside the supported range", false, map[string]any{"max": maximum})
	}
	return value, nil
}
