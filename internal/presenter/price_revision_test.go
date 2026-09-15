package presenter

import (
	"context"
	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"testing"
)

type revisionReader struct {
	fakeReader
	requested *int64
}

func (r revisionReader) PriceMetadataBySnapshotID(_ context.Context, id int64, _ []string) (map[string]store.PriceMetadata, error) {
	*r.requested = id
	return map[string]store.PriceMetadata{}, nil
}
func TestPresenterUsesPersistedPriceBatch(t *testing.T) {
	var id int64
	s := New(revisionReader{requested: &id})
	_, _, err := s.priceInfo(context.Background(), BuildRow{Quote: validate.Quote{SnapshotDate: "2026-09-14", SnapshotID: 42}})
	if err != nil || id != 42 {
		t.Fatalf("persisted batch not read: %d %v", id, err)
	}
}
