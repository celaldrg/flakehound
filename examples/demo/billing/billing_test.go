package billing

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

// Talks to a "payments sandbox" that sometimes refuses connections.
func TestChargeCard(t *testing.T) {
	if rand.IntN(100) < 22 {
		t.Fatalf("dial tcp 127.0.0.1:%d: connect: connection refused", 40000+rand.IntN(9000))
	}
}

func TestRefund(t *testing.T) {
	if rand.IntN(100) < 6 {
		t.Fatalf("dial tcp 127.0.0.1:%d: connect: connection refused", 40000+rand.IntN(9000))
	}
}

// Fails on every run: a real bug, not a flake.
func TestLegacyExport(t *testing.T) {
	t.Fatal(fmt.Sprintf("export format v%d is not supported", 1))
}

func TestInvoiceTotal(t *testing.T)   {}
func TestTaxRounding(t *testing.T)    {}
func TestCurrencyFormat(t *testing.T) {}
func TestProration(t *testing.T)      {}
func TestDiscounts(t *testing.T)      {}
