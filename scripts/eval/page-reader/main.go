// Bounded real-page recheck. No search, model, database or product-session calls.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/planning"
)

func main() {
	dataset := flag.String("dataset", "", "original A/B dataset JSON")
	out := flag.String("out", "", "new evidence directory")
	maxReads := flag.Int("max-reads", 24, "physical request ceiling (at most 24)")
	flag.Parse()
	if *out == "" || *dataset == "" || *maxReads < 1 || *maxReads > 24 {
		panic("dataset, new out and max-reads 1..24 required")
	}
	if _, err := os.Stat(*out); !os.IsNotExist(err) {
		panic("refusing to overwrite existing results")
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		panic(err)
	}
	data, err := os.ReadFile(*dataset)
	if err != nil {
		panic(err)
	}
	var pages []struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(data, &pages); err != nil {
		panic(err)
	}
	w := planning.NewWeb(nil)
	count := 0
	for _, p := range pages {
		for _, method := range []string{"http", "browser"} {
			if count >= *maxReads {
				return
			}
			count++
			started := time.Now()
			var attempts []planning.ReadAttempt
			w.OnReadAttempt = func(a planning.ReadAttempt) { attempts = append(attempts, a) }
			page, err := w.ReadPage(context.Background(), p.URL, method)
			message := ""
			if err != nil {
				message = err.Error()
			}
			row := map[string]any{"id": p.ID, "method": method, "page": page, "error": message, "attempts": attempts, "duration_ms": time.Since(started).Milliseconds()}
			body, _ := json.MarshalIndent(row, "", "  ")
			if err := os.WriteFile(filepath.Join(*out, p.ID+"-"+method+".json"), body, 0644); err != nil {
				panic(err)
			}
			fmt.Printf("%d %s %s status=%d chars=%d elapsed_ms=%d error=%s\n", count, p.ID, method, page.HTTPStatus, len([]rune(page.Text)), time.Since(started).Milliseconds(), message)
		}
	}
}
