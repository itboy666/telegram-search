package app

import (
	"reflect"
	"testing"
)

func TestSearchUniformRandomness(t *testing.T) {
	wins := 0
	for i := 0; i < 10000; i++ {
		seed := "shuffle-fixture-" + str(i)
		first, second := searchShuffleScore(seed, 1), searchShuffleScore(seed, 2)
		if first <= 0 || first >= 1 || second <= 0 || second >= 1 {
			t.Fatal("random scores must stay within (0,1)")
		}
		if first < second {
			wins++
		}
	}
	if wins < 4500 || wins > 5500 {
		t.Fatalf("resource won %d/10000; shuffle should give equal chances", wins)
	}
}

func TestSearchReservoirStableAndBounded(t *testing.T) {
	selectIDs := func(seed string, reverse bool) []int64 {
		h := searchCandidateHeap{}
		for i := int64(1); i <= 200; i++ {
			id := i
			if reverse {
				id = 201 - i
			}
			addSearchCandidate(&h, 15, searchCandidate{id: id, score: searchShuffleScore(seed, id)})
			if len(h) > 15 {
				t.Fatal("candidate memory exceeded the requested window")
			}
		}
		return orderedSearchIDs(h)
	}
	ids := selectIDs("seed-a", false)
	if len(ids) != 15 || !reflect.DeepEqual(ids, selectIDs("seed-a", true)) {
		t.Fatal("same seed depended on database row order")
	}
	if reflect.DeepEqual(ids, selectIDs("seed-b", false)) {
		t.Fatal("new search seed did not change the selection")
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatal("duplicate search result")
		}
		seen[id] = true
	}
	snapshot := Row{"candidate_ids": encode(ids)}
	for page := int64(1); page <= 3; page++ {
		got, err := searchPageIDs(snapshot, page, 5)
		if err != nil || !reflect.DeepEqual(got, ids[(page-1)*5:page*5]) {
			t.Fatal("pagination did not preserve fixed slots", err)
		}
	}
}

func TestSearchQueryIdentity(t *testing.T) {
	original := Row{"q": " 服务器 ", "type": "group", "category_id": "2", "days": "7"}
	same := Row{"q": "服务器", "type": "group", "category_id": int64(2), "days": int64(7), "page": 9}
	if searchQueryHash(original, 12) != searchQueryHash(same, 12) {
		t.Fatal("equivalent query changed the snapshot identity")
	}
	for _, changed := range []Row{
		catalogMerge(same, Row{"type": "bot"}),
		catalogMerge(same, Row{"q": "音乐"}),
		catalogMerge(same, Row{"days": 30}),
		catalogMerge(same, Row{"featured": true}),
	} {
		if searchQueryHash(original, 12) == searchQueryHash(changed, 12) {
			t.Fatal("different query reused a snapshot identity")
		}
	}
	if searchQueryHash(original, 12) == searchQueryHash(original, 20) {
		t.Fatal("changed page size reused a snapshot")
	}
}
