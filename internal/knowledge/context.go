package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/storage"
)

const (
	defaultContextLimit  = 20
	maxContextLimit      = 50
	maxContextFieldBytes = 8 << 10
)

type contextArguments struct {
	Topic                   string `json:"topic,omitempty"`
	Query                   string `json:"query,omitempty"`
	Limit                   int    `json:"limit,omitempty"`
	AsOf                    string `json:"as_of,omitempty"`
	MissingResultAfterHours int    `json:"missing_result_after_hours,omitempty"`
}

type contextArticle struct {
	ArticleID   string     `json:"article_id"`
	Slug        string     `json:"slug"`
	Title       string     `json:"title"`
	Path        string     `json:"path"`
	Sensitivity string     `json:"sensitivity"`
	Version     int        `json:"version"`
	Hash        string     `json:"hash"`
	Tags        []string   `json:"tags,omitempty"`
	SourceIDs   []string   `json:"source_ids,omitempty"`
	Citations   []citation `json:"citations,omitempty"`
	Drift       bool       `json:"drift,omitempty"`
	Evidence    string     `json:"evidence"`
}

type contextFact struct {
	FactID      string     `json:"fact_id"`
	FactKey     string     `json:"fact_key"`
	Kind        string     `json:"kind"`
	Version     int        `json:"version"`
	Status      string     `json:"status"`
	Freshness   string     `json:"freshness"`
	ReviewAfter string     `json:"review_after,omitempty"`
	Text        string     `json:"text"`
	Sensitivity string     `json:"sensitivity"`
	SourceIDs   []string   `json:"source_ids"`
	Citations   []citation `json:"citations"`
}

type contextAction struct {
	ActionID    string `json:"action_id"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Details     string `json:"details,omitempty"`
	Status      string `json:"status"`
	DueAt       string `json:"due_at,omitempty"`
	SourceID    string `json:"source_id,omitempty"`
	Sensitivity string `json:"sensitivity"`
	Revision    int    `json:"revision"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type contextResult struct {
	ResultID    string         `json:"result_id"`
	ActionID    string         `json:"action_id"`
	Version     int            `json:"version"`
	Status      string         `json:"status"`
	Summary     string         `json:"summary"`
	SourceID    string         `json:"source_id,omitempty"`
	Sensitivity string         `json:"sensitivity"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   string         `json:"created_at"`
}

type contextBundleData struct {
	Topic       string           `json:"topic,omitempty"`
	Query       string           `json:"query,omitempty"`
	Articles    []contextArticle `json:"articles"`
	Facts       []contextFact    `json:"facts"`
	Relations   []RelationView   `json:"relations"`
	Actions     []contextAction  `json:"actions"`
	Results     []contextResult  `json:"results"`
	ReviewItems []reviewItem     `json:"review_items"`
	Counts      map[string]int   `json:"counts"`
	Count       int              `json:"count"`
	Truncated   bool             `json:"truncated"`
}

func ContextBundle(ctx context.Context, store *storage.Storage, req protocol.Request) (any, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[contextArguments](req)
	if codedErr != nil {
		return nil, codedErr
	}
	topic, query := strings.TrimSpace(args.Topic), strings.TrimSpace(args.Query)
	if len([]byte(topic)) > 240 || len([]byte(query)) > 240 {
		return nil, protocol.NewCodedError("REQUEST_INVALID", "topic or query exceeds the maximum size", false, map[string]any{"max_bytes": 240})
	}
	limit, codedErr := boundedLimit(args.Limit, defaultContextLimit, maxContextLimit)
	if codedErr != nil {
		return nil, codedErr
	}
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
	if codedErr != nil {
		return nil, codedErr
	}
	term := topic
	if term == "" {
		term = query
	}
	articles, codedErr := loadContextArticles(ctx, store, term, allowSensitive, limit)
	if codedErr != nil {
		return nil, codedErr
	}
	facts, codedErr := loadContextFacts(ctx, store, term, allowSensitive, limit)
	if codedErr != nil {
		return nil, codedErr
	}
	actions, codedErr := loadContextActions(ctx, store, term, allowSensitive, limit)
	if codedErr != nil {
		return nil, codedErr
	}
	results, codedErr := loadContextResults(ctx, store, term, allowSensitive, limit)
	if codedErr != nil {
		return nil, codedErr
	}
	selected := make(map[string]bool)
	for _, article := range articles {
		selected[EndpointArticle+":"+article.ArticleID] = true
	}
	for _, fact := range facts {
		selected[EndpointFact+":"+fact.FactID] = true
	}
	for _, action := range actions {
		selected[EndpointAction+":"+action.ActionID] = true
	}
	for _, result := range results {
		selected[EndpointActionResult+":"+result.ResultID] = true
	}
	relationCap := maxContextLimit * 10
	relations, codedErr := LoadPermittedRelations(ctx, store.DB, allowSensitive, relationCap+1)
	if codedErr != nil {
		return nil, codedErr
	}
	relationCandidatesTruncated := len(relations) > relationCap
	if relationCandidatesTruncated {
		relations = relations[:relationCap]
	}
	filteredRelations := make([]RelationView, 0, limit)
	for _, relation := range relations {
		if !selected[relation.From.Type+":"+relation.From.ID] && !selected[relation.To.Type+":"+relation.To.ID] {
			continue
		}
		filteredRelations = append(filteredRelations, relation)
		if len(filteredRelations) == limit {
			break
		}
	}
	reviewArgs := protocol.Request{ProtocolVersion: protocol.SupportedVersion, Arguments: map[string]json.RawMessage{}}
	if term != "" {
		reviewArgs.Arguments["topic"] = json.RawMessage(marshalJSONString(term))
	}
	reviewArgs.Arguments["limit"] = json.RawMessage([]byte(`200`))
	if args.AsOf != "" {
		reviewArgs.Arguments["as_of"] = json.RawMessage(marshalJSONString(args.AsOf))
	}
	if args.MissingResultAfterHours != 0 {
		reviewArgs.Arguments["missing_result_after_hours"] = json.RawMessage([]byte(formatInt(args.MissingResultAfterHours)))
	}
	if allowSensitive {
		reviewArgs.Options = map[string]json.RawMessage{"allow_sensitive": json.RawMessage(`true`)}
	}
	reviewValue, reviewErr := reviewDataFromRequest(ctx, store, reviewArgs)
	if reviewErr != nil {
		return nil, reviewErr
	}
	reviewItems := reviewValue.Items
	truncated := len(relations) > len(filteredRelations) || relationCandidatesTruncated || reviewValue.Truncated
	if len(reviewItems) > limit {
		reviewItems = reviewItems[:limit]
		truncated = true
	}
	counts := map[string]int{"articles": len(articles), "facts": len(facts), "relations": len(filteredRelations), "actions": len(actions), "results": len(results), "review_items": len(reviewItems)}
	count := len(articles) + len(facts) + len(filteredRelations) + len(actions) + len(results) + len(reviewItems)
	return contextBundleData{Topic: topic, Query: query, Articles: articles, Facts: facts, Relations: filteredRelations, Actions: actions, Results: results, ReviewItems: reviewItems, Counts: counts, Count: count, Truncated: truncated}, nil
}

func reviewDataFromRequest(ctx context.Context, store *storage.Storage, req protocol.Request) (reviewData, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[reviewArguments](req)
	if codedErr != nil {
		return reviewData{}, codedErr
	}
	asOf, codedErr := parseReviewAsOf(args.AsOf)
	if codedErr != nil {
		return reviewData{}, codedErr
	}
	hours := args.MissingResultAfterHours
	if hours == 0 {
		hours = defaultMissingResultHour
	}
	if hours < 1 || hours > maxMissingResultHour {
		return reviewData{}, protocol.NewCodedError("REQUEST_INVALID", "missing result threshold is outside the supported range", false, map[string]any{"max_hours": maxMissingResultHour})
	}
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
	if codedErr != nil {
		return reviewData{}, codedErr
	}
	scan, codedErr := scanReviewItems(ctx, store, reviewContext{Topic: strings.TrimSpace(args.Topic), AsOf: asOf, MissingResultAfterHour: hours, AllowSensitive: allowSensitive})
	if codedErr != nil {
		return reviewData{}, codedErr
	}
	return reviewData{Topic: strings.TrimSpace(args.Topic), AsOf: asOf.Format("2006-01-02T15:04:05.999999999Z07:00"), MissingResultAfterHours: hours, Items: scan.Items, Count: len(scan.Items), TotalCount: len(scan.Items), Truncated: scan.Truncated}, nil
}

func loadContextArticles(ctx context.Context, store *storage.Storage, term string, allowSensitive bool, limit int) ([]contextArticle, *protocol.CodedError) {
	rows, codedErr := loadArticles(ctx, store)
	if codedErr != nil {
		return nil, codedErr
	}
	result := make([]contextArticle, 0, limit)
	for _, row := range rows {
		if !sensitivityAllowed(row.Sensitivity, allowSensitive) {
			continue
		}
		parsed, _, path, readErr := readManagedArticle(store, row)
		if readErr != nil && !reviewTopicMatch(term, row.Title, row.Slug) {
			continue
		}
		if readErr == nil && term != "" && !reviewTopicMatch(term, parsed.Title, parsed.Slug, parsed.Summary, strings.Join(parsed.Tags, " "), parsed.Body) {
			continue
		}
		citations, sourceIDs, permitted := loadPermittedArticleCitations(ctx, store.DB, row.ArticleID, row.Version, allowSensitive)
		if !permitted {
			continue
		}
		item := contextArticle{ArticleID: row.ArticleID, Slug: row.Slug, Title: row.Title, Path: path, Sensitivity: row.Sensitivity, Version: row.Version, Hash: row.Hash, Citations: citations, SourceIDs: sourceIDs, Evidence: "verified", Drift: readErr != nil}
		if readErr != nil {
			item.Evidence = "unavailable"
		} else {
			item.Tags = parsed.Tags
		}
		result = append(result, item)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func loadContextFacts(ctx context.Context, store *storage.Storage, term string, allowSensitive bool, limit int) ([]contextFact, *protocol.CodedError) {
	rows, err := store.DB.QueryContext(ctx, `SELECT f.fact_id, f.fact_key, f.kind, f.current_version, f.status, f.freshness, COALESCE(f.review_after, ''), fv.text FROM facts f JOIN fact_versions fv ON fv.fact_id = f.fact_id AND fv.version = f.current_version ORDER BY f.fact_key ASC, f.fact_id ASC LIMIT ?`, maxContextLimit*10)
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read context facts", true, nil)
	}
	defer rows.Close()
	result := make([]contextFact, 0, limit)
	for rows.Next() {
		var fact contextFact
		if err := rows.Scan(&fact.FactID, &fact.FactKey, &fact.Kind, &fact.Version, &fact.Status, &fact.Freshness, &fact.ReviewAfter, &fact.Text); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode context fact", true, nil)
		}
		if !reviewTopicMatch(term, fact.FactKey, fact.Text) {
			continue
		}
		var evidenceAvailable bool
		fact.SourceIDs, fact.Sensitivity, evidenceAvailable = permittedFactEvidence(ctx, store.DB, fact.FactID, fact.Version, allowSensitive)
		if !evidenceAvailable || len(fact.SourceIDs) == 0 {
			continue
		}
		fact.Text = boundContextField(fact.Text)
		fact.Citations, _ = loadFactCitations(ctx, store.DB, fact.FactID, fact.Version, allowSensitive)
		if len(fact.Citations) > maxContextCitations {
			fact.Citations = fact.Citations[:maxContextCitations]
		}
		result = append(result, fact)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func loadContextActions(ctx context.Context, store *storage.Storage, term string, allowSensitive bool, limit int) ([]contextAction, *protocol.CodedError) {
	rows, err := store.DB.QueryContext(ctx, `SELECT a.action_id, a.kind, a.title, a.details, a.status, COALESCE(a.due_at, ''), COALESCE(a.source_id, ''), a.sensitivity, a.revision, a.created_at, a.updated_at, COALESCE(s.source_id, ''), COALESCE(s.sensitivity, 'normal'), COALESCE(s.forgotten_at, '') FROM actions a LEFT JOIN sources s ON s.source_id = a.source_id WHERE a.forgotten_at IS NULL ORDER BY a.action_id ASC LIMIT ?`, maxContextLimit*10)
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read context actions", true, nil)
	}
	defer rows.Close()
	result := make([]contextAction, 0, limit)
	for rows.Next() {
		var item contextAction
		var joinedSourceID, sourceSensitivity, sourceForgotten string
		if err := rows.Scan(&item.ActionID, &item.Kind, &item.Title, &item.Details, &item.Status, &item.DueAt, &item.SourceID, &item.Sensitivity, &item.Revision, &item.CreatedAt, &item.UpdatedAt, &joinedSourceID, &sourceSensitivity, &sourceForgotten); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode context action", true, nil)
		}
		if !sensitivityAllowed(item.Sensitivity, allowSensitive) || !reviewTopicMatch(term, item.Title, item.Details) {
			continue
		}
		if item.SourceID != "" && (joinedSourceID == "" || sourceForgotten != "" || !sensitivityAllowed(sourceSensitivity, allowSensitive)) {
			continue
		}
		item.Details = boundContextField(item.Details)
		result = append(result, item)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func loadContextResults(ctx context.Context, store *storage.Storage, term string, allowSensitive bool, limit int) ([]contextResult, *protocol.CodedError) {
	rows, err := store.DB.QueryContext(ctx, `SELECT ar.result_id, ar.action_id, ar.version, ar.status, ar.summary, COALESCE(ar.source_id, ''), ar.sensitivity, COALESCE(ar.metadata_json, '{}'), ar.created_at, a.title, a.details, a.sensitivity, COALESCE(a.source_id, ''), COALESCE(action_source.source_id, ''), COALESCE(action_source.sensitivity, 'normal'), COALESCE(action_source.forgotten_at, '') FROM action_results ar JOIN actions a ON a.action_id = ar.action_id AND a.forgotten_at IS NULL LEFT JOIN sources action_source ON action_source.source_id = a.source_id WHERE ar.forgotten_at IS NULL ORDER BY ar.result_id ASC LIMIT ?`, maxContextLimit*10)
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read context action results", true, nil)
	}
	defer rows.Close()
	result := make([]contextResult, 0, limit)
	for rows.Next() {
		var item contextResult
		var metadata []byte
		var title, details, actionSensitivity, actionSourceID, joinedActionSourceID, actionSourceSensitivity, actionSourceForgotten string
		if err := rows.Scan(&item.ResultID, &item.ActionID, &item.Version, &item.Status, &item.Summary, &item.SourceID, &item.Sensitivity, &metadata, &item.CreatedAt, &title, &details, &actionSensitivity, &actionSourceID, &joinedActionSourceID, &actionSourceSensitivity, &actionSourceForgotten); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode context action result", true, nil)
		}
		if !sensitivityAllowed(item.Sensitivity, allowSensitive) || !sensitivityAllowed(actionSensitivity, allowSensitive) || !reviewTopicMatch(term, title, details, item.Summary) {
			continue
		}
		if actionSourceID != "" && (joinedActionSourceID == "" || actionSourceForgotten != "" || !sensitivityAllowed(actionSourceSensitivity, allowSensitive)) {
			continue
		}
		if item.SourceID != "" {
			var sourceSensitivity, forgotten string
			if err := store.DB.QueryRowContext(ctx, `SELECT sensitivity, COALESCE(forgotten_at, '') FROM sources WHERE source_id = ?`, item.SourceID).Scan(&sourceSensitivity, &forgotten); err != nil || forgotten != "" || !sensitivityAllowed(sourceSensitivity, allowSensitive) {
				continue
			}
		}
		item.Summary = boundContextField(item.Summary)
		if len(metadata) <= maxContextFieldBytes {
			_ = json.Unmarshal(metadata, &item.Metadata)
		} else {
			item.Metadata = map[string]any{"redacted": true, "reason": "context_field_limit"}
		}
		result = append(result, item)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func loadPermittedArticleCitations(ctx context.Context, db *sql.DB, articleID string, version int, allowSensitive bool) ([]citation, []string, bool) {
	rows, err := db.QueryContext(ctx, `SELECT ac.source_id, ac.locator, s.sensitivity, COALESCE(s.forgotten_at, '') FROM article_citations ac JOIN sources s ON s.source_id = ac.source_id WHERE ac.article_id = ? AND ac.version = ? ORDER BY ac.source_id ASC, ac.locator ASC`, articleID, version)
	if err != nil {
		return nil, nil, false
	}
	defer rows.Close()
	citations := make([]citation, 0)
	sources := make([]string, 0)
	seen := map[string]bool{}
	blocked := false
	for rows.Next() {
		var sourceID, locator, sensitivity, forgotten string
		if rows.Scan(&sourceID, &locator, &sensitivity, &forgotten) != nil || forgotten != "" {
			continue
		}
		if !sensitivityAllowed(sensitivity, allowSensitive) {
			blocked = true
			continue
		}
		citations = append(citations, citation{SourceID: sourceID, Locator: locator})
		if !seen[sourceID] {
			seen[sourceID] = true
			sources = append(sources, sourceID)
		}
	}
	if len(citations) > maxContextCitations {
		citations = citations[:maxContextCitations]
	}
	if len(sources) > maxContextCitations {
		sources = sources[:maxContextCitations]
	}
	return citations, sources, len(citations) > 0 && !blocked
}

func loadFactCitations(ctx context.Context, db *sql.DB, factID string, version int, allowSensitive bool) ([]citation, *protocol.CodedError) {
	rows, err := db.QueryContext(ctx, `SELECT fc.source_id, fc.locator, s.sensitivity, COALESCE(s.forgotten_at, '') FROM fact_citations fc JOIN sources s ON s.source_id = fc.source_id WHERE fc.fact_id = ? AND fc.version = ? ORDER BY fc.source_id ASC, fc.locator ASC`, factID, version)
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read context fact citations", true, nil)
	}
	defer rows.Close()
	result := make([]citation, 0)
	for rows.Next() {
		var sourceID, locator, sensitivity, forgotten string
		if err := rows.Scan(&sourceID, &locator, &sensitivity, &forgotten); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode context fact citation", true, nil)
		}
		if forgotten == "" && sensitivityAllowed(sensitivity, allowSensitive) {
			result = append(result, citation{SourceID: sourceID, Locator: locator})
		}
	}
	return result, nil
}

const maxContextCitations = 100

func boundContextField(value string) string {
	if len([]byte(value)) <= maxContextFieldBytes {
		return value
	}
	end := maxContextFieldBytes
	for end > 0 && end < len(value) && (value[end]&0xc0) == 0x80 {
		end--
	}
	return value[:end]
}

func marshalJSONString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func formatInt(value int) string {
	return strconv.Itoa(value)
}
