package producthttp

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/runevents"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func (a *API) streamRunEvents(w http.ResponseWriter, r *http.Request) {
	_, run, ok := a.ownerForRun(w, r, r.PathValue("run_id"))
	if !ok {
		return
	}
	lastID := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if len(lastID) > 128 || strings.ContainsAny(lastID, "\r\n") {
		a.writeProblem(w, r, product.NewProblem("invalid_request", "Last-Event-ID 无效", 400,
			"事件游标长度或字符非法。", requestID(r)))
		return
	}
	history, exists, err := a.events.History(r.Context(), run.ID, lastID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	if !exists {
		a.writeProblem(w, r, product.NewProblem("events_expired", "运行事件已过期", 410,
			"请改为读取 Run 和 Session 的最终状态。", requestID(r)))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.writeProblem(w, r, product.NewProblem("internal_error", "服务器不支持流式响应", 500, "", requestID(r)))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	terminal := writeEvents(w, history, &lastID)
	flusher.Flush()
	if terminal || (len(history) == 0 && run.Status != store.RunRunning) {
		return
	}
	for {
		events, err := a.events.Wait(r.Context(), run.ID, lastID, 15*time.Second)
		if err != nil {
			return
		}
		if len(events) == 0 {
			_, _ = fmt.Fprintf(w, ": heartbeat %s\n\n", time.Now().UTC().Format(time.RFC3339))
			flusher.Flush()
			continue
		}
		terminal = writeEvents(w, events, &lastID)
		flusher.Flush()
		if terminal {
			return
		}
	}
}

func writeEvents(w http.ResponseWriter, events []runevents.Event, lastID *string) bool {
	terminal := false
	for _, event := range events {
		_, _ = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, event.Data)
		*lastID = event.ID
		if event.Type == "run.completed" {
			terminal = true
		}
	}
	return terminal
}
