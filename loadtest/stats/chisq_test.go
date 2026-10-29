package stats

import (
	"math"
	"testing"
)

// Reference critical values from standard chi-square tables.
func TestChiSquareSF(t *testing.T) {
	cases := []struct {
		x  float64
		df int
		p  float64
	}{
		{3.841459, 1, 0.05},
		{6.634897, 1, 0.01},
		{16.918978, 9, 0.05},
		{21.665994, 9, 0.01},
		{9.0, 9, 0.437274}, // median-ish region
		{2.087901, 9, 0.99},
		{124.342113, 100, 0.05},
	}
	for _, c := range cases {
		got := ChiSquareSF(c.x, c.df)
		if math.Abs(got-c.p) > 1e-4 {
			t.Errorf("ChiSquareSF(%v, %d) = %.6f, want %.6f", c.x, c.df, got, c.p)
		}
	}
}

func TestWinFrequencyPerfectlyEven(t *testing.T) {
	counts := []int{50, 50, 50, 50, 50, 50, 50, 50, 50, 50}
	p, a := WinFrequency(counts, 100, 5)
	if p.Statistic != 0 || a.Statistic != 0 || p.PValue != 1 || a.PValue != 1 {
		t.Fatalf("even counts should give chi2=0, p=1: %+v %+v", p, a)
	}
}
