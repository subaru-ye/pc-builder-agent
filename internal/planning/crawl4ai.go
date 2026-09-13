package planning

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The endpoint is operator configuration, never model input. Its client must
// reach the local Docker service, but must never forward its bearer on redirect.
func crawlerClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{Transport: transport, Timeout: 55 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func publicPageURL(ctx context.Context, link string) error {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return fmt.Errorf("仅接受公开HTTPS资料链接（端口443）")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("网页域名解析失败")
	}
	for _, ip := range ips {
		if !ip.IP.IsGlobalUnicast() || ip.IP.IsPrivate() || ip.IP.IsLoopback() || ip.IP.IsLinkLocalUnicast() {
			return fmt.Errorf("网页地址不是公开网络地址")
		}
	}
	return nil
}

// Crawl4AI 0.9.3 additionally checks browser destinations and redirects. Do not
// enable ALLOW_INTERNAL_URLS or ALLOW_INSECURE_TLS in its deployment. The local
// precheck alone cannot prevent DNS rebinding inside the remote browser.
func (w *Web) readWithCrawler(ctx context.Context, link string) (Evidence, error) {
	if err := publicPageURL(ctx, link); err != nil {
		return Evidence{}, err
	}
	base, err := url.Parse(w.CrawlerURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || w.CrawlerToken == "" {
		return Evidence{}, fmt.Errorf("网页读取服务配置不完整")
	}
	// One requested page per tool call, no deep crawling, scripts or LLM strategy.
	// raw_markdown preserves short specification rows; aggressive pruning can
	// discard socket/power/BIOS facts and must not be the only evidence retained.
	payload := map[string]any{
		"urls": []string{link},
		"crawler_config": map[string]any{"type": "CrawlerRunConfig", "params": map[string]any{
			"stream": false, "cache_mode": map[string]any{"type": "CacheMode", "params": "bypass"}, "page_timeout": 40000,
			"wait_until": "domcontentloaded", "delay_before_return_html": 2.0,
			"word_count_threshold": 0, "excluded_tags": []string{"nav", "footer"},
		}},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(w.CrawlerURL, "/")+"/crawl", bytes.NewReader(body))
	if err != nil {
		return Evidence{}, fmt.Errorf("网页读取服务配置无效")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.CrawlerToken)
	client := w.CrawlerClient
	if client == nil {
		client = crawlerClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return Evidence{}, fmt.Errorf("网页读取服务暂时不可用或超时，已保留已有资料")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Evidence{}, fmt.Errorf("网页读取服务返回HTTP %d，已保留已有资料", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024+1))
	if err != nil || len(data) > 8*1024*1024 {
		return Evidence{}, fmt.Errorf("网页读取结果无法读取或过大")
	}
	return decodeCrawlPage(data, link)
}

func decodeCrawlPage(data []byte, requestedURL string) (Evidence, error) {
	var result struct {
		Success bool `json:"success"`
		Results []struct {
			Success    bool `json:"success"`
			StatusCode int  `json:"status_code"`
			Markdown   struct {
				Raw string `json:"raw_markdown"`
			} `json:"markdown"`
			Metadata struct {
				Title string `json:"title"`
			} `json:"metadata"`
		} `json:"results"`
	}
	if json.Unmarshal(data, &result) != nil || !result.Success || len(result.Results) != 1 {
		return Evidence{}, fmt.Errorf("网页读取结果无效或抓取失败")
	}
	page := result.Results[0]
	if !page.Success || page.StatusCode < 200 || page.StatusCode >= 300 {
		return Evidence{}, fmt.Errorf("目标网页未成功读取，已保留已有资料")
	}
	text := []rune(strings.TrimSpace(page.Markdown.Raw))
	if len(text) == 0 {
		return Evidence{}, fmt.Errorf("目标网页正文为空，可改查其他资料来源")
	}
	if len(text) > 16000 {
		text = append(text[:16000], []rune(" [正文已截断]")...)
	}
	title := page.Metadata.Title
	if title == "" {
		title = requestedURL
	}
	return Evidence{URL: requestedURL, Title: title, Text: string(text), CapturedAt: time.Now().UTC().Format(time.RFC3339), Kind: "page"}, nil
}
