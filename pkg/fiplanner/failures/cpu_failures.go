package failures

import "math/rand"

func registerPageFault(r *FailureRegistry) {
	gen := func(rng *rand.Rand) map[string]string {
		args := make(map[string]string)

		if rng.Float64() < 0.5 {
			args["type"] = "minor, major"
		} else {
			// Add at least one type of page fault.
			pageFaultTypes := []string{"minor", "major"}
			args["type"] = pageFaultTypes[rng.Intn(len(pageFaultTypes))]
		}

		return args
	}
	r.Add(FailureSpec{
		Name:         "Page Fault",
		GenerateArgs: gen,
	})
}
