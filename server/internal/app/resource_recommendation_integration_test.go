package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type recommendationFixture struct {
	t         *testing.T
	a         *App
	tag       string
	users     []int64
	resources []int64
	tokens    map[string]bool
	sessions  []string
}

func newRecommendationFixture(t *testing.T) *recommendationFixture {
	t.Helper()
	if os.Getenv("STARINDEX_RECOMMENDATION_TEST") != "1" {
		t.Skip("set STARINDEX_RECOMMENDATION_TEST=1 for isolated recommendation fixtures")
	}
	root, _ := filepath.Abs("../../..")
	a, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	f := &recommendationFixture{t: t, a: a, tag: "gorec_" + randomHex(7), tokens: map[string]bool{}}
	a.TelegramCall = func(context.Context, string, Row) (Row, error) {
		t.Error("unexpected Telegram call outside a mock")
		return nil, errors.New("Telegram fixture has no transport")
	}
	t.Cleanup(func() {
		for token := range f.tokens {
			if _, err := a.DB.Exec("DELETE FROM ts_search_snapshots WHERE token_hash=?", hash(token)); err != nil {
				t.Error(err)
			}
		}
		for _, session := range f.sessions {
			_, _ = a.DB.Exec("DELETE FROM ts_bot_sessions WHERE id=?", session)
		}
		for _, id := range f.resources {
			_, _ = a.DB.Exec("DELETE FROM ts_resource_likes WHERE resource_id=?", id)
			_, _ = a.DB.Exec("DELETE FROM ts_resources WHERE id=?", id)
		}
		for _, id := range f.users {
			for _, table := range []string{"resource_likes", "telegram_profiles", "sessions", "reward_claims", "wallets"} {
				_, _ = a.DB.Exec("DELETE FROM ts_"+table+" WHERE user_id=?", id)
			}
			_, _ = a.DB.Exec("DELETE FROM ts_users WHERE id=?", id)
			_, _ = a.DB.Exec("DELETE FROM ts_rate_limits WHERE bucket=?", "resource-like:"+str(id))
		}
		_, _ = a.DB.Exec("DELETE FROM ts_search_logs WHERE query=?", f.tag)
		_ = a.DB.Close()
	})
	return f
}

func (f *recommendationFixture) user() int64 {
	f.t.Helper()
	id, err := catalogInsert(f.a.DB, "users", Row{"username": f.tag + "_" + randomHex(3), "display_name": "Recommendation fixture", "status": 1, "created_at": now()})
	if err != nil {
		f.t.Fatal(err)
	}
	f.users = append(f.users, id)
	return id
}

func (f *recommendationFixture) resource(kind string) int64 {
	f.t.Helper()
	link := "https://t.me/" + f.tag + randomHex(3)
	id, err := catalogInsert(f.a.DB, "resources", Row{"title": f.tag + " " + str(len(f.resources)), "description": "", "url": link, "url_hash": hash(link), "type": kind, "status": "published", "created_at": now(), "updated_at": now()})
	if err != nil {
		f.t.Fatal(err)
	}
	f.resources = append(f.resources, id)
	return id
}

func (f *recommendationFixture) search(input Row) Row {
	f.t.Helper()
	result, err := f.a.Search(catalogMerge(Row{"q": f.tag, "page_size": 3}, input), "recommendation-test")
	if err != nil {
		f.t.Fatal(err)
	}
	f.tokens[str(result["search_token"])] = true
	return result
}

func recommendationResultIDs(result Row) []int64 {
	ids := []int64{}
	for _, item := range rowsOf(result["items"]) {
		ids = append(ids, integer(item["id"]))
	}
	return ids
}

func TestSearchMySQLIgnoresHistoricalRecommendations(t *testing.T) {
	f := newRecommendationFixture(t)
	for i := 0; i < 12; i++ {
		id := f.resource([]string{"group", "channel", "bot", "video"}[i%4])
		if _, err := f.a.DB.Exec("UPDATE ts_resources SET like_count=? WHERE id=?", int64(i)*1_000_000, id); err != nil {
			t.Fatal(err)
		}
	}
	result := f.search(Row{"page_size": 50})
	seed := str(result["search_token"])
	want := append([]int64{}, f.resources...)
	sort.Slice(want, func(i, j int) bool {
		return searchShuffleScore(seed, want[i]) < searchShuffleScore(seed, want[j])
	})
	if !reflect.DeepEqual(want, recommendationResultIDs(result)) {
		t.Fatal("historical vote counts still influence search ordering")
	}
	for _, item := range rowsOf(result["items"]) {
		if item["like_count"] != nil || item["liked"] != nil {
			t.Fatal("search still exposes recommendation fields")
		}
	}
}

func TestRecommendationMySQLStableSnapshotsAndFiltering(t *testing.T) {
	f := newRecommendationFixture(t)
	for i := 0; i < 11; i++ {
		f.resource("group")
	}
	first := f.search(Row{})
	token := str(first["search_token"])
	if len(token) != 48 || integer(first["total"]) != 11 || integer(first["window_total"]) != 11 || integer(first["total_pages"]) != 4 {
		t.Fatal("snapshot metadata incorrect", first)
	}
	all := []int64{}
	for page := int64(1); page <= 4; page++ {
		result := f.search(Row{"search_token": token, "page": page})
		again := f.search(Row{"search_token": token, "page": page})
		ids := recommendationResultIDs(result)
		if !reflect.DeepEqual(ids, recommendationResultIDs(again)) {
			t.Fatal("page order changed within a snapshot")
		}
		all = append(all, ids...)
	}
	seen := map[int64]bool{}
	for _, id := range all {
		if seen[id] {
			t.Fatal("resource repeated across pages")
		}
		seen[id] = true
	}
	if len(seen) != 11 {
		t.Fatal("snapshot lost matching resources")
	}
	second := f.search(Row{"search_token": token, "page": 2})
	beforeSecond := recommendationResultIDs(second)
	hidden := recommendationResultIDs(first)[0]
	_, _ = f.a.DB.Exec("UPDATE ts_resources SET status='hidden' WHERE id=?", hidden)
	afterFirst := f.search(Row{"search_token": token})
	afterSecond := f.search(Row{"search_token": token, "page": 2})
	if len(recommendationResultIDs(afterFirst)) != 2 || !reflect.DeepEqual(beforeSecond, recommendationResultIDs(afterSecond)) {
		t.Fatal("moderation shifted fixed page slots or exposed a hidden resource")
	}
	f.resource("channel")
	old := f.search(Row{"search_token": token})
	if integer(old["total"]) != 11 {
		t.Fatal("snapshot total changed after catalog edits")
	}
	newSearch := f.search(Row{})
	if str(newSearch["search_token"]) == token || integer(newSearch["total"]) != 11 {
		t.Fatal("new search did not rebuild from currently published resources")
	}
	if _, err := f.a.Search(Row{"q": "changed", "page_size": 3, "search_token": token}, "web"); err == nil {
		t.Fatal("token accepted for a different query")
	}
	if _, err := f.a.DB.Exec("UPDATE ts_search_snapshots SET expires_at=? WHERE token_hash=?", cDate(time.Now().Add(-time.Minute)), hash(token)); err != nil {
		t.Fatal(err)
	}
	_, err := f.a.Search(Row{"q": f.tag, "page_size": 3, "search_token": token}, "web")
	var de *DomainError
	if !errors.As(err, &de) || de.Status != 410 {
		t.Fatal("expired snapshot was rebuilt silently", err)
	}
}

func TestRecommendationMySQLWindowReportsActualTotal(t *testing.T) {
	f := newRecommendationFixture(t)
	for i := 0; i < 105; i++ {
		f.resource("bot")
	}
	result := f.search(Row{"page_size": 1})
	if integer(result["total"]) != 105 || integer(result["window_total"]) != 100 || integer(result["total_pages"]) != 100 {
		t.Fatal("bounded window disguised the full matching count")
	}
	last := f.search(Row{"page_size": 1, "page": 100, "search_token": result["search_token"]})
	if len(rowsOf(last["items"])) != 1 || boolean(last["has_more"]) {
		t.Fatal("last window page incorrect")
	}
}

func TestRetiredRecommendationMySQLHTTP(t *testing.T) {
	f := newRecommendationFixture(t)
	user, resource := f.user(), f.resource("bot")
	token, csrf := randomHex(32), randomHex(24)
	_, err := f.a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
		return Row{}, insertSession(tx, "user", user, token, csrf)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.a.RegisterCatalog()
	request := func(cookieName, cookie, csrfValue string, body Row) (int, Row) {
		encoded, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "http://starindex.test/user/api/resources/"+str(resource)+"/like", bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://starindex.test")
		req.Header.Set("X-CSRF-Token", csrfValue)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: cookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		f.a.Handler().ServeHTTP(w, req)
		data := decode(w.Body.String())
		return w.Code, object(data["data"])
	}
	for _, tc := range []struct {
		name, cookie, csrf string
		want               int
	}{
		{"starindex_user", "", "", 401},
		{"starindex_admin", token, csrf, 401},
		{"starindex_user", token, "", 403},
		{"starindex_user", token, csrf, 410},
		{"starindex_user", token, csrf, 410},
	} {
		status, _ := request(tc.name, tc.cookie, tc.csrf, Row{})
		if status != tc.want {
			t.Fatalf("HTTP got %d want %d", status, tc.want)
		}
	}
	if status, _ := request("starindex_user", token, csrf, Row{"like_count": 500}); status != 410 {
		t.Fatal("old client could still submit a recommendation")
	}
	for _, path := range []string{"/user/api/resource-likes?ids=" + str(resource), "/api/public/resources/" + str(resource)} {
		req := httptest.NewRequest("GET", path, nil)
		req.AddCookie(&http.Cookie{Name: "starindex_user", Value: token})
		w := httptest.NewRecorder()
		f.a.Handler().ServeHTTP(w, req)
		if strings.HasPrefix(path, "/user/") {
			if w.Code != 410 {
				t.Fatal("legacy like-states endpoint is still active")
			}
		} else if w.Code != 200 || object(decode(w.Body.String())["data"])["like_count"] != nil {
			t.Fatal("public resource detail exposes the retired count")
		}
	}
	record, err := one(f.a.DB, "SELECT like_count,(SELECT COUNT(*) FROM ts_resource_likes WHERE resource_id=?) AS votes FROM ts_resources WHERE id=?", resource, resource)
	if err != nil || integer(record["like_count"]) != 0 || integer(record["votes"]) != 0 {
		t.Fatal("retired HTTP endpoints changed votes", err)
	}
	_, _ = f.a.DB.Exec("UPDATE ts_users SET status=0 WHERE id=?", user)
	if status, _ := request("starindex_user", token, csrf, Row{}); status != 401 && status != 403 {
		t.Fatal("disabled account was authorized")
	}
}

func TestRetiredRecommendationMySQLBotButtons(t *testing.T) {
	f := newRecommendationFixture(t)
	user := f.user()
	tg := int64(9_500_000_000_000) + int64(securityRandom(1_000_000_000))
	_, err := f.a.DB.Exec("UPDATE ts_users SET telegram_id=? WHERE id=?", tg, user)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		f.resource("group")
	}
	result := f.search(Row{})
	ids := recommendationResultIDs(result)
	session, message := randomHex(12), int64(909)
	f.sessions = append(f.sessions, session)
	credentials, err := f.a.botCredentials()
	if err != nil {
		t.Fatal(err)
	}
	botID := integer(object(credentials["bot"])["id"])
	policy := f.a.securityPolicy(botID, -tg)
	_, err = f.a.DB.Exec("INSERT INTO ts_bot_verifications(bot_id,chat_id,user_id,policy_hash,state,answer_hashes,payload,reply_context,verified_until,expires_at) VALUES(?,?,?,?,'verified','[]','{}','{}',?,?)", botID, -tg, tg, policy["hash"], time.Now().Unix()+3600, future(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.a.DB.Exec("DELETE FROM ts_bot_verifications WHERE bot_id=? AND chat_id=? AND user_id=?", botID, -tg, tg)
		_, _ = f.a.DB.Exec("DELETE FROM ts_rate_limits WHERE bucket=?", "bot-callback:"+str(tg))
	})
	_, err = f.a.DB.Exec("INSERT INTO ts_bot_sessions(id,telegram_user_id,chat_id,message_id,query,type,page,revision,page_size,filters_expanded,search_token,expires_at) VALUES(?,?,?,?,?,'',1,1,3,0,?,?)", session, tg, -tg, message, f.tag, result["search_token"], future(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	calls := []Row{}
	f.a.TelegramCall = func(_ context.Context, method string, params Row) (Row, error) {
		calls = append(calls, Row{"method": method, "params": params})
		return Row{"message_id": message}, nil
	}
	cb := Row{"id": "recommendation-fixture", "data": "s:" + session + ":1:l" + str(ids[0]), "from": Row{"id": tg + 1}, "message": Row{"chat": Row{"id": -tg, "type": "supergroup"}, "message_id": message}}
	if err = f.a.botCallback(context.Background(), cb, botID); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || !boolean(object(calls[0]["params"])["show_alert"]) || !strings.Contains(str(object(calls[0]["params"])["text"]), "不能操作别人的") {
		t.Fatal("other member voted through the owner's result")
	}
	cb["from"] = Row{"id": tg, "first_name": "Recommendation fixture"}
	for _, wrongMessage := range []Row{
		{"chat": Row{"id": -tg - 1, "type": "supergroup"}, "message_id": message},
		{"chat": Row{"id": -tg, "type": "supergroup"}, "message_id": message + 1},
	} {
		calls = nil
		forged := catalogMerge(cb, Row{"message": wrongMessage})
		if err = f.a.botCallback(context.Background(), forged, botID); err != nil || len(calls) != 1 || !strings.Contains(str(object(calls[0]["params"])["text"]), "不属于当前消息") {
			t.Fatal("recommendation callback accepted another chat/message", err)
		}
	}
	otherPage := f.search(Row{"search_token": result["search_token"], "page": 2})
	forged := catalogMerge(cb, Row{"data": "s:" + session + ":1:l" + str(recommendationResultIDs(otherPage)[0])})
	if err = f.a.botCallback(context.Background(), forged, botID); err != nil {
		t.Fatal("retired callback should terminate without retrying", err)
	}
	calls = nil
	if err = f.a.botCallback(context.Background(), cb, botID); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || str(calls[0]["method"]) != "answerCallbackQuery" || !strings.Contains(str(object(calls[0]["params"])["text"]), "推荐功能已取消") {
		t.Fatal("old recommendation button did not explain retirement")
	}
	calls = nil
	if err = f.a.botCallback(context.Background(), cb, botID); err != nil || len(calls) != 1 || !strings.Contains(str(object(calls[0]["params"])["text"]), "推荐功能已取消") {
		t.Fatal("repeated retired button did not terminate", err)
	}
	record, err := one(f.a.DB, "SELECT like_count,(SELECT COUNT(*) FROM ts_resource_likes WHERE resource_id=?) AS votes FROM ts_resources WHERE id=?", ids[0], ids[0])
	if err != nil || integer(record["like_count"]) != 0 || integer(record["votes"]) != 0 {
		t.Fatal("old Telegram buttons changed votes", err)
	}
	_, _ = f.a.DB.Exec("UPDATE ts_bot_sessions SET revision=2 WHERE id=?", session)
	calls = nil
	if err = f.a.botCallback(context.Background(), cb, botID); err != nil || len(calls) != 1 || !strings.Contains(str(object(calls[0]["params"])["text"]), "已经更新") {
		t.Fatal("stale recommendation button was accepted", err)
	}
	cb["data"] = "s:" + session + ":2:l" + str(f.resource("group"))
	if err = f.a.botCallback(context.Background(), cb, botID); err != nil {
		t.Fatal("retired forged button should terminate", err)
	}
	cb["data"] = "s:" + session + ":2:l" + str(ids[1])
	_, _ = f.a.DB.Exec("UPDATE ts_resources SET status='hidden' WHERE id=?", ids[1])
	if err = f.a.botCallback(context.Background(), cb, botID); err != nil {
		t.Fatal("retired hidden-resource button should terminate", err)
	}
	_, _ = f.a.DB.Exec("UPDATE ts_bot_sessions SET expires_at=? WHERE id=?", cDate(time.Now().Add(-time.Minute)), session)
	calls = nil
	if err = f.a.botCallback(context.Background(), cb, botID); err != nil || len(calls) != 1 || !strings.Contains(str(object(calls[0]["params"])["text"]), "搜索已过期") {
		t.Fatal("expired search allowed a recommendation", err)
	}
}

func TestRecommendationMySQLPerformanceBaseline(t *testing.T) {
	if os.Getenv("STARINDEX_RECOMMENDATION_BENCH") != "1" {
		t.Skip("set STARINDEX_RECOMMENDATION_BENCH=1 for local catalog baseline")
	}
	f := newRecommendationFixture(t)
	for _, keyword := range []string{"", "服务器"} {
		var elapsed time.Duration
		var result Row
		for i := 0; i < 3; i++ {
			start := time.Now()
			var err error
			// bot-page suppresses search analytics; only isolated temporary
			// snapshot records are created by this local performance probe.
			result, err = f.a.Search(Row{"q": keyword, "page_size": 50}, "bot-page")
			elapsed += time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			f.tokens[str(result["search_token"])] = true
		}
		start := time.Now()
		next, err := f.a.Search(Row{"q": keyword, "page_size": 50, "page": 2, "search_token": result["search_token"]}, "bot-page")
		pageElapsed := time.Since(start)
		if err != nil || str(next["search_token"]) != str(result["search_token"]) {
			t.Fatal("baseline snapshot pagination failed", err)
		}
		t.Logf("keyword=%q total=%d window=%d new_search_avg=%s reuse_page=%s", keyword, integer(result["total"]), integer(result["window_total"]), elapsed/3, pageElapsed)
	}
}
