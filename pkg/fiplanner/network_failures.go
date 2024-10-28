package fiplanner

import "math/rand"

func registerLimitBandwidth(r *failureRegistry) {
	gen := func(rng *rand.Rand) map[string]string {
		args := make(map[string]string)
		possibleRates := []string{"0mbps", "1mbps", "10mbps"}
		args["rate"] = possibleRates[rng.Intn(len(possibleRates))]

		return args
	}
	r.Add(failureSpec{
		Name:         "Limit Bandwidth",
		GenerateArgs: gen,
	})
}
