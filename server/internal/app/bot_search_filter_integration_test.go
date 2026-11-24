package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestBotSearchFiltersMySQLCatalogScopesAndSnapshots(t *testing.T) {
	f := newRecommendationFixture(t)
	communities, posts := map[int64]bool{}, map[int64]bool{}
	add := func(kind string, post bool) int64 {
		t.Helper()
		id := f.resource(kind)
		if post {
			link := "https://t.me/" + f.tag + "/" + str(id)
			if _, err := f.a.DB.Exec("UPDATE ts_resources SET url=?,url_hash=? WHERE id=?", link, hash(link), id); err != nil {
				t.Fatal(err)
			}
			posts[id] = true
		} else if kind == "group" || kind == "channel" {
			communities[id] = true
		}
		return id
	}
	for _, kind := range []string{"group", "channel", "group", "channel", "bot", "article", "video", "audio", "file", "shop", "other"} {
		add(kind, false)
	}
	for _, kind := range []string{"article", "video", "audio", "file", "channel", "group", "other"} {
		add(kind, true)
	}
	// A regular website path that happens to end in a number is not a Telegram post.
	website := add("website", false)
	link := "https://example.com/" + f.tag + "/123"
	if _, err := f.a.DB.Exec("UPDATE ts_resources SET url=?,url_hash=? WHERE id=?", link, hash(link), website); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind string
		want map[int64]bool
	}{{botSearchCommunities, communities}, {botSearchPosts, posts}} {
		t.Run(tc.kind, func(t *testing.T) {
			first := f.search(Row{"type": tc.kind})
			if integer(first["total"]) != int64(len(tc.want)) {
				t.Fatalf("scope %s total = %v; want %d", tc.kind, first["total"], len(tc.want))
			}
			seen := map[int64]bool{}
			token := str(first["search_token"])
			for page := int64(1); page <= integer(first["total_pages"]); page++ {
				result := f.search(Row{"type": tc.kind, "page": page, "search_token": token})
				if str(result["search_token"]) != token {
					t.Fatal("pagination replaced the search snapshot")
				}
				for _, item := range rowsOf(result["items"]) {
					id := integer(item["id"])
					if !tc.want[id] || seen[id] {
						t.Fatalf("scope %s returned an unrelated or repeated resource %d", tc.kind, id)
					}
					seen[id] = true
				}
			}
			if len(seen) != len(tc.want) {
				t.Fatal("pagination omitted scoped resources")
			}
			for _, other := range []string{"", botSearchCommunities, botSearchPosts} {
				if other == tc.kind {
					continue
				}
				if _, err := f.a.Search(Row{"q": f.tag, "type": other, "page_size": 3, "search_token": token}, "bot-page"); err == nil {
					t.Fatalf("%s snapshot was reused for %q", tc.kind, other)
				}
			}
		})
	}
	for _, source := range []string{"web", "api"} {
		result, err := f.a.Search(Row{"q": f.tag, "page_size": 50}, source)
		if err != nil {
			t.Fatal(err)
		}
		f.tokens[str(result["search_token"])] = true
		if integer(result["total"]) != int64(len(f.resources)) {
			t.Fatalf("%s default search no longer includes every resource type", source)
		}
		if _, err = f.a.Search(Row{"q": f.tag, "type": botSearchCommunities, "page_size": 50, "search_token": result["search_token"]}, "bot-page"); err == nil {
			t.Fatal("legacy all-types snapshot was accepted as a community snapshot")
		}
	}
}

func seedBotSearchFilterResources(t *testing.T, f *botGroupFixture) (communities, posts map[int64]bool) {
	t.Helper()
	communities, posts = map[int64]bool{}, map[int64]bool{}
	for i := int64(0); i <= f.a.botPageSize(); i++ {
		for _, isPost := range []bool{false, true} {
			kind := []string{"group", "channel"}[i%2]
			link := "https://t.me/go_group_" + randomHex(8)
			if isPost {
				kind = []string{"article", "video", "audio", "file", "channel"}[i%5]
				link += "/" + str(i+1)
			}
			id, err := catalogInsert(f.a.DB, "resources", Row{"title": f.keyword + " " + str(i), "description": "", "url": link, "url_hash": hash(link), "type": kind, "status": "published", "created_at": now(), "updated_at": now()})
			if err != nil {
				t.Fatal(err)
			}
			f.resourceIDs = append(f.resourceIDs, id)
			if isPost {
				posts[id] = true
			} else {
				communities[id] = true
			}
		}
	}
	return communities, posts
}

func assertBotSearchFilterSnapshot(t *testing.T, f *botGroupFixture, session Row, kind string, want map[int64]bool) []Row {
	t.Helper()
	if str(session["type"]) != kind {
		t.Fatalf("session filter = %q; want %q", str(session["type"]), kind)
	}
	size := integer(session["page_size"])
	snapshot, err := f.a.readSearchSnapshot(str(session["search_token"]), searchQueryHash(Row{"q": f.keyword, "type": kind}, size))
	if err != nil {
		t.Fatal(err)
	}
	if integer(snapshot["total"]) != int64(len(want)) {
		t.Fatalf("session total = %v; want %d", snapshot["total"], len(want))
	}
	ids, err := snapshotIDs(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if !want[id] {
			t.Fatalf("session scope %q includes unrelated resource %d", kind, id)
		}
	}
	items, err := f.a.searchSnapshotPage(snapshot, integer(session["page"]), size)
	if err != nil || len(items) == 0 {
		t.Fatal("expected a nonempty scoped page", err)
	}
	return items
}

func TestBotSearchFiltersMySQLPrivateAndGroupFlow(t *testing.T) {
	for _, chatType := range []string{"private", "supergroup"} {
		t.Run(chatType, func(t *testing.T) {
			f := newBotGroupFixture(t, chatType, 0)
			if chatType == "private" {
				f.chat["id"] = f.from["id"]
			} else {
				f.join()
			}
			communities, posts := seedBotSearchFilterResources(t, f)
			f.trustIfRequired()
			tokens := map[string]bool{}
			t.Cleanup(func() {
				for token := range tokens {
					if _, err := f.a.DB.Exec("DELETE FROM ts_search_snapshots WHERE token_hash=?", hash(token)); err != nil {
						t.Error(err)
					}
				}
				_, _ = f.a.DB.Exec("DELETE FROM ts_resource_likes WHERE user_id=?", f.userID)
				_, _ = f.a.DB.Exec("DELETE FROM ts_rate_limits WHERE bucket=?", "resource-like:"+str(f.userID))
			})
			f.update(Row{"message": f.message()})
			session := f.session()
			tokens[str(session["search_token"])] = true
			assertBotSearchFilterSnapshot(t, f, session, botSearchCommunities, communities)
			payload := object(f.calls[len(f.calls)-1]["params"])
			keyboard := array(object(payload["reply_markup"])["inline_keyboard"])
			if len(keyboard) == 0 {
				t.Fatal("search reply has no filter buttons")
			}
			firstRow := array(keyboard[0])
			if len(firstRow) != 5 || !strings.Contains(str(payload["text"]), "\n群组与频道\n") {
				t.Fatal("default search must show the combined scope and five first-row buttons")
			}
			foundPost, foundCommunities := false, false
			labels := []string{"群频道", "群组", "频道", "帖子", "更多筛选"}
			for i, button := range firstRow {
				b := object(button)
				foundPost = foundPost || strings.HasSuffix(str(b["callback_data"]), ":t"+botSearchPosts)
				foundCommunities = foundCommunities || strings.HasSuffix(str(b["callback_data"]), ":t"+botSearchCommunities)
				if str(b["text"]) != labels[i] || str(b["icon_custom_emoji_id"]) == "" || (str(b["style"]) == "primary") != (i == 0) {
					t.Fatal("first-row label, selected state or Telegram icon is missing")
				}
			}
			if !foundPost || !foundCommunities {
				t.Fatal("posts and combined communities must be available in the first filter row")
			}
			callback := func(action string) Row {
				t.Helper()
				current := f.session()
				cb := Row{"id": "search-filter-fixture", "data": fmt.Sprintf("s:%s:%d:%s", str(current["id"]), integer(current["revision"]), action), "from": f.from, "message": Row{"chat": f.chat, "message_id": current["message_id"]}}
				if err := f.a.botCallback(context.Background(), cb, integer(f.bot["id"])); err != nil {
					t.Fatal(err)
				}
				next := f.session()
				tokens[str(next["search_token"])] = true
				return next
			}
			if strings.Contains(str(payload["text"]), "推荐") || len(keyboard) != 2 {
				t.Fatal("search results still display recommendation counts or buttons")
			}
			next := callback("p2")
			assertBotSearchFilterSnapshot(t, f, next, botSearchCommunities, communities)
			if integer(next["page"]) != 2 || str(next["search_token"]) != str(session["search_token"]) {
				t.Fatal("community pagination changed the filter or snapshot")
			}
			next = callback("t" + botSearchPosts)
			assertBotSearchFilterSnapshot(t, f, next, botSearchPosts, posts)
			if integer(next["page"]) != 1 || str(next["search_token"]) == str(session["search_token"]) {
				t.Fatal("switching to posts did not reset the page and snapshot")
			}
			postToken := str(next["search_token"])
			next = callback("p2")
			assertBotSearchFilterSnapshot(t, f, next, botSearchPosts, posts)
			if integer(next["page"]) != 2 || str(next["search_token"]) != postToken {
				t.Fatal("post pagination changed the filter or snapshot")
			}
			next = callback("f1")
			if integer(next["page"]) != 2 || str(next["search_token"]) != postToken || str(next["type"]) != botSearchPosts {
				t.Fatal("expanding filters discarded the selected scope or page")
			}
			expanded := object(f.calls[len(f.calls)-1]["params"])
			filters := map[string]bool{}
			for _, row := range array(object(expanded["reply_markup"])["inline_keyboard"]) {
				for _, button := range array(row) {
					parts := strings.Split(str(object(button)["callback_data"]), ":")
					if len(parts) == 4 && strings.HasPrefix(parts[3], "t") {
						filters[strings.TrimPrefix(parts[3], "t")] = true
					}
				}
			}
			for kind := range ResourceTypes {
				if !filters[kind] {
					t.Fatalf("expanded filters lost resource type %q", kind)
				}
			}
			if !filters[""] || !filters[botSearchCommunities] || !filters[botSearchPosts] {
				t.Fatal("expanded filters lost all-types or combined scopes")
			}
			next = callback("t" + botSearchCommunities)
			assertBotSearchFilterSnapshot(t, f, next, botSearchCommunities, communities)
			if integer(next["page"]) != 1 || str(next["search_token"]) == postToken {
				t.Fatal("returning to communities did not reset the page and snapshot")
			}
			// The existing empty type remains a deliberate all-types selection.
			next = callback("t")
			all := map[int64]bool{}
			for _, id := range f.resourceIDs {
				all[id] = true
			}
			assertBotSearchFilterSnapshot(t, f, next, "", all)
			allToken := str(next["search_token"])
			next = callback("p2")
			assertBotSearchFilterSnapshot(t, f, next, "", all)
			if str(next["search_token"]) != allToken {
				t.Fatal("legacy empty-type session lost its all-types snapshot")
			}
		})
	}
}

func TestBotSearchFiltersMySQLCaptchaResumesCommunityDefault(t *testing.T) {
	f := newBotGroupFixture(t, "supergroup", 77)
	f.join()
	communities, _ := seedBotSearchFilterResources(t, f)
	if _, err := f.a.DB.Exec("UPDATE ts_bot_groups SET verify_mode='button' WHERE bot_id=? AND chat_id=?", f.bot["id"], f.chat["id"]); err != nil {
		t.Fatal(err)
	}
	if str(f.a.securityPolicy(integer(f.bot["id"]), integer(f.chat["id"]))["mode"]) != "button" {
		t.Skip("global policy disallows per-group button verification; real settings are preserved")
	}
	f.update(Row{"message": f.message()})
	if count, err := cCount(f.a.DB, "SELECT COUNT(*) FROM ts_bot_sessions WHERE chat_id=?", f.chat["id"]); err != nil || count != 0 {
		t.Fatal("search ran before verification", err)
	}
	challenge, err := one(f.a.DB, "SELECT * FROM ts_bot_verifications WHERE bot_id=? AND chat_id=? AND user_id=?", f.bot["id"], f.chat["id"], f.from["id"])
	if err != nil || challenge == nil {
		t.Fatal("verification challenge missing", err)
	}
	key := ""
	for choice := range object(decode(str(challenge["payload"]))["choices"]) {
		key = choice
	}
	if len(key) != 12 {
		t.Fatal("button verification callback missing")
	}
	cb := Row{"id": "filter-captcha-fixture", "data": "v:" + str(challenge["challenge_id"]) + ":" + key, "from": f.from, "message": Row{"chat": f.chat, "message_id": challenge["message_id"], "message_thread_id": f.thread}}
	f.calls = nil
	f.update(Row{"callback_query": cb})
	assertBotSearchFilterSnapshot(t, f, f.session(), botSearchCommunities, communities)
	f.assertSearchReply(object(f.calls[len(f.calls)-1]["params"]))
}
