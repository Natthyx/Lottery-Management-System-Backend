package lottery

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// ============================================================
// LOTTERY ALGORITHM
// Read every comment — this is what Bloomberg will ask about.
// ============================================================

// FairShuffle performs a cryptographically secure Fisher-Yates (Knuth) shuffle.
//
// WHY NOT math/rand:
//   math/rand is a PRNG seeded by a deterministic value. If an attacker can
//   guess or observe the seed, they predict the entire sequence — and therefore
//   predict the lottery winner. Unacceptable for any high-stakes system.
//
// WHY crypto/rand:
//   crypto/rand reads from /dev/urandom on Linux — kernel-level entropy derived
//   from hardware events (CPU jitter, interrupt timing). It is the same source
//   used for TLS key generation and is computationally infeasible to predict.
//
// WHY Fisher-Yates:
//   It guarantees every permutation of the input is equally likely (uniform
//   distribution). Naive approaches like "sort by random score" have subtle
//   statistical biases.
//
// Algorithm walkthrough for [A, B, C, D]:
//   i=3: swap D with rand[0..3] -> say 1 -> [A, D, C, B]
//   i=2: swap C with rand[0..2] -> say 0 -> [C, D, A, B]
//   i=1: swap D with rand[0..1] -> say 1 -> [C, D, A, B]
//
// Time:  O(n)
// Space: O(n) — copies input to avoid mutating caller's slice
func FairShuffle(participants []int64) ([]int64, error) {
	n := len(participants)
	if n == 0 {
		return []int64{}, nil
	}

	result := make([]int64, n)
	copy(result, participants)

	for i := n - 1; i > 0; i-- {
		// Generate a secure random integer in [0, i] inclusive.
		// rand.Int(rand.Reader, max) returns a value in [0, max), so
		// we pass i+1 to include i in the range.
		maxBig := big.NewInt(int64(i + 1))
		jBig, err := rand.Int(rand.Reader, maxBig)
		if err != nil {
			// Never fall back to a weaker source. A predictable lottery
			// is worse than no lottery.
			return nil, fmt.Errorf("crypto/rand failed at position %d: %w", i, err)
		}

		j := jBig.Int64()
		result[i], result[j] = result[j], result[i]
	}

	return result, nil
}

// SelectWinners shuffles participants and splits into winners + waitlist.
//
// winners  = first `count` elements (rank 1..count)
// waitlist = remaining elements in shuffle order (rank count+1..n)
func SelectWinners(participants []int64, count int) (winners, waitlist []int64, err error) {
	if len(participants) == 0 {
		return nil, nil, fmt.Errorf("no participants in the lottery pool")
	}
	if count < 1 {
		return nil, nil, fmt.Errorf("winner count must be at least 1")
	}

	shuffled, err := FairShuffle(participants)
	if err != nil {
		return nil, nil, fmt.Errorf("shuffling participants: %w", err)
	}

	if count >= len(shuffled) {
		return shuffled, []int64{}, nil
	}

	return shuffled[:count], shuffled[count:], nil
}
