package system

import (
	"context"
	"sort"

	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/storage"
)

const (
	CLIVersion   = "0.1.0"
	SkillVersion = "0.1.0"
)

type handshakeData struct {
	CLIVersion        string                `json:"cli_version"`
	SkillVersion      string                `json:"skill_version"`
	ProtocolVersion   string                `json:"protocol_version"`
	SupportedVersions []string              `json:"supported_versions"`
	Capabilities      []protocol.Capability `json:"capabilities"`
}

type capabilityData struct {
	CLIVersion        string                `json:"cli_version"`
	ProtocolVersion   string                `json:"protocol_version"`
	SupportedVersions []string              `json:"supported_versions"`
	SourceTypes       []string              `json:"source_types"`
	Operations        []protocol.Capability `json:"operations"`
}

func Handshake(req protocol.Request) (any, *protocol.CodedError) {
	if req.ProtocolVersion != protocol.SupportedVersion {
		return nil, protocol.NewCodedError("PROTOCOL_VERSION_UNSUPPORTED", "request protocol version is not supported", false, map[string]any{"supported": []string{protocol.SupportedVersion}})
	}
	if req.Actor.Type != "claude-skill" {
		return nil, protocol.NewCodedError("ACTOR_UNSUPPORTED", "only claude-skill actors are supported by this runtime", false, nil)
	}
	if req.Actor.SkillVersion != SkillVersion {
		return nil, protocol.NewCodedError("SKILL_VERSION_UNSUPPORTED", "Skill version is not compatible with this CLI", false, map[string]any{"supported": SkillVersion})
	}
	return handshakeData{
		CLIVersion:        CLIVersion,
		SkillVersion:      SkillVersion,
		ProtocolVersion:   protocol.SupportedVersion,
		SupportedVersions: []string{protocol.SupportedVersion},
		Capabilities:      sortedCapabilities(),
	}, nil
}

func Capabilities() any {
	return capabilityData{
		CLIVersion:        CLIVersion,
		ProtocolVersion:   protocol.SupportedVersion,
		SupportedVersions: []string{protocol.SupportedVersion},
		SourceTypes:       []string{"document", "markdown", "text"},
		Operations:        sortedCapabilities(),
	}
}

func Health(ctx context.Context, store *storage.Storage) (any, *protocol.CodedError) {
	result, err := store.Health(ctx)
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "health checks could not complete", true, nil)
	}
	if result.Overall != "healthy" {
		return result, protocol.NewCodedError("STORAGE_UNHEALTHY", "one or more Moss health checks failed", true, result)
	}
	return result, nil
}

func sortedCapabilities() []protocol.Capability {
	capabilities := append([]protocol.Capability(nil), protocol.SupportedCapabilities()...)
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i].Operation < capabilities[j].Operation })
	return capabilities
}
