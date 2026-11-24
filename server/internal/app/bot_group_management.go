package app

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const groupDefaultWelcome = "欢迎 {{mention}} 加入 {{chat_title}}！"

var groupWelcomeTags = []string{"user_id", "nickname", "username", "first_name", "last_name", "mention", "chat_id", "chat_title", "chat_username", "datetime", "date", "time", "timezone", "bot_username"}
var groupPermissionFields = []string{"can_send_messages", "can_send_audios", "can_send_documents", "can_send_photos", "can_send_videos", "can_send_video_notes", "can_send_voice_notes", "can_send_polls", "can_send_other_messages", "can_add_web_page_previews", "can_change_info", "can_invite_users", "can_pin_messages", "can_manage_topics"}

func validateGroupWelcomeTemplate(text string) error {
	if utf8.RuneCountInString(text) > 2000 {
		return catalogErr(422, "入群欢迎语最多 2000 字。")
	}
	re := regexp.MustCompile(`\{\{([a-z_]+)\}\}`)
	for _, match := range re.FindAllStringSubmatch(text, -1) {
		if !slices.Contains(groupWelcomeTags, match[1]) {
			return catalogErr(422, "不支持的欢迎语标签："+match[1])
		}
	}
	rest := re.ReplaceAllString(text, "")
	if strings.ContainsAny(rest, "{}") {
		return catalogErr(422, "欢迎语标签须使用 {{nickname}} 形式。")
	}
	return nil
}

func renderGroupWelcome(template string, user, chat, bot Row) string {
	if template == "" {
		template = groupDefaultWelcome
	}
	name := strings.TrimSpace(str(user["first_name"]) + " " + str(user["last_name"]))
	if name == "" {
		name = "新成员"
	}
	t := time.Now().In(location)
	values := Row{"user_id": user["id"], "nickname": name, "username": user["username"], "first_name": user["first_name"], "last_name": user["last_name"], "chat_id": chat["id"], "chat_title": chat["title"], "chat_username": chat["username"], "datetime": t.Format("2006-01-02 15:04:05"), "date": t.Format("2006-01-02"), "time": t.Format("15:04:05"), "timezone": "Asia/Shanghai", "bot_username": bot["username"]}
	re := regexp.MustCompile(`\{\{([a-z_]+)\}\}`)
	var out strings.Builder
	remaining, truncated := 2999, false
	appendText := func(text string, mention bool) {
		if truncated {
			return
		}
		var part strings.Builder
		for _, r := range text {
			units := 1
			if r > 0xFFFF {
				units = 2
			}
			if units > remaining {
				truncated = true
				break
			}
			part.WriteRune(r)
			remaining -= units
		}
		if part.Len() == 0 {
			return
		}
		if mention {
			out.WriteString(`<a href="tg://user?id=` + str(integer(user["id"])) + `">`)
		}
		out.WriteString(html.EscapeString(part.String()))
		if mention {
			out.WriteString("</a>")
		}
	}
	// Reserve room for verification instructions and truncate visible UTF-16
	// units per segment, never in the middle of HTML markup or an escaped entity.
	offset := 0
	for _, match := range re.FindAllStringSubmatchIndex(template, -1) {
		appendText(template[offset:match[0]], false)
		key := template[match[2]:match[3]]
		if key == "mention" {
			appendText(name, true)
		} else {
			appendText(str(values[key]), false)
		}
		offset = match[1]
	}
	appendText(template[offset:], false)
	if truncated {
		out.WriteString("…")
	}
	return out.String()
}

func groupMessageReason(m, group Row) string {
	text := str(m["text"])
	if text == "" {
		text = str(m["caption"])
	}
	if limit := integer(group["message_max_chars"]); limit > 0 && int64(utf8.RuneCountInString(text)) > limit {
		return fmt.Sprintf("消息超长（%d 字 / 上限 %d 字）", utf8.RuneCountInString(text), limit)
	}
	if boolean(group["block_media"]) {
		for _, key := range []string{"photo", "video", "document", "audio", "voice", "sticker", "animation", "video_note", "contact", "location", "venue", "poll", "dice", "game"} {
			if m[key] != nil {
				return "媒体消息"
			}
		}
	}
	if boolean(group["block_forwards"]) && (m["forward_origin"] != nil || m["forward_from"] != nil || m["forward_from_chat"] != nil || m["forward_date"] != nil || boolean(m["is_automatic_forward"])) {
		return "转发消息"
	}
	return securityAdReason(m, group)
}

func groupPermissions(input Row) Row {
	p := Row{}
	for _, field := range groupPermissionFields {
		p[field] = boolean(input[field])
	}
	return p
}
func groupHasMute(member Row) bool {
	return str(member["status"]) == "restricted" && boolean(member["is_member"]) && integer(member["until_date"]) == 0 && encode(groupPermissions(member)) == encode(groupPermissions(Row{}))
}
func groupPresent(member Row) bool {
	return slices.Contains([]string{"member", "administrator", "creator"}, str(member["status"])) || str(member["status"]) == "restricted" && boolean(member["is_member"])
}
func (a *App) groupIssue(row Row, state, message string) error {
	result, err := a.DB.Exec("UPDATE ts_bot_join_challenges SET state=?,last_error=?,lease_until=NULL,updated_at=? WHERE id=? AND challenge_id=? AND state IN('issuing','restricting','pending','welcoming','releasing','failing')", state, clip(message, 500), now(), row["id"], row["challenge_id"])
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil
	}
	_, err = a.DB.Exec("UPDATE ts_bot_groups SET last_error=?,updated_at=? WHERE bot_id=? AND chat_id=?", clip(message, 255), now(), row["bot_id"], row["chat_id"])
	return err
}
func (a *App) checkGroupCapabilities(ctx context.Context, group, values Row) error {
	merged := catalogMerge(group, values)
	needsDelete := integer(merged["message_max_chars"]) > 0 || boolean(merged["block_media"]) || boolean(merged["block_forwards"]) || str(merged["ad_mode"]) != "off" || integer(merged["flood_limit"]) > 0
	needsRestrict := str(merged["join_verify_mode"]) != "" && str(merged["join_verify_mode"]) != "off"
	if !needsDelete && !needsRestrict {
		return nil
	}
	chat, err := a.Telegram(ctx, "getChat", Row{"chat_id": group["chat_id"]})
	if err != nil {
		return catalogErr(422, "无法读取群资料，请确认机器人仍在群内。")
	}
	self, err := a.Telegram(ctx, "getChatMember", Row{"chat_id": group["chat_id"], "user_id": group["bot_id"]})
	if err != nil {
		return catalogErr(422, "无法确认机器人群权限，请重试。")
	}
	admin := str(self["status"]) == "administrator"
	canDelete := admin && (str(chat["type"]) == "group" || boolean(self["can_delete_messages"]))
	canRestrict := admin && str(chat["type"]) == "supergroup" && boolean(self["can_restrict_members"])
	_, _ = a.DB.Exec("UPDATE ts_bot_groups SET can_delete=?,can_restrict=? WHERE id=?", canDelete, canRestrict, group["id"])
	if needsDelete && !canDelete {
		return catalogErr(422, "请先授予机器人管理员及删除消息权限。")
	}
	if needsRestrict && !canRestrict {
		return catalogErr(422, "新人验证需要超级群组，并授予机器人管理员及限制成员权限。")
	}
	return nil
}

// GroupServiceMessage handles only batch join/leave service messages. Ordinary
// content must still pass moderation before the separate search admission gate.
func (a *App) GroupServiceMessage(ctx context.Context, m Row, botID int64) (bool, error) {
	chat := object(m["chat"])
	if integer(chat["id"]) >= 0 {
		return false, nil
	}
	if users := rowsOf(m["new_chat_members"]); len(users) > 0 {
		for _, user := range users {
			if err := a.beginGroupJoin(ctx, chat, user, botID, integer(m["date"]), integer(m["_update_id"]), false); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	if left := object(m["left_chat_member"]); len(left) > 0 {
		return true, a.endGroupJoin(botID, integer(chat["id"]), integer(left["id"]), integer(m["_update_id"]))
	}
	return false, nil
}

// GroupSearchAllowed is deliberately database-only. Existing members without
// an admission record keep searching; disabling this group's verification or
// an observed administrator release also permits searching.
func (a *App) GroupSearchAllowed(botID, chatID, userID int64) (bool, error) {
	if chatID >= 0 {
		return true, nil
	}
	group, err := one(a.DB, "SELECT join_verify_mode FROM ts_bot_groups WHERE bot_id=? AND chat_id=?", botID, chatID)
	if err != nil {
		return false, err
	}
	if group == nil || str(group["join_verify_mode"]) == "off" || str(group["join_verify_mode"]) == "" {
		return true, nil
	}
	row, err := one(a.DB, "SELECT state FROM ts_bot_join_challenges WHERE bot_id=? AND chat_id=? AND user_id=?", botID, chatID, userID)
	if err != nil {
		return false, err
	}
	return row == nil || slices.Contains([]string{"passed", "exempt", "welcomed", "admin_released"}, str(row["state"])), nil
}

func (a *App) GroupMemberEvent(ctx context.Context, event Row, botID int64) error {
	chat, old, member := object(event["chat"]), object(event["old_chat_member"]), object(event["new_chat_member"])
	user := object(member["user"])
	if integer(chat["id"]) >= 0 || integer(user["id"]) <= 0 || integer(user["id"]) == botID {
		return nil
	}
	if !groupPresent(member) {
		return a.endGroupJoin(botID, integer(chat["id"]), integer(user["id"]), integer(event["_update_id"]))
	}
	if !groupPresent(old) {
		return a.beginGroupJoin(ctx, chat, user, botID, integer(event["date"]), integer(event["_update_id"]), true)
	}
	if str(member["status"]) != "restricted" && str(old["status"]) != "restricted" {
		return nil
	}
	actor := integer(object(event["from"])["id"])
	if actor == botID && groupHasMute(member) {
		_, err := a.DB.Exec("UPDATE ts_bot_join_challenges SET restriction_confirmed=1,last_update_id=GREATEST(last_update_id,?),updated_at=? WHERE bot_id=? AND chat_id=? AND user_id=? AND state IN('restricting','pending','releasing') AND manual_override=0 AND (?=0 OR last_update_id<=?)", integer(event["_update_id"]), now(), botID, chat["id"], user["id"], integer(event["_update_id"]), integer(event["_update_id"]))
		return err
	}
	if actor != botID {
		adminReleased := groupPresent(member) && (str(member["status"]) != "restricted" || boolean(member["can_send_messages"]))
		stateSQL, issue := "", "成员权限已被其他管理员修改，验证不再自动解除限制。"
		if adminReleased {
			stateSQL, issue = "state='admin_released',lease_until=NULL,", ""
		}
		result, err := a.DB.Exec("UPDATE ts_bot_join_challenges SET "+stateSQL+"manual_override=1,last_update_id=GREATEST(last_update_id,?),last_error=?,updated_at=? WHERE bot_id=? AND chat_id=? AND user_id=? AND state IN('issuing','restricting','pending','releasing','failing','failed','manual_review','error') AND (?=0 OR last_update_id<=?)", integer(event["_update_id"]), issue, now(), botID, chat["id"], user["id"], integer(event["_update_id"]), integer(event["_update_id"]))
		if err == nil {
			if n, _ := result.RowsAffected(); n > 0 {
				_, err = a.DB.Exec("UPDATE ts_bot_groups SET last_error=?,updated_at=? WHERE bot_id=? AND chat_id=?", issue, now(), botID, chat["id"])
			}
		}
		return err
	}
	return nil
}
func (a *App) endGroupJoin(botID, chatID, userID, updateID int64) error {
	_, err := a.DB.Exec("UPDATE ts_bot_join_challenges SET state='left',last_update_id=GREATEST(last_update_id,?),lease_until=NULL,updated_at=? WHERE bot_id=? AND chat_id=? AND user_id=? AND (?=0 OR last_update_id<=?)", updateID, now(), botID, chatID, userID, updateID, updateID)
	return err
}

func (a *App) beginGroupJoin(ctx context.Context, chat, user Row, botID, eventAt, updateID int64, explicitJoin bool) error {
	if boolean(user["is_bot"]) || integer(user["id"]) <= 0 || integer(user["id"]) == botID {
		return nil
	}
	group, err := one(a.DB, "SELECT * FROM ts_bot_groups WHERE bot_id=? AND chat_id=?", botID, chat["id"])
	if err != nil || group == nil {
		return err
	}
	mode := str(group["join_verify_mode"])
	if mode == "" {
		mode = "off"
	}
	if mode == "off" && !boolean(group["welcome_enabled"]) {
		return nil
	}
	if eventAt <= 0 {
		eventAt = time.Now().Unix()
	}
	row, err := cTx(a, func(tx *sql.Tx) (Row, error) {
		old, err := one(tx, "SELECT * FROM ts_bot_join_challenges WHERE bot_id=? AND chat_id=? AND user_id=? FOR UPDATE", botID, chat["id"], user["id"])
		if err != nil {
			return nil, err
		}
		if old != nil {
			if updateID > 0 && integer(old["last_update_id"]) > updateID {
				return nil, nil
			}
			if str(old["state"]) != "left" {
				// A later explicit left->member transition can recover a missed
				// leave event. Same-time service/member notifications still dedupe.
				if !explicitJoin || eventAt <= integer(old["event_at"]) {
					return nil, nil
				}
			}
			if updateID > 0 && integer(old["last_update_id"]) >= updateID {
				return nil, nil
			}
		}
		id := randomHex(16)
		timeout := integer(group["join_timeout_seconds"])
		if timeout < 30 || timeout > 3600 {
			timeout = 180
		}
		fields := Row{"bot_id": botID, "chat_id": chat["id"], "user_id": user["id"], "challenge_id": id, "event_at": eventAt, "last_update_id": updateID, "state": "issuing", "method": mode, "max_attempts": group["join_max_attempts"], "failure_action": group["join_failure_action"], "answer_hashes": "[]", "payload": "{}", "user_profile": encode(user), "chat_profile": encode(chat), "expected_permissions": encode(groupPermissions(Row{})), "expires_at": future(time.Duration(timeout) * time.Second), "lease_until": future(time.Minute), "created_at": now(), "updated_at": now()}
		if old != nil {
			if _, err = tx.Exec("DELETE FROM ts_bot_join_challenges WHERE id=?", old["id"]); err != nil {
				return nil, err
			}
		}
		newID, err := catalogInsert(tx, "bot_join_challenges", fields)
		if err != nil {
			return nil, err
		}
		fields["id"] = newID
		return fields, nil
	})
	if err != nil || row == nil {
		return err
	}
	if mode != "off" {
		if str(chat["type"]) != "supergroup" {
			return a.groupIssue(row, "error", "新人验证未执行：只支持超级群组。")
		}
		self, err := a.Telegram(ctx, "getChatMember", Row{"chat_id": chat["id"], "user_id": botID})
		if err != nil || str(self["status"]) != "administrator" || !boolean(self["can_restrict_members"]) {
			return a.groupIssue(row, "error", "新人验证未执行：机器人缺少限制成员权限。")
		}
		member, err := a.Telegram(ctx, "getChatMember", Row{"chat_id": chat["id"], "user_id": user["id"]})
		if err != nil {
			return a.groupIssue(row, "error", "新人验证未执行：无法读取新成员身份。")
		}
		if str(member["status"]) == "administrator" || str(member["status"]) == "creator" {
			return a.groupIssue(row, "exempt", "")
		}
		if str(member["status"]) != "member" {
			return a.groupIssue(row, "manual_review", "新成员已有其他权限限制或已离群，本次验证不会覆盖管理员设置。")
		}
		_, err = a.DB.Exec("UPDATE ts_bot_join_challenges SET state='restricting',restriction_owned=1,updated_at=? WHERE id=? AND state='issuing'", now(), row["id"])
		if err != nil {
			return err
		}
		_, err = a.Telegram(ctx, "restrictChatMember", Row{"chat_id": chat["id"], "user_id": user["id"], "permissions": groupPermissions(Row{}), "use_independent_chat_permissions": true, "until_date": 0})
		if err != nil {
			return a.groupIssue(row, "manual_review", "限制新成员失败或结果不确定，请管理员核对该成员权限。")
		}
		result, err := a.DB.Exec("UPDATE ts_bot_join_challenges SET state='pending',lease_until=NULL,updated_at=? WHERE id=? AND state='restricting'", now(), row["id"])
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return nil
		}
	} else {
		if _, err = a.DB.Exec("UPDATE ts_bot_join_challenges SET state='welcoming',updated_at=? WHERE id=?", now(), row["id"]); err != nil {
			return err
		}
	}
	cred, err := a.botCredentials()
	if err != nil {
		return a.groupIssue(row, "error", "无法读取机器人用户名，欢迎消息未发送。")
	}
	bot := object(cred["bot"])
	text := renderGroupWelcome(str(group["welcome_template"]), user, chat, bot)
	if !boolean(group["welcome_enabled"]) {
		text = renderGroupWelcome("{{mention}}，请完成入群验证。", user, chat, bot)
	}
	params := Row{"chat_id": chat["id"], "text": text, "parse_mode": "HTML", "link_preview_options": Row{"is_disabled": true}}
	if mode != "off" {
		if str(bot["username"]) == "" {
			return a.groupIssue(row, "manual_review", "机器人用户名不可用，请管理员协助新成员解除限制。")
		}
		params["text"] = text + "\n\n请在 " + str(group["join_timeout_seconds"]) + " 秒内点击按钮，前往机器人私聊完成验证。验证前暂时不能在群内发言。"
		params["reply_markup"] = Row{"inline_keyboard": [][]Row{{{"text": "前往私聊验证", "url": "https://t.me/" + str(bot["username"]) + "?start=join_" + str(row["challenge_id"])}}}}
	}
	sent, err := a.Telegram(ctx, "sendMessage", params)
	if err != nil {
		return a.groupIssue(row, "manual_review", "入群欢迎或验证通知发送失败或结果不确定，请管理员查看新成员状态；不会重复发送。")
	}
	state := "pending"
	if mode == "off" {
		state = "welcomed"
	}
	_, err = a.DB.Exec("UPDATE ts_bot_join_challenges SET state=?,welcome_message_id=?,lease_until=NULL,updated_at=? WHERE id=? AND challenge_id=? AND state IN('pending','welcoming')", state, sent["message_id"], now(), row["id"], row["challenge_id"])
	return err
}

func (a *App) JoinVerificationStart(ctx context.Context, m Row, botID int64, challenge string) error {
	user, chat := integer(object(m["from"])["id"]), integer(object(m["chat"])["id"])
	if chat <= 0 || chat != user || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(challenge) {
		return a.sendText(ctx, chat, "请使用本人的 Telegram 账号，通过群内按钮打开验证。", nil)
	}
	if err := a.RateLimit(fmt.Sprintf("join-start:%d:%d", botID, user), 6, 60); err != nil {
		return a.sendText(ctx, chat, "验证打开过于频繁，请稍后重试。", nil)
	}
	row, err := one(a.DB, "SELECT * FROM ts_bot_join_challenges WHERE bot_id=? AND challenge_id=?", botID, challenge)
	if err != nil {
		return err
	}
	if row == nil || integer(row["user_id"]) != user {
		return a.sendText(ctx, chat, "这不是你的入群验证。请使用自己收到的群内验证按钮。", nil)
	}
	if str(row["state"]) != "pending" || expired(row["expires_at"]) || boolean(row["manual_override"]) {
		return a.sendText(ctx, chat, "这次入群验证已结束、超时或被管理员接管，请联系群管理员。", nil)
	}
	puzzle, err := makeSecurityPuzzle(str(row["method"]))
	if err != nil {
		return err
	}
	hashes, choices := []string{}, Row{}
	for _, answer := range puzzle.Answers {
		hashes = append(hashes, hash(answer))
	}
	for _, choice := range puzzle.Choices {
		choices[choice.Key] = choice.Label
	}
	generation := randomHex(6)
	result, err := a.DB.Exec("UPDATE ts_bot_join_challenges SET method=?,answer_hashes=?,payload=?,private_generation=?,private_message_id=0,step=0,updated_at=? WHERE id=? AND challenge_id=? AND state='pending' AND manual_override=0 AND expires_at>?",
		puzzle.Method, encode(hashes), encode(Row{"choices": choices}), generation, now(), row["id"], challenge, now())
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return a.sendText(ctx, chat, "这次验证已失效，请回到群内检查状态。", nil)
	}
	prompt := "入群验证 · " + str(decode(str(row["chat_profile"]))["title"]) + "\n\n" + strings.ReplaceAll(puzzle.Prompt, "继续搜索", "完成入群验证")
	params := Row{"chat_id": chat, "text": prompt}
	if len(puzzle.Choices) > 0 {
		buttons := [][]Row{}
		line := []Row{}
		for _, choice := range puzzle.Choices {
			line = append(line, Row{"text": choice.Label, "callback_data": "j:" + challenge + ":" + choice.Key})
			if len(line) == 3 {
				buttons = append(buttons, line)
				line = []Row{}
			}
		}
		if len(line) > 0 {
			buttons = append(buttons, line)
		}
		params["reply_markup"] = Row{"inline_keyboard": buttons}
	} else {
		params["reply_markup"] = Row{"force_reply": true, "input_field_placeholder": "请回复本题答案"}
	}
	method := "sendMessage"
	if len(puzzle.Photo) > 0 {
		method = "sendPhoto"
		params["caption"] = prompt
		params["photo"] = puzzle.Photo
		delete(params, "text")
	}
	sent, err := a.Telegram(ctx, method, params)
	if err != nil {
		_, _ = a.DB.Exec("UPDATE ts_bot_join_challenges SET last_error='私聊验证发送失败，请通过群按钮重新打开。',updated_at=? WHERE id=? AND private_generation=?", now(), row["id"], generation)
		return err
	}
	_, err = a.DB.Exec("UPDATE ts_bot_join_challenges SET private_message_id=?,last_error='',updated_at=? WHERE id=? AND challenge_id=? AND private_generation=? AND state='pending'", sent["message_id"], now(), row["id"], challenge, generation)
	return err
}

func (a *App) verifyGroupJoin(botID int64, challenge string, user, message int64, answer string, button bool) (Row, error) {
	return cTx(a, func(tx *sql.Tx) (Row, error) {
		row, err := one(tx, "SELECT * FROM ts_bot_join_challenges WHERE bot_id=? AND challenge_id=? FOR UPDATE", botID, challenge)
		if err != nil {
			return nil, err
		}
		refuse := func(text string) (Row, error) { return Row{"status": "refused", "text": text}, nil }
		if row == nil || integer(row["user_id"]) != user {
			return refuse("不能操作别人的入群验证。")
		}
		if str(row["state"]) != "pending" || expired(row["expires_at"]) {
			return refuse("这次入群验证已结束或超时，请联系群管理员。")
		}
		if boolean(row["manual_override"]) {
			return refuse("群管理员已修改你的权限，请联系管理员处理。")
		}
		if message <= 0 || integer(row["private_message_id"]) != message {
			return refuse("请使用最近收到的验证题目。")
		}
		textMethod := str(row["method"]) == "text" || str(row["method"]) == "math"
		if textMethod == button {
			return refuse("请按本题要求点击按钮或回复答案。")
		}
		if button {
			if _, ok := object(decode(str(row["payload"]))["choices"])[answer]; !ok {
				return refuse("此按钮已失效，请重新打开验证。")
			}
		}
		hashes := []string{}
		if json.Unmarshal([]byte(str(row["answer_hashes"])), &hashes) != nil || len(hashes) == 0 {
			return refuse("题目已失效，请通过群按钮重新打开验证。")
		}
		step := int(integer(row["step"]))
		if step < 0 || step >= len(hashes) {
			return refuse("题目已完成，请勿重复操作。")
		}
		if subtle.ConstantTimeCompare([]byte(hash(answer)), []byte(hashes[step])) != 1 {
			attempts := integer(row["attempts"]) + 1
			if attempts >= integer(row["max_attempts"]) {
				_, err = tx.Exec("UPDATE ts_bot_join_challenges SET state='failing',attempts=?,step=0,lease_until=?,updated_at=? WHERE id=?", attempts, future(time.Minute), now(), row["id"])
				return Row{"status": "failed", "text": "尝试次数已用完，请联系群管理员。", "row": row}, err
			}
			_, err = tx.Exec("UPDATE ts_bot_join_challenges SET attempts=?,step=0,updated_at=? WHERE id=?", attempts, now(), row["id"])
			return Row{"status": "wrong", "text": fmt.Sprintf("答案不正确，还可尝试 %d 次。顺序题请从第一个开始。", integer(row["max_attempts"])-attempts)}, err
		}
		if step+1 < len(hashes) {
			_, err = tx.Exec("UPDATE ts_bot_join_challenges SET step=step+1,updated_at=? WHERE id=?", now(), row["id"])
			return Row{"status": "progress", "text": fmt.Sprintf("已完成 %d/%d，请继续。", step+1, len(hashes))}, err
		}
		_, err = tx.Exec("UPDATE ts_bot_join_challenges SET state='releasing',lease_until=?,updated_at=? WHERE id=?", future(time.Minute), now(), row["id"])
		return Row{"status": "passed", "text": "答案正确，正在核对群权限。", "row": row}, err
	})
}

func (a *App) JoinVerificationCallback(ctx context.Context, cb Row, botID int64) error {
	match := regexp.MustCompile(`^j:([a-f0-9]{32}):([a-f0-9]{12})$`).FindStringSubmatch(str(cb["data"]))
	m, from := object(cb["message"]), object(cb["from"])
	if match == nil || integer(object(m["chat"])["id"]) != integer(from["id"]) || integer(from["id"]) <= 0 {
		return a.answerCallback(ctx, cb, "请在本人私聊中完成入群验证。", true)
	}
	if err := a.RateLimit(fmt.Sprintf("join-answer:%d:%v", botID, from["id"]), 15, 60); err != nil {
		return a.answerCallback(ctx, cb, "操作过于频繁，请稍后再试。", true)
	}
	result, err := a.verifyGroupJoin(botID, match[1], integer(from["id"]), integer(m["message_id"]), match[2], true)
	if err != nil {
		return err
	}
	if str(result["status"]) == "passed" || str(result["status"]) == "failed" {
		text, err := a.finishGroupJoin(ctx, object(result["row"]), str(result["status"]) == "passed")
		if err != nil {
			return err
		}
		return a.answerCallback(ctx, cb, clip(text, 200), true)
	}
	return a.answerCallback(ctx, cb, str(result["text"]), str(result["status"]) != "progress")
}

func (a *App) JoinVerificationText(ctx context.Context, m Row, botID int64) (bool, error) {
	chat, user := integer(object(m["chat"])["id"]), integer(object(m["from"])["id"])
	text := strings.TrimSpace(str(m["text"]))
	reply := integer(object(m["reply_to_message"])["message_id"])
	if chat <= 0 || chat != user || reply <= 0 || text == "" || strings.HasPrefix(text, "/") {
		return false, nil
	}
	row, err := one(a.DB, "SELECT challenge_id FROM ts_bot_join_challenges WHERE bot_id=? AND user_id=? AND private_message_id=? AND method IN('text','math') ORDER BY id DESC LIMIT 1", botID, user, reply)
	if err != nil || row == nil {
		return false, err
	}
	if err = a.RateLimit(fmt.Sprintf("join-answer:%d:%d", botID, user), 15, 60); err != nil {
		return true, a.sendText(ctx, chat, "验证回答过于频繁，请稍后再试。", replyContext(m))
	}
	result, err := a.verifyGroupJoin(botID, str(row["challenge_id"]), user, reply, strings.ToUpper(text), false)
	if err != nil {
		return true, err
	}
	response := str(result["text"])
	if str(result["status"]) == "passed" || str(result["status"]) == "failed" {
		response, err = a.finishGroupJoin(ctx, object(result["row"]), str(result["status"]) == "passed")
		if err != nil {
			return true, err
		}
	}
	return true, a.sendText(ctx, chat, response, replyContext(m))
}

// finishGroupJoin never assumes all permissions should be true. A live change
// by another administrator, missing provenance, or an ambiguous network result
// is handed to an administrator instead of silently removing their restriction.
func (a *App) finishGroupJoin(ctx context.Context, initial Row, passed bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	row, err := one(a.DB, "SELECT * FROM ts_bot_join_challenges WHERE id=? AND challenge_id=?", initial["id"], initial["challenge_id"])
	if err != nil || row == nil {
		return "验证已失效。", err
	}
	expected := "failing"
	if passed {
		expected = "releasing"
	}
	if str(row["state"]) != expected {
		return "这次验证已处理，请勿重复操作。", nil
	}
	manual := func(message string) (string, error) {
		return message, a.groupIssue(row, "manual_review", message)
	}
	if boolean(row["manual_override"]) || !boolean(row["restriction_owned"]) {
		return manual("成员权限已有人工调整，请联系群管理员；机器人不会自动解除限制。")
	}
	self, err := a.Telegram(ctx, "getChatMember", Row{"chat_id": row["chat_id"], "user_id": row["bot_id"]})
	if err != nil || str(self["status"]) != "administrator" || !boolean(self["can_restrict_members"]) {
		return manual("机器人缺少限制成员权限，请联系群管理员处理。")
	}
	member, err := a.Telegram(ctx, "getChatMember", Row{"chat_id": row["chat_id"], "user_id": row["user_id"]})
	if err != nil {
		return manual("无法确认当前成员权限，请联系群管理员处理。")
	}
	if str(member["status"]) == "administrator" || str(member["status"]) == "creator" {
		return "你已是群管理员，无需解除成员限制。", a.groupIssue(row, "exempt", "")
	}
	if !groupPresent(member) {
		return "你已离开该群，本次验证结束。", a.groupIssue(row, "left", "")
	}
	if !groupHasMute(member) {
		return manual("成员权限与本次验证限制不一致，请群管理员核对；不会覆盖现有权限。")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if passed && !boolean(row["restriction_confirmed"]) {
		return manual("答案正确，但缺少本次限制的成员事件确认。请群管理员手动核对并解除限制。")
	}
	current, err := one(a.DB, "SELECT manual_override,state FROM ts_bot_join_challenges WHERE id=? AND challenge_id=?", row["id"], row["challenge_id"])
	if err != nil {
		return "", err
	}
	if boolean(current["manual_override"]) || str(current["state"]) != expected {
		return manual("成员状态已变化，请联系群管理员处理。")
	}
	if !passed && str(row["failure_action"]) != "kick" {
		// The initial restriction was indefinite; failure intentionally keeps it.
		return "入群验证失败或超时，已保留禁言，请联系群管理员。", a.groupIssue(row, "failed", "")
	}
	method, params := "banChatMember", Row{"chat_id": row["chat_id"], "user_id": row["user_id"], "until_date": time.Now().Unix() + 60, "revoke_messages": false}
	message, state := "入群验证失败或超时，已移出群；1 分钟后可重新加入。", "failed"
	if passed {
		chat, err := a.Telegram(ctx, "getChat", Row{"chat_id": row["chat_id"]})
		if err != nil || str(chat["type"]) != "supergroup" {
			return manual("无法读取群默认权限，请群管理员手动解除限制。")
		}
		defaults := object(chat["permissions"])
		if _, ok := defaults["can_send_messages"]; !ok {
			return manual("群默认权限资料不完整，请群管理员手动核对。")
		}
		method = "restrictChatMember"
		params = Row{"chat_id": row["chat_id"], "user_id": row["user_id"], "permissions": groupPermissions(defaults), "use_independent_chat_permissions": true, "until_date": 0}
		message, state = "入群验证通过，已恢复该群默认成员权限。", "passed"
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	current, err = one(a.DB, "SELECT manual_override,state FROM ts_bot_join_challenges WHERE id=? AND challenge_id=?", row["id"], row["challenge_id"])
	if err != nil {
		return "", err
	}
	if boolean(current["manual_override"]) || str(current["state"]) != expected {
		return manual("成员状态在操作前已变化，请联系群管理员处理。")
	}
	if _, err = a.Telegram(ctx, method, params); err != nil {
		return manual("权限操作失败或结果不确定，请群管理员核对；不会自动重复权限操作。")
	}
	return message, a.groupIssue(row, state, "")
}

// GroupManagementOnce claims a bounded batch under row locks. Telegram HTTP
// requests happen after commit, and stale operation leases require manual review.
func (a *App) GroupManagementOnce(ctx context.Context) error {
	credentials, err := a.botCredentials()
	if err != nil {
		return err
	}
	botID := integer(object(credentials["bot"])["id"])
	if botID <= 0 {
		return nil
	}
	return a.groupManagementForBot(ctx, botID)
}

func (a *App) groupManagementForBot(ctx context.Context, botID int64) error {
	for i := 0; i < 50 && ctx.Err() == nil; i++ {
		row, err := cTx(a, func(tx *sql.Tx) (Row, error) {
			r, err := one(tx, "SELECT * FROM ts_bot_join_challenges WHERE bot_id=? AND state='pending' AND expires_at<=? ORDER BY expires_at,id LIMIT 1 FOR UPDATE SKIP LOCKED", botID, now())
			if err != nil || r == nil {
				return r, err
			}
			_, err = tx.Exec("UPDATE ts_bot_join_challenges SET state='failing',lease_until=?,updated_at=? WHERE id=? AND state='pending'", future(time.Minute), now(), r["id"])
			return r, err
		})
		if err != nil {
			return err
		}
		if row == nil {
			break
		}
		if _, err = a.finishGroupJoin(ctx, row, false); err != nil {
			return err
		}
	}
	rows, err := query(a.DB, "SELECT * FROM ts_bot_join_challenges WHERE bot_id=? AND state IN('issuing','restricting','welcoming','releasing','failing') AND lease_until<? ORDER BY lease_until LIMIT 50", botID, now())
	if err != nil {
		return err
	}
	for _, row := range rows {
		// The prior process may have sent a successful HTTP request before dying.
		// Repeating a restriction/release would risk overriding an administrator.
		result, e := a.DB.Exec("UPDATE ts_bot_join_challenges SET state='manual_review',lease_until=NULL,last_error='入群处理进程中断或操作超时，请管理员核对该成员权限和欢迎消息。',updated_at=? WHERE id=? AND challenge_id=? AND state=? AND lease_until<?", now(), row["id"], row["challenge_id"], row["state"], now())
		if e != nil {
			return e
		}
		if n, _ := result.RowsAffected(); n > 0 {
			_, err = a.DB.Exec("UPDATE ts_bot_groups SET last_error='入群处理进程中断，请管理员核对待验证成员权限。',updated_at=? WHERE bot_id=? AND chat_id=?", now(), row["bot_id"], row["chat_id"])
		}
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}
