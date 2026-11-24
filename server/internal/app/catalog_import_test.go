package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReadImportRows(t *testing.T) {
	dir := t.TempDir()
	for _, test := range []struct {
		name, contents string
		valid          bool
		length         int
	}{
		{"rows.json", `{"rows":[{"title":"开发","url":"https://t.me/telegram","members":123}]}`, true, 1},
		{"array.json", `[{"title":"开发","url":"https://t.me/telegram"}]`, true, 1},
		{"empty.json", `{"rows":[]}`, true, 0},
		{"bad.json", `{"secrets":[]}`, false, 0},
		{"double.json", `[] []`, false, 0},
		{"rows.csv", "title,url,members\n开发,https://t.me/telegram,123\n", true, 1},
		{"bad.csv", "name,link\nbad,https://t.me/telegram\n", false, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(dir, test.name)
			if err := os.WriteFile(path, []byte(test.contents), 0600); err != nil {
				t.Fatal(err)
			}
			rows, err := readImportRows(path)
			if test.valid {
				if err != nil || len(rows) != test.length {
					t.Fatalf("rows %d err %v", len(rows), err)
				}
			} else if err == nil {
				t.Fatal("invalid import accepted")
			}
		})
	}
}

func TestCatalogImportMySQL(t *testing.T) {
	if os.Getenv("STARINDEX_TEST_MYSQL") != "1" {
		t.Skip("set STARINDEX_TEST_MYSQL=1")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer a.DB.Close()
	a.Root = t.TempDir()
	tag := "goimp_" + randomHex(5)
	link := "https://t.me/" + tag
	defer func() {
		_, _ = a.DB.Exec("DELETE FROM ts_resources WHERE source=? AND url_hash=?", tag, hash(link))
		_, _ = a.DB.Exec("DELETE FROM ts_link_locks WHERE url_hash=?", hash(link))
		_, _ = a.DB.Exec("DELETE FROM ts_audit_logs WHERE action='resource.import' AND actor_id IS NULL AND detail LIKE ?", `%"source":"`+tag+`"%`)
	}()
	input := Row{"rows": []Row{
		{"title": "导入测试", "url": link, "type": "channel", "source_file": "source.xlsx", "source_sheet": "频道", "source_row": 2},
		{"title": "重复测试", "url": "http://telegram.me/" + tag + "?start=xyz", "type": "channel"},
		{"title": "邀请测试", "url": "https://t.me/+private", "type": "group"},
		{"title": "无效测试", "url": "https://example.com", "type": "channel"},
	}}
	payload, _ := json.Marshal(input)
	path := filepath.Join(a.Root, "fixture.json")
	if err = os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := a.ImportResources(context.Background(), path, tag, false)
	if err != nil {
		t.Fatal(err)
	}
	counts := result["counts"].(Row)
	if integer(counts["planned"]) != 1 || integer(counts["duplicate_input"]) != 1 || integer(counts["invalid"]) != 1 || integer(counts["unsupported_invite"]) != 1 {
		t.Fatalf("dry run counts %v", counts)
	}
	row, err := one(a.DB, "SELECT id FROM ts_resources WHERE url_hash=?", hash(link))
	if err != nil || row != nil {
		t.Fatal("dry-run changed database")
	}
	result, err = a.ImportResources(context.Background(), path, tag, true)
	if err != nil {
		t.Fatal(err)
	}
	if integer(result["counts"].(Row)["inserted"]) != 1 {
		t.Fatal("import did not insert exactly one row")
	}
	data, err := os.ReadFile(str(result["report"]))
	if err != nil {
		t.Fatal(err)
	}
	report := decode(string(data))
	rows := array(report["rows"])
	if str(object(rows[0])["source_sheet"]) != "频道" || integer(object(rows[0])["source_row"]) != 2 {
		t.Fatal("source provenance lost")
	}
	_, err = a.DB.Exec("UPDATE ts_resources SET status='blocked',status_reason='test block' WHERE source=? AND url_hash=?", tag, hash(link))
	if err != nil {
		t.Fatal(err)
	}
	result, err = a.ImportResources(context.Background(), path, tag, true)
	if err != nil {
		t.Fatal(err)
	}
	counts = result["counts"].(Row)
	if integer(counts["inserted"]) != 0 || integer(counts["duplicate_existing"]) != 1 {
		t.Fatalf("repeat import counts %v", counts)
	}
	row, err = one(a.DB, "SELECT status,status_reason FROM ts_resources WHERE url_hash=?", hash(link))
	if err != nil || str(row["status"]) != "blocked" || str(row["status_reason"]) != "test block" {
		t.Fatal("import overwrote moderation")
	}
}
