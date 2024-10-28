package fiplanner

import "math/rand"

func registerDiskStall(r *failureRegistry) {
	gen := func(rng *rand.Rand) map[string]string {
		args := make(map[string]string)

		if rng.Float64() < 0.5 {
			args["type"] = "read, write"
		} else {
			// Add at least one type of disk stall.
			pageFaultTypes := []string{"read", "write"}
			args["type"] = pageFaultTypes[rng.Intn(len(pageFaultTypes))]
		}

		return args
	}
	r.Add(failureSpec{
		Name:         "Disk Stall",
		GenerateArgs: gen,
	})
}
