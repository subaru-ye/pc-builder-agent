package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type Quota interface {
	ReserveSearch(context.Context, int, int) error
}
type Web struct {
	Client          *http.Client
	Key, BaseURL    string
	Budget          int
	RequireFree     bool
	Quota           Quota
	OnSearchRequest func()
}

func NewWeb(quota Quota) *Web {
	budget, _ := strconv.Atoi(os.Getenv("SERPAPI_MONTHLY_REQUEST_BUDGET"))
	if budget <= 0 {
		budget = 240
	}
	base := os.Getenv("SERPAPI_BASE_URL")
	if base == "" {
		base = "https://serpapi.com"
	}
	return &Web{Client: safeClient(), Key: os.Getenv("SERPAPI_API_KEY"), BaseURL: base, Budget: budget, RequireFree: os.Getenv("SERPAPI_REQUIRE_FREE_PLAN") != "false", Quota: quota}
}

// DNS is checked in the actual dialer, including redirects, to prevent a fetched
// page or a model-generated URL from reaching private services or rebinding DNS.
func safeClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil {
			return nil, fmt.Errorf("网页域名解析失败")
		}
		for _, ip := range ips {
			if !ip.IP.IsGlobalUnicast() || ip.IP.IsPrivate() || ip.IP.IsLoopback() || ip.IP.IsLinkLocalUnicast() {
				return nil, fmt.Errorf("网页地址不是公开网络地址")
			}
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("网页域名无地址")
		}
		return (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	return &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 4 || r.URL.Scheme != "https" || r.URL.User != nil {
			return fmt.Errorf("不支持的网页跳转")
		}
		if len(via) > 0 && via[0].URL.Query().Has("api_key") && r.URL.Host != via[0].URL.Host {
			return fmt.Errorf("搜索服务跳转被拒绝")
		}
		return nil
	}}
}

func (w *Web) get(ctx context.Context, target string) ([]byte, error) {
	u, e := url.Parse(target)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("仅接受公开HTTPS资料链接")
	}
	r, e := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if e != nil {
		return nil, fmt.Errorf("资料链接无效")
	}
	r.Header.Set("User-Agent", "pc-builder-agent/1")
	response, e := w.Client.Do(r)
	if e != nil {
		return nil, fmt.Errorf("资料服务暂时不可用")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("资料服务返回HTTP %d", response.StatusCode)
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if e != nil || len(data) > 2*1024*1024 {
		return nil, fmt.Errorf("资料内容无法读取或过大")
	}
	return data, nil
}

func (w *Web) api(ctx context.Context, path string, q url.Values) (map[string]any, error) {
	q.Set("api_key", w.Key)
	data, e := w.get(ctx, strings.TrimRight(w.BaseURL, "/")+path+"?"+q.Encode())
	if e != nil {
		return nil, e
	}
	var result map[string]any
	if json.Unmarshal(data, &result) != nil {
		return nil, fmt.Errorf("搜索服务返回格式无效")
	}
	if result["error"] != nil {
		return nil, fmt.Errorf("搜索服务无法完成本次查询")
	}
	return result, nil
}

func (w *Web) Search(ctx context.Context, query string) ([]Evidence, error) {
	if w.Key == "" {
		return nil, fmt.Errorf("尚未配置外部搜索，可继续讨论或提供商品资料链接")
	}
	if len([]rune(query)) == 0 || len([]rune(query)) > 240 {
		return nil, fmt.Errorf("搜索词须为1至240字")
	}
	account, e := w.api(ctx, "/account.json", url.Values{})
	if e != nil {
		return nil, e
	}
	plan, _ := account["plan_name"].(string)
	if plan == "" {
		plan, _ = account["plan"].(string)
	}
	usage, ok := account["this_month_usage"].(float64)
	remaining, rok := account["total_searches_left"].(float64)
	if !rok {
		remaining, rok = account["plan_searches_left"].(float64)
	}
	if !ok || !rok || (w.RequireFree && !strings.Contains(strings.ToLower(plan), "free")) || int(usage) >= w.Budget || remaining < 1 {
		return nil, fmt.Errorf("外部搜索额度或套餐条件不满足，已保留已有资料")
	}
	if w.Quota == nil {
		return nil, fmt.Errorf("搜索额度计量服务不可用")
	}
	if e = w.Quota.ReserveSearch(ctx, int(usage), w.Budget); e != nil {
		return nil, fmt.Errorf("本月搜索额度已用完或无法核验")
	}
	if w.OnSearchRequest != nil {
		w.OnSearchRequest()
	}
	result, e := w.api(ctx, "/search.json", url.Values{"engine": {"baidu"}, "q": {query}, "device": {"mobile"}})
	if e != nil {
		return nil, e
	}
	items, _ := result["organic_results"].([]any)
	rows := []Evidence{}
	for _, item := range items {
		v, ok := item.(map[string]any)
		if !ok {
			continue
		}
		link, _ := v["link"].(string)
		title, _ := v["title"].(string)
		snippet, _ := v["snippet"].(string)
		if link != "" {
			rows = append(rows, Evidence{URL: link, Title: title, Text: snippet, CapturedAt: time.Now().UTC().Format(time.RFC3339), Kind: "search"})
		}
		if len(rows) >= 8 {
			break
		}
	}
	return rows, nil
}

func (w *Web) Read(ctx context.Context, link string) (Evidence, error) {
	data, e := w.get(ctx, link)
	if e != nil {
		return Evidence{}, e
	}
	doc, e := html.Parse(strings.NewReader(string(data)))
	if e != nil {
		return Evidence{}, fmt.Errorf("网页正文无法解析")
	}
	var chunks []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript") {
			return
		}
		if n.Type == html.TextNode {
			if s := strings.TrimSpace(n.Data); s != "" {
				chunks = append(chunks, s)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	text := []rune(strings.Join(chunks, " "))
	if len(text) > 16000 {
		text = append(text[:16000], []rune(" [正文已截断]")...)
	}
	return Evidence{URL: link, Title: link, Text: string(text), CapturedAt: time.Now().UTC().Format(time.RFC3339), Kind: "page"}, nil
}
