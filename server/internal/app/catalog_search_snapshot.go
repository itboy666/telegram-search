package app

import (
	"container/heap"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

const searchWindowLimit = 5000

type searchCandidate struct {
	id    int64
	score float64
}
type searchCandidateHeap []searchCandidate

func (h searchCandidateHeap) Len() int           { return len(h) }
func (h searchCandidateHeap) Less(i, j int) bool { return h[i].score > h[j].score }
func (h searchCandidateHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *searchCandidateHeap) Push(value any)    { *h = append(*h, value.(searchCandidate)) }
func (h *searchCandidateHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}

// Each new search shuffles candidates uniformly. A resource's historical votes
// have no effect; the seed keeps ordering independent of database row order.
func searchShuffleScore(seed string, id int64) float64 {
	sum := sha256.Sum256([]byte(seed + ":" + str(id)))
	bits := binary.BigEndian.Uint64(sum[:8]) >> 11
	return (float64(bits) + 1) / (float64(1<<53) + 2)
}

func addSearchCandidate(h *searchCandidateHeap, limit int, candidate searchCandidate) {
	if len(*h) < limit {
		heap.Push(h, candidate)
	} else if candidate.score < (*h)[0].score {
		(*h)[0] = candidate
		heap.Fix(h, 0)
	}
}

func orderedSearchIDs(h searchCandidateHeap) []int64 {
	sort.Slice(h, func(i, j int) bool {
		if h[i].score == h[j].score {
			return h[i].id < h[j].id
		}
		return h[i].score < h[j].score
	})
	ids := make([]int64, len(h))
	for i := range h {
		ids[i] = h[i].id
	}
	return ids
}

func searchQueryHash(input Row, size int64) string {
	// Do not hash the computed "days" date cutoff; it must stay valid as time
	// advances while the browser is paging through a saved result.
	days := integer(input["days"])
	if days != 3 && days != 7 && days != 30 {
		days = 0
	}
	return hash(encode(Row{"q": strings.TrimSpace(str(input["q"])), "type": str(input["type"]), "category_id": integer(input["category_id"]), "featured": boolean(input["featured"]), "days": days, "page_size": size}))
}

func snapshotIDs(snapshot Row) ([]int64, error) {
	ids := []int64{}
	if err := json.Unmarshal([]byte(str(snapshot["candidate_ids"])), &ids); err != nil || len(ids) > searchWindowLimit {
		return nil, catalogErr(410, "搜索结果已失效，请重新搜索。")
	}
	return ids, nil
}

func (a *App) readSearchSnapshot(token, queryHash string) (Row, error) {
	if len(token) != 48 || strings.Trim(token, "0123456789abcdef") != "" {
		return nil, catalogErr(410, "搜索结果已失效，请重新搜索。")
	}
	snapshot, err := one(a.DB, "SELECT * FROM ts_search_snapshots WHERE token_hash=?", hash(token))
	if err != nil {
		return nil, err
	}
	if snapshot == nil || expired(snapshot["expires_at"]) {
		return nil, catalogErr(410, "搜索结果已过期，请重新搜索。")
	}
	if queryHash != "" && str(snapshot["query_hash"]) != queryHash {
		return nil, catalogErr(409, "搜索条件已改变，请发起新的搜索。")
	}
	return snapshot, nil
}

func (a *App) newSearchSnapshot(input Row, where string, args []any, size int64) (Row, string, error) {
	limit := int(size * 100)
	if limit > searchWindowLimit {
		limit = searchWindowLimit
	}
	token := randomHex(24)
	// Stream every matching ID, never title/description or ORDER BY RAND.
	// This is O(matches*log(window)) worst-case sampling time and O(window)
	// memory, not search-engine scale. All matches are read for an exact total.
	rows, err := a.DB.Query("SELECT r.id FROM ts_resources r WHERE "+where, args...)
	if err != nil {
		return nil, "", err
	}
	candidates := searchCandidateHeap{}
	total := int64(0)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		total++
		addSearchCandidate(&candidates, limit, searchCandidate{id: id, score: searchShuffleScore(token, id)})
	}
	if err == nil {
		err = rows.Err()
	}
	// Release the connection before the INSERT, including when the pool is one.
	_ = rows.Close()
	if err != nil {
		return nil, "", err
	}
	ids := orderedSearchIDs(candidates)
	snapshot := Row{"query_hash": searchQueryHash(input, size), "candidate_ids": encode(ids), "total": total, "window_total": len(ids), "page_size": size, "created_at": now(), "expires_at": future(2 * time.Hour)}
	_, err = a.DB.Exec("INSERT INTO ts_search_snapshots(token_hash,query_hash,candidate_ids,total,window_total,page_size,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?)", hash(token), snapshot["query_hash"], snapshot["candidate_ids"], total, len(ids), size, snapshot["created_at"], snapshot["expires_at"])
	return snapshot, token, err
}

func searchPageIDs(snapshot Row, page, size int64) ([]int64, error) {
	ids, err := snapshotIDs(snapshot)
	if err != nil {
		return nil, err
	}
	start := (page - 1) * size
	if start < 0 || start >= int64(len(ids)) {
		return []int64{}, nil
	}
	end := start + size
	if end > int64(len(ids)) {
		end = int64(len(ids))
	}
	return ids[start:end], nil
}

func (a *App) searchSnapshotPage(snapshot Row, page, size int64) ([]Row, error) {
	ids, err := searchPageIDs(snapshot, page, size)
	if err != nil || len(ids) == 0 {
		return []Row{}, err
	}
	marks, args := []string{}, []any{}
	for _, id := range ids {
		marks = append(marks, "?")
		args = append(args, id)
	}
	rows, err := query(a.DB, "SELECT r.*,c.name AS category_name FROM ts_resources r LEFT JOIN ts_categories c ON c.id=r.category_id WHERE r.status='published' AND r.id IN ("+strings.Join(marks, ",")+")", args...)
	if err != nil {
		return nil, err
	}
	byID := map[int64]Row{}
	for _, row := range rows {
		delete(row, "like_count")
		byID[integer(row["id"])] = row
	}
	result := []Row{}
	for slot, id := range ids {
		if row := byID[id]; row != nil {
			row["snapshot_position"] = (page-1)*size + int64(slot) + 1
			result = append(result, row)
		}
	}
	return result, nil
}

func (a *App) snapshotSearch(input Row, where string, args []any, page, size int64) (Row, error) {
	token := str(input["search_token"])
	var snapshot Row
	var err error
	if token == "" {
		snapshot, token, err = a.newSearchSnapshot(input, where, args, size)
	} else {
		snapshot, err = a.readSearchSnapshot(token, searchQueryHash(input, size))
	}
	if err != nil {
		return nil, err
	}
	rows, err := a.searchSnapshotPage(snapshot, page, size)
	window := integer(snapshot["window_total"])
	return Row{"items": rows, "total": snapshot["total"], "window_total": window, "page": page, "page_size": size, "total_pages": (window + size - 1) / size, "has_more": page*size < window, "search_token": token, "snapshot_at": snapshot["created_at"], "expires_at": snapshot["expires_at"]}, err
}

// PurgeSearchSnapshots only removes expired short-lived search state.
func (a *App) PurgeSearchSnapshots() (int64, error) {
	res, err := a.DB.Exec("DELETE FROM ts_search_snapshots WHERE expires_at<=? LIMIT 2000", now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
