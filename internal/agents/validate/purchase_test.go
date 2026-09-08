package validate

import (
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"testing"
)

func TestPurchaseMissingOwnedPriceAndQuantity(t *testing.T) {
	sel := schemas.BuildSelection{CPU: "cpu", Motherboard: "mb", Memory: "mem", SSDs: []schemas.SSDSelection{{SKU: "ssd", Quantity: 2}}, PSU: "psu", Case: "case", Cooler: "cooler"}
	prices := map[string]string{"mb": "100.00", "mem": "100.00", "ssd": "50.00", "psu": "100.00", "case": "100.00", "cooler": "100.00"}
	q := computeQuote(sel, "2026-09-08", prices)
	spec := schemas.RequirementSpec{OwnedParts: []schemas.OwnedPart{{Category: schemas.CategoryCPU, Model: "CPU"}}, BudgetBasis: "new_purchase"}
	q = WithOwnership(q, spec)
	if q.MissingCount != 1 || q.PurchaseMissingCount != 0 || *q.PurchaseTotalCNY != "600.00" || !q.Lines[0].Owned {
		t.Fatalf("quote: %+v", q)
	}
	if BudgetQuote(spec, q).MissingCount != 0 {
		t.Fatal("owned reference price blocked purchase")
	}
	spec.BudgetBasis = "full_build"
	if BudgetQuote(spec, q).MissingCount != 1 {
		t.Fatal("full build hid missing price")
	}
	delete(prices, "ssd")
	q = WithOwnership(computeQuote(sel, "2026-09-08", prices), spec)
	if q.PurchaseMissingCount != 1 {
		t.Fatal("missing purchase price not reported")
	}
}
