package pairing

// MemberRecord is the persistence-facing member model needed by the pairing
// boundary. Infrastructure adapters remain responsible for database access.
type MemberRecord struct {
	ID            string
	Name          string
	Metadata      map[string]string
	LastRoundWith map[string]int
}

// MembersFromRecords converts active persistence records using the selected
// organization trait. History maps are copied to keep layers independent.
func MembersFromRecords(records []MemberRecord, trait string) MembersMap {
	members := make(MembersMap, len(records))
	for _, record := range records {
		history := make(map[string]int, len(record.LastRoundWith))
		for partner, round := range record.LastRoundWith {
			history[partner] = round
		}
		members[record.ID] = MinimalMember{
			ID:            record.ID,
			Name:          record.Name,
			Trait:         record.Metadata[trait],
			LastRoundWith: history,
		}
	}
	return members
}

// PairRecord is the persistence-facing representation of one pair row.
type PairRecord struct {
	Organization string
	ID1          string
	ID2          string
	ExtraID      string
	Round         int
}

// PairRecords converts a round to stable persistence rows.
func PairRecords(organization string, round Round) []PairRecord {
	pairs := round.DrawOrderedPairs
	if len(pairs) == 0 && len(round.Pairs) > 0 {
		pairs = make([]Pair, 0, len(round.Pairs))
		for pair := range round.Pairs {
			pairs = append(pairs, pair)
		}
		for i := 0; i < len(pairs); i++ {
			for j := i + 1; j < len(pairs); j++ {
				if pairs[j].ID1 < pairs[i].ID1 || (pairs[j].ID1 == pairs[i].ID1 && pairs[j].ID2 < pairs[i].ID2) {
					pairs[i], pairs[j] = pairs[j], pairs[i]
				}
			}
		}
	}
	records := make([]PairRecord, 0, len(pairs))
	for _, pair := range pairs {
		records = append(records, PairRecord{
			Organization: organization,
			ID1:          pair.ID1,
			ID2:          pair.ID2,
			ExtraID:      pair.ExtraID,
			Round:         round.Number,
		})
	}
	return records
}
