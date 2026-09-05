package knowledge

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/storage"
)

const (
	defaultReviewLimit       = 50
	maxReviewLimit           = 200
	defaultMissingResultHour = 24 * 7
	maxMissingResultHour     = 24 * 365
	maxReviewCandidates      = 2000
)

type reviewArguments struct {
	Topic                     string `json:"topic,omitempty"`
	Limit                     int    `json:"limit,omitempty"`
	AsOf                      string `json:"as_of,omitempty"`
	MissingResultAfterHours   int    `json:"missing_result_after_hours,omitempty"`
	MissingResultThresholdHrs int    `json:"missing_result_threshold_hours,omitempty"`
}

type reviewItem struct {
	Kind       string         `json:"kind"`
	Severity   string         `json:"severity"`
	EntityType string         `json:"entity_type"`
	EntityID   string         `json:"entity_id"`
	Version    int            `json:"version,omitempty"`
	Relation   *RelationView  `json:"relation,omitempty"`
	SourceIDs  []string       `json:"source_ids,omitempty"`
	Reason     string         `json:"reason"`
	Impact     map[string]any `json:"impact,omitempty"`
}

type reviewData struct {
	Topic                   string       `json:"topic,omitempty"`
	AsOf                    string       `json:"as_of"`
	MissingResultAfterHours int          `json:"missing_result_after_hours"`
	Items                   []reviewItem `json:"items"`
	Count                   int          `json:"count"`
	TotalCount              int          `json:"total_count"`
	Truncated               bool         `json:"truncated"`
}

type reviewScanResult struct {
	Items     []reviewItem
	Truncated bool
}

type reviewContext struct {
	Topic                  string
	AsOf                   time.Time
	MissingResultAfterHour int
	AllowSensitive         bool
}

func ReviewScan(ctx context.Context, store *storage.Storage, req protocol.Request) (any, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[reviewArguments](req)
	if codedErr != nil {
		return nil, codedErr
	}
	topic := strings.TrimSpace(args.Topic)
	if len([]byte(topic)) > 240 {
		return nil, protocol.NewCodedError("REQUEST_INVALID", "topic exceeds the maximum size", false, map[string]any{"max_bytes": 240})
	}
	limit, codedErr := boundedLimit(args.Limit, defaultReviewLimit, maxReviewLimit)
	if codedErr != nil {
		return nil, codedErr
	}
	asOf, codedErr := parseReviewAsOf(args.AsOf)
	if codedErr != nil {
		return nil, codedErr
	}
	hours := args.MissingResultAfterHours
	if hours == 0 {
		hours = args.MissingResultThresholdHrs
	}
	if hours == 0 {
		hours = defaultMissingResultHour
	}
	if hours < 1 || hours > maxMissingResultHour {
		return nil, protocol.NewCodedError("REQUEST_INVALID", "missing result threshold is outside the supported range", false, map[string]any{"max_hours": maxMissingResultHour})
	}
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
	if codedErr != nil {
		return nil, codedErr
	}
	scan, codedErr := scanReviewItems(ctx, store, reviewContext{Topic: topic, AsOf: asOf, MissingResultAfterHour: hours, AllowSensitive: allowSensitive})
	if codedErr != nil {
		return nil, codedErr
	}
	total := len(scan.Items)
	items := scan.Items
	truncated := scan.Truncated
	if len(items) > limit {
		items = items[:limit]
		truncated = true
	}
	return reviewData{Topic: topic, AsOf: asOf.Format(time.RFC3339Nano), MissingResultAfterHours: hours, Items: items, Count: len(items), TotalCount: total, Truncated: truncated}, nil
}

func scanReviewItems(ctx context.Context, store *storage.Storage, settings reviewContext) (reviewScanResult, *protocol.CodedError) {
	scan := reviewScanResult{Items: make([]reviewItem, 0)}
	factItems, factTruncated := scanFactReviewItems(ctx, store, settings)
	scan.Items = append(scan.Items, factItems...)
	scan.Truncated = scan.Truncated || factTruncated
	articleItems, codedErr := scanArticleReviewItems(ctx, store, settings)
	if codedErr != nil {
		return reviewScanResult{}, codedErr
	}
	scan.Items = append(scan.Items, articleItems...)
	relationItems, codedErr := scanContradictionItems(ctx, store, settings)
	if codedErr != nil {
		return reviewScanResult{}, codedErr
	}
	scan.Items = append(scan.Items, relationItems...)
	actionItems, actionTruncated, codedErr := scanActionReviewItems(ctx, store, settings)
	if codedErr != nil {
		return reviewScanResult{}, codedErr
	}
	scan.Items = append(scan.Items, actionItems...)
	scan.Truncated = scan.Truncated || actionTruncated
	resultItems, resultTruncated, codedErr := scanResultReviewItems(ctx, store, settings)
	if codedErr != nil {
		return reviewScanResult{}, codedErr
	}
	scan.Items = append(scan.Items, resultItems...)
	scan.Truncated = scan.Truncated || resultTruncated
	sort.SliceStable(scan.Items, func(i, j int) bool {
		if scan.Items[i].Kind != scan.Items[j].Kind {
			return scan.Items[i].Kind < scan.Items[j].Kind
		}
		if scan.Items[i].EntityType != scan.Items[j].EntityType {
			return scan.Items[i].EntityType < scan.Items[j].EntityType
		}
		if scan.Items[i].EntityID != scan.Items[j].EntityID {
			return scan.Items[i].EntityID < scan.Items[j].EntityID
		}
		if scan.Items[i].Version != scan.Items[j].Version {
			return scan.Items[i].Version < scan.Items[j].Version
		}
		if scan.Items[i].Relation == nil || scan.Items[j].Relation == nil {
			return scan.Items[i].Relation == nil
		}
		return scan.Items[i].Relation.RelationID < scan.Items[j].Relation.RelationID
	})
	return scan, nil
}

func scanFactReviewItems(ctx context.Context, store *storage.Storage, settings reviewContext) ([]reviewItem, bool) {
	rows, err := store.DB.QueryContext(ctx, `SELECT f.fact_id, f.fact_key, f.kind, f.current_version, f.status, f.freshness, COALESCE(f.review_after, ''), COALESCE(fv.text, ''), COALESCE(fv.extraction_id, '') FROM facts f JOIN fact_versions fv ON fv.fact_id = f.fact_id AND fv.version = f.current_version ORDER BY f.fact_id ASC LIMIT ?`, maxReviewCandidates+1)
	if err != nil {
		return []reviewItem{{Kind: "storage_error", Severity: "error", EntityType: "storage", EntityID: "facts", Reason: "fact review scan could not read facts"}}, false
	}
	defer rows.Close()
	items := make([]reviewItem, 0)
	candidateCount := 0
	candidateTruncated := false
	for rows.Next() {
		if candidateCount == maxReviewCandidates {
			candidateTruncated = true
			break
		}
		candidateCount++
		var factID, factKey, kind, status, freshness, reviewAfter, text, extractionID string
		var version int
		if err := rows.Scan(&factID, &factKey, &kind, &version, &status, &freshness, &reviewAfter, &text, &extractionID); err != nil {
			continue
		}
		if !reviewTopicMatch(settings.Topic, factKey, text) {
			continue
		}
		sourceIDs, sensitivity, evidenceAvailable, evidenceUnavailable, sensitiveEvidenceBlocked := reviewFactEvidence(ctx, store, factID, version, settings.AllowSensitive)
		if sensitiveEvidenceBlocked {
			continue
		}
		factKind := kind
		base := func(signalKind, reason, severity string) reviewItem {
			return reviewItem{Kind: signalKind, Severity: severity, EntityType: EndpointFact, EntityID: factID, Version: version, SourceIDs: sourceIDs, Reason: reason, Impact: map[string]any{"fact_key": factKey, "fact_kind": factKind}}
		}
		if evidenceUnavailable || !evidenceAvailable || len(sourceIDs) == 0 {
			items = append(items, base("evidence_unavailable", "fact citations cannot be fully verified", "high"))
			if !evidenceAvailable || len(sourceIDs) == 0 {
				continue
			}
		}
		if status == "retracted" {
			items = append(items, base("retracted", "current fact version is retracted", "high"))
		}
		if status == "superseded" {
			items = append(items, base("superseded", "current fact version is superseded", "medium"))
		}
		if freshness == "stale" {
			items = append(items, base("stale", "current fact freshness is stale", "medium"))
		}
		if reviewAfter != "" {
			if deadline, err := time.Parse(time.RFC3339Nano, reviewAfter); err == nil && !deadline.After(settings.AsOf) {
				items = append(items, base("due_for_review", "fact review deadline has passed", "medium"))
			}
		}
		if extractionID != "" {
			_, usable, _ := usableInsightExtraction(ctx, store, extractionID)
			if !usable {
				items = append(items, reviewItem{Kind: "evidence_unavailable", Severity: "high", EntityType: EndpointFact, EntityID: factID, Version: version, SourceIDs: sourceIDs, Reason: "fact extraction cannot be verified"})
			}
		}
		_ = sensitivity
	}
	return items, candidateTruncated
}

// reviewFactEvidence preserves the privacy behavior of permittedFactEvidence
// while distinguishing a forgotten/missing citation from a citation that is
// intentionally hidden because it is sensitive. Review should report the
// former and omit the latter entirely.
func reviewFactEvidence(ctx context.Context, store *storage.Storage, factID string, version int, allowSensitive bool) (sourceIDs []string, sensitivity string, usable, unavailable, sensitiveBlocked bool) {
	rows, err := store.DB.QueryContext(ctx, `SELECT fc.source_id, COALESCE(s.sensitivity, ''), COALESCE(s.forgotten_at, '') FROM fact_citations fc LEFT JOIN sources s ON s.source_id = fc.source_id WHERE fc.fact_id = ? AND fc.version = ? ORDER BY fc.source_id ASC`, factID, version)
	if err != nil {
		return nil, "", false, true, false
	}
	defer rows.Close()
	sensitivity = "normal"
	seen := make(map[string]bool)
	hasCitation := false
	for rows.Next() {
		var sourceID, sourceSensitivity, forgotten string
		if err := rows.Scan(&sourceID, &sourceSensitivity, &forgotten); err != nil {
			return sourceIDs, sensitivity, false, true, false
		}
		hasCitation = true
		if sourceSensitivity == "" || forgotten != "" {
			unavailable = true
			continue
		}
		if !sensitivityAllowed(sourceSensitivity, allowSensitive) {
			sensitiveBlocked = true
			continue
		}
		if !reviewSourceAvailable(ctx, store, sourceID) {
			unavailable = true
			continue
		}
		if !seen[sourceID] {
			seen[sourceID] = true
			sourceIDs = append(sourceIDs, sourceID)
		}
		if reviewSensitivityRank(sourceSensitivity) > reviewSensitivityRank(sensitivity) {
			sensitivity = sourceSensitivity
		}
	}
	if err := rows.Err(); err != nil {
		return sourceIDs, sensitivity, false, true, false
	}
	if !hasCitation {
		unavailable = true
	}
	usable = len(sourceIDs) > 0 && !sensitiveBlocked && !unavailable
	return sourceIDs, sensitivity, usable, unavailable, sensitiveBlocked
}

func reviewSourceAvailable(ctx context.Context, store *storage.Storage, sourceID string) bool {
	var contentHash, rawPath string
	if err := store.DB.QueryRowContext(ctx, `SELECT s.content_hash, b.raw_path FROM sources s JOIN blobs b ON b.content_hash = s.content_hash WHERE s.source_id = ? AND s.forgotten_at IS NULL`, sourceID).Scan(&contentHash, &rawPath); err != nil {
		return false
	}
	path := rawPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(store.Paths.Root, filepath.FromSlash(path))
	}
	path = filepath.Clean(path)
	if !within(path, store.Paths.Raw) {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false
	}
	contents, err := os.ReadFile(path)
	return err == nil && hashBytes(contents) == contentHash
}

func scanArticleReviewItems(ctx context.Context, store *storage.Storage, settings reviewContext) ([]reviewItem, *protocol.CodedError) {
	articles, codedErr := loadArticles(ctx, store)
	if codedErr != nil {
		return nil, codedErr
	}
	items := make([]reviewItem, 0)
	for _, article := range articles {
		if !sensitivityAllowed(article.Sensitivity, settings.AllowSensitive) {
			continue
		}
		if _, _, permitted := loadPermittedArticleCitations(ctx, store.DB, article.ArticleID, article.Version, settings.AllowSensitive); !permitted {
			continue
		}
		parsed, _, _, readErr := readManagedArticle(store, article)
		if readErr == nil {
			if settings.Topic != "" && !reviewTopicMatch(settings.Topic, strings.Join(parsed.Tags, " "), parsed.Summary, parsed.Body) {
				continue
			}
			continue
		}
		if settings.Topic != "" && !reviewTopicMatch(settings.Topic, article.Slug, article.Title) {
			continue
		}
		items = append(items, reviewItem{Kind: "article_drift", Severity: "high", EntityType: EndpointArticle, EntityID: article.ArticleID, Version: article.Version, Reason: "managed article path or hash cannot be verified", Impact: map[string]any{"slug": article.Slug}})
	}
	return items, nil
}

func scanContradictionItems(ctx context.Context, store *storage.Storage, settings reviewContext) ([]reviewItem, *protocol.CodedError) {
	relations, codedErr := LoadPermittedRelations(ctx, store.DB, settings.AllowSensitive, maxReviewCandidates)
	if codedErr != nil {
		return nil, codedErr
	}
	items := make([]reviewItem, 0)
	for index := range relations {
		relation := relations[index]
		if relation.RelationType != RelationContradicts {
			continue
		}
		if !relationEndpointTopicMatch(ctx, store.DB, relation.From, settings.Topic) && !relationEndpointTopicMatch(ctx, store.DB, relation.To, settings.Topic) {
			continue
		}
		relationCopy := relation
		items = append(items, reviewItem{Kind: "contradicts", Severity: "high", EntityType: "relation", EntityID: relation.RelationID, Relation: &relationCopy, Reason: "an explicit contradicts relation requires review"})
	}
	return items, nil
}

func scanActionReviewItems(ctx context.Context, store *storage.Storage, settings reviewContext) ([]reviewItem, bool, *protocol.CodedError) {
	cutoff := settings.AsOf.Add(-time.Duration(settings.MissingResultAfterHour) * time.Hour)
	rows, err := store.DB.QueryContext(ctx, `SELECT a.action_id, a.title, a.details, a.status, a.sensitivity, a.created_at, COALESCE(a.source_id, ''), COALESCE(s.source_id, ''), COALESCE(s.sensitivity, 'normal'), COALESCE(s.forgotten_at, '') FROM actions a LEFT JOIN sources s ON s.source_id = a.source_id WHERE a.forgotten_at IS NULL AND NOT EXISTS (SELECT 1 FROM action_results ar WHERE ar.action_id = a.action_id AND ar.forgotten_at IS NULL) ORDER BY a.action_id ASC LIMIT ?`, maxReviewCandidates+1)
	if err != nil {
		return nil, false, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read actions for review scan", true, nil)
	}
	defer rows.Close()
	items := make([]reviewItem, 0)
	candidateCount := 0
	candidateTruncated := false
	for rows.Next() {
		if candidateCount == maxReviewCandidates {
			candidateTruncated = true
			break
		}
		candidateCount++
		var actionID, title, details, status, sensitivity, createdAt, sourceID, joinedSourceID, sourceSensitivity, sourceForgotten string
		if err := rows.Scan(&actionID, &title, &details, &status, &sensitivity, &createdAt, &sourceID, &joinedSourceID, &sourceSensitivity, &sourceForgotten); err != nil {
			return nil, false, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode action review candidate", true, nil)
		}
		if !sensitivityAllowed(sensitivity, settings.AllowSensitive) || !reviewTopicMatch(settings.Topic, title, details) {
			continue
		}
		if sourceID != "" && (joinedSourceID == "" || sourceForgotten != "" || !sensitivityAllowed(sourceSensitivity, settings.AllowSensitive)) {
			continue
		}
		created, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil || created.After(cutoff) {
			continue
		}
		items = append(items, reviewItem{Kind: "action_missing_result", Severity: "medium", EntityType: EndpointAction, EntityID: actionID, Reason: "action has no recorded result after the configured threshold", Impact: map[string]any{"status": status, "created_at": createdAt}})
	}
	return items, candidateTruncated, nil
}

func scanResultReviewItems(ctx context.Context, store *storage.Storage, settings reviewContext) ([]reviewItem, bool, *protocol.CodedError) {
	permittedRelations, codedErr := LoadPermittedRelations(ctx, store.DB, settings.AllowSensitive, maxReviewCandidates)
	if codedErr != nil {
		return nil, false, codedErr
	}
	feedback := make(map[string]bool)
	for _, relation := range permittedRelations {
		if relation.RelationType == RelationResultedIn && relation.From.Type == EndpointActionResult {
			feedback[relation.From.ID] = true
		}
	}
	rows, err := store.DB.QueryContext(ctx, `SELECT ar.result_id, ar.action_id, ar.version, ar.sensitivity, a.title, a.sensitivity, COALESCE(ar.source_id, ''), COALESCE(s.source_id, ''), COALESCE(s.sensitivity, 'normal'), COALESCE(s.forgotten_at, ''), COALESCE(a.source_id, ''), COALESCE(action_source.source_id, ''), COALESCE(action_source.sensitivity, 'normal'), COALESCE(action_source.forgotten_at, '') FROM action_results ar JOIN actions a ON a.action_id = ar.action_id AND a.forgotten_at IS NULL LEFT JOIN sources s ON s.source_id = ar.source_id LEFT JOIN sources action_source ON action_source.source_id = a.source_id WHERE ar.forgotten_at IS NULL ORDER BY ar.result_id ASC LIMIT ?`, maxReviewCandidates+1)
	if err != nil {
		return nil, false, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read action results for review scan", true, nil)
	}
	defer rows.Close()
	items := make([]reviewItem, 0)
	candidateCount := 0
	candidateTruncated := false
	for rows.Next() {
		if candidateCount == maxReviewCandidates {
			candidateTruncated = true
			break
		}
		candidateCount++
		var resultID, actionID, sensitivity, title, actionSensitivity, sourceID, joinedSourceID, sourceSensitivity, sourceForgotten, actionSourceID, joinedActionSourceID, actionSourceSensitivity, actionSourceForgotten string
		var version int
		if err := rows.Scan(&resultID, &actionID, &version, &sensitivity, &title, &actionSensitivity, &sourceID, &joinedSourceID, &sourceSensitivity, &sourceForgotten, &actionSourceID, &joinedActionSourceID, &actionSourceSensitivity, &actionSourceForgotten); err != nil {
			return nil, false, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode action result review candidate", true, nil)
		}
		if !sensitivityAllowed(sensitivity, settings.AllowSensitive) || !sensitivityAllowed(actionSensitivity, settings.AllowSensitive) || !reviewTopicMatch(settings.Topic, title, actionID) {
			continue
		}
		if sourceID != "" && (joinedSourceID == "" || sourceForgotten != "" || !sensitivityAllowed(sourceSensitivity, settings.AllowSensitive)) {
			continue
		}
		if actionSourceID != "" && (joinedActionSourceID == "" || actionSourceForgotten != "" || !sensitivityAllowed(actionSourceSensitivity, settings.AllowSensitive)) {
			continue
		}
		if feedback[resultID] {
			continue
		}
		items = append(items, reviewItem{Kind: "result_feedback_gap", Severity: "medium", EntityType: EndpointActionResult, EntityID: resultID, Version: version, Reason: "action result has no explicit resulted_in knowledge relation", Impact: map[string]any{"action_id": actionID}})
	}
	return items, candidateTruncated, nil
}

func permittedFactEvidence(ctx context.Context, db *sql.DB, factID string, version int, allowSensitive bool) ([]string, string, bool) {
	rows, err := db.QueryContext(ctx, `SELECT fc.source_id, s.sensitivity, COALESCE(s.forgotten_at, '') FROM fact_citations fc JOIN sources s ON s.source_id = fc.source_id WHERE fc.fact_id = ? AND fc.version = ? ORDER BY fc.source_id ASC`, factID, version)
	if err != nil {
		return nil, "", false
	}
	defer rows.Close()
	ids := make([]string, 0)
	sensitivity := "normal"
	active := false
	blocked := false
	for rows.Next() {
		var sourceID, sourceSensitivity, forgotten string
		if rows.Scan(&sourceID, &sourceSensitivity, &forgotten) != nil {
			continue
		}
		if forgotten != "" {
			continue
		}
		if !sensitivityAllowed(sourceSensitivity, allowSensitive) {
			blocked = true
			continue
		}
		active = true
		ids = append(ids, sourceID)
		if reviewSensitivityRank(sourceSensitivity) > reviewSensitivityRank(sensitivity) {
			sensitivity = sourceSensitivity
		}
	}
	return ids, sensitivity, active && !blocked
}

func relationEndpointTopicMatch(ctx context.Context, db *sql.DB, endpoint RelationEndpoint, topic string) bool {
	if topic == "" {
		return true
	}
	var left, right string
	switch endpoint.Type {
	case EndpointFact:
		if db.QueryRowContext(ctx, `SELECT fact_key, COALESCE(fv.text, '') FROM facts f JOIN fact_versions fv ON fv.fact_id = f.fact_id AND fv.version = f.current_version WHERE f.fact_id = ?`, endpoint.ID).Scan(&left, &right) != nil {
			return false
		}
	case EndpointArticle:
		if db.QueryRowContext(ctx, `SELECT slug, title FROM articles WHERE article_id = ? AND forgotten_at IS NULL`, endpoint.ID).Scan(&left, &right) != nil {
			return false
		}
	case EndpointAction:
		if db.QueryRowContext(ctx, `SELECT title, details FROM actions WHERE action_id = ? AND forgotten_at IS NULL`, endpoint.ID).Scan(&left, &right) != nil {
			return false
		}
	default:
		return false
	}
	return reviewTopicMatch(topic, left, right)
}

func reviewTopicMatch(topic string, values ...string) bool {
	topic = strings.ToLower(strings.TrimSpace(topic))
	if topic == "" {
		return true
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), topic) {
			return true
		}
	}
	return false
}

func parseReviewAsOf(value string) (time.Time, *protocol.CodedError) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Now().UTC(), nil
	}
	asOf, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, protocol.NewCodedError("AS_OF_INVALID", "as_of must be an RFC3339 timestamp", false, nil)
	}
	return asOf.UTC(), nil
}

func reviewSensitivityRank(value string) int {
	switch value {
	case "normal":
		return 0
	case "sensitive":
		return 1
	case "restricted":
		return 2
	default:
		return -1
	}
}
