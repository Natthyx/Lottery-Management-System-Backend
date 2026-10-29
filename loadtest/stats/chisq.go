// Package stats has the chi-square helpers used by the L5 fairness check.
// It is stdlib-only so the main module gains no dependencies.
package stats

import (
	"fmt"
	"math"
)

// ChiSquareSF returns P(X >= x) for a chi-square distribution with df
// degrees of freedom, i.e. the upper-tail p-value. It is the regularized
// upper incomplete gamma Q(df/2, x/2), computed with the series / continued
// fraction split from Numerical Recipes (gammq).
func ChiSquareSF(x float64, df int) float64 {
	if x <= 0 {
		return 1
	}
	return gammaQ(float64(df)/2, x/2)
}

func gammaQ(a, x float64) float64 {
	if x < a+1 {
		return 1 - gammaPSeries(a, x)
	}
	return gammaQContinuedFraction(a, x)
}

func gammaPSeries(a, x float64) float64 {
	lg, _ := math.Lgamma(a)
	ap, sum, del := a, 1/a, 1/a
	for n := 0; n < 10000; n++ {
		ap++
		del *= x / ap
		sum += del
		if math.Abs(del) < math.Abs(sum)*1e-15 {
			break
		}
	}
	return sum * math.Exp(-x+a*math.Log(x)-lg)
}

func gammaQContinuedFraction(a, x float64) float64 {
	const tiny = 1e-300
	lg, _ := math.Lgamma(a)
	b := x + 1 - a
	c := 1 / tiny
	d := 1 / b
	h := d
	for i := 1; i < 10000; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = b + an/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < 1e-15 {
			break
		}
	}
	return math.Exp(-x+a*math.Log(x)-lg) * h
}

// Result is one chi-square test.
type Result struct {
	Name      string
	Statistic float64
	DF        int
	PValue    float64
	Observed  []int
	Expected  float64
	Note      string
}

func (r Result) String() string {
	return fmt.Sprintf("%s: chi2=%.4f df=%d p=%.4f expected_per_cell=%.1f observed=%v\n  note: %s",
		r.Name, r.Statistic, r.DF, r.PValue, r.Expected, r.Observed, r.Note)
}

// WinFrequency tests per-participant win counts after `draws` draws of
// `winners` out of `k` participants.
//
// It reports two statistics:
//   - Pearson: the textbook sum (O-E)^2/E with df=k-1. This assumes the
//     cells are multinomial, which is NOT true here: each draw picks
//     `winners` DISTINCT participants, so counts are less variable than
//     multinomial and Pearson is conservative (too-large p-values).
//   - Adjusted: the same sum divided by the correct null variance
//     draws*p*(1-p)*k/(k-1), p = winners/k. Under H0 (uniform k-subsets)
//     this is asymptotically chi-square with k-1 df.
func WinFrequency(counts []int, draws, winners int) (pearson, adjusted Result) {
	k := len(counts)
	p := float64(winners) / float64(k)
	e := float64(draws) * p
	var ss float64
	for _, o := range counts {
		ss += (float64(o) - e) * (float64(o) - e)
	}
	pearsonStat := ss / e
	adjStat := ss / (float64(draws) * p * (1 - p) * float64(k) / float64(k-1))
	pearson = Result{
		Name: "win frequency, textbook Pearson", Statistic: pearsonStat, DF: k - 1,
		PValue: ChiSquareSF(pearsonStat, k-1), Observed: counts, Expected: e,
		Note: "assumes multinomial cells; conservative here because each draw picks distinct winners",
	}
	adjusted = Result{
		Name: "win frequency, variance-adjusted", Statistic: adjStat, DF: k - 1,
		PValue: ChiSquareSF(adjStat, k-1), Observed: counts, Expected: e,
		Note: "divides by the exact null variance of a uniform k-subset draw; asymptotically chi2(k-1)",
	}
	return pearson, adjusted
}

// Uniform is a standard Pearson goodness-of-fit test against equal cell
// probabilities. Used for "who got rank 1", where each draw contributes
// exactly one count, so the multinomial assumption holds exactly.
func Uniform(name string, counts []int) Result {
	k := len(counts)
	var n int
	for _, c := range counts {
		n += c
	}
	e := float64(n) / float64(k)
	var stat float64
	for _, o := range counts {
		stat += (float64(o) - e) * (float64(o) - e) / e
	}
	return Result{
		Name: name, Statistic: stat, DF: k - 1, PValue: ChiSquareSF(stat, k-1),
		Observed: counts, Expected: e, Note: "one count per draw; multinomial assumption holds",
	}
}
