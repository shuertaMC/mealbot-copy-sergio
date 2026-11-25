package pairing

// Match forms one complete round. The returned map is a deep copy; input
// histories are unchanged. Matching retries recent repeats up to MaxTries.
func Match(members MembersMap, roundNumber int, random Random) (MembersMap, Round) {
	if random == nil {
		random = RandomFunc(nil)
	}
	ids := members.IDs()
	if len(ids) < 2 {
		return members.clone(), NewRound(roundNumber)
	}

	var matched MembersMap
	var round Round
	for attempt := 0; attempt < MaxTries; attempt++ {
		matched = members.clone()
		round = NewRound(roundNumber)
		recentMatches := 0

		holdout := ""
		if len(ids)%2 == 1 {
			holdout = ids[randomIndex(random, len(ids))]
		}

		for _, memberID := range ids {
			if memberID == holdout || round.IsPaired(memberID) {
				continue
			}
			partner, recent, ok := choosePartner(matched, round, memberID, holdout, ids, random)
			if !ok {
				continue
			}
			round.AddPair(memberID, partner)
			matched.UpdateLastRoundWith(memberID, partner, roundNumber)
			if recent {
				recentMatches++
			}
		}

		if recentMatches == 0 || attempt == MaxTries-1 {
			return matched, round
		}
	}
	return matched, round
}

func choosePartner(members MembersMap, round Round, memberID, holdout string, ids []string, random Random) (string, bool, bool) {
	member, exists := members[memberID]
	if !exists {
		return "", false, false
	}
	good := make([]string, 0)
	okay := make([]string, 0)
	bad := make([]string, 0)
	for _, candidateID := range ids {
		if candidateID == memberID || candidateID == holdout || round.IsPaired(candidateID) {
			continue
		}
		candidate, exists := members[candidateID]
		if !exists {
			continue
		}
		lastRound, known := member.LastRoundWith[candidateID]
		if !known {
			lastRound = -1
		}
		notRecent := lastRound == -1 || round.Number-lastRound > RecentRoundRange
		// Equality is the frozen legacy preference despite the historical local
		// variable being named hasDifferentTraits.
		traitsPreferred := member.Trait == candidate.Trait
		switch {
		case notRecent && traitsPreferred:
			good = append(good, candidateID)
		case notRecent:
			okay = append(okay, candidateID)
		default:
			bad = append(bad, candidateID)
		}
	}
	if len(good) > 0 {
		return good[randomIndex(random, len(good))], false, true
	}
	if len(okay) > 0 {
		return okay[randomIndex(random, len(okay))], false, true
	}
	if len(bad) > 0 {
		return bad[randomIndex(random, len(bad))], true, true
	}
	return "", false, false
}

func randomIndex(random Random, size int) int {
	if size <= 1 {
		if size == 1 {
			// Preserve the legacy random call for single-candidate selections; retry
			// exhaustion is observable through deterministic fakes.
			random.Intn(size)
		}
		return 0
	}
	index := random.Intn(size)
	if index < 0 {
		index = -index
	}
	return index % size
}
