package espn

// Helpers for building leagues in tests (used by other packages' tests).

// EligibleSlotsFor returns ESPN's usual eligible slots for a position.
func EligibleSlotsFor(pos string) []int {
	switch pos {
	case "QB":
		return []int{SlotQB, SlotOP, SlotBench, SlotIR}
	case "RB":
		return []int{SlotRB, SlotRBWR, SlotFlex, SlotOP, SlotBench, SlotIR}
	case "WR":
		return []int{SlotWR, SlotRBWR, SlotWRTE, SlotFlex, SlotOP, SlotBench, SlotIR}
	case "TE":
		return []int{SlotTE, SlotWRTE, SlotFlex, SlotOP, SlotBench, SlotIR}
	case "K":
		return []int{SlotK, SlotBench, SlotIR}
	case "D/ST":
		return []int{SlotDST, SlotBench, SlotIR}
	}
	return []int{SlotBench}
}

// StandardSlots is a common 1QB/2RB/2WR/1TE/1FLEX/K/DST, 7 bench, 1 IR setup.
func StandardSlots() map[int]int {
	return map[int]int{
		SlotQB: 1, SlotRB: 2, SlotWR: 2, SlotTE: 1, SlotFlex: 1,
		SlotDST: 1, SlotK: 1, SlotBench: 7, SlotIR: 1,
	}
}
