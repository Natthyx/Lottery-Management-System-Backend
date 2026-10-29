// Command fairness is scenario L5 (algorithm level): it calls
// lottery.SelectWinners directly, many times, on an identical participant
// set and runs chi-square tests on the outcome. A sanity check, not a proof.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Natthyx/lottery-system/internal/lottery"
	"github.com/Natthyx/lottery-system/loadtest/stats"
)

func main() {
	k := flag.Int("k", 10, "participants")
	w := flag.Int("w", 5, "winners per draw")
	draws := flag.Int("draws", 5000, "number of draws")
	repeat := flag.Int("repeat", 1, "independent experiments; >1 prints only how often p fell below 0.05 and 0.01")
	flag.Parse()

	participants := make([]int64, *k)
	for i := range participants {
		participants[i] = int64(1000 + i) // arbitrary distinct IDs
	}
	index := map[int64]int{}
	for i, id := range participants {
		index[id] = i
	}

	if *repeat > 1 {
		calibrate(participants, index, *w, *draws, *repeat)
		return
	}
	wins, firsts := experiment(participants, index, *w, *draws)

	fmt.Printf("source=algorithm (internal/lottery.SelectWinners) participants=%d winners_per_draw=%d draws=%d\n", *k, *w, *draws)
	p, a := stats.WinFrequency(wins, *draws, *w)
	fmt.Println(p)
	fmt.Println(a)
	fmt.Println(stats.Uniform("rank-1 frequency", firsts))
}

// calibrate runs many independent experiments. If the algorithm is uniform
// and a test is well calibrated, about 5% of p-values fall below 0.05 and
// about 1% below 0.01. The textbook Pearson win-frequency test is expected
// to fall below that (it is conservative, see stats.WinFrequency).
func calibrate(participants []int64, index map[int64]int, w, draws, repeat int) {
	names := []string{"win frequency, textbook Pearson", "win frequency, variance-adjusted", "rank-1 frequency"}
	below05 := make([]int, 3)
	below01 := make([]int, 3)
	for r := 0; r < repeat; r++ {
		wins, firsts := experiment(participants, index, w, draws)
		p, a := stats.WinFrequency(wins, draws, w)
		for i, res := range []stats.Result{p, a, stats.Uniform("rank-1", firsts)} {
			if res.PValue < 0.05 {
				below05[i]++
			}
			if res.PValue < 0.01 {
				below01[i]++
			}
		}
	}
	fmt.Printf("source=algorithm calibration: %d independent experiments of %d draws, %d participants, %d winners\n",
		repeat, draws, len(participants), w)
	for i, n := range names {
		fmt.Printf("%s: p<0.05 in %d/%d (%.2f%%), p<0.01 in %d/%d (%.2f%%)\n", n,
			below05[i], repeat, 100*float64(below05[i])/float64(repeat),
			below01[i], repeat, 100*float64(below01[i])/float64(repeat))
	}
}

func experiment(participants []int64, index map[int64]int, w, draws int) (wins, firsts []int) {
	k := len(participants)
	wins = make([]int, k)
	firsts = make([]int, k)
	for d := 0; d < draws; d++ {
		winners, waitlist, err := lottery.SelectWinners(participants, w)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if len(winners) != w || len(winners)+len(waitlist) != k {
			fmt.Fprintf(os.Stderr, "draw %d: got %d winners, %d waitlist\n", d, len(winners), len(waitlist))
			os.Exit(1)
		}
		for _, id := range winners {
			wins[index[id]]++
		}
		firsts[index[winners[0]]]++
	}
	return wins, firsts
}
