package evaldesk

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
)

func loopbackHost(host string) bool {
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = hostname
	}
	host = strings.Trim(host, "[]")
	return strings.EqualFold(host, "localhost") || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
}

// Handler intentionally has no static file route, CORS grant, or write endpoint.
func Handler(store *Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !loopbackHost(r.Host) {
			apiError(w, http.StatusForbidden, "仅限本机访问")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !loopbackHost(u.Host) || (u.Scheme != "http" && u.Scheme != "https") {
				apiError(w, http.StatusForbidden, "不接受外部来源请求")
				return
			}
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			apiError(w, http.StatusMethodNotAllowed, "此工作台仅支持只读查看")
			return
		}
		query := r.URL.Query()
		switch r.URL.Path {
		case "/api/evaldesk/runs":
			writeJSON(w, store.Runs())
		case "/api/evaldesk/timeline":
			writeJSON(w, store.Timeline())
		case "/api/evaldesk/provenance":
			if query.Get("run") == "" {
				apiError(w, http.StatusBadRequest, "请选择要查看的运行")
				return
			}
			response, err := store.Provenance(query.Get("run"))
			if err != nil {
				apiError(w, http.StatusNotFound, "运行不存在或产物已移除")
				return
			}
			writeJSON(w, response)
		case "/api/evaldesk/compare":
			a, b := query.Get("baseline"), query.Get("candidate")
			if a == "" || b == "" {
				apiError(w, http.StatusBadRequest, "请选择基线和候选运行")
				return
			}
			response, err := store.Compare(a, b)
			if err != nil {
				apiError(w, http.StatusNotFound, "运行不存在或产物已移除")
				return
			}
			writeJSON(w, response)
		case "/api/evaldesk/cases":
			response, err := store.Cases(query.Get("baseline"), query.Get("candidate"), query.Get("case"))
			if err != nil {
				apiError(w, http.StatusNotFound, "题目或运行不存在")
				return
			}
			writeJSON(w, response)
		default:
			apiError(w, http.StatusNotFound, "没有此只读接口")
		}
	})
}

func writeJSON(w http.ResponseWriter, value any) { _ = json.NewEncoder(w).Encode(value) }
func apiError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	writeJSON(w, map[string]string{"error": message})
}

// ListenAddress prevents flags from accidentally publishing saved model outputs.
func ListenAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	return err == nil && port != "" && loopbackHost(host)
}
