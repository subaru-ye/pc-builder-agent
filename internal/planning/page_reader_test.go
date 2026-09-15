package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestHTTPFirstAndExplicitBrowser(t *testing.T) {
	crawlCalls := 0
	crawler := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { crawlCalls++; fmt.Fprint(w, crawlPageFixture) }))
	defer crawler.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/shell":
			fmt.Fprint(w, `<div id="root"></div><script src="app.js"></script>`)
		case "/login":
			fmt.Fprint(w, `<title>登录</title><input type="password"><script src="app.js"></script>`)
		case "/challenge":
			fmt.Fprint(w, `<title>Just a moment</title>Verify you are human`)
		case "/limited":
			w.WriteHeader(429)
		default:
			fmt.Fprint(w, `<h1>CPU</h1><table><tr><td>插槽</td><td>AM4</td></tr></table>`)
		}
	}))
	defer server.Close()
	// Rewrite a public URL inside this test-only transport; production keeps
	// public DNS pinning. No live page or model request is made by these tests.
	client := server.Client()
	base := client.Transport
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		u := *r.URL
		copy.URL = &u
		copy.URL.Host = strings.TrimPrefix(server.URL, "https://")
		return base.RoundTrip(copy)
	})
	var attempts []ReadAttempt
	w := &Web{Client: client, CrawlerURL: crawler.URL, CrawlerToken: "test", OnReadAttempt: func(a ReadAttempt) { attempts = append(attempts, a) }}
	for _, path := range []string{"/", "/login", "/challenge", "/limited"} {
		page, err := w.Read(context.Background(), "https://1.1.1.1"+path)
		if path == "/" && (err != nil || !strings.Contains(page.Text, "AM4")) {
			t.Fatalf("%+v %v", page, err)
		}
		if path != "/" && err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	if crawlCalls != 0 {
		t.Fatal("ordinary page or access block escalated")
	}
	page, err := w.Read(context.Background(), "https://1.1.1.1/shell")
	if err != nil || crawlCalls != 1 || page.Reader != "browser" {
		t.Fatalf("shell not rendered: %+v %v", page, err)
	}
	if _, err := w.ReadPage(context.Background(), "https://1.1.1.1/spec", "browser"); err != nil || crawlCalls != 2 {
		t.Fatal(err)
	}
	if len(attempts) != 7 || attempts[3].Status != 429 || attempts[4].Outcome != "dynamic_shell" {
		t.Fatalf("bad attempt accounting: %+v", attempts)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGBKAndTableBoundaries(t *testing.T) {
	raw, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(`<title>处理器参数</title><nav>购物车</nav><table><tr><td>插槽</td><td>AM4</td></tr><tr><td>功耗</td><td>105W</td></tr></table>`))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=gbk")
		w.Write(raw)
	}))
	defer server.Close()
	w := &Web{Client: server.Client()}
	page, err := w.Read(context.Background(), server.URL)
	if err != nil || page.Title != "处理器参数" || !strings.Contains(page.Text, "插槽 | AM4") || !strings.Contains(page.Text, "\n功耗 | 105W") || strings.Contains(page.Text, "购物车") {
		t.Fatalf("%+v %v", page, err)
	}
}

func TestEvidenceWindowPersistsTailAndContinuesWithoutNetwork(t *testing.T) {
	full := strings.Repeat("导航文字\n", 4000) + "处理器插槽 AM4\n功耗 105W\n" + strings.Repeat("其他内容\n", 4000)
	x := execution{evidence: []Evidence{{ID: "source-1", Kind: "page", Text: full}}}
	call := func(payload string) map[string]any {
		return x.call(context.Background(), map[string]any{"action": "read_evidence", "payload": payload})
	}
	view := call(`{"id":"source-1","query":"处理器插槽 AM4"}`)
	if !view["query_matched"].(bool) || !strings.Contains(view["source"].(Evidence).Text, "105W") {
		t.Fatalf("tail missing: %+v", view)
	}
	var combined strings.Builder
	for offset := 0; ; {
		view = call(fmt.Sprintf(`{"id":"source-1","offset":%d}`, offset))
		combined.WriteString(view["source"].(Evidence).Text)
		if view["next_offset"] == nil {
			break
		}
		offset = view["next_offset"].(int)
	}
	if combined.String() != full || x.evidence[0].Text != full || x.result.PageCalls != 0 {
		t.Fatal("paging lost/changed source or used network")
	}
	data, _ := json.Marshal(x.evidence)
	var restored []Evidence
	if json.Unmarshal(data, &restored) != nil || restored[0].Text != full {
		t.Fatal("snapshot lost full body")
	}
}

func TestCrawlerFinalStatusAndAccessScreens(t *testing.T) {
	for _, tc := range []struct {
		status            int
		final             int
		text, title, link string
		good              bool
	}{
		{307, 200, "NH-D15 height 168 mm", "Specifications", "https://example.com/final", true},
		{307, 0, "NH-D15 height 168 mm", "Specifications", "https://example.com/final", false},
		{200, 404, "Not found", "", "https://example.com/final", false},
		{200, 200, "请输入手机号", "登录", "https://plogin.m.jd.com/", false},
		{200, 200, "![](https://example.com/image)", "", "https://example.com/", false},
		{200, 200, "access denied", "Access Denied", "https://example.com/", false},
		{200, 200, "A troubleshooting guide explains access denied errors and login configuration.", "How to fix access denied", "https://example.com/guide", true},
		{200, 200, "Your choice regarding cookies on this site. Please click 'Accept All' to accept the cookies and continue. Need more help?", "Support", "https://example.com/", false},
		{200, 200, "facts", "", "http://example.com/", false},
	} {
		data, _ := json.Marshal(map[string]any{"success": true, "results": []any{map[string]any{"success": true, "status_code": tc.status, "final_status": tc.final, "final_url": tc.link, "markdown": map[string]string{"raw_markdown": tc.text}, "metadata": map[string]string{"title": tc.title}}}})
		page, err := decodeCrawlPage(data, "https://example.com/")
		if (err == nil) != tc.good {
			t.Fatalf("case %+v error %v", tc, err)
		}
		if tc.good && (page.FinalURL != tc.link || page.HTTPStatus != 200) {
			t.Fatal("final navigation evidence not preserved")
		}
	}
}
