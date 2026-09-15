package planning

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Original 2026-09-14 A/B bytes, not rewritten model output or fabricated pages.
// Large raw evidence stays in the separately hashed evaluation archive.
func TestRecordedPageReaderLosses(t *testing.T) {
	root := os.Getenv("PAGE_READER_RECORDINGS")
	if root == "" {
		t.Skip("set PAGE_READER_RECORDINGS to the original A/B artifact directory")
	}
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, name, "response-0.body"))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	for _, tc := range []struct {
		id, query string
		want      []string
	}{
		{"noctua", "Height Weight", []string{"168 mm", "160 mm", "1320 g", "980 g", "6 years"}},
		{"zol", "插槽类型 Socket AM4", []string{"Socket AM4", "105W", "DDR4", "仅供参考"}},
	} {
		t.Run(tc.id, func(t *testing.T) {
			page, err := decodeCrawlPage(read("first-"+tc.id+"-B"), "https://example.com/spec")
			if err != nil {
				t.Fatal(err)
			}
			view := evidenceWindow(page, tc.query, 0, 16000)["source"].(Evidence)
			for _, fact := range tc.want {
				if !strings.Contains(view.Text, fact) {
					t.Errorf("recorded fact missing from model window: %s", fact)
				}
			}
			if tc.id == "noctua" && (page.HTTPStatus != 200 || !strings.Contains(page.FinalURL, "/products/nh-d15/")) {
				t.Fatal("observed redirect ignored")
			}
		})
	}
	raw := read("first-zol-A")
	w := &Web{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html; charset=GBK"}}, Body: io.NopCloser(bytes.NewReader(raw)), Request: r}, nil
	})}}
	page, err := w.Read(context.Background(), "https://detail.zol.com.cn/1341/1340449/param.shtml")
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range []string{"插槽类型", "Socket AM4", "105W", "DDR4", "仅供参考"} {
		if !strings.Contains(page.Text, fact) {
			t.Errorf("GBK recording lost %s", fact)
		}
	}
	for _, id := range []string{"jd", "taobao"} {
		if _, err := decodeCrawlPage(read("first-"+id+"-B"), "https://example.com/"); err == nil {
			t.Errorf("accepted recorded %s access screen", id)
		}
	}
}

func TestLiveRecheckSavedWindows(t *testing.T) {
	root := os.Getenv("PAGE_READER_RECHECK")
	if root == "" {
		t.Skip("set PAGE_READER_RECHECK to the new saved recheck directory")
	}
	for _, tc := range []struct {
		id, query string
		facts     []string
	}{
		{"kingston", "cannot mix DDR4 DDR5", []string{"cannot mix DDR4 and DDR5", "1.2V", "1.1V", "64-bit", "32-bit", "compatible motherboard and processor"}},
		{"amd", "Default TDP", []string{"AM4", "105W", "Discrete Graphics Card Required", "3200 MT/s", "11/5/2020"}},
		{"zol", "插槽类型 Socket AM4", []string{"Socket AM4", "105W", "DDR4", "仅供参考"}},
		{"asus", "FlashBack", []string{"FAT32", ".CAP", "five seconds", "11th"}},
		{"msi-spec", "ECC UDIMM", []string{"128GB", "non-ECC mode", "M.2_2, PCI_E3", "processor with integrated graphics"}},
		{"noctua", "Height Weight", []string{"168 mm", "160 mm", "1320 g", "980 g", "6 years"}},
	} {
		t.Run(tc.id, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, tc.id+"-browser.json"))
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Page  Evidence
				Error string
			}
			if err = json.Unmarshal(data, &result); err != nil || result.Error != "" {
				t.Fatalf("read failed %v %s", err, result.Error)
			}
			view := evidenceWindow(result.Page, tc.query, 0, 16000)["source"].(Evidence)
			// Kingston's compatibility answer and voltage/channel table are
			// separated. A second local evidence query retrieves the table;
			// it does not fetch the webpage again or silently widen a window.
			if tc.id == "kingston" {
				view.Text += evidenceWindow(result.Page, "Operating voltage", 0, 16000)["source"].(Evidence).Text
			}
			for _, fact := range tc.facts {
				if !strings.Contains(view.Text, fact) {
					t.Errorf("live recorded fact missing from selected window: %s", fact)
				}
			}
		})
	}
	data, err := os.ReadFile(filepath.Join(root, "msi-support-browser.json"))
	if err != nil {
		t.Fatal(err)
	}
	var support struct{ Page Evidence }
	if json.Unmarshal(data, &support) != nil {
		t.Fatal("invalid recording")
	}
	if err := checkPageContent(support.Page, false, false); err == nil {
		t.Fatal("cookie-only body accepted as support evidence")
	}
}
