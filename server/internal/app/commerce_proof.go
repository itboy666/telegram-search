package app

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
)

const rechargeProofMaxBytes = 5 << 20

// Re-encoding strips metadata and rejects executable/SVG or malformed content.
// A separate private table keeps blobs out of order lists and public file roots.
func normalizeRechargeProof(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data) > rechargeProofMaxBytes {
		return nil, cErr(422, "凭证图片不能超过 5 MB。")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png" && format != "webp") {
		return nil, cErr(422, "请发送有效的 JPEG、PNG 或 WebP 转账截图。")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8000 || cfg.Height > 8000 || int64(cfg.Width)*int64(cfg.Height) > 12_000_000 {
		return nil, cErr(422, "截图尺寸过大，请压缩到 1200 万像素以内。")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, cErr(422, "截图内容损坏，请重新发送。")
	}
	var out bytes.Buffer
	if err = png.Encode(&out, img); err != nil || out.Len() > rechargeProofMaxBytes {
		return nil, cErr(422, "截图转换后过大，请缩小图片后重新发送。")
	}
	return out.Bytes(), nil
}

func (a *App) downloadRechargeProof(ctx context.Context, botID int64, fileID string) ([]byte, error) {
	if a.TelegramProofDownload != nil {
		return a.TelegramProofDownload(ctx, botID, fileID)
	}
	credentials, err := a.botCredentials()
	if err != nil {
		return nil, err
	}
	if integer(object(credentials["bot"])["id"]) != botID {
		return nil, cErr(409, "机器人配置已变更，请重新打开钱包后提交。")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result, err := a.telegramRaw(ctx, str(credentials["token"]), "getFile", Row{"file_id": fileID})
	if err != nil {
		return nil, cErr(503, "暂时无法读取截图，请稍后重新发送。")
	}
	f := object(result)
	path := str(f["file_path"])
	if integer(f["file_size"]) > rechargeProofMaxBytes || !regexp.MustCompile(`^[A-Za-z0-9_/-]+\.[A-Za-z0-9]+$`).MatchString(path) || strings.Contains(path, "..") || strings.HasPrefix(path, "/") {
		return nil, cErr(422, "截图大小或格式不支持，请重新发送。")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.telegram.org/file/bot"+str(credentials["token"])+"/"+path, nil)
	if err != nil {
		return nil, cErr(503, "暂时无法读取截图。")
	}
	client := *a.HTTP
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		// Never return url.Error: its URL contains the bot credential.
		return nil, cErr(503, "截图下载失败，请稍后重新发送。")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > rechargeProofMaxBytes {
		return nil, cErr(422, "截图无法下载或超过 5 MB，请重新发送。")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, rechargeProofMaxBytes+1))
	if err != nil || len(data) > rechargeProofMaxBytes {
		return nil, cErr(422, "截图内容不完整或超过 5 MB，请重新发送。")
	}
	return data, nil
}

func validateRechargeProofOrder(row Row) error {
	if str(row["method"]) != "offline" || (str(row["status"]) != "pending" && str(row["status"]) != "review") || expired(row["expires_at"]) {
		return cErr(409, "该订单已过期或不能补交凭证，请刷新订单状态。")
	}
	return nil
}

func (a *App) rechargeTelegramProof(ctx context.Context, user, id, botID int64, m Row) (Row, error) {
	from, chat := object(m["from"]), object(m["chat"])
	if botID <= 0 || integer(from["id"]) <= 0 || str(chat["type"]) != "private" || integer(chat["id"]) != integer(from["id"]) {
		return nil, cErr(403, "请在本人私聊订单中提交凭证。")
	}
	file := Row{}
	if photos := array(m["photo"]); len(photos) > 0 {
		file = object(photos[len(photos)-1])
	} else if d := object(m["document"]); len(d) > 0 {
		mime := str(d["mime_type"])
		if mime != "image/jpeg" && mime != "image/png" && mime != "image/webp" {
			return nil, cErr(422, "请发送 JPEG、PNG 或 WebP 截图，不支持其他附件。")
		}
		file = d
	}
	if str(file["file_id"]) == "" || integer(file["file_size"]) > rechargeProofMaxBytes {
		return nil, cErr(422, "请发送不超过 5 MB 的转账截图。")
	}
	caption := strings.TrimSpace(str(m["caption"]))
	if caption != "" && (len([]rune(caption)) < 5 || len([]rune(caption)) > 500) {
		return nil, cErr(422, "截图说明请填写 5–500 字，也可以不填。")
	}
	// Reject unauthorized requests before any download; recheck after I/O inside
	// the same transaction which changes the order and its attachment.
	order, err := rechargeOwned(a.DB, id, user, false)
	if err != nil {
		return nil, err
	}
	if err = validateRechargeProofOrder(order); err != nil {
		return nil, err
	}
	owner, err := one(a.DB, "SELECT telegram_id,status FROM ts_users WHERE id=?", user)
	if err != nil {
		return nil, err
	}
	if !boolean(owner["status"]) || integer(owner["telegram_id"]) != integer(from["id"]) {
		return nil, cErr(403, "账号已停用或 Telegram 绑定已变更。")
	}
	previous, err := one(a.DB, "SELECT bot_id FROM ts_recharge_proofs WHERE order_id=?", id)
	if err != nil {
		return nil, err
	}
	if previous != nil && integer(previous["bot_id"]) != botID {
		return nil, cErr(409, "此凭证由其他机器人接收，请联系管理员核对。")
	}
	if err = a.commerceLimit(fmt.Sprintf("recharge-proof:%d", user), 10, time.Minute); err != nil {
		return nil, err
	}
	data, err := a.downloadRechargeProof(ctx, botID, str(file["file_id"]))
	if err != nil {
		return nil, err
	}
	data, err = normalizeRechargeProof(data)
	if err != nil {
		return nil, err
	}
	return cTx(a, func(tx *sql.Tx) (Row, error) {
		row, err := rechargeOwned(tx, id, user, true)
		if err != nil {
			return nil, err
		}
		if err = validateRechargeProofOrder(row); err != nil {
			return nil, err
		}
		if err = activeCommerceUser(tx, user); err != nil {
			return nil, err
		}
		owner, err := one(tx, "SELECT telegram_id FROM ts_users WHERE id=?", user)
		if err != nil {
			return nil, err
		}
		if integer(owner["telegram_id"]) != integer(from["id"]) {
			return nil, cErr(403, "Telegram 绑定已变更，请重新打开钱包。")
		}
		previous, err := one(tx, "SELECT bot_id FROM ts_recharge_proofs WHERE order_id=? FOR UPDATE", id)
		if err != nil {
			return nil, err
		}
		if previous != nil && integer(previous["bot_id"]) != botID {
			return nil, cErr(409, "此凭证由其他机器人接收，请联系管理员核对。")
		}
		note := caption
		if note == "" {
			note = str(row["proof"])
			if note == "" {
				note = "已通过机器人提交转账截图，请核对图片。"
			}
		}
		_, err = tx.Exec("INSERT INTO ts_recharge_proofs(order_id,user_id,bot_id,image,created_at,updated_at) VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE image=VALUES(image),updated_at=VALUES(updated_at)", id, user, botID, data, now(), now())
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec("UPDATE ts_recharges SET proof=?,status='review' WHERE id=?", note, id); err != nil {
			return nil, err
		}
		row, err = rechargeOwned(tx, id, user, false)
		return moneyRow(row), err
	})
}

func (a *App) annotateRechargeProof(row Row) error {
	count, err := cCount(a.DB, "SELECT COUNT(*) FROM ts_recharge_proofs WHERE order_id=?", row["id"])
	if err == nil {
		row["has_proof_image"] = count > 0
	}
	return err
}

func (a *App) registerCommerceProof() {
	for _, scope := range []string{"admin", "user"} {
		permission := ""
		if scope == "admin" {
			permission = "recharges.read"
		}
		a.Handle("GET /"+scope+"/api/commerce/recharges/{id}/proof-image", scope, permission, false, func(w http.ResponseWriter, r *http.Request, i *Identity) {
			if scope == "admin" {
				if _, ok := i.Access.Project("recharges", Row{"proof": ""})["proof"]; !ok {
					Fail(w, 403, "没有查看转账凭证的权限。")
					return
				}
			}
			user := i.ID
			if scope == "admin" {
				user = 0
			}
			if _, err := rechargeOwned(a.DB, commerceID(r), user, false); err != nil {
				commerceReply(w, nil, err)
				return
			}
			var data []byte
			err := a.DB.QueryRowContext(r.Context(), "SELECT image FROM ts_recharge_proofs WHERE order_id=?", commerceID(r)).Scan(&data)
			if err != nil {
				if err == sql.ErrNoRows {
					Fail(w, 404, "此订单没有截图。")
				} else {
					Fail(w, 503, "暂时无法读取截图。")
				}
				return
			}
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Disposition", "inline; filename=\"payment-proof.png\"")
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
			w.Write(data)
		})
	}
}
