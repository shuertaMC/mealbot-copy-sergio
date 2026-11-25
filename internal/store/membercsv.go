package store

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	ErrMemberNameHeaderRequired  = errors.New("CSV header must contain name")
	ErrMemberEmailHeaderRequired = errors.New("CSV header must contain email")
	ErrMemberRecordsRequired     = errors.New("CSV must contain at least one member record")
)

// MemberInput is the transport-independent input used to plan a member sync.
type MemberInput struct {
	Name     string
	Email    string
	Metadata map[string]string
}

// MemberSyncPlan is a fully validated, mutation-free description of a sync.
// SynchronizeMembers validates it again so callers cannot invalidate a plan by
// mutating exported fields between planning and persistence.
type MemberSyncPlan struct {
	Organization string
	Members      []MemberInput
}

// ParseMemberCSV consumes and validates the complete document. It performs no
// persistence and never includes row values in errors, keeping metadata out of
// logs and responses.
func ParseMemberCSV(source io.Reader) ([]MemberInput, error) {
	if source == nil {
		return nil, errors.New("CSV reader is required")
	}
	reader := csv.NewReader(source)
	reader.FieldsPerRecord = 0

	headers, err := reader.Read()
	if errors.Is(err, io.EOF) {
		return nil, ErrMemberNameHeaderRequired
	}
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	normalized, nameColumn, emailColumn, err := normalizeMemberHeaders(headers)
	if err != nil {
		return nil, err
	}

	members := make([]MemberInput, 0)
	seenEmails := make(map[string]int)
	for rowNumber := 2; ; rowNumber++ {
		row, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read CSV row %d: %w", rowNumber, err)
		}
		name := strings.TrimSpace(row[nameColumn])
		email := strings.TrimSpace(row[emailColumn])
		if name == "" {
			return nil, fmt.Errorf("CSV row %d has an empty name", rowNumber)
		}
		if email == "" {
			return nil, fmt.Errorf("CSV row %d has an empty email", rowNumber)
		}
		emailIdentity := strings.ToLower(email)
		if firstRow, duplicate := seenEmails[emailIdentity]; duplicate {
			return nil, fmt.Errorf("CSV rows %d and %d contain duplicate email identities", firstRow, rowNumber)
		}
		seenEmails[emailIdentity] = rowNumber

		metadata := make(map[string]string, len(row)-2)
		for column, value := range row {
			if column == nameColumn || column == emailColumn {
				continue
			}
			metadata[normalized[column]] = value
		}
		members = append(members, MemberInput{Name: name, Email: email, Metadata: metadata})
	}
	if len(members) == 0 {
		return nil, ErrMemberRecordsRequired
	}
	return members, nil
}

func normalizeMemberHeaders(headers []string) ([]string, int, int, error) {
	normalized := make([]string, len(headers))
	seen := make(map[string]int, len(headers))
	nameColumn, emailColumn := -1, -1
	for column, header := range headers {
		key := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(header, "\ufeff")))
		if key == "" {
			return nil, -1, -1, fmt.Errorf("CSV header column %d is empty", column+1)
		}
		if firstColumn, duplicate := seen[key]; duplicate {
			return nil, -1, -1, fmt.Errorf("CSV header columns %d and %d are ambiguous duplicates", firstColumn+1, column+1)
		}
		seen[key] = column
		normalized[column] = key
		switch key {
		case "name":
			nameColumn = column
		case "email":
			emailColumn = column
		}
	}
	if nameColumn < 0 {
		return nil, -1, -1, ErrMemberNameHeaderRequired
	}
	if emailColumn < 0 {
		return nil, -1, -1, ErrMemberEmailHeaderRequired
	}
	return normalized, nameColumn, emailColumn, nil
}

// PlanMemberSynchronization validates and defensively copies parsed records.
func PlanMemberSynchronization(organization string, members []MemberInput) (MemberSyncPlan, error) {
	if strings.TrimSpace(organization) == "" {
		return MemberSyncPlan{}, ErrOrganizationNameRequired
	}
	if len(members) == 0 {
		return MemberSyncPlan{}, ErrMemberRecordsRequired
	}
	planned := make([]MemberInput, 0, len(members))
	seenEmails := make(map[string]int, len(members))
	for index, member := range members {
		name := strings.TrimSpace(member.Name)
		email := strings.TrimSpace(member.Email)
		if name == "" {
			return MemberSyncPlan{}, fmt.Errorf("member record %d has an empty name", index+1)
		}
		if email == "" {
			return MemberSyncPlan{}, fmt.Errorf("member record %d has an empty email", index+1)
		}
		identity := strings.ToLower(email)
		if first, duplicate := seenEmails[identity]; duplicate {
			return MemberSyncPlan{}, fmt.Errorf("member records %d and %d contain duplicate email identities", first+1, index+1)
		}
		seenEmails[identity] = index
		metadata := make(map[string]string, len(member.Metadata))
		for rawKey, value := range member.Metadata {
			key := strings.ToLower(strings.TrimSpace(rawKey))
			if key == "" || key == "name" || key == "email" {
				return MemberSyncPlan{}, fmt.Errorf("member record %d contains an invalid metadata key", index+1)
			}
			if _, duplicate := metadata[key]; duplicate {
				return MemberSyncPlan{}, fmt.Errorf("member record %d contains ambiguous metadata keys", index+1)
			}
			metadata[key] = value
		}
		planned = append(planned, MemberInput{Name: name, Email: email, Metadata: metadata})
	}
	return MemberSyncPlan{Organization: organization, Members: planned}, nil
}
