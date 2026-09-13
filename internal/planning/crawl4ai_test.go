package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const crawlPageFixture = `{"success":true,"results":[{"success":true,"status_code":200,"metadata":{"title":"CPU 规格"},"markdown":{"raw_markdown":"# CPU\n\n| 插槽 | 功耗 |\n| AM4 | 105W |","fit_markdown":"CPU"}}]}`

func TestCrawlPageToolContract(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/crawl" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			URLs   []string `json:"urls"`
			Config struct {
				Type   string         `json:"type"`
				Params map[string]any `json:"params"`
			} `json:"crawler_config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		cache, _ := body.Config.Params["cache_mode"].(map[string]any)
		if len(body.URLs) != 1 || body.URLs[0] != "https://1.1.1.1/spec" || body.Config.Type != "CrawlerRunConfig" || cache["type"] != "CacheMode" || cache["params"] != "bypass" {
			t.Errorf("unexpected config %+v", body)
		}
		for _, forbidden := range []string{"extraction_strategy", "deep_crawl_strategy", "js_code", "llm_config"} {
			if _, ok := body.Config.Params[forbidden]; ok {
				t.Errorf("unexpected %s", forbidden)
			}
		}
		fmt.Fprint(w, crawlPageFixture)
	}))
	defer server.Close()
	w := &Web{CrawlerURL: server.URL, CrawlerToken: "test-token"}
	page, err := w.Read(context.Background(), "https://1.1.1.1/spec")
	if err != nil || calls != 1 || page.Title != "CPU 规格" || page.Kind != "page" || page.CapturedAt == "" || !strings.Contains(page.Text, "105W") || page.URL != "https://1.1.1.1/spec" {
		t.Fatalf("page=%+v calls=%d err=%v", page, calls, err)
	}
	for _, link := range []string{"https://127.0.0.1/", "https://[::1]/", "https://192.168.1.1/", "file:///etc/passwd", "http://1.1.1.1/", "https://u:p@1.1.1.1/", "https://1.1.1.1:8082/"} {
		if _, err := w.Read(context.Background(), link); err == nil {
			t.Errorf("accepted %s", link)
		}
	}
	if calls != 1 {
		t.Fatal("unsafe page forwarded to crawler")
	}
}

func TestCrawlFailuresStayToolFailures(t *testing.T) {
	for _, payload := range []string{
		`not JSON`, `{"success":false,"results":[]}`, `{"success":true,"results":[]}`,
		`{"success":true,"results":[{"success":false,"status_code":200}]}`,
		`{"success":true,"results":[{"success":true,"status_code":403,"markdown":{"raw_markdown":"denied"}}]}`,
		`{"success":true,"results":[{"success":true,"status_code":200,"markdown":{"raw_markdown":" "}}]}`,
	} {
		if _, err := decodeCrawlPage([]byte(payload), "https://example.com"); err == nil {
			t.Fatalf("accepted %s", payload)
		}
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(crawlPageFixture), &payload)
	row := payload["results"].([]any)[0].(map[string]any)
	row["markdown"].(map[string]any)["raw_markdown"] = strings.Repeat("中", 17000)
	data, _ := json.Marshal(payload)
	page, err := decodeCrawlPage(data, "https://example.com")
	if err != nil || !strings.HasSuffix(page.Text, " [正文已截断]") || len([]rune(page.Text)) > 16100 {
		t.Fatalf("truncation err=%v", err)
	}
}

func TestCrawlerRedirectDoesNotLeakToken(t *testing.T) {
	forwarded := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	w := &Web{CrawlerURL: server.URL, CrawlerToken: "test-token"}
	if _, err := w.Read(context.Background(), "https://1.1.1.1/spec"); err == nil || forwarded {
		t.Fatal("redirect followed or accepted")
	}
}

func TestCrawlerCanceledRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &Web{CrawlerURL: "http://127.0.0.1:1", CrawlerToken: "test-token"}
	if _, err := w.Read(ctx, "https://1.1.1.1/spec"); err == nil {
		t.Fatal("canceled request accepted")
	}
}
