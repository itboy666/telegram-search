package app

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

func nodeAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func nodeClass(n *html.Node, class string) bool {
	for _, s := range strings.Fields(nodeAttr(n, "class")) {
		if s == class {
			return true
		}
	}
	return false
}
func findNode(n *html.Node, predicate func(*html.Node) bool) *html.Node {
	if predicate(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findNode(c, predicate); found != nil {
			return found
		}
	}
	return nil
}
func classNode(n *html.Node, class string) *html.Node {
	if n == nil {
		return nil
	}
	return findNode(n, func(n *html.Node) bool { return nodeClass(n, class) })
}
func nodeText(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Data == "br" {
			b.WriteString(" ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}
func parseTelegramMetadata(body, link string) (Row, error) {
	doc, e := html.Parse(strings.NewReader(body))
	if e != nil {
		return nil, e
	}
	u, _ := url.Parse(link)
	path := strings.Trim(u.Path, "/")
	title, desc, kind := "", "", "other"
	bad := func() (Row, error) {
		return nil, &DomainError{Status: 422, Message: "无法获取该链接的公开标题，请确认链接有效且内容可公开访问。"}
	}
	_, invite := telegramInviteURL(link)
	if !invite && strings.Contains(path, "/") {
		n := findNode(doc, func(n *html.Node) bool {
			return nodeClass(n, "tgme_widget_message") && strings.EqualFold(nodeAttr(n, "data-post"), path)
		})
		if n == nil || classNode(n, "tgme_widget_message_error") != nil {
			return bad()
		}
		desc = nodeText(classNode(n, "tgme_widget_message_text"))
		switch {
		case classNode(n, "tgme_widget_message_video_player") != nil:
			kind = "video"
		case classNode(n, "tgme_widget_message_audio") != nil:
			kind = "audio"
		case classNode(n, "tgme_widget_message_document") != nil:
			kind = "file"
		case classNode(n, "tgme_widget_message_photo_wrap") != nil:
			kind = "article"
		case desc != "":
			kind = "article"
		default:
			return bad()
		}
		for _, class := range []string{"tgme_widget_message_document_title", "tgme_widget_message_audio_title", "tgme_widget_message_text", "tgme_widget_message_owner_name"} {
			if title = nodeText(classNode(n, class)); title != "" {
				break
			}
		}
	} else {
		n := classNode(doc, "tgme_page_title")
		if n == nil {
			return bad()
		}
		title = nodeText(n)
		meta := findNode(doc, func(n *html.Node) bool { return n.Data == "meta" && nodeAttr(n, "property") == "og:title" })
		if meta != nil && nodeAttr(meta, "content") != "" {
			title = nodeAttr(meta, "content")
		}
		desc = nodeText(classNode(doc, "tgme_page_description"))
		extra := strings.ToLower(nodeText(classNode(doc, "tgme_page_extra")))
		switch {
		case !invite && (strings.Contains(body, "tgme_action_bot") || strings.Contains(body, `data-bot`) || strings.Contains(body, "Send Message") && strings.HasSuffix(strings.ToLower(path), "bot")):
			kind = "bot"
		case strings.Contains(extra, "subscriber"):
			kind = "channel"
		case strings.Contains(extra, "member"):
			kind = "group"
		}
	}
	if strings.TrimSpace(title) == "" {
		return bad()
	}
	return Row{"title": clip(title, 180), "description": clip(desc, 5000), "type": kind, "url": link}, nil
}
func (a *App) submitTelegram(ctx context.Context, userID int64, raw string) (Row, error) {
	link, e := NormalizeResourceURL(raw, "other")
	if e != nil {
		return nil, e
	}
	if str(a.Settings()["submissions_enabled"]) != "1" {
		return nil, catalogErr(403, "暂未开放资源提交，请稍后再来。")
	}
	u, e := one(a.DB, "SELECT * FROM ts_users WHERE id=?", userID)
	if e != nil {
		return nil, e
	}
	if u == nil || !boolean(u["status"]) {
		return nil, &DomainError{Status: 403, Message: "当前账号已停用。"}
	}
	if !boolean(u["submission_allowed"]) {
		return nil, &DomainError{Status: 403, Message: "你已被禁止提交收录。" + str(u["submission_note"])}
	}
	for _, table := range []string{"resources", "submissions"} {
		r, e := one(a.DB, "SELECT * FROM ts_"+table+" WHERE url_hash=?", hash(link))
		if e != nil {
			return nil, e
		}
		if r != nil {
			return nil, &DomainError{Status: 409, Message: catalogDuplicateMessage(r, table == "submissions")}
		}
	}
	_, invite := telegramInviteURL(link)
	meta, e := a.fetchTelegramMetadata(ctx, link)
	if e != nil {
		if !invite || ctx.Err() != nil {
			return nil, e
		}
		meta = Row{"title": "Telegram 私人邀请", "type": "other", "url": link}
	}
	return a.Submit(userID, meta)
}

func (a *App) fetchTelegramMetadata(ctx context.Context, link string) (Row, error) {
	preview := link
	if strings.Count(strings.TrimPrefix(link, "https://t.me"), "/") == 2 {
		preview += "?embed=1&mode=tme"
	}
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	if a.HTTP != nil {
		client.Transport = a.HTTP.Transport
	}
	req, e := http.NewRequestWithContext(ctx, "GET", preview, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 StarIndex/1.0")
	res, e := client.Do(req)
	if e != nil {
		return nil, &DomainError{Status: 503, Message: "暂时无法获取链接标题，请稍后重新发送。"}
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, &DomainError{Status: 422, Message: "链接没有可访问的公开页面。"}
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if e != nil {
		return nil, e
	}
	return parseTelegramMetadata(string(b), link)
}
