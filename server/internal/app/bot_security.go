package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

var securityModeNames = map[string]string{"off": "不验证", "emoji": "选对 emoji", "button": "点击确认（轻量）", "text": "图片字母数字", "math": "四则运算", "sequence": "按顺序点击 emoji", "random": "随机组合"}

func securityRandom(max int) int {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		panic("secure randomness unavailable")
	}
	return int(n.Int64())
}
func securityShuffle[T any](items []T) {
	for i := len(items) - 1; i > 0; i-- {
		j := securityRandom(i + 1)
		items[i], items[j] = items[j], items[i]
	}
}

type securityChoice struct{ Key, Label string }
type securityPuzzle struct {
	Method, Prompt string
	Answers        []string
	Choices        []securityChoice
	Photo          []byte
}

func makeSecurityPuzzle(method string) (securityPuzzle, error) {
	if method == "random" {
		method = []string{"emoji", "math", "sequence", "text"}[securityRandom(4)]
	}
	p := securityPuzzle{Method: method, Answers: []string{}, Choices: []securityChoice{}}
	switch method {
	case "button":
		key := randomHex(6)
		p.Choices = []securityChoice{{key, "点击验证"}}
		p.Answers = []string{key}
		p.Prompt = "点击下方按钮，确认后继续搜索。"
	case "text":
		letters := "ABCDEFGHJKLMNPQRSTUVWXYZ"
		digits := "23456789"
		alphabet := letters + digits
		code := []byte{letters[securityRandom(len(letters))], digits[securityRandom(len(digits))]}
		for len(code) < 6 {
			code = append(code, alphabet[securityRandom(len(alphabet))])
		}
		securityShuffle(code)
		p.Answers = []string{string(code)}
		p.Prompt = "请回复这张验证图片，输入图中的 6 位字母数字（不区分大小写）。"
		var err error
		p.Photo, err = securityImage(string(code))
		if err != nil {
			return p, err
		}
	case "math":
		a, b := 2+securityRandom(14), 2+securityRandom(8)
		op := []string{"+", "−", "×", "÷"}[securityRandom(4)]
		answer := 0
		switch op {
		case "+":
			answer = a + b
		case "−":
			if a < b {
				a, b = b, a
			}
			answer = a - b
		case "×":
			answer = a * b
		case "÷":
			a *= b
			answer = a / b
		}
		p.Prompt = fmt.Sprintf("请回复这条消息，输入计算结果：\n\n%d %s %d = ?", a, op, b)
		p.Answers = []string{strconv.Itoa(answer)}
	case "emoji", "sequence":
		emojis := []string{"🍎", "🍋", "🍇", "🍉", "🚗", "✈️", "🐱", "🐶", "🌻", "⚽", "🎸", "⭐"}
		securityShuffle(emojis)
		for _, emoji := range emojis[:6] {
			p.Choices = append(p.Choices, securityChoice{randomHex(6), emoji})
		}
		targets := append([]securityChoice{}, p.Choices...)
		securityShuffle(targets)
		count := 1
		if method == "sequence" {
			count = 3
		}
		labels := []string{}
		for _, choice := range targets[:count] {
			p.Answers = append(p.Answers, choice.Key)
			labels = append(labels, choice.Label)
		}
		if method == "sequence" {
			p.Prompt = "请按顺序点击这 3 个 emoji：\n\n" + strings.Join(labels, " → ")
		} else {
			p.Prompt = "请点击和这个相同的 emoji：\n\n" + labels[0]
		}
	default:
		return p, catalogErr(422, "验证方式不支持。")
	}
	return p, nil
}
func securityImage(code string) ([]byte, error) {
	canvas := image.NewRGBA(image.Rect(0, 0, 300, 96))
	bg := color.RGBA{247, 249, 253, 255}
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	for i := 0; i < 1200; i++ {
		canvas.SetRGBA(securityRandom(300), securityRandom(96), color.RGBA{180, 194, 215, 255})
	}
	for i, ch := range code {
		glyph := image.NewRGBA(image.Rect(0, 0, 9, 16))
		d := font.Drawer{Dst: glyph, Src: image.NewUniform(color.RGBA{uint8(25 + securityRandom(40)), uint8(35 + securityRandom(40)), uint8(70 + securityRandom(40)), 255}), Face: basicfont.Face7x13, Dot: fixed.P(1, 13)}
		d.DrawString(string(ch))
		ox := 17 + i*45
		oy := 18 + securityRandom(12)
		skew := securityRandom(3) - 1
		for y := 0; y < 48; y++ {
			for x := 0; x < 27; x++ {
				c := glyph.RGBAAt(x/3, y/3)
				if c.A > 0 {
					canvas.SetRGBA(ox+x+skew*(y/12), oy+y, c)
				}
			}
		}
	}
	var b bytes.Buffer
	err := png.Encode(&b, canvas)
	return b.Bytes(), err
}

func securityReply(m Row) Row {
	r := Row{"reply_parameters": Row{"message_id": integer(m["message_id"]), "allow_sending_without_reply": true}}
	if id := integer(m["message_thread_id"]); id > 0 {
		r["message_thread_id"] = id
	}
	return r
}
func (a *App) securitySend(ctx context.Context, m Row, text string) error {
	params := securityReply(m)
	params["chat_id"] = object(m["chat"])["id"]
	params["text"] = text
	_, err := a.sendReply(ctx, "sendMessage", params)
	return err
}
func (a *App) CaptchaTrusted(botID, chat, user int64) bool {
	p := a.securityPolicy(botID, chat)
	if str(p["mode"]) == "off" {
		return true
	}
	r, err := cOne(a.DB, "SELECT policy_hash,verified_until FROM ts_bot_verifications WHERE bot_id=? AND chat_id=? AND user_id=?", botID, chat, user)
	return err == nil && r != nil && str(r["policy_hash"]) == str(p["hash"]) && integer(r["verified_until"]) > time.Now().Unix()
}
func (a *App) securityThrottle(botID, chat, user int64) error {
	p := a.securitySettings()
	if err := a.RateLimit(fmt.Sprintf("guard-search:%d:%d", botID, user), int(integer(p["search_limit"])), 60); err != nil {
		return err
	}
	if chat < 0 {
		return a.RateLimit(fmt.Sprintf("guard-chat:%d:%d", botID, chat), int(integer(p["group_limit"])), 60)
	}
	return nil
}

// CaptchaGate returns true when the search must wait for a challenge.
func (a *App) CaptchaGate(ctx context.Context, m Row, botID int64, keyword string) (bool, error) {
	chat, user := integer(object(m["chat"])["id"]), integer(object(m["from"])["id"])
	if utf8.RuneCountInString(keyword) > 120 {
		return true, catalogErr(422, "搜索词最多 120 字。")
	}
	if a.CaptchaTrusted(botID, chat, user) {
		return false, a.securityThrottle(botID, chat, user)
	}
	p := a.securityPolicy(botID, chat)
	var puzzle securityPuzzle
	issued, err := cTx(a, func(tx *sql.Tx) (Row, error) {
		if _, e := tx.Exec("INSERT IGNORE INTO ts_bot_verifications(bot_id,chat_id,user_id,answer_hashes,payload,reply_context,expires_at) VALUES(?,?,?,'[]','{}','{}',?)", botID, chat, user, cDate(time.Now().Add(24*time.Hour))); e != nil {
			return nil, e
		}
		r, e := cOne(tx, "SELECT * FROM ts_bot_verifications WHERE bot_id=? AND chat_id=? AND user_id=? FOR UPDATE", botID, chat, user)
		if e != nil {
			return nil, e
		}
		if integer(r["blocked_until"]) > time.Now().Unix() {
			return nil, catalogErr(429, fmt.Sprintf("验证失败次数过多，请在 %d 秒后重新发送关键词。", integer(r["blocked_until"])-time.Now().Unix()))
		}
		if str(r["policy_hash"]) == str(p["hash"]) && str(r["state"]) == "pending" && integer(r["challenge_until"]) > time.Now().Unix() && (integer(r["message_id"]) > 0 || integer(r["issued_at"])+15 > time.Now().Unix()) {
			return nil, nil
		}
		if e = a.RateLimit(fmt.Sprintf("guard-issue:%d:%d:%d", botID, chat, user), 3, 60); e != nil {
			return nil, e
		}
		if e = a.securityThrottle(botID, chat, user); e != nil {
			return nil, e
		}
		puzzle, e = makeSecurityPuzzle(str(p["mode"]))
		if e != nil {
			return nil, e
		}
		challenge := randomHex(16)
		hashes := []string{}
		choices := Row{}
		for _, answer := range puzzle.Answers {
			hashes = append(hashes, hash(answer))
		}
		for _, choice := range puzzle.Choices {
			choices[choice.Key] = choice.Label
		}
		original := Row{"chat": m["chat"], "from": m["from"], "message_id": m["message_id"], "message_thread_id": m["message_thread_id"], "text": keyword}
		timestamp := time.Now().Unix()
		_, e = tx.Exec("UPDATE ts_bot_verifications SET challenge_id=?,policy_hash=?,state='pending',method=?,answer_hashes=?,payload=?,pending_query=?,reply_context=?,thread_id=?,message_id=0,attempts=0,step=0,issued_at=?,challenge_until=?,verified_until=0,blocked_until=0,expires_at=? WHERE id=?", challenge, p["hash"], puzzle.Method, encode(hashes), encode(Row{"prompt": puzzle.Prompt, "choices": choices}), keyword, encode(Row{"original_message": original}), integer(m["message_thread_id"]), timestamp, timestamp+integer(p["challenge_seconds"]), cDate(time.Now().Add(25*time.Hour)), r["id"])
		if e != nil {
			return nil, e
		}
		r["challenge_id"] = challenge
		return r, nil
	})
	if err != nil {
		return true, err
	}
	if issued == nil {
		return true, nil
	}
	text := fmt.Sprintf("搜索前请完成验证 · 仅本人可操作\n\n%s\n\n%d 秒内有效；最多答错 %d 次。通过后自动继续本次搜索。", puzzle.Prompt, integer(p["challenge_seconds"]), integer(p["max_attempts"]))
	params := securityReply(m)
	params["chat_id"] = chat
	if len(puzzle.Choices) > 0 {
		rows := [][]Row{}
		line := []Row{}
		for _, choice := range puzzle.Choices {
			line = append(line, Row{"text": choice.Label, "callback_data": "v:" + str(issued["challenge_id"]) + ":" + choice.Key})
			if len(line) == 3 {
				rows = append(rows, line)
				line = []Row{}
			}
		}
		if len(line) > 0 {
			rows = append(rows, line)
		}
		params["reply_markup"] = Row{"inline_keyboard": rows}
	} else {
		params["reply_markup"] = Row{"force_reply": true, "selective": true, "input_field_placeholder": "输入验证答案"}
	}
	method := "sendMessage"
	params["text"] = text
	if len(puzzle.Photo) > 0 {
		method = "sendPhoto"
		delete(params, "text")
		params["photo"] = puzzle.Photo
		params["caption"] = text
	}
	sent, err := a.sendReply(ctx, method, params)
	if err != nil {
		_, _ = a.DB.Exec("UPDATE ts_bot_verifications SET state='expired' WHERE id=? AND challenge_id=?", issued["id"], issued["challenge_id"])
		return true, err
	}
	_, err = a.DB.Exec("UPDATE ts_bot_verifications SET message_id=? WHERE id=? AND challenge_id=?", integer(sent["message_id"]), issued["id"], issued["challenge_id"])
	return true, err
}

func (a *App) verifySecurity(botID int64, challenge string, user, chat, message, thread int64, answer string, button bool) (Row, error) {
	return cTx(a, func(tx *sql.Tx) (Row, error) {
		r, err := cOne(tx, "SELECT * FROM ts_bot_verifications WHERE bot_id=? AND challenge_id=? FOR UPDATE", botID, challenge)
		if err != nil {
			return nil, err
		}
		reject := func(text string) (Row, error) { return Row{"status": "error", "text": text}, nil }
		if r == nil {
			return reject("验证已失效，请重新发送关键词。")
		}
		if integer(r["user_id"]) != user {
			return reject("不能操作别人的验证，请发送自己的搜索关键词。")
		}
		if integer(r["chat_id"]) != chat || integer(r["message_id"]) != message || integer(r["thread_id"]) != thread || message == 0 {
			return reject("此验证不属于当前消息或话题。")
		}
		if str(r["state"]) != "pending" {
			return reject("此验证已完成或已失效，请重新搜索。")
		}
		p := a.securityPolicy(botID, chat)
		if integer(r["challenge_until"]) <= time.Now().Unix() || str(r["policy_hash"]) != str(p["hash"]) {
			_, err = tx.Exec("UPDATE ts_bot_verifications SET state='expired' WHERE id=?", r["id"])
			if err != nil {
				return nil, err
			}
			return reject("验证已过期或规则已更新，请重新发送关键词。")
		}
		if err = a.RateLimit(fmt.Sprintf("guard-answer:%d:%d:%d", botID, chat, user), 12, 60); err != nil {
			return reject("验证操作过于频繁，请稍后重试。")
		}
		payload := decode(str(r["payload"]))
		textMethod := str(r["method"]) == "text" || str(r["method"]) == "math"
		if button == textMethod {
			return reject("请按本题提示完成验证。")
		}
		if button {
			if _, ok := object(payload["choices"])[answer]; !ok {
				return reject("请按本题提示完成验证。")
			}
		}
		hashes := []string{}
		if err = json.Unmarshal([]byte(str(r["answer_hashes"])), &hashes); err != nil {
			return reject("验证内容已失效，请重新发送关键词。")
		}
		step := int(integer(r["step"]))
		if step < 0 || step >= len(hashes) {
			return reject("验证内容已失效，请重新发送关键词。")
		}
		if subtle.ConstantTimeCompare([]byte(hashes[step]), []byte(hash(answer))) != 1 {
			attempts := integer(r["attempts"]) + 1
			state := "pending"
			blocked := int64(0)
			status := "wrong"
			text := fmt.Sprintf("答案不正确，还可尝试 %d 次。", integer(p["max_attempts"])-attempts)
			if str(r["method"]) == "sequence" {
				text += "请从第一个 emoji 重新开始。"
			}
			if attempts >= integer(p["max_attempts"]) {
				state = "failed"
				status = "failed"
				blocked = time.Now().Unix() + integer(p["cooldown_seconds"])
				text = fmt.Sprintf("答错次数已用完，请在 %d 秒后重新搜索。", integer(p["cooldown_seconds"]))
			}
			_, err = tx.Exec("UPDATE ts_bot_verifications SET attempts=?,step=0,state=?,blocked_until=? WHERE id=?", attempts, state, blocked, r["id"])
			return Row{"status": status, "text": text, "row": r}, err
		}
		if step+1 < len(hashes) {
			_, err = tx.Exec("UPDATE ts_bot_verifications SET step=? WHERE id=?", step+1, r["id"])
			return Row{"status": "progress", "text": fmt.Sprintf("已完成 %d/%d，继续点击下一个。", step+1, len(hashes))}, err
		}
		_, err = tx.Exec("UPDATE ts_bot_verifications SET state='verified',verified_until=? WHERE id=?", time.Now().Unix()+integer(p["trust_minutes"])*60, r["id"])
		return Row{"status": "passed", "text": "验证通过，正在继续搜索。", "row": r}, err
	})
}

func (a *App) finishSecurity(ctx context.Context, row Row, text string) {
	params := Row{"chat_id": row["chat_id"], "message_id": row["message_id"], "reply_markup": Row{"inline_keyboard": [][]Row{}}}
	method := "editMessageText"
	params["text"] = text
	if str(row["method"]) == "text" {
		method = "editMessageCaption"
		delete(params, "text")
		params["caption"] = text
	}
	_, _ = a.Telegram(ctx, method, params)
}
func (a *App) resumeSecurity(ctx context.Context, row Row) error {
	stored := decode(str(row["reply_context"]))
	original := object(stored["original_message"])
	if len(original) == 0 {
		// Compatibility for challenges issued before the migration.
		original = Row{"chat": Row{"id": row["chat_id"]}, "from": Row{"id": row["user_id"]}, "message_id": object(stored["reply_parameters"])["message_id"], "message_thread_id": stored["message_thread_id"]}
	}
	return a.startSearch(ctx, original, str(row["pending_query"]), "")
}
func (a *App) CaptchaCallback(ctx context.Context, cb Row, botID int64) error {
	match := regexp.MustCompile(`^v:([a-f0-9]{32}):([a-f0-9]{12})$`).FindStringSubmatch(str(cb["data"]))
	if match == nil {
		_, err := a.Telegram(ctx, "answerCallbackQuery", Row{"callback_query_id": cb["id"], "text": "验证按钮无效，请重新发送关键词。", "show_alert": true})
		return err
	}
	m := object(cb["message"])
	result, err := a.verifySecurity(botID, match[1], integer(object(cb["from"])["id"]), integer(object(m["chat"])["id"]), integer(m["message_id"]), integer(m["message_thread_id"]), match[2], true)
	if err != nil {
		return err
	}
	_, _ = a.Telegram(ctx, "answerCallbackQuery", Row{"callback_query_id": cb["id"], "text": result["text"], "show_alert": str(result["status"]) != "passed"})
	if status := str(result["status"]); status == "passed" || status == "failed" {
		a.finishSecurity(ctx, object(result["row"]), str(result["text"]))
	}
	if str(result["status"]) == "passed" {
		return a.resumeSecurity(ctx, object(result["row"]))
	}
	return nil
}
func (a *App) CaptchaText(ctx context.Context, m Row, botID int64) (bool, error) {
	chat, user := integer(object(m["chat"])["id"]), integer(object(m["from"])["id"])
	if strings.HasPrefix(strings.TrimSpace(str(m["text"])), "/") {
		return false, nil
	}
	r, err := cOne(a.DB, "SELECT * FROM ts_bot_verifications WHERE bot_id=? AND chat_id=? AND user_id=? AND state='pending'", botID, chat, user)
	if err != nil {
		return false, err
	}
	if r == nil || (str(r["method"]) != "text" && str(r["method"]) != "math") {
		return false, nil
	}
	reply := integer(object(m["reply_to_message"])["message_id"])
	if chat < 0 && reply != integer(r["message_id"]) || reply > 0 && reply != integer(r["message_id"]) {
		return false, nil
	}
	result, err := a.verifySecurity(botID, str(r["challenge_id"]), user, chat, integer(r["message_id"]), integer(m["message_thread_id"]), strings.ToUpper(strings.TrimSpace(str(m["text"]))), false)
	if err != nil {
		return true, err
	}
	status := str(result["status"])
	if status == "passed" || status == "failed" {
		a.finishSecurity(ctx, object(result["row"]), str(result["text"]))
	} else {
		if a.RateLimit(fmt.Sprintf("guard-answer-notice:%d:%d:%d", botID, chat, user), 1, 5) == nil {
			_ = a.securitySend(ctx, m, str(result["text"]))
		}
	}
	if status == "passed" {
		err = a.resumeSecurity(ctx, object(result["row"]))
	}
	return true, err
}

func securityClean(s string) string {
	return strings.ToLower(strings.Map(func(r rune) rune {
		if r >= 0x200B && r <= 0x200D || r == 0xFEFF || r == 0x2060 {
			return -1
		}
		return r
	}, s))
}
func securityAdReason(m, group Row) string {
	raw := str(m["text"])
	if raw == "" {
		raw = str(m["caption"])
	}
	text := securityClean(raw)
	mode := str(group["ad_mode"])
	if mode == "keywords" || mode == "strict" {
		for _, word := range strings.Split(str(group["ad_keywords"]), "\n") {
			word = securityClean(strings.TrimSpace(word))
			if word != "" && strings.Contains(text, word) {
				return "关键词"
			}
		}
	}
	if mode != "links" && mode != "strict" {
		return ""
	}
	urls := []string{}
	entities := append(append([]any{}, array(m["entities"])...), array(m["caption_entities"])...)
	for _, rawEntity := range entities {
		entity := object(rawEntity)
		switch str(entity["type"]) {
		case "text_link":
			urls = append(urls, str(entity["url"]))
		case "url":
			units := utf16.Encode([]rune(raw))
			offset, length := int(integer(entity["offset"])), int(integer(entity["length"]))
			if offset >= 0 && length > 0 && offset <= len(units) && length <= len(units)-offset {
				urls = append(urls, string(utf16.Decode(units[offset:offset+length])))
			}
		}
	}
	urls = append(urls, regexp.MustCompile(`(?i)(?:https?://|tg://|www\.|t\.me/|telegram\.me/)[^\s<>()]+`).FindAllString(text, -1)...)
	for _, link := range urls {
		link = securityClean(link)
		if !strings.Contains(link, "://") {
			link = "https://" + link
		}
		u, err := url.Parse(link)
		host := ""
		if err == nil {
			host = strings.TrimRight(strings.ToLower(u.Hostname()), ".")
		}
		allowed := false
		for _, domain := range strings.Split(str(group["allow_domains"]), "\n") {
			domain = strings.ToLower(strings.TrimSpace(domain))
			if domain != "" && (host == domain || strings.HasSuffix(host, "."+domain)) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "链接"
		}
	}
	return ""
}

func validateGroupSettings(input Row) (Row, error) {
	out := Row{}
	for key, value := range input {
		valid := true
		switch key {
		case "verify_mode":
			valid = securityModeNames[str(value)] != "" || str(value) == "inherit"
		case "ad_mode":
			s := str(value)
			valid = s == "off" || s == "links" || s == "keywords" || s == "strict"
		case "flood_limit":
			n := integer(value)
			valid = (n == 0 || n >= 4 && n <= 30) && regexp.MustCompile(`^\d+$`).MatchString(str(value))
			value = n
		case "message_max_chars", "join_timeout_seconds", "join_max_attempts":
			limits := map[string][2]int64{"message_max_chars": {0, 4096}, "join_timeout_seconds": {30, 3600}, "join_max_attempts": {1, 10}}
			n, bounds := integer(value), limits[key]
			valid = regexp.MustCompile(`^\d+$`).MatchString(str(value)) && n >= bounds[0] && n <= bounds[1]
			value = n
		case "welcome_enabled", "block_media", "block_forwards":
			valid = str(value) == "0" || str(value) == "1"
			value = integer(value)
		case "join_verify_mode":
			valid = securityModeNames[str(value)] != ""
		case "join_failure_action":
			valid = str(value) == "mute" || str(value) == "kick"
		case "welcome_template":
			s, ok := value.(string)
			if !ok {
				return nil, catalogErr(422, "欢迎语须为文字。")
			}
			if err := validateGroupWelcomeTemplate(s); err != nil {
				return nil, err
			}
		case "ad_keywords", "allow_domains":
			s, ok := value.(string)
			valid = ok && utf8.RuneCountInString(s) <= 2000
			lines := []string{}
			seen := map[string]bool{}
			for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
				line = strings.TrimSpace(line)
				if key == "allow_domains" {
					line = strings.ToLower(line)
				}
				if line == "" || seen[line] {
					continue
				}
				if key == "allow_domains" {
					valid = valid && regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`).MatchString(line)
				} else {
					valid = valid && utf8.RuneCountInString(line) >= 2 && utf8.RuneCountInString(line) <= 60
				}
				seen[line] = true
				lines = append(lines, line)
			}
			valid = valid && len(lines) <= 50
			value = strings.Join(lines, "\n")
		default:
			valid = false
		}
		if !valid {
			return nil, &DomainError{Status: 422, Message: "群设置格式无效，请检查字段：" + key, Fields: map[string]string{key: "请使用有效的选项、整数范围或模板标签。"}}
		}
		out[key] = value
	}
	return out, nil
}
func (a *App) saveGroupSettings(id int64, input Row, adminID, telegramUID int64) (Row, error) {
	values, err := validateGroupSettings(input)
	if err != nil {
		return nil, err
	}
	return cTx(a, func(tx *sql.Tx) (Row, error) {
		r, e := cOne(tx, "SELECT id FROM ts_bot_groups WHERE id=? FOR UPDATE", id)
		if e != nil {
			return nil, e
		}
		if r == nil {
			return nil, catalogErr(404, "群记录不存在。")
		}
		keys := []string{}
		args := []any{}
		for _, key := range []string{"verify_mode", "ad_mode", "ad_keywords", "allow_domains", "flood_limit", "message_max_chars", "join_verify_mode", "join_timeout_seconds", "join_max_attempts", "join_failure_action", "welcome_enabled", "welcome_template", "block_media", "block_forwards"} {
			if value, ok := values[key]; ok {
				keys = append(keys, key+"=?")
				args = append(args, value)
			}
		}
		var actor any
		if telegramUID > 0 {
			actor = telegramUID
		}
		keys = append(keys, "updated_by=?", "updated_at=?")
		args = append(args, actor, now(), id)
		if _, e = tx.Exec("UPDATE ts_bot_groups SET "+strings.Join(keys, ",")+" WHERE id=?", args...); e != nil {
			return nil, e
		}
		if e = cAudit(tx, adminID, "更新机器人群设置", fmt.Sprintf("群记录 #%d；Telegram 管理员 %d", id, telegramUID)); e != nil {
			return nil, e
		}
		return cOne(tx, "SELECT * FROM ts_bot_groups WHERE id=?", id)
	})
}
func (a *App) rememberSecurityGroup(botID int64, chat Row, inviter int64) (Row, error) {
	var inviterValue any
	if inviter > 0 {
		inviterValue = inviter
	}
	_, err := a.DB.Exec("INSERT IGNORE INTO ts_bot_groups(bot_id,chat_id,title,inviter_id,ad_keywords,allow_domains,created_at,updated_at) VALUES(?,?,?,?,'','',?,?)", botID, chat["id"], cText(chat["title"], 255), inviterValue, now(), now())
	if err != nil {
		return nil, err
	}
	if inviter > 0 {
		_, err = a.DB.Exec("UPDATE ts_bot_groups SET title=?,inviter_id=?,updated_at=? WHERE bot_id=? AND chat_id=?", cText(chat["title"], 255), inviter, now(), botID, chat["id"])
	} else {
		_, err = a.DB.Exec("UPDATE ts_bot_groups SET title=?,updated_at=? WHERE bot_id=? AND chat_id=?", cText(chat["title"], 255), now(), botID, chat["id"])
	}
	if err != nil {
		return nil, err
	}
	return cOne(a.DB, "SELECT * FROM ts_bot_groups WHERE bot_id=? AND chat_id=?", botID, chat["id"])
}
func (a *App) Membership(ctx context.Context, event Row, botID int64) error {
	chat, member := object(event["chat"]), object(event["new_chat_member"])
	if (str(chat["type"]) != "group" && str(chat["type"]) != "supergroup") || integer(object(member["user"])["id"]) != botID || botID == 0 {
		return nil
	}
	old, status := str(object(event["old_chat_member"])["status"]), str(member["status"])
	joined := (old == "left" || old == "kicked") && status != "left" && status != "kicked"
	inviter := int64(0)
	if joined {
		inviter = integer(object(event["from"])["id"])
	}
	group, err := a.rememberSecurityGroup(botID, chat, inviter)
	if err != nil {
		return err
	}
	canDelete := status == "administrator" && (str(chat["type"]) == "group" || boolean(member["can_delete_messages"]))
	canRestrict := status == "administrator" && str(chat["type"]) == "supergroup" && boolean(member["can_restrict_members"])
	_, err = a.DB.Exec("UPDATE ts_bot_groups SET status=?,can_delete=?,can_restrict=?,last_error='',updated_at=? WHERE id=?", status, canDelete, canRestrict, now(), group["id"])
	if err != nil {
		return err
	}
	if joined || status == "left" || status == "kicked" {
		_, err = a.DB.Exec("DELETE FROM ts_bot_verifications WHERE bot_id=? AND chat_id=?", botID, chat["id"])
		if err != nil {
			return err
		}
	}
	if joined && a.RateLimit(fmt.Sprintf("group-welcome:%d:%v", botID, chat["id"]), 1, 300) == nil {
		_, err = a.Telegram(ctx, "sendMessage", Row{"chat_id": chat["id"], "text": groupSearchWelcome(status)})
	}
	return err
}

func groupSearchWelcome(status string) string {
	text := "已加入本群。\n\n直接发送关键词，例如：服务器，即可搜索，无需输入 /search。结果会回复对应消息，翻页和筛选仅限发起搜索的人操作。"
	if status != "administrator" {
		text += "\n\n普通关键词没有回复？请机器人所有者在 BotFather 使用 /setprivacy 关闭群隐私模式，再重新添加机器人入群。群管理员身份的机器人可直接接收普通群消息。设置完成前，也可以回复本消息发送关键词。"
	}
	return text + "\n\n群主和当前管理员可发送 /groupsettings 配置群管理。\n/captcha emoji 搜索验证\n/joinverify emoji 新人入群验证\n/maxlength 200 限制消息字符数\n/welcome on 开启入群欢迎\n/ads on 拦截普通成员链接\n/flood on 开启防刷屏\n\n内容拦截需要删除消息权限；新人验证需要超级群组及限制成员权限。"
}
func (a *App) GroupCommand(ctx context.Context, m Row, botID int64, command, argument string) error {
	chat, user := integer(object(m["chat"])["id"]), integer(object(m["from"])["id"])
	if chat >= 0 {
		return a.securitySend(ctx, m, "请在要管理的群里发送 /groupsettings。仅群主及当前管理员可以配置。")
	}
	if err := a.RateLimit(fmt.Sprintf("group-config:%d:%d:%d", botID, chat, user), 6, 60); err != nil {
		return err
	}
	// Anonymous sender identities cannot be proven to be the human invoking a setting.
	if len(object(m["sender_chat"])) > 0 {
		return catalogErr(403, "请使用个人身份发送群管理命令。")
	}
	member, err := a.Telegram(ctx, "getChatMember", Row{"chat_id": chat, "user_id": user})
	if err != nil {
		return catalogErr(403, "暂时无法确认管理员身份，请将机器人设为群管理员后重试。")
	}
	if str(member["status"]) != "creator" && str(member["status"]) != "administrator" {
		return catalogErr(403, "只有本群群主和当前管理员可以修改群设置。")
	}
	row, err := a.rememberSecurityGroup(botID, object(m["chat"]), 0)
	if err != nil {
		return err
	}
	changes := Row{}
	arg := strings.ToLower(strings.TrimSpace(argument))
	switch command {
	case "maxlength":
		changes["message_max_chars"] = arg
	case "joinverify":
		changes["join_verify_mode"] = arg
	case "welcome", "media", "forwards":
		if arg != "on" && arg != "off" {
			return a.securitySend(ctx, m, "用法：/"+command+" on|off")
		}
		field := map[string]string{"welcome": "welcome_enabled", "media": "block_media", "forwards": "block_forwards"}[command]
		changes[field] = arg == "on"
	case "captcha":
		if arg == "" {
			return a.securitySend(ctx, m, "用法：/captcha off|emoji|button|text|math|sequence|random|inherit")
		}
		if !boolean(a.securitySettings()["allow_group_override"]) {
			return catalogErr(403, "总后台已统一管理群搜索验证，本群不能覆盖。")
		}
		changes["verify_mode"] = arg
	case "ads":
		if arg == "on" {
			arg = "links"
		}
		changes["ad_mode"] = arg
	case "adwords":
		if arg == "clear" {
			argument = ""
		}
		changes["ad_keywords"] = strings.ReplaceAll(strings.TrimSpace(argument), "|", "\n")
	case "allowdomain":
		if arg == "clear" {
			arg = ""
		}
		changes["allow_domains"] = strings.ReplaceAll(arg, "|", "\n")
	case "flood":
		if arg == "on" {
			arg = "6"
		} else if arg == "off" {
			arg = "0"
		}
		changes["flood_limit"] = arg
	}
	if len(changes) > 0 {
		values, e := validateGroupSettings(changes)
		if e != nil {
			return e
		}
		if e = a.checkGroupCapabilities(ctx, row, values); e != nil {
			return e
		}
		row, err = a.saveGroupSettings(integer(row["id"]), values, 0, user)
		if err != nil {
			return err
		}
	}
	p := a.securityPolicy(botID, chat)
	origin := "本群设置"
	if !boolean(a.securitySettings()["allow_group_override"]) {
		origin = "总后台统一管理"
	} else if str(row["verify_mode"]) == "inherit" {
		origin = "继承总后台"
	}
	flood := "关闭"
	if integer(row["flood_limit"]) > 0 {
		flood = fmt.Sprintf("每人 10 秒最多 %d 条，重复内容 30 秒最多 3 条", integer(row["flood_limit"]))
	}
	title := "本群安全设置"
	if len(changes) > 0 {
		title = "群设置已保存"
	}
	text := fmt.Sprintf("%s\n\n搜索验证：%s\n来源：%s\n新人验证：%s（%d 秒，最多 %d 次）\n消息字数上限：%d（0 关闭）\n入群欢迎：%t\n媒体拦截：%t，转发拦截：%t\n广告规则：%s\n防刷屏：%s\n\n/captcha emoji（off / button / text / math / sequence / random / inherit）\n/joinverify emoji（off / button / text / math / sequence / random）\n/maxlength 200（0–4096）\n/welcome on|off\n/media on|off\n/forwards on|off\n/ads on（off / links / keywords / strict）\n/adwords 关键词一|关键词二\n/allowdomain example.com|example.org\n/flood on（off 或 4–30）\n\n新人验证独立于搜索验证，禁言后在机器人私聊回答。adwords、allowdomain 使用 clear 清空。群主与管理员免拦截。", title, securityModeNames[str(p["mode"])], origin, securityModeNames[str(row["join_verify_mode"])], integer(row["join_timeout_seconds"]), integer(row["join_max_attempts"]), integer(row["message_max_chars"]), boolean(row["welcome_enabled"]), boolean(row["block_media"]), boolean(row["block_forwards"]), map[string]string{"off": "关闭", "links": "拦截链接", "keywords": "拦截关键词", "strict": "链接及关键词"}[str(row["ad_mode"])], flood)
	return a.securitySend(ctx, m, text)
}
func (a *App) Moderate(ctx context.Context, m Row, botID int64) (bool, error) {
	chat, user := integer(object(m["chat"])["id"]), integer(object(m["from"])["id"])
	if chat >= 0 || integer(m["message_id"]) == 0 || user == botID {
		return false, nil
	}
	if integer(object(m["sender_chat"])["id"]) == chat {
		return false, nil
	}
	group, err := cOne(a.DB, "SELECT * FROM ts_bot_groups WHERE bot_id=? AND chat_id=?", botID, chat)
	if err != nil {
		return false, err
	}
	if group == nil || str(group["ad_mode"]) == "off" && integer(group["flood_limit"]) == 0 && integer(group["message_max_chars"]) == 0 && !boolean(group["block_media"]) && !boolean(group["block_forwards"]) {
		return false, nil
	}
	reason := groupMessageReason(m, group)
	text := str(m["text"])
	if text == "" {
		text = str(m["caption"])
	}
	if user > 0 && integer(group["flood_limit"]) > 0 && integer(m["edit_date"]) == 0 {
		err = a.RateLimit(fmt.Sprintf("group-flood:%d:%d:%d", botID, chat, user), int(integer(group["flood_limit"])), 10)
		if err == nil && text != "" {
			err = a.RateLimit(fmt.Sprintf("group-repeat:%d:%d:%d:%s", botID, chat, user, hash(securityClean(text))), 3, 30)
		}
		if err != nil && reason == "" {
			reason = "刷屏"
		}
	}
	if reason == "" {
		return false, nil
	}
	if len(object(m["sender_chat"])) == 0 && user > 0 {
		member, e := a.Telegram(ctx, "getChatMember", Row{"chat_id": chat, "user_id": user})
		if e != nil {
			_, _ = a.DB.Exec("UPDATE ts_bot_groups SET last_error=? WHERE id=?", clip(reason+"；无法检查发言者身份，已跳过删除且不会触发搜索。", 255), group["id"])
			return true, nil
		}
		if str(member["status"]) == "creator" || str(member["status"]) == "administrator" {
			return false, nil
		}
	}
	if _, err = a.Telegram(ctx, "deleteMessage", Row{"chat_id": chat, "message_id": m["message_id"]}); err != nil {
		_, _ = a.DB.Exec("UPDATE ts_bot_groups SET last_error=? WHERE id=?", clip(reason+"；删除失败，请检查机器人管理员和删除权限。不会触发搜索。", 255), group["id"])
		return true, nil
	}
	_, err = a.DB.Exec("UPDATE ts_bot_groups SET blocked_count=blocked_count+1,last_error='' WHERE id=?", group["id"])
	return true, err
}
