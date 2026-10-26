package migration

import (
	"reflect"
	"testing"

	"github.com/johnamadeo/mealbot/internal/store"
)

func TestRecordPair(t *testing.T) {
	history := map[string]map[string]int{"a": {}, "b": {}, "c": {}}

	RecordPair(history, store.PairRecord{ID1: "a", ID2: "b"}, 0)
	RecordPair(history, store.PairRecord{ID1: "a", ID2: "c", ExtraID: "b"}, 1)
	// inactive members are ignored
	RecordPair(history, store.PairRecord{ID1: "a", ID2: "gone"}, 2)

	want := map[string]map[string]int{
		"a": {"b": 1, "c": 1},
		"b": {"a": 1, "c": 1},
		"c": {"a": 1, "b": 1},
	}
	if !reflect.DeepEqual(history, want) {
		t.Errorf("history = %v, want %v", history, want)
	}
}
