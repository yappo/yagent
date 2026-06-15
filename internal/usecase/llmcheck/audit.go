package llmcheck

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"time"
)

const AuditScratchKind = "llm_doctor"

type AuditRecord struct {
	ID              string           `json:"id"`
	ServerName      string           `json:"server_name"`
	URL             string           `json:"url"`
	API             string           `json:"api"`
	Model           string           `json:"model"`
	ModelFound      bool             `json:"model_found"`
	ModelExactMatch bool             `json:"model_exact_match"`
	MatchedModel    string           `json:"matched_model,omitempty"`
	Warnings        []string         `json:"warnings,omitempty"`
	Problems        []string         `json:"problems,omitempty"`
	Suggestions     []string         `json:"suggestions,omitempty"`
	Recommendations []Recommendation `json:"recommendations,omitempty"`
	Runtime         RuntimeResult    `json:"runtime,omitempty"`
	Probe           ProbeResult      `json:"probe,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
}

func NewAuditRecord(result Result, createdAt time.Time) AuditRecord {
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	record := AuditRecord{
		ServerName:      result.ServerName,
		URL:             result.URL,
		API:             result.API,
		Model:           result.Model,
		ModelFound:      result.ModelFound,
		ModelExactMatch: result.ModelExactMatch,
		MatchedModel:    result.MatchedModel,
		Warnings:        append([]string(nil), result.Warnings...),
		Problems:        append([]string(nil), result.Problems...),
		Suggestions:     append([]string(nil), result.Suggestions...),
		Recommendations: append([]Recommendation(nil), result.Recommendations...),
		Runtime:         result.Runtime,
		Probe:           result.Probe,
		CreatedAt:       createdAt,
	}
	record.ID = auditRecordID(record)
	return record
}

func AuditSummary(record AuditRecord) string {
	status := "ok"
	if len(record.Problems) > 0 {
		status = "problem"
	} else if len(record.Warnings) > 0 {
		status = "warning"
	}
	return strings.TrimSpace(strings.Join([]string{
		status,
		record.ServerName,
		fallback(record.MatchedModel, record.Model),
	}, " "))
}

func auditRecordID(record AuditRecord) string {
	sum := sha1.Sum([]byte(strings.Join([]string{
		record.ServerName,
		record.URL,
		record.API,
		record.Model,
		record.MatchedModel,
		record.CreatedAt.Format(time.RFC3339Nano),
	}, "\x00")))
	return "llm-doctor-" + hex.EncodeToString(sum[:])
}
