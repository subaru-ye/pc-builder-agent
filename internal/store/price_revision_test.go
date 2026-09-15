package store

import (
	"context"
	"reflect"
	"testing"
)

func TestSameDaySnapshotKeepsHistoricalMetadata(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	old, err := s.LatestSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prices, err := s.PricesBySnapshot(ctx, old.ID)
	if err != nil || len(prices) == 0 {
		t.Fatalf("missing fixture: %v", err)
	}
	sku := prices[0].SKU
	before, err := s.PriceMetadataBySnapshotDate(ctx, old.SnapshotDate.Format("2006-01-02"), []string{sku})
	if err != nil {
		t.Fatal(err)
	}
	var revisedID int64
	err = s.pool.QueryRow(ctx, `INSERT INTO price_snapshots(snapshot_date,file_sha256) VALUES($1,'revision-test') RETURNING id`, old.SnapshotDate).Scan(&revisedID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO prices(snapshot_id,sku,price_cny,source,observed_at) VALUES($1,$2,1,'revision','2026-07-28')`, revisedID, sku)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := s.LatestSnapshot(ctx)
	if err != nil || latest.ID != revisedID {
		t.Fatalf("latest revision lost: %+v %v", latest, err)
	}
	legacy, err := s.SnapshotByDate(ctx, old.SnapshotDate)
	if err != nil || legacy.ID != old.ID {
		t.Fatalf("legacy date changed: %+v %v", legacy, err)
	}
	after, err := s.PriceMetadataBySnapshotDate(ctx, old.SnapshotDate.Format("2006-01-02"), []string{sku})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("legacy metadata changed: %+v %v", after, err)
	}
	revised, err := s.PriceMetadataBySnapshotID(ctx, revisedID, []string{sku})
	if err != nil || revised[sku].ObservedAt == nil {
		t.Fatalf("revision metadata missing: %+v %v", revised, err)
	}
	original, err := s.PricesBySnapshot(ctx, old.ID)
	if err != nil || original[0].PriceCNY != prices[0].PriceCNY {
		t.Fatal("old price overwritten")
	}
}
