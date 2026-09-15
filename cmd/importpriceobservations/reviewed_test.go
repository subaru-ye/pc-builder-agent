package main

import (
	"context"
	"fmt"
	"testing"
)

func TestReviewedQuoteCannotClaimStock(t *testing.T) {
	_, rows, _ := priceFixture("2026-09-14")
	row := rows[0]
	row.CollectorID, row.PriceType = "maishou_reviewed", "listing"
	row.StockStatus, row.AvailabilityBasis = "unknown", "unknown"
	if err := validateObservation(row); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*observation){
		func(r *observation) { r.StockStatus = "in_stock" },
		func(r *observation) { r.PriceType = "regular" },
		func(r *observation) { r.AvailabilityBasis = "confirmed_stock" },
		func(r *observation) { r.VariantMatch = "unknown" },
	} {
		changed := row
		mutate(&changed)
		if validateObservation(changed) == nil {
			t.Fatal("unsupported aggregate claim accepted")
		}
	}
}

func TestReviewedSameDayReleaseIsManualAndImmutable(t *testing.T) {
	conn := setupPriceConn(t)
	ctx := context.Background()
	first, rows, selected := priceFixture("2026-09-14")
	if _, err := importRelease(ctx, conn, "", first, rows, selected); err != nil {
		t.Fatal(err)
	}
	second, rows2, selected2 := priceFixture("2026-09-14")
	second.ManifestSHA256 = fmt.Sprintf("%064d", 51)
	second.SelectionSHA = fmt.Sprintf("%064d", 31)
	second.InputSHA256 = fmt.Sprintf("%064d", 11)
	rows2[0].ID = fmt.Sprintf("%064d", 61)
	rows2[0].CollectorID, rows2[0].PriceType = "maishou_reviewed", "listing"
	rows2[0].StockStatus, rows2[0].AvailabilityBasis = "unknown", "unknown"
	selected2[0].ObservationID, selected2[0].PriceType = &rows2[0].ID, "listing"
	second.Policy = "automatic"
	if _, err := importRelease(ctx, conn, "", second, rows2, selected2); err == nil {
		t.Fatal("automatic publication accepted")
	}
	second.Policy = "manual"
	if _, err := importRelease(ctx, conn, "", second, rows2, selected2); err != nil {
		t.Fatal(err)
	}
	if skipped, err := importRelease(ctx, conn, "", second, rows2, selected2); err != nil || !skipped {
		t.Fatalf("retry: %v %v", skipped, err)
	}
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM price_snapshots WHERE snapshot_date='2026-09-14'").Scan(&count); err != nil || count != 2 {
		t.Fatalf("snapshots %d: %v", count, err)
	}
}
