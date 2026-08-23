package knowledge

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chenquan/moss/internal/protocol"
)

const (
	RelationSupports    = "supports"
	RelationContradicts = "contradicts"
	RelationSupersedes  = "supersedes"
	RelationDependsOn   = "depends_on"
	RelationProduces    = "produces"
	RelationResultedIn  = "resulted_in"

	EndpointSource       = "source"
	EndpointFact         = "fact"
	EndpointArticle      = "article"
	EndpointAction       = "action"
	EndpointActionResult = "action_result"
)

var relationTypes = map[string]struct{}{
	RelationSupports: {}, RelationContradicts: {}, RelationSupersedes: {},
	RelationDependsOn: {}, RelationProduces: {}, RelationResultedIn: {},
}

var endpointTypes = map[string]struct{}{
	EndpointSource: {}, EndpointFact: {}, EndpointArticle: {},
	EndpointAction: {}, EndpointActionResult: {},
}

// RelationEndpoint is the versioned identity of one graph endpoint. Version
// means fact version, article version, action revision, or result version;
// sources intentionally use zero because they are immutable identities.
type RelationEndpoint struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Version  int    `json:"version,omitempty"`
	Revision int    `json:"revision,omitempty"`
}

// RelationInput is accepted by the multi-source write stage. Flat fields are
// retained as a compatibility spelling for Skills that do not construct
// nested endpoint objects.
type RelationInput struct {
	RelationType string           `json:"relation_type,omitempty"`
	Type         string           `json:"type,omitempty"`
	From         RelationEndpoint `json:"from,omitempty"`
	To           RelationEndpoint `json:"to,omitempty"`
	FromType     string           `json:"from_type,omitempty"`
	FromID       string           `json:"from_id,omitempty"`
	FromVersion  int              `json:"from_version,omitempty"`
	FromRevision int              `json:"from_revision,omitempty"`
	ToType       string           `json:"to_type,omitempty"`
	ToID         string           `json:"to_id,omitempty"`
	ToVersion    int              `json:"to_version,omitempty"`
	ToRevision   int              `json:"to_revision,omitempty"`
	SourceID     string           `json:"source_id,omitempty"`
}

type ResolvedRelation struct {
	RelationID   string
	RelationType string
	From         RelationEndpoint
	To           RelationEndpoint
	SourceID     string
	Sensitivity  string
}

type RelationView struct {
	RelationID   string           `json:"relation_id"`
	RelationType string           `json:"relation_type"`
	From         RelationEndpoint `json:"from"`
	To           RelationEndpoint `json:"to"`
	SourceID     string           `json:"source_id,omitempty"`
	CreatedAt    string           `json:"created_at"`
}

type relationQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type endpointSnapshot struct {
	Sensitivity string
	Current     int
	Forgotten   bool
}

func (r RelationInput) Normalize() (ResolvedRelation, *protocol.CodedError) {
	relationType := strings.TrimSpace(r.RelationType)
	if relationType == "" {
		relationType = strings.TrimSpace(r.Type)
	}
	from := r.From
	if from.Type == "" {
		from = RelationEndpoint{Type: r.FromType, ID: r.FromID, Version: r.FromVersion, Revision: r.FromRevision}
	}
	to := r.To
	if to.Type == "" {
		to = RelationEndpoint{Type: r.ToType, ID: r.ToID, Version: r.ToVersion, Revision: r.ToRevision}
	}
	from.Type, from.ID = strings.TrimSpace(from.Type), strings.TrimSpace(from.ID)
	to.Type, to.ID = strings.TrimSpace(to.Type), strings.TrimSpace(to.ID)
	if from.Version == 0 && from.Revision != 0 {
		from.Version = from.Revision
	}
	if to.Version == 0 && to.Revision != 0 {
		to.Version = to.Revision
	}
	return ResolvedRelation{RelationType: relationType, From: from, To: to, SourceID: strings.TrimSpace(r.SourceID)}, nil
}

func ValidateRelation(ctx context.Context, q relationQuerier, input RelationInput, allowedSources map[string]bool, allowSensitive bool) (ResolvedRelation, *protocol.CodedError) {
	relation, _ := input.Normalize()
	if _, ok := relationTypes[relation.RelationType]; !ok {
		return relation, protocol.NewCodedError("RELATION_TYPE_INVALID", "relation type is not supported", false, nil)
	}
	_, fromOK := endpointTypes[relation.From.Type]
	_, toOK := endpointTypes[relation.To.Type]
	if !fromOK || !toOK {
		return relation, protocol.NewCodedError("RELATION_ENDPOINT_TYPE_INVALID", "relation endpoint type is not supported", false, nil)
	}
	if relation.From.ID == "" || relation.To.ID == "" {
		return relation, protocol.NewCodedError("RELATION_ENDPOINT_INVALID", "relation endpoints require type and id", false, nil)
	}
	for _, endpoint := range []RelationEndpoint{relation.From, relation.To} {
		if endpoint.Version < 0 || (endpoint.Type == EndpointSource && endpoint.Version != 0) {
			return relation, protocol.NewCodedError("RELATION_ENDPOINT_INVALID", "relation endpoint version is invalid", false, nil)
		}
	}
	if allowedSources != nil {
		for _, endpoint := range []RelationEndpoint{relation.From, relation.To} {
			if endpoint.Type == EndpointSource && !allowedSources[endpoint.ID] {
				return relation, protocol.NewCodedError("SOURCE_REFERENCE_INVALID", "relation endpoint source is outside the compile job", false, map[string]any{"source_id": endpoint.ID})
			}
			if codedErr := validateEndpointSourceScope(ctx, q, endpoint, allowedSources); codedErr != nil {
				return relation, codedErr
			}
		}
	}
	if relation.From.Type == relation.To.Type && relation.From.ID == relation.To.ID && relation.From.Version == relation.To.Version {
		return relation, protocol.NewCodedError("RELATION_SELF_REFERENCE", "a relation cannot point to itself", false, nil)
	}
	if relation.RelationType == RelationProduces && (relation.From.Type != EndpointAction || relation.To.Type != EndpointActionResult) {
		return relation, protocol.NewCodedError("RELATION_ENDPOINT_INVALID", "produces must connect an action to an action_result", false, nil)
	}
	if relation.RelationType == RelationResultedIn && (relation.From.Type != EndpointActionResult || (relation.To.Type != EndpointFact && relation.To.Type != EndpointArticle)) {
		return relation, protocol.NewCodedError("RELATION_ENDPOINT_INVALID", "resulted_in must connect an action_result to a fact or article", false, nil)
	}
	provenanceSensitivity := "normal"
	if relation.SourceID != "" {
		if allowedSources != nil && !allowedSources[relation.SourceID] {
			return relation, protocol.NewCodedError("SOURCE_REFERENCE_INVALID", "relation provenance is outside the compile job", false, nil)
		}
		var sensitivity, forgotten string
		if err := q.QueryRowContext(ctx, `SELECT sensitivity, COALESCE(forgotten_at, '') FROM sources WHERE source_id = ?`, relation.SourceID).Scan(&sensitivity, &forgotten); errors.Is(err, sql.ErrNoRows) {
			return relation, protocol.NewCodedError("SOURCE_NOT_FOUND", "relation provenance source was not found", false, nil)
		} else if err != nil {
			return relation, relationStorageError("cannot validate relation provenance", err)
		} else if forgotten != "" {
			return relation, protocol.NewCodedError("SOURCE_NOT_FOUND", "relation provenance source was forgotten", false, nil)
		} else if !allowSensitive && sensitivityRank(sensitivity) > sensitivityRank("normal") {
			return relation, protocol.NewCodedError("SENSITIVITY_DENIED", "relation provenance requires sensitive-content permission", false, nil)
		} else {
			provenanceSensitivity = sensitivity
		}
	}
	from, codedErr := resolveEndpoint(ctx, q, &relation.From, true)
	if codedErr != nil {
		return relation, codedErr
	}
	to, codedErr := resolveEndpoint(ctx, q, &relation.To, true)
	if codedErr != nil {
		return relation, codedErr
	}
	if !allowSensitive && (sensitivityRank(from.Sensitivity) > sensitivityRank("normal") || sensitivityRank(to.Sensitivity) > sensitivityRank("normal")) {
		return relation, protocol.NewCodedError("SENSITIVITY_DENIED", "relation endpoint requires sensitive-content permission", false, nil)
	}
	relation.From.Version = from.Current
	relation.To.Version = to.Current
	relation.Sensitivity = maxSensitivity(from.Sensitivity, to.Sensitivity)
	if sensitivityRank(relation.Sensitivity) < sensitivityRank(provenanceSensitivity) {
		return relation, protocol.NewCodedError("SENSITIVITY_ESCALATION_REQUIRED", "relation sensitivity cannot be lower than its provenance source", false, nil)
	}
	relation.Sensitivity = maxSensitivity(relation.Sensitivity, provenanceSensitivity)
	return relation, nil
}

func resolveEndpoint(ctx context.Context, q relationQuerier, endpoint *RelationEndpoint, strict bool) (endpointSnapshot, *protocol.CodedError) {
	var snapshot endpointSnapshot
	endpoint.Type, endpoint.ID = strings.TrimSpace(endpoint.Type), strings.TrimSpace(endpoint.ID)
	if _, ok := endpointTypes[endpoint.Type]; !ok || endpoint.ID == "" {
		return snapshot, protocol.NewCodedError("RELATION_ENDPOINT_INVALID", "relation endpoint is invalid", false, nil)
	}
	var err error
	switch endpoint.Type {
	case EndpointSource:
		var forgotten string
		err = q.QueryRowContext(ctx, `SELECT sensitivity, COALESCE(forgotten_at, '') FROM sources WHERE source_id = ?`, endpoint.ID).Scan(&snapshot.Sensitivity, &forgotten)
		if err == nil {
			snapshot.Forgotten = forgotten != ""
		}
	case EndpointFact:
		err = q.QueryRowContext(ctx, `SELECT current_version FROM facts WHERE fact_id = ?`, endpoint.ID).Scan(&snapshot.Current)
		if err == nil {
			snapshot.Sensitivity, err = entityCitationSensitivity(ctx, q, `fact_citations`, `fact_id`, endpoint.ID, snapshot.Current)
			var total, active int
			if countErr := q.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN s.forgotten_at IS NULL THEN 1 ELSE 0 END), 0) FROM fact_citations fc JOIN sources s ON s.source_id = fc.source_id WHERE fc.fact_id = ? AND fc.version = ?`, endpoint.ID, snapshot.Current).Scan(&total, &active); countErr == nil && total > 0 && active == 0 {
				snapshot.Forgotten = true
			}
		}
	case EndpointArticle:
		var forgotten string
		err = q.QueryRowContext(ctx, `SELECT sensitivity, current_version, COALESCE(forgotten_at, '') FROM articles WHERE article_id = ?`, endpoint.ID).Scan(&snapshot.Sensitivity, &snapshot.Current, &forgotten)
		snapshot.Forgotten = forgotten != ""
		if err == nil {
			citationSensitivity, citationErr := entityCitationSensitivity(ctx, q, `article_citations`, `article_id`, endpoint.ID, snapshot.Current)
			if citationErr != nil {
				return snapshot, relationStorageError("cannot resolve article citation sensitivity", citationErr)
			}
			snapshot.Sensitivity = maxSensitivity(snapshot.Sensitivity, citationSensitivity)
			var total, active int
			if countErr := q.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN s.forgotten_at IS NULL THEN 1 ELSE 0 END), 0) FROM article_citations ac JOIN sources s ON s.source_id = ac.source_id WHERE ac.article_id = ? AND ac.version = ?`, endpoint.ID, snapshot.Current).Scan(&total, &active); countErr == nil && total > 0 && active == 0 {
				snapshot.Forgotten = true
			}
		}
	case EndpointAction:
		var forgotten, sourceSensitivity, sourceForgotten string
		err = q.QueryRowContext(ctx, `SELECT a.sensitivity, a.revision, COALESCE(a.forgotten_at, ''), COALESCE(s.sensitivity, 'normal'), COALESCE(s.forgotten_at, '') FROM actions a LEFT JOIN sources s ON s.source_id = a.source_id WHERE a.action_id = ?`, endpoint.ID).Scan(&snapshot.Sensitivity, &snapshot.Current, &forgotten, &sourceSensitivity, &sourceForgotten)
		snapshot.Sensitivity = maxSensitivity(snapshot.Sensitivity, sourceSensitivity)
		snapshot.Forgotten = forgotten != "" || sourceForgotten != ""
	case EndpointActionResult:
		var forgotten, actionForgotten, actionSensitivity, actionSourceSensitivity, actionSourceForgotten, resultSourceSensitivity, resultSourceForgotten string
		err = q.QueryRowContext(ctx, `SELECT ar.sensitivity, a.sensitivity, ar.version, COALESCE(ar.forgotten_at, ''), COALESCE(a.forgotten_at, ''), COALESCE(action_source.sensitivity, 'normal'), COALESCE(action_source.forgotten_at, ''), COALESCE(result_source.sensitivity, 'normal'), COALESCE(result_source.forgotten_at, '') FROM action_results ar JOIN actions a ON a.action_id = ar.action_id LEFT JOIN sources action_source ON action_source.source_id = a.source_id LEFT JOIN sources result_source ON result_source.source_id = ar.source_id WHERE ar.result_id = ?`, endpoint.ID).Scan(&snapshot.Sensitivity, &actionSensitivity, &snapshot.Current, &forgotten, &actionForgotten, &actionSourceSensitivity, &actionSourceForgotten, &resultSourceSensitivity, &resultSourceForgotten)
		snapshot.Sensitivity = maxSensitivity(snapshot.Sensitivity, actionSensitivity)
		snapshot.Sensitivity = maxSensitivity(snapshot.Sensitivity, actionSourceSensitivity)
		snapshot.Sensitivity = maxSensitivity(snapshot.Sensitivity, resultSourceSensitivity)
		snapshot.Forgotten = forgotten != "" || actionForgotten != "" || actionSourceForgotten != "" || resultSourceForgotten != ""
	}
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot, protocol.NewCodedError("RELATION_ENDPOINT_NOT_FOUND", "relation endpoint was not found", false, nil)
	}
	if err != nil {
		return snapshot, relationStorageError("cannot resolve relation endpoint", err)
	}
	if snapshot.Forgotten {
		return snapshot, protocol.NewCodedError("RELATION_ENDPOINT_NOT_FOUND", "relation endpoint was forgotten", false, nil)
	}
	if endpoint.Type == EndpointSource {
		snapshot.Current = 0
	} else if endpoint.Version > 0 && strict && endpoint.Version != snapshot.Current {
		return snapshot, protocol.NewCodedError("RELATION_VERSION_STALE", "relation endpoint version is not current", false, nil)
	} else if endpoint.Version > snapshot.Current {
		return snapshot, protocol.NewCodedError("RELATION_VERSION_STALE", "relation endpoint version is unavailable", false, nil)
	}
	return snapshot, nil
}

func validateEndpointSourceScope(ctx context.Context, q relationQuerier, endpoint RelationEndpoint, allowedSources map[string]bool) *protocol.CodedError {
	var rows *sql.Rows
	var err error
	switch endpoint.Type {
	case EndpointFact:
		rows, err = q.QueryContext(ctx, `SELECT source_id FROM fact_citations WHERE fact_id = ? AND version = (SELECT current_version FROM facts WHERE fact_id = ?) ORDER BY source_id ASC`, endpoint.ID, endpoint.ID)
	case EndpointArticle:
		rows, err = q.QueryContext(ctx, `SELECT source_id FROM article_citations WHERE article_id = ? AND version = (SELECT current_version FROM articles WHERE article_id = ?) ORDER BY source_id ASC`, endpoint.ID, endpoint.ID)
	case EndpointAction:
		var sourceID string
		err = q.QueryRowContext(ctx, `SELECT COALESCE(source_id, '') FROM actions WHERE action_id = ?`, endpoint.ID).Scan(&sourceID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return relationStorageError("cannot inspect relation action provenance", err)
		}
		if sourceID != "" && !allowedSources[sourceID] {
			return protocol.NewCodedError("SOURCE_REFERENCE_INVALID", "relation action provenance is outside the compile job", false, map[string]any{"source_id": sourceID})
		}
		return nil
	case EndpointActionResult:
		rows, err = q.QueryContext(ctx, `SELECT source_id FROM action_results WHERE result_id = ? UNION SELECT a.source_id FROM action_results ar JOIN actions a ON a.action_id = ar.action_id WHERE ar.result_id = ? ORDER BY source_id`, endpoint.ID, endpoint.ID)
	default:
		return nil
	}
	if err != nil {
		return relationStorageError("cannot inspect relation endpoint provenance", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sourceID sql.NullString
		if err := rows.Scan(&sourceID); err != nil {
			return relationStorageError("cannot decode relation endpoint provenance", err)
		}
		if sourceID.Valid && sourceID.String != "" && !allowedSources[sourceID.String] {
			return protocol.NewCodedError("SOURCE_REFERENCE_INVALID", "relation endpoint provenance is outside the compile job", false, map[string]any{"source_id": sourceID.String})
		}
	}
	if err := rows.Err(); err != nil {
		return relationStorageError("cannot finish relation endpoint provenance", err)
	}
	return nil
}

func entityCitationSensitivity(ctx context.Context, q relationQuerier, table, idColumn, id string, version int) (string, error) {
	query := fmt.Sprintf(`SELECT s.sensitivity FROM %s c JOIN sources s ON s.source_id = c.source_id WHERE c.%s = ? AND c.version = ? AND s.forgotten_at IS NULL ORDER BY CASE s.sensitivity WHEN 'restricted' THEN 2 WHEN 'sensitive' THEN 1 ELSE 0 END DESC LIMIT 1`, table, idColumn)
	var sensitivity string
	err := q.QueryRowContext(ctx, query, id, version).Scan(&sensitivity)
	if errors.Is(err, sql.ErrNoRows) {
		return "normal", nil
	}
	return sensitivity, err
}

func InsertRelationTx(ctx context.Context, tx *sql.Tx, relation ResolvedRelation, originKind, originID, createdAt string) (string, *protocol.CodedError) {
	relationID := relation.RelationID
	if relationID == "" {
		relationID = newRelationID()
	}
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO relations(relation_id, relation_type, from_type, from_id, from_version, to_type, to_id, to_version, source_id, origin_kind, origin_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), ?) ON CONFLICT(relation_type, from_type, from_id, from_version, to_type, to_id, to_version) DO NOTHING`, relationID, relation.RelationType, relation.From.Type, relation.From.ID, relation.From.Version, relation.To.Type, relation.To.ID, relation.To.Version, relation.SourceID, originKind, originID, createdAt)
	if err != nil {
		return "", relationStorageError("cannot insert relation", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return "", protocol.NewCodedError("RELATION_DUPLICATE", "relation already exists", false, nil)
	}
	return relationID, nil
}

func LoadPermittedRelations(ctx context.Context, q relationQuerier, allowSensitive bool, limit int) ([]RelationView, *protocol.CodedError) {
	if limit < 1 {
		limit = 1
	}
	rows, err := q.QueryContext(ctx, `SELECT relation_id, relation_type, from_type, from_id, from_version, to_type, to_id, to_version, COALESCE(source_id, ''), created_at FROM relations ORDER BY relation_type, from_type, from_id, from_version, to_type, to_id, to_version, relation_id LIMIT ?`, limit)
	if err != nil {
		return nil, relationStorageError("cannot query relations", err)
	}
	defer rows.Close()
	result := make([]RelationView, 0, limit)
	for rows.Next() {
		var view RelationView
		if err := rows.Scan(&view.RelationID, &view.RelationType, &view.From.Type, &view.From.ID, &view.From.Version, &view.To.Type, &view.To.ID, &view.To.Version, &view.SourceID, &view.CreatedAt); err != nil {
			return nil, relationStorageError("cannot decode relation", err)
		}
		from, codedErr := resolveEndpoint(ctx, q, &view.From, false)
		if codedErr != nil || (!allowSensitive && sensitivityRank(from.Sensitivity) > sensitivityRank("normal")) {
			continue
		}
		to, codedErr := resolveEndpoint(ctx, q, &view.To, false)
		if codedErr != nil || (!allowSensitive && sensitivityRank(to.Sensitivity) > sensitivityRank("normal")) {
			continue
		}
		if view.SourceID != "" {
			var sensitivity, forgotten string
			if err := q.QueryRowContext(ctx, `SELECT sensitivity, COALESCE(forgotten_at, '') FROM sources WHERE source_id = ?`, view.SourceID).Scan(&sensitivity, &forgotten); err != nil || forgotten != "" || (!allowSensitive && sensitivityRank(sensitivity) > sensitivityRank("normal")) {
				continue
			}
		}
		result = append(result, view)
	}
	if err := rows.Err(); err != nil {
		return nil, relationStorageError("cannot finish relation query", err)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].RelationType != result[j].RelationType {
			return result[i].RelationType < result[j].RelationType
		}
		if result[i].From.Type != result[j].From.Type {
			return result[i].From.Type < result[j].From.Type
		}
		if result[i].From.ID != result[j].From.ID {
			return result[i].From.ID < result[j].From.ID
		}
		if result[i].To.Type != result[j].To.Type {
			return result[i].To.Type < result[j].To.Type
		}
		if result[i].To.ID != result[j].To.ID {
			return result[i].To.ID < result[j].To.ID
		}
		return result[i].RelationID < result[j].RelationID
	})
	return result, nil
}

func RelationTypes() []string {
	result := make([]string, 0, len(relationTypes))
	for value := range relationTypes {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func EndpointTypes() []string {
	result := make([]string, 0, len(endpointTypes))
	for value := range endpointTypes {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sensitivityRank(value string) int {
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

func maxSensitivity(left, right string) string {
	if sensitivityRank(right) > sensitivityRank(left) {
		return right
	}
	return left
}

func relationStorageError(message string, err error) *protocol.CodedError {
	return protocol.NewCodedError("STORAGE_UNHEALTHY", message, true, err.Error())
}

func newRelationID() string {
	var random [6]byte
	_, _ = rand.Read(random[:])
	return fmt.Sprintf("rel_%s_%s", time.Now().UTC().Format("20060102T150405.000000000Z"), hex.EncodeToString(random[:]))
}

// NewRelationID is used by preview plans so a relation has a stable identity
// before the confirmation boundary is crossed.
func NewRelationID() string { return newRelationID() }
