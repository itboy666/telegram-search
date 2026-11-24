package app

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var importSourcePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,30}$`)
var importInvitePattern = regexp.MustCompile(`(?i)^(?:https?://)?(?:www\.)?(?:t\.me|telegram\.me)/(?:\+|joinchat/)`)

func readImportRows(path string) ([]Row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("无法打开导入文件。")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > 256<<20 {
		return nil, errors.New("单个导入文件最多 256 MB，请分批导入。")
	}
	rows := []Row{}
	if strings.ToLower(filepath.Ext(path)) == ".csv" {
		reader := csv.NewReader(f)
		headers, err := reader.Read()
		if err != nil {
			return nil, errors.New("CSV 缺少标题行。")
		}
		for i, h := range headers {
			headers[i] = strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))
		}
		hasURL, hasTitle := false, false
		for _, h := range headers {
			hasURL = hasURL || h == "url"
			hasTitle = hasTitle || h == "title"
		}
		if !hasURL || !hasTitle {
			return nil, errors.New("CSV 必须包含 title 和 url 列。")
		}
		for index := 2; ; index++ {
			values, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("CSV 第 %d 行格式无效。", index)
			}
			row := Row{"source_file": filepath.Base(path), "source_row": index}
			for i, key := range headers {
				v := values[i]
				if key == "members" || key == "category_id" || key == "featured" {
					if strings.TrimSpace(v) != "" {
						row[key] = json.Number(v)
					}
				} else {
					row[key] = v
				}
			}
			rows = append(rows, row)
			if len(rows) > 100000 {
				return nil, errors.New("单批最多 100000 行，请拆分文件。")
			}
		}
		return rows, nil
	}
	reader := json.NewDecoder(f)
	reader.UseNumber()
	var document any
	if err = reader.Decode(&document); err != nil {
		return nil, errors.New("请输入 UTF-8 JSON 数组或包含 rows 数组的对象。")
	}
	var extra any
	if err = reader.Decode(&extra); err != io.EOF {
		return nil, errors.New("导入文件必须只包含一个 JSON 文档。")
	}
	var raw []any
	switch value := document.(type) {
	case []any:
		raw = value
	case map[string]any:
		raw, _ = value["rows"].([]any)
	}
	if raw == nil {
		return nil, errors.New("导入文件缺少 rows 数组。")
	}
	if len(raw) > 100000 {
		return nil, errors.New("单批最多 100000 行，请拆分文件。")
	}
	for _, value := range raw {
		row, ok := value.(map[string]any)
		if !ok {
			row = Row{"_invalid_row": true}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// ImportResources never overwrites an existing URL, including hidden or rejected
// records. The default dry-run checks the same identities but performs no writes.
func (a *App) ImportResources(ctx context.Context, path, source string, apply bool) (Row, error) {
	if !importSourcePattern.MatchString(source) {
		return nil, errors.New("来源标识须为 1–30 位字母、数字、下划线或连字符。")
	}
	rows, err := readImportRows(path)
	if err != nil {
		return nil, err
	}
	mode := "dry_run"
	if apply {
		mode = "apply"
	}
	report := Row{"mode": mode, "source": source, "created_at": now(), "input_file": filepath.Base(path)}
	counts := Row{"total": len(rows), "inserted": int64(0), "planned": int64(0), "duplicate_input": int64(0), "duplicate_existing": int64(0), "duplicate_submission": int64(0), "unsupported_invite": int64(0), "invalid": int64(0)}
	entries := make([]Row, len(rows))
	prepared := []Row{}
	indexes := []int{}
	seen := map[string]int{}
	for i, row := range rows {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		entry := Row{"input_row": i + 1}
		for _, k := range []string{"source_file", "source_sheet", "source_row"} {
			if v, ok := row[k]; ok {
				switch v.(type) {
				case string, json.Number, float64, int64, int:
					entry[k] = v
				}
			}
		}
		entries[i] = entry
		if boolean(row["_invalid_row"]) {
			entry["status"] = "invalid"
			entry["reason"] = "记录必须是对象。"
			continue
		}
		if importInvitePattern.MatchString(strings.TrimSpace(str(row["url"]))) {
			entry["status"] = "unsupported_invite"
			entry["reason"] = "当前资源库仅收录公开链接。"
			continue
		}
		if row["type"] == nil {
			row["type"] = "group"
		}
		row["featured"] = 0
		data, e := catalogValidateResource(a.DB, row)
		if e != nil {
			entry["status"] = "invalid"
			entry["reason"] = e.Error()
			continue
		}
		h := str(data["url_hash"])
		entry["url"] = data["url"]
		if first, ok := seen[h]; ok {
			entry["status"] = "duplicate_input"
			entry["first_input_row"] = first + 1
			continue
		}
		seen[h] = i
		prepared = append(prepared, data)
		indexes = append(indexes, i)
	}
	// Each batch owns canonical locks in a stable order, then reads both tables
	// under those locks. A simultaneous user submission can never be overwritten.
	for offset := 0; offset < len(prepared); offset += 500 {
		if err = ctx.Err(); err != nil {
			report["error"] = "导入已取消，已提交批次仍保留。"
			break
		}
		end := offset + 500
		if end > len(prepared) {
			end = len(prepared)
		}
		batch := prepared[offset:end]
		var result Row
		if apply {
			result, err = a.catalogTransaction(func(tx *sql.Tx) (Row, error) { return importBatch(tx, tx, batch, source, true) })
		} else {
			result, err = importBatch(a.DB, nil, batch, source, false)
		}
		if err != nil {
			report["error"] = "批次失败，之前完成的批次已保留，可使用相同文件重新运行。"
			break
		}
		statuses := result["rows"].([]Row)
		for j, status := range statuses {
			for k, v := range status {
				entries[indexes[offset+j]][k] = v
			}
		}
	}
	for _, entry := range entries {
		status := str(entry["status"])
		if status == "" {
			status = "not_processed"
			entry["status"] = status
		}
		counts[status] = integer(counts[status]) + 1
	}
	report["counts"] = counts
	report["rows"] = entries
	report["completed_at"] = now()
	dir := filepath.Join(a.Root, "runtime", "import-reports", "go-"+time.Now().In(location).Format("20060102-150405")+"-"+randomHex(3))
	if e := os.MkdirAll(dir, 0700); e != nil {
		return Row{"counts": counts}, fmt.Errorf("导入结果已产生，但无法保存报告：%w", e)
	}
	reportPath := filepath.Join(dir, "report.json")
	payload, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		return nil, e
	}
	if e = os.WriteFile(reportPath, payload, 0600); e != nil {
		return Row{"counts": counts}, fmt.Errorf("无法写入导入报告：%w", e)
	}
	summary := Row{"mode": mode, "source": source, "counts": counts, "report": reportPath}
	if apply && integer(counts["inserted"]) > 0 {
		_ = a.Audit(0, "resource.import", encode(Row{"source": source, "counts": counts}))
	}
	return summary, err
}

func importBatch(db Queryer, writer catalogExecutor, batch []Row, source string, apply bool) (Row, error) {
	hashes := []string{}
	args := []any{}
	marks := []string{}
	for _, data := range batch {
		h := str(data["url_hash"])
		hashes = append(hashes, h)
		args = append(args, h)
		marks = append(marks, "?")
	}
	if apply {
		if err := catalogLock(writer.(*sql.Tx), hashes...); err != nil {
			return nil, err
		}
	}
	lock := ""
	if apply {
		lock = " FOR UPDATE"
	}
	existing, err := query(db, "SELECT id,url_hash,status,status_reason FROM ts_resources WHERE url_hash IN ("+strings.Join(marks, ",")+")"+lock, args...)
	if err != nil {
		return nil, err
	}
	pending, err := query(db, "SELECT id,url_hash,status,review_note FROM ts_submissions WHERE url_hash IN ("+strings.Join(marks, ",")+")"+lock, args...)
	if err != nil {
		return nil, err
	}
	resources, submissions := map[string]Row{}, map[string]Row{}
	for _, r := range existing {
		resources[str(r["url_hash"])] = r
	}
	for _, r := range pending {
		submissions[str(r["url_hash"])] = r
	}
	statuses := []Row{}
	inserts := []Row{}
	for _, data := range batch {
		h := str(data["url_hash"])
		entry := Row{}
		if row := resources[h]; row != nil {
			entry = Row{"status": "duplicate_existing", "resource_id": row["id"], "resource_status": row["status"], "reason": catalogDuplicateMessage(row, false)}
		} else if row := submissions[h]; row != nil {
			entry = Row{"status": "duplicate_submission", "submission_id": row["id"], "reason": catalogDuplicateMessage(row, true)}
		} else if !apply {
			entry["status"] = "planned"
		} else {
			if category := integer(data["category_id"]); category > 0 {
				if _, err := catalogRecord(db, "categories", category, true); err != nil {
					return nil, err
				}
			}
			copy := catalogMerge(data, Row{"source": source, "owner_id": nil, "created_at": now()})
			inserts = append(inserts, copy)
			entry["status"] = "inserted"
		}
		statuses = append(statuses, entry)
	}
	if len(inserts) > 0 {
		keys := []string{"title", "description", "url", "url_hash", "type", "category_id", "status", "status_reason", "members", "tags", "featured", "source", "owner_id", "created_at", "updated_at"}
		values := []any{}
		groups := []string{}
		for _, row := range inserts {
			items := []string{}
			for _, key := range keys {
				values = append(values, row[key])
				items = append(items, "?")
			}
			groups = append(groups, "("+strings.Join(items, ",")+")")
		}
		if _, err = writer.Exec("INSERT INTO ts_resources (`"+strings.Join(keys, "`,`")+"`) VALUES "+strings.Join(groups, ","), values...); err != nil {
			return nil, err
		}
		saved, err := query(db, "SELECT id,url_hash FROM ts_resources WHERE url_hash IN ("+strings.Join(marks, ",")+")", args...)
		if err != nil {
			return nil, err
		}
		ids := map[string]any{}
		for _, row := range saved {
			ids[str(row["url_hash"])] = row["id"]
		}
		for i, entry := range statuses {
			if entry["status"] == "inserted" {
				entry["resource_id"] = ids[str(batch[i]["url_hash"])]
			}
		}
	}
	return Row{"rows": statuses}, nil
}

func (a *App) Maintain() (Row, error) {
	result := Row{}
	jobs := []struct {
		table, condition string
		args             []any
	}{
		{"sessions", "expires_at<?", []any{now()}}, {"admin_sessions", "expires_at<?", []any{now()}},
		{"bot_sessions", "expires_at<?", []any{now()}}, {"bot_verifications", "expires_at<?", []any{now()}},
		{"rate_limits", "expires_at<?", []any{now()}}, {"bot_login_codes", "expires_at<?", []any{now()}},
		{"telegram_auth_challenges", "expires_at<?", []any{now()}},
		{"search_snapshots", "expires_at<?", []any{now()}},
		{"bot_message_cleanup", "expires_at<?", []any{now()}},
		{"bot_chat_views", "updated_at<?", []any{future(-72 * time.Hour)}},
		{"bot_updates", "state IN ('done','ignored') AND created_at<?", []any{time.Now().In(location).AddDate(0, 0, -7).Format("2006-01-02 15:04:05")}},
		{"search_logs", "created_at<?", []any{time.Now().In(location).AddDate(0, 0, -90).Format("2006-01-02 15:04:05")}},
	}
	for _, job := range jobs {
		var count int64
		for {
			res, err := a.DB.Exec("DELETE FROM ts_"+job.table+" WHERE "+job.condition+" LIMIT 2000", job.args...)
			if err != nil {
				return result, err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return result, err
			}
			count += n
			if n < 2000 {
				break
			}
		}
		result[job.table] = count
	}
	return result, nil
}
