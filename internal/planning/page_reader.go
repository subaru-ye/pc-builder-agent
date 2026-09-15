package planning

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const maxPageBytes = 2 * 1024 * 1024
const pageWindow = 16000

// ReadAttempt counts physical reads, including failures, separately from model
// tool calls. It deliberately excludes URLs and page text from diagnostic logs.
type ReadAttempt struct {
	Method     string `json:"method"`
	DurationMS int64  `json:"duration_ms"`
	Bytes      int    `json:"bytes"`
	Status     int    `json:"status"`
	Outcome    string `json:"outcome"`
}

type pageError struct{ code, message string }

func (e *pageError) Error() string           { return e.message }
func pageFailure(code, message string) error { return &pageError{code, message} }

// Read keeps the existing entry point, but enabling the browser no longer
// makes every request render JavaScript. Automatic escalation is deliberately
// limited to empty/dynamic shells, never authentication, challenges or quotas.
func (w *Web) Read(ctx context.Context, link string) (Evidence, error) {
	return w.ReadPage(ctx, link, "auto")
}

func (w *Web) ReadPage(ctx context.Context, link, method string) (Evidence, error) {
	if method == "" {
		method = "auto"
	}
	if method != "auto" && method != "http" && method != "browser" {
		return Evidence{}, fmt.Errorf("读取方式须为auto、http或browser")
	}
	ctx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	if method == "browser" {
		return w.attemptRead(ctx, link, "browser")
	}
	page, err := w.attemptRead(ctx, link, "http")
	if err == nil || method == "http" || w.CrawlerURL == "" || ctx.Err() != nil {
		return page, err
	}
	if failure, ok := err.(*pageError); ok && failure.code == "dynamic_shell" {
		return w.attemptRead(ctx, link, "browser")
	}
	return page, err
}

func (w *Web) attemptRead(ctx context.Context, link, method string) (page Evidence, err error) {
	start := time.Now()
	defer func() {
		outcome := "ok"
		if err != nil {
			outcome = "unavailable"
			if e, ok := err.(*pageError); ok {
				outcome = e.code
			}
		}
		if w.OnReadAttempt != nil {
			w.OnReadAttempt(ReadAttempt{method, time.Since(start).Milliseconds(), page.ReadBytes, page.HTTPStatus, outcome})
		}
	}()
	if method == "browser" {
		if w.CrawlerURL == "" {
			return page, pageFailure("not_configured", "浏览器读取未配置，可继续使用已有资料或其他公开来源")
		}
		return w.readWithCrawler(ctx, link)
	}
	return w.readHTTPPage(ctx, link)
}

func (w *Web) readHTTPPage(ctx context.Context, link string) (Evidence, error) {
	page := Evidence{URL: link, Reader: "http", Kind: "page", CapturedAt: time.Now().UTC().Format(time.RFC3339)}
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return page, pageFailure("unsafe_url", "仅接受公开HTTPS资料链接")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return page, pageFailure("unsafe_url", "资料链接无效")
	}
	req.Header.Set("User-Agent", "pc-builder-agent/1")
	client := w.Client
	if client == nil {
		client = safeClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return page, pageFailure("network", "资料服务暂时不可用或超时；可改查其他公开来源")
	}
	defer resp.Body.Close()
	page.HTTPStatus = resp.StatusCode
	page.FinalURL = resp.Request.URL.String()
	if resp.StatusCode != http.StatusOK {
		return page, pageFailure("http_status", fmt.Sprintf("资料服务返回HTTP %d，不自动重试浏览器", resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
	page.ReadBytes = len(data)
	if err != nil || len(data) > maxPageBytes {
		return page, pageFailure("too_large", "资料内容无法读取或超过2MiB上限")
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	if !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml") && !strings.Contains(contentType, "text/plain") {
		return page, pageFailure("unsupported", "当前读取支持HTML或纯文本，请改查公开网页资料")
	}
	decoded, err := charset.NewReader(strings.NewReader(string(data)), contentType)
	if err != nil {
		return page, pageFailure("encoding", "网页字符集无法解析")
	}
	utf8, err := io.ReadAll(io.LimitReader(decoded, maxPageBytes+1))
	if err != nil || len(utf8) > maxPageBytes {
		return page, pageFailure("too_large", "解码后的正文超过2MiB上限")
	}
	if strings.Contains(contentType, "text/plain") {
		page.Title, page.Text = link, strings.TrimSpace(string(utf8))
		return page, checkPageContent(page, false, false)
	}
	page.Title, page.Text, err = extractHTML(string(utf8))
	if err != nil {
		return page, err
	}
	if page.Title == "" {
		page.Title = link
	}
	lower := strings.ToLower(string(utf8))
	password := strings.Contains(lower, `type="password"`) || strings.Contains(lower, `type='password'`)
	visible := strings.ToLower(strings.TrimSpace(page.Text))
	dynamic := strings.Contains(lower, "<script") && (visible == "" || visible == "loading..." || visible == "加载中" || (len([]rune(visible)) < 300 && strings.Contains(visible, "enable javascript")))
	return page, checkPageContent(page, password, dynamic)
}

// Keep paragraph and table-cell boundaries, including short specification rows.
// We do not drop arbitrary short text or restrict extraction to one guessed div.
func extractHTML(source string) (string, string, error) {
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return "", "", pageFailure("parse", "网页正文无法解析")
	}
	var body, title strings.Builder
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inTitle bool) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "nav", "footer", "svg":
				return
			}
			if n.Data == "title" {
				inTitle = true
			}
			switch n.Data {
			case "p", "div", "section", "article", "h1", "h2", "h3", "li", "tr", "br":
				body.WriteString("\n")
			}
		}
		if n.Type == html.TextNode {
			s := strings.TrimSpace(n.Data)
			if s != "" {
				if inTitle {
					title.WriteString(s + " ")
				} else {
					body.WriteString(s + " ")
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inTitle)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "td", "th":
				body.WriteString("| ")
			case "p", "div", "li", "tr", "section", "article":
				body.WriteString("\n")
			}
		}
	}
	walk(doc, false)
	lines := []string{}
	for _, line := range strings.Split(body.String(), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			lines = append(lines, strings.TrimRight(line, " \t\r"))
		}
	}
	return strings.TrimSpace(title.String()), strings.TrimSpace(strings.Join(lines, "\n")), nil
}

func checkPageContent(page Evidence, password, dynamic bool) error {
	text := strings.ToLower(strings.TrimSpace(page.Text))
	title := strings.ToLower(page.Title)
	u, _ := url.Parse(page.FinalURL)
	host := ""
	if u != nil {
		host = strings.ToLower(u.Hostname())
	}
	// Detect clear access screens, not every article mentioning login/captcha.
	blocked := []string{"access denied", "request rejected", "just a moment", "security verification", "verify you are human", "checking your browser", "访问被拒绝", "安全验证", "人机验证"}
	for _, s := range blocked {
		if strings.HasPrefix(title, s) || (len([]rune(text)) < 1200 && strings.HasPrefix(strings.TrimLeft(text, "# \t\r\n"), s)) {
			return pageFailure("access_blocked", "网页返回访问验证或拒绝页面；不重试绕过，请使用其他公开来源")
		}
	}
	if password || strings.HasPrefix(host, "login.") || strings.HasPrefix(host, "passport.") || strings.HasPrefix(host, "plogin.") || ((strings.Contains(title, "登录") || strings.Contains(title, "sign in")) && len([]rune(text)) < 1200) {
		return pageFailure("login", "网页需要登录，未读取商品正文；请使用其他公开资料")
	}
	if len([]rune(text)) < 1200 && (strings.Contains(text, "accept the cookies and continue") || strings.Contains(text, "enable cookies to continue")) {
		return pageFailure("consent_wall", "网页仅返回Cookie同意提示，尚无目标正文；请使用其他公开资料")
	}
	if dynamic {
		return pageFailure("dynamic_shell", "网页仅有动态加载入口，可尝试浏览器读取")
	}
	if text == "" {
		return pageFailure("empty", "网页正文为空，请使用其他公开来源")
	}
	// A Markdown image is not readable evidence, even when it has a long URL.
	visible := text
	for strings.HasPrefix(strings.TrimSpace(visible), "![") {
		end := strings.Index(visible, ")")
		if end < 0 {
			break
		}
		visible = strings.TrimSpace(visible[end+1:])
	}
	if visible == "" {
		return pageFailure("empty", "网页只有图片入口，没有可读取正文")
	}
	return nil
}

// The complete bounded text stays in the server evidence snapshot. Model views
// are windows of that same text; offsets count Unicode characters, not bytes.
func evidenceWindow(e Evidence, query string, offset, limit int) map[string]any {
	text := []rune(e.Text)
	if limit <= 0 || limit > pageWindow {
		limit = pageWindow
	}
	if offset < 0 {
		offset = 0
	}
	matched := false
	if query != "" && offset == 0 {
		best, position := 0, 0
		terms := strings.Fields(strings.ToLower(query))
		for pos := 0; pos < len(text); {
			end := pos
			for end < len(text) && text[end] != '\n' {
				end++
			}
			line, score := strings.ToLower(string(text[pos:end])), 0
			for _, term := range terms {
				if strings.Contains(line, term) {
					score++
				}
			}
			if score > best {
				best, position = score, pos
			}
			pos = end + 1
		}
		if best > 0 {
			matched = true
			offset = max(0, position-1000)
		}
	}
	if offset > len(text) {
		offset = len(text)
	}
	end := min(len(text), offset+limit)
	e.Text = string(text[offset:end])
	var next any
	if end < len(text) {
		next = end
	}
	return map[string]any{"source": e, "offset": offset, "next_offset": next, "total_chars": len(text), "truncated": offset > 0 || end < len(text), "query_matched": matched}
}
