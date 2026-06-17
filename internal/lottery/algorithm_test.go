package lottery

import (
	"math"
	"sort"
	"testing"
)

// TestFairShuffle_PreservesElements verifies the shuffle contains exactly
// the same elements as the input — just reordered. Nothing added or lost.
func TestFairShuffle_PreservesElements(t *testing.T) {
	input := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	result, err := FairShuffle(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != len(input) {
		t.Fatalf("length mismatch: got %d, want %d", len(result), len(input))
	}

	inputCopy := make([]int64, len(input))
	copy(inputCopy, input)
	sort.Slice(inputCopy, func(i, j int) bool { return inputCopy[i] < inputCopy[j] })
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })

	for i := range inputCopy {
		if inputCopy[i] != result[i] {
			t.Errorf("element mismatch at index %d: got %d, want %d", i, result[i], inputCopy[i])
		}
	}
}

// TestFairShuffle_DoesNotMutateInput verifies we copy before shuffling.
func TestFairShuffle_DoesNotMutateInput(t *testing.T) {
	input := []int64{10, 20, 30, 40, 50}
	original := make([]int64, len(input))
	copy(original, input)

	_, err := FairShuffle(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i := range input {
		if input[i] != original[i] {
			t.Errorf("input was mutated at index %d: got %d, want %d", i, input[i], original[i])
		}
	}
}

// TestFairShuffle_UniformDistribution runs 10,000 shuffles of 5 elements
// and verifies each element appears in each position roughly 20% of the time.
// A biased PRNG would show one position appearing significantly more.
func TestFairShuffle_UniformDistribution(t *testing.T) {
	const (
		n         = 5
		trials    = 10_000
		expected  = float64(trials) / float64(n) // 2000 per position
		tolerance = 0.05                         // 5% deviation allowed
	)

	counts := make([][]int, n)
	for i := range counts {
		counts[i] = make([]int, n)
	}

	participants := []int64{0, 1, 2, 3, 4}
	for trial := 0; trial < trials; trial++ {
		result, err := FairShuffle(participants)
		if err != nil {
			t.Fatalf("shuffle failed on trial %d: %v", trial, err)
		}
		for pos, elem := range result {
			counts[elem][pos]++
		}
	}

	for elem := 0; elem < n; elem++ {
		for pos := 0; pos < n; pos++ {
			observed := float64(counts[elem][pos])
			deviation := math.Abs(observed-expected) / expected
			if deviation > tolerance {
				t.Errorf(
					"bias detected: element %d at position %d — "+
						"observed %.0f, expected %.0f (%.1f%% deviation, tolerance %.1f%%)",
					elem, pos, observed, expected, deviation*100, tolerance*100,
				)
			}
		}
	}
}

// TestFairShuffle_EmptyInput returns empty slice, no error.
func TestFairShuffle_EmptyInput(t *testing.T) {
	result, err := FairShuffle([]int64{})
	if err != nil {
		t.Fatalf("unexpected error for empty input: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected empty result, got %v", result)
	}
}

// TestSelectWinners_CountRespected verifies exactly `count` winners returned.
func TestSelectWinners_CountRespected(t *testing.T) {
	participants := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for _, wantCount := range []int{1, 3, 5, 10} {
		winners, _, err := SelectWinners(participants, wantCount)
		if err != nil {
			t.Fatalf("SelectWinners(%d) error: %v", wantCount, err)
		}
		if len(winners) != wantCount {
			t.Errorf("SelectWinners(%d): got %d winners, want %d", wantCount, len(winners), wantCount)
		}
	}
}

// TestSelectWinners_WaitlistIsRemainder verifies winners + waitlist = all participants.
func TestSelectWinners_WaitlistIsRemainder(t *testing.T) {
	participants := []int64{1, 2, 3, 4, 5}
	winners, waitlist, err := SelectWinners(participants, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(winners)+len(waitlist) != len(participants) {
		t.Errorf("winners(%d) + waitlist(%d) != participants(%d)", len(winners), len(waitlist), len(participants))
	}
}

// TestSelectWinners_NoDuplicates verifies no participant wins twice.
func TestSelectWinners_NoDuplicates(t *testing.T) {
	participants := []int64{101, 102, 103, 104, 105}
	winners, waitlist, err := SelectWinners(participants, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	seen := make(map[int64]bool)
	all := append(winners, waitlist...)
	for _, id := range all {
		if seen[id] {
			t.Errorf("duplicate: user %d appeared more than once", id)
		}
		seen[id] = true
	}
}

// TestSelectWinners_MoreWinnersThanParticipants — edge case: everyone wins.
func TestSelectWinners_MoreWinnersThanParticipants(t *testing.T) {
	participants := []int64{1, 2, 3}
	winners, waitlist, err := SelectWinners(participants, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(winners) != 3 {
		t.Errorf("got %d winners, want 3 (all participants win)", len(winners))
	}
	if len(waitlist) != 0 {
		t.Errorf("expected empty waitlist, got %v", waitlist)
	}
}

// TestSelectWinners_SingleParticipant — if only one person booked, they always win.
func TestSelectWinners_SingleParticipant(t *testing.T) {
	participants := []int64{42}
	winners, _, err := SelectWinners(participants, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(winners) != 1 || winners[0] != 42 {
		t.Errorf("single participant should always win; got %v", winners)
	}
}

// TestSelectWinners_WinnersAreSubsetOfParticipants verifies no phantom winners.
func TestSelectWinners_WinnersAreSubsetOfParticipants(t *testing.T) {
	participants := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	winners, _, err := SelectWinners(participants, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	participantSet := make(map[int64]bool)
	for _, id := range participants {
		participantSet[id] = true
	}
	for _, w := range winners {
		if !participantSet[w] {
			t.Errorf("winner %d is not in the participant list", w)
		}
	}
}
