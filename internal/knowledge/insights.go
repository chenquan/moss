package knowledge

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/storage"
)

const (
	defaultInsightsLimit    = 20
	maxInsightsLimit        = 50
	maxInsightsTopicBytes   = 240
	maxInsightCitations     = 100
	maxInsightArticles      = 20
	maxInsightActions       = 20
	maxInsightReviewItems   = 200
	maxInsightSourceIDs     = 100
	maxInsightReviewSignals = 8
)

type insightsArguments struct {
	Topic string `json:"topic,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type insightsData struct {
	Topic       string              `json:"topic,omitempty"`
	Decisions   []decisionInsight   `json:"decisions"`
	ReviewItems []insightReviewItem `json:"review_items"`
	Count       int                 `json:"count"`
	TotalCount  int                 `json:"total_count"`
	Truncated   bool                `json:"truncated"`
}

type decisionInsight struct {
	FactID      string           `json:"fact_id"`
	FactKey     string           `json:"fact_key"`
	Kind        string           `json:"kind"`
	Text        string           `json:"text"`
	Version     int              `json:"version"`
	Status      string           `json:"status"`
	Freshness   string           `json:"freshness"`
	Sensitivity string           `json:"sensitivity"`
	UpdatedAt   string           `json:"updated_at"`
	SourceIDs   []string         `json:"source_ids"`
	Citations   []citation       `json:"citations"`
	Articles    []insightArticle `json:"articles,omitempty"`
	Actions     []insightAction  `json:"actions,omitempty"`
}

type insightArticle struct {
	ArticleID   string   `json:"article_id"`
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Sensitivity string   `json:"sensitivity"`
	Version     int      `json:"version"`
	Hash        string   `json:"hash"`
	SourceIDs   []string `json:"source_ids"`
	Drift       bool     `json:"drift,omitempty"`
}

type insightAction struct {
	ActionID    string `json:"action_id"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	DueAt       string `json:"due_at,omitempty"`
	Sensitivity string `json:"sensitivity"`
	SourceID    string `json:"source_id"`
	Revision    int    `json:"revision"`
	Association string `json:"association"`
}

type insightReviewItem struct {
	Kind                string   `json:"kind"`
	FactID              string   `json:"fact_id"`
	Version             int      `json:"version,omitempty"`
	ArticleID           string   `json:"article_id,omitempty"`
	SupersededByFactID  string   `json:"superseded_by_fact_id,omitempty"`
	SupersededByVersion int      `json:"superseded_by_version,omitempty"`
	Reason              string   `json:"reason"`
	SourceIDs           []string `json:"source_ids,omitempty"`
}

type insightCandidate struct {
	Decision    decisionInsight
	ReviewItems []insightReviewItem
	Priority    int
	FactKey     string
	FactID      string
}

type insightFactRow struct {
	FactID       string
	FactKey      string
	Kind         string
	Version      int
	Status       string
	Freshness    string
	Text         string
	ExtractionID string
	UpdatedAt    string
}

type insightArticleRow struct {
	ArticleID   string
	Slug        string
	Title       string
	Path        string
	Sensitivity string
	Version     int
	Hash        string
}

// Insights returns a bounded, deterministic view over the current decision
// facts. It deliberately derives all associations at read time so freshness,
// forgotten sources, action revisions, and Wiki drift are not hidden behind a
// second projection that could become stale.
func Insights(ctx context.Context, store *storage.Storage, req protocol.Request) (any, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[insightsArguments](req)
	if codedErr != nil {
		return nil, codedErr
	}
	topic := strings.TrimSpace(args.Topic)
	if len([]byte(topic)) > maxInsightsTopicBytes {
		return nil, protocol.NewCodedError("REQUEST_INVALID", "topic exceeds the maximum size", false, map[string]any{"max_bytes": maxInsightsTopicBytes})
	}
	limit, codedErr := boundedLimit(args.Limit, defaultInsightsLimit, maxInsightsLimit)
	if codedErr != nil {
		return nil, codedErr
	}
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
	if codedErr != nil {
		return nil, codedErr
	}

	rows, totalCount, codedErr := loadInsightFactRows(ctx, store.DB, topic, limit, allowSensitive)
	if codedErr != nil {
		return nil, codedErr
	}
	candidates := make([]insightCandidate, 0, len(rows))
	for _, row := range rows {
		citations, sourceIDs, sensitivity, unavailableSources, codedErr := loadInsightFactEvidence(ctx, store, row.FactID, row.Version, row.ExtractionID, allowSensitive)
		if codedErr != nil {
			return nil, codedErr
		}
		// A fact without at least one permitted, live citation cannot be
		// surfaced as a trusted decision insight. This also prevents a
		// forgotten source's fact text from reappearing through the insight API.
		if len(sourceIDs) == 0 {
			continue
		}

		decision := decisionInsight{
			FactID: row.FactID, FactKey: row.FactKey, Kind: row.Kind, Text: row.Text,
			Version: row.Version, Status: row.Status, Freshness: row.Freshness,
			Sensitivity: sensitivity, UpdatedAt: row.UpdatedAt,
			SourceIDs: sourceIDs, Citations: citations,
		}
		reviewItems, historicalChange, codedErr := loadInsightReviewSignals(ctx, store.DB, row)
		if codedErr != nil {
			return nil, codedErr
		}
		for _, sourceID := range unavailableSources {
			reviewItems = append(reviewItems, insightReviewItem{Kind: "evidence_unavailable", FactID: row.FactID, Version: row.Version, Reason: "a cited source or extraction is unavailable", SourceIDs: []string{sourceID}})
		}
		priority := insightPriority(row.Status, row.Freshness, historicalChange)
		candidates = append(candidates, insightCandidate{Decision: decision, ReviewItems: reviewItems, Priority: priority, FactKey: row.FactKey, FactID: row.FactID})
	}
	truncated := totalCount > len(candidates)

	result := insightsData{Topic: topic, Decisions: make([]decisionInsight, 0, len(candidates)), ReviewItems: make([]insightReviewItem, 0), Count: len(candidates), TotalCount: totalCount, Truncated: truncated}
	for _, candidate := range candidates {
		articles, articleReview, codedErr := loadInsightArticles(ctx, store, candidate.Decision.FactID, candidate.Decision.SourceIDs, allowSensitive)
		if codedErr != nil {
			return nil, codedErr
		}
		actions, codedErr := loadInsightActions(ctx, store.DB, candidate.Decision.SourceIDs, allowSensitive)
		if codedErr != nil {
			return nil, codedErr
		}
		candidate.Decision.Articles = articles
		candidate.Decision.Actions = actions
		result.Decisions = append(result.Decisions, candidate.Decision)
		result.ReviewItems = append(result.ReviewItems, candidate.ReviewItems...)
		result.ReviewItems = append(result.ReviewItems, articleReview...)
	}
	sortInsightReviewItems(result.ReviewItems)
	if len(result.ReviewItems) > maxInsightReviewItems {
		result.ReviewItems = result.ReviewItems[:maxInsightReviewItems]
	}
	return result, nil
}

func loadInsightFactRows(ctx context.Context, db *sql.DB, topic string, limit int, allowSensitive bool) ([]insightFactRow, int, *protocol.CodedError) {
	from := ` FROM facts f JOIN fact_versions fv ON fv.fact_id = f.fact_id AND fv.version = f.current_version`
	where := ` WHERE f.kind = 'decision'
		AND EXISTS (SELECT 1 FROM fact_citations eligible_fc JOIN sources eligible_s ON eligible_s.source_id = eligible_fc.source_id
			WHERE eligible_fc.fact_id = f.fact_id AND eligible_fc.version = f.current_version AND eligible_s.forgotten_at IS NULL)`
	args := make([]any, 0, 3)
	if !allowSensitive {
		where += ` AND NOT EXISTS (SELECT 1 FROM fact_citations blocked_fc JOIN sources blocked_s ON blocked_s.source_id = blocked_fc.source_id
			WHERE blocked_fc.fact_id = f.fact_id AND blocked_fc.version = f.current_version AND blocked_s.forgotten_at IS NULL AND blocked_s.sensitivity IN ('sensitive', 'restricted'))`
	}
	if topic != "" {
		where += ` AND (LOWER(f.fact_key) LIKE ? ESCAPE '\' OR LOWER(fv.text) LIKE ? ESCAPE '\')`
		pattern := insightLikePattern(topic)
		args = append(args, pattern, pattern)
	}
	countQuery := `SELECT COUNT(*)` + from + where
	var totalCount int
	if err := db.QueryRowContext(ctx, countQuery, args...).Scan(&totalCount); err != nil {
		return nil, 0, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot count decision facts", true, nil)
	}
	query := `SELECT f.fact_id, f.fact_key, f.kind, f.current_version, f.status, f.freshness, fv.text, COALESCE(fv.extraction_id, ''), f.updated_at` +
		from + where + ` ORDER BY CASE WHEN f.status = 'retracted' THEN 0 WHEN f.freshness = 'stale' THEN 1
			WHEN f.status = 'superseded' OR EXISTS (SELECT 1 FROM fact_versions history_v WHERE history_v.fact_id = f.fact_id AND history_v.version < f.current_version AND history_v.status IN ('superseded', 'retracted'))
			OR EXISTS (SELECT 1 FROM fact_versions successor_v JOIN facts successor_f ON successor_f.fact_id = successor_v.fact_id AND successor_f.kind = 'decision'
				WHERE successor_v.supersedes_fact_id = f.fact_id AND successor_v.fact_id <> f.fact_id) THEN 2 ELSE 3 END ASC, f.fact_key ASC, f.fact_id ASC LIMIT ?`
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read decision facts", true, nil)
	}
	defer rows.Close()
	result := make([]insightFactRow, 0, limit)
	for rows.Next() {
		var row insightFactRow
		if err := rows.Scan(&row.FactID, &row.FactKey, &row.Kind, &row.Version, &row.Status, &row.Freshness, &row.Text, &row.ExtractionID, &row.UpdatedAt); err != nil {
			return nil, 0, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode decision fact", true, nil)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish decision fact read", true, nil)
	}
	return result, totalCount, nil
}

func insightLikePattern(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return `%` + strings.ToLower(value) + `%`
}

func loadInsightFactEvidence(ctx context.Context, store *storage.Storage, factID string, version int, extractionID string, allowSensitive bool) ([]citation, []string, string, []string, *protocol.CodedError) {
	db := store.DB
	rows, err := db.QueryContext(ctx, `
		SELECT fc.source_id, fc.locator, s.sensitivity, COALESCE(s.forgotten_at, '')
		FROM fact_citations fc
		JOIN sources s ON s.source_id = fc.source_id
		WHERE fc.fact_id = ? AND fc.version = ?
		ORDER BY fc.source_id ASC, fc.locator ASC`, factID, version)
	if err != nil {
		return nil, nil, "", nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read decision citations", true, nil)
	}
	defer rows.Close()

	citations := make([]citation, 0)
	sourceIDs := make([]string, 0)
	seenSources := map[string]bool{}
	unavailable := make([]string, 0)
	maxSensitivity := "normal"
	for rows.Next() {
		var sourceID, locator, sensitivity, forgotten string
		if err := rows.Scan(&sourceID, &locator, &sensitivity, &forgotten); err != nil {
			return nil, nil, "", nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode decision citation", true, nil)
		}
		if forgotten != "" {
			if !containsString(unavailable, sourceID) {
				unavailable = append(unavailable, sourceID)
			}
			continue
		}
		if sensitivityRankInsight(sensitivity) > sensitivityRankInsight(maxSensitivity) {
			maxSensitivity = sensitivity
		}
		if !allowSensitive && sensitivityRankInsight(sensitivity) > sensitivityRankInsight("normal") {
			return nil, nil, "", nil, nil
		}
		if len(citations) < maxInsightCitations {
			citations = append(citations, citation{SourceID: sourceID, Locator: locator})
		}
		if !seenSources[sourceID] && len(sourceIDs) < maxInsightSourceIDs {
			seenSources[sourceID] = true
			sourceIDs = append(sourceIDs, sourceID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, "", nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish decision citation read", true, nil)
	}
	if extractionID != "" {
		extractionSourceID, usable, codedErr := usableInsightExtraction(ctx, store, extractionID)
		if codedErr != nil {
			return nil, nil, "", nil, codedErr
		}
		if !usable {
			if extractionSourceID == "" && len(sourceIDs) > 0 {
				extractionSourceID = sourceIDs[0]
			}
			if extractionSourceID != "" && !containsString(unavailable, extractionSourceID) {
				unavailable = append(unavailable, extractionSourceID)
			}
		}
	}
	return citations, sourceIDs, maxSensitivity, unavailable, nil
}

func usableInsightExtraction(ctx context.Context, store *storage.Storage, extractionID string) (string, bool, *protocol.CodedError) {
	var sourceID, status, path, contentHash string
	err := store.DB.QueryRowContext(ctx, `SELECT source_id, status, path, content_hash FROM extractions WHERE extraction_id = ?`, extractionID).Scan(&sourceID, &status, &path, &contentHash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read decision extraction", true, nil)
	}
	if status != "active" {
		return sourceID, false, nil
	}
	managed := filepath.Clean(filepath.Join(store.Paths.Root, filepath.FromSlash(path)))
	if !within(managed, store.Paths.Extractions) {
		return sourceID, false, nil
	}
	info, err := os.Lstat(managed)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return sourceID, false, nil
	}
	contents, err := os.ReadFile(managed)
	if err != nil || hashBytes(contents) != contentHash {
		return sourceID, false, nil
	}
	return sourceID, true, nil
}

func loadInsightReviewSignals(ctx context.Context, db *sql.DB, row insightFactRow) ([]insightReviewItem, bool, *protocol.CodedError) {
	items := make([]insightReviewItem, 0)
	if row.Freshness == "stale" {
		items = append(items, insightReviewItem{Kind: "stale", FactID: row.FactID, Version: row.Version, Reason: "current decision freshness is stale"})
	}
	if row.Status == "retracted" {
		items = append(items, insightReviewItem{Kind: "retracted", FactID: row.FactID, Version: row.Version, Reason: "current decision version is retracted"})
	}
	if row.Status == "superseded" {
		items = append(items, insightReviewItem{Kind: "superseded", FactID: row.FactID, Version: row.Version, Reason: "current decision version is superseded"})
	}
	rows, err := db.QueryContext(ctx, `SELECT version, status FROM fact_versions WHERE fact_id = ? AND version < ? AND status IN ('superseded', 'retracted') ORDER BY version DESC LIMIT ?`, row.FactID, row.Version, maxInsightReviewSignals)
	if err != nil {
		return nil, false, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read decision history", true, nil)
	}
	defer rows.Close()
	historicalChange := false
	for rows.Next() {
		var version int
		var status string
		if err := rows.Scan(&version, &status); err != nil {
			return nil, false, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode decision history", true, nil)
		}
		historicalChange = true
		items = append(items, insightReviewItem{Kind: status, FactID: row.FactID, Version: version, Reason: "historical decision version was " + status})
	}
	if err := rows.Err(); err != nil {
		return nil, false, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish decision history read", true, nil)
	}
	crossRows, err := db.QueryContext(ctx, `SELECT successor_v.fact_id, successor_v.version FROM fact_versions successor_v JOIN facts successor_f ON successor_f.fact_id = successor_v.fact_id AND successor_f.kind = 'decision' WHERE successor_v.supersedes_fact_id = ? AND successor_v.fact_id <> ? ORDER BY successor_v.fact_id ASC, successor_v.version DESC LIMIT ?`, row.FactID, row.FactID, maxInsightReviewSignals)
	if err != nil {
		return nil, false, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read cross-fact decision history", true, nil)
	}
	defer crossRows.Close()
	for crossRows.Next() {
		var successorFactID string
		var successorVersion int
		if err := crossRows.Scan(&successorFactID, &successorVersion); err != nil {
			return nil, false, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode cross-fact decision history", true, nil)
		}
		historicalChange = true
		items = append(items, insightReviewItem{Kind: "superseded", FactID: row.FactID, Version: row.Version, SupersededByFactID: successorFactID, SupersededByVersion: successorVersion, Reason: "decision was explicitly superseded by another fact"})
	}
	if err := crossRows.Err(); err != nil {
		return nil, false, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish cross-fact decision history read", true, nil)
	}
	return items, historicalChange, nil
}

func loadInsightArticles(ctx context.Context, store *storage.Storage, factID string, sourceIDs []string, allowSensitive bool) ([]insightArticle, []insightReviewItem, *protocol.CodedError) {
	if len(sourceIDs) == 0 {
		return nil, nil, nil
	}
	query := `SELECT DISTINCT a.article_id, a.slug, a.title, a.path, a.sensitivity, a.current_version, a.current_hash FROM articles a JOIN article_citations ac ON ac.article_id = a.article_id AND ac.version = a.current_version WHERE a.forgotten_at IS NULL AND ac.source_id IN (` + placeholdersAny(len(sourceIDs)) + `)`
	if !allowSensitive {
		query += ` AND a.sensitivity = 'normal'`
	}
	query += ` ORDER BY a.slug ASC, a.article_id ASC LIMIT ?`
	args := stringSliceAny(sourceIDs)
	args = append(args, maxInsightArticles)
	rows, err := store.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read decision articles", true, nil)
	}
	defer rows.Close()

	articles := make([]insightArticle, 0)
	review := make([]insightReviewItem, 0)
	for rows.Next() {
		var row insightArticleRow
		if err := rows.Scan(&row.ArticleID, &row.Slug, &row.Title, &row.Path, &row.Sensitivity, &row.Version, &row.Hash); err != nil {
			return nil, nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode decision article", true, nil)
		}
		shared, codedErr := sharedArticleSources(ctx, store.DB, row.ArticleID, row.Version, sourceIDs)
		if codedErr != nil {
			return nil, nil, codedErr
		}
		if len(shared) == 0 {
			continue
		}
		_, _, _, readErr := readManagedArticle(store, articleRow{ArticleID: row.ArticleID, Slug: row.Slug, Title: row.Title, Path: row.Path, Sensitivity: row.Sensitivity, Version: row.Version, Hash: row.Hash})
		drift := readErr != nil
		articles = append(articles, insightArticle{ArticleID: row.ArticleID, Slug: row.Slug, Title: row.Title, Sensitivity: row.Sensitivity, Version: row.Version, Hash: row.Hash, SourceIDs: shared, Drift: drift})
		if drift {
			review = append(review, insightReviewItem{Kind: "article_drift", FactID: factID, ArticleID: row.ArticleID, Reason: "associated article is not verified against its managed hash", SourceIDs: shared})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish decision article read", true, nil)
	}
	return articles, review, nil
}

func sharedArticleSources(ctx context.Context, db *sql.DB, articleID string, version int, sourceIDs []string) ([]string, *protocol.CodedError) {
	query := `SELECT source_id FROM article_citations WHERE article_id = ? AND version = ? AND source_id IN (` + placeholdersAny(len(sourceIDs)) + `) ORDER BY source_id ASC`
	args := []any{articleID, version}
	args = append(args, stringSliceAny(sourceIDs)...)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read shared article citations", true, nil)
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var sourceID string
		if err := rows.Scan(&sourceID); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode shared article citation", true, nil)
		}
		result = append(result, sourceID)
	}
	if err := rows.Err(); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish shared article citation read", true, nil)
	}
	return uniqueStrings(result), nil
}

func loadInsightActions(ctx context.Context, db *sql.DB, sourceIDs []string, allowSensitive bool) ([]insightAction, *protocol.CodedError) {
	if len(sourceIDs) == 0 {
		return nil, nil
	}
	query := `SELECT action_id, kind, title, status, COALESCE(due_at, ''), sensitivity, source_id, revision FROM actions WHERE forgotten_at IS NULL AND source_id IN (` + placeholdersAny(len(sourceIDs)) + `)`
	if !allowSensitive {
		query += ` AND sensitivity = 'normal'`
	}
	query += ` ORDER BY action_id ASC LIMIT ?`
	args := stringSliceAny(sourceIDs)
	args = append(args, maxInsightActions)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read decision actions", true, nil)
	}
	defer rows.Close()
	result := make([]insightAction, 0)
	for rows.Next() {
		var item insightAction
		if err := rows.Scan(&item.ActionID, &item.Kind, &item.Title, &item.Status, &item.DueAt, &item.Sensitivity, &item.SourceID, &item.Revision); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode decision action", true, nil)
		}
		item.Association = "shared_source"
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish decision action read", true, nil)
	}
	return result, nil
}

func insightPriority(status, freshness string, historicalChange bool) int {
	if status == "retracted" {
		return 0
	}
	if freshness == "stale" {
		return 1
	}
	if status == "superseded" || historicalChange {
		return 2
	}
	return 3
}

func sortInsightReviewItems(items []insightReviewItem) {
	priority := func(kind string) int {
		switch kind {
		case "retracted":
			return 0
		case "stale":
			return 1
		case "superseded":
			return 2
		case "evidence_unavailable":
			return 3
		case "article_drift":
			return 4
		default:
			return 5
		}
	}
	sort.Slice(items, func(i, j int) bool {
		pi, pj := priority(items[i].Kind), priority(items[j].Kind)
		if pi != pj {
			return pi < pj
		}
		if items[i].FactID != items[j].FactID {
			return items[i].FactID < items[j].FactID
		}
		if items[i].Version != items[j].Version {
			return items[i].Version < items[j].Version
		}
		return items[i].ArticleID < items[j].ArticleID
	})
}

func sensitivityRankInsight(value string) int {
	switch value {
	case "restricted":
		return 2
	case "sensitive":
		return 1
	default:
		return 0
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
