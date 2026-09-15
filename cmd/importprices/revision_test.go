package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSameDateRequiresExplicitImmutableRevision(t *testing.T) {
	conn := setupConn(t)
	ctx := context.Background()
	day := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	a := priceBatch{SnapshotDate: day, FileSHA256: strings.Repeat("a", 64), Rows: []priceRow{{SKU: "cpu-a", PriceCNY: "100", Source: "manual", ObservedAt: day}}}
	if _, err := importPrices(ctx, conn, a); err != nil {
		t.Fatal(err)
	}
	b := a
	b.FileSHA256 = strings.Repeat("b", 64)
	b.Rows = []priceRow{{SKU: "cpu-a", PriceCNY: "200", Source: "manual", ObservedAt: day}}
	if _, err := importPrices(ctx, conn, b); err == nil {
		t.Fatal("implicit revision accepted")
	}
	b.AllowRevision = true
	if _, err := importPrices(ctx, conn, b); err != nil {
		t.Fatal(err)
	}
	if skipped, err := importPrices(ctx, conn, b); err != nil || !skipped {
		t.Fatalf("retry not idempotent: %v %v", skipped, err)
	}
	var count int
	var total string
	if err := conn.QueryRow(ctx, `SELECT count(*),sum(p.price_cny)::text FROM prices p JOIN price_snapshots s ON s.id=p.snapshot_id WHERE s.snapshot_date=$1`, day).Scan(&count, &total); err != nil {
		t.Fatal(err)
	}
	if count != 2 || total != "300.00" {
		t.Fatalf("old batch replaced: %d %s", count, total)
	}
}
