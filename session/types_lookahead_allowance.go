//go:build linux

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type LookaheadAllowanceStatus string

const (
	LookaheadAllowanceReserved LookaheadAllowanceStatus = "reserved"
	LookaheadAllowanceOpen     LookaheadAllowanceStatus = "open"
	LookaheadAllowanceReleased LookaheadAllowanceStatus = "released"
	LookaheadAllowanceExpired  LookaheadAllowanceStatus = "expired"
)

type LookaheadAllowance struct {
	AllowanceID        string
	AdminChatID        int64
	ReviewEventID      int64
	SourceSessionID    string
	TargetSessionID    string
	Status             LookaheadAllowanceStatus
	NextActionRecordID string
	EntryID            string
	Reason             string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ExpiresAt          time.Time
	ReleasedAt         time.Time
}

type LookaheadAllowanceInput struct {
	AllowanceID        string
	AdminChatID        int64
	ReviewEventID      int64
	SourceSessionID    string
	TargetSessionID    string
	Status             LookaheadAllowanceStatus
	NextActionRecordID string
	EntryID            string
	Reason             string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ExpiresAt          time.Time
	ReleasedAt         time.Time
}

func NormalizeLookaheadAllowanceStatus(status LookaheadAllowanceStatus) LookaheadAllowanceStatus {
	switch LookaheadAllowanceStatus(normalizeEnumValue(string(status))) {
	case LookaheadAllowanceReserved,
		LookaheadAllowanceOpen,
		LookaheadAllowanceReleased,
		LookaheadAllowanceExpired:
		return LookaheadAllowanceStatus(normalizeEnumValue(string(status)))
	default:
		return ""
	}
}

func NormalizeLookaheadAllowanceInput(input LookaheadAllowanceInput) LookaheadAllowanceInput {
	input.AllowanceID = strings.TrimSpace(input.AllowanceID)
	input.SourceSessionID = strings.TrimSpace(input.SourceSessionID)
	input.TargetSessionID = strings.TrimSpace(input.TargetSessionID)
	input.Status = NormalizeLookaheadAllowanceStatus(input.Status)
	if input.Status == "" {
		input.Status = LookaheadAllowanceReserved
	}
	input.NextActionRecordID = strings.TrimSpace(input.NextActionRecordID)
	input.EntryID = strings.TrimSpace(input.EntryID)
	input.Reason = normalizeEnumValue(input.Reason)
	if !input.CreatedAt.IsZero() {
		input.CreatedAt = input.CreatedAt.UTC()
	}
	if !input.UpdatedAt.IsZero() {
		input.UpdatedAt = input.UpdatedAt.UTC()
	}
	if !input.ExpiresAt.IsZero() {
		input.ExpiresAt = input.ExpiresAt.UTC()
	}
	if !input.ReleasedAt.IsZero() {
		input.ReleasedAt = input.ReleasedAt.UTC()
	}
	if input.AllowanceID == "" {
		input.AllowanceID = LookaheadAllowanceID(input.AdminChatID, input.ReviewEventID, input.CreatedAt, input.NextActionRecordID, input.EntryID)
	}
	return input
}

func NormalizeLookaheadAllowance(record LookaheadAllowance) LookaheadAllowance {
	normalized := NormalizeLookaheadAllowanceInput(LookaheadAllowanceInput(record))
	return LookaheadAllowance(normalized)
}

func ValidateLookaheadAllowanceInput(input LookaheadAllowanceInput) error {
	input = NormalizeLookaheadAllowanceInput(input)
	if input.AllowanceID == "" {
		return fmt.Errorf("lookahead allowance requires allowance_id")
	}
	if input.AdminChatID == 0 {
		return fmt.Errorf("lookahead allowance requires admin_chat_id")
	}
	if input.Status == "" {
		return fmt.Errorf("lookahead allowance requires status")
	}
	return nil
}

func LookaheadAllowanceID(adminChatID int64, reviewEventID int64, createdAt time.Time, nextActionRecordID string, entryID string) string {
	seed := strings.Join([]string{
		fmt.Sprint(adminChatID),
		fmt.Sprint(reviewEventID),
		strings.TrimSpace(nextActionRecordID),
		strings.TrimSpace(entryID),
		createdAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "lookahead:" + hex.EncodeToString(sum[:16])
}
