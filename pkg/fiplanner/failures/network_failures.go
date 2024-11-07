package failures

import "math/rand"

func registerLimitBandwidth(r *FailureRegistry) {
	gen := func(rng *rand.Rand) map[string]string {
		args := make(map[string]string)
		possibleRates := []string{"0mbps", "1mbps", "10mbps"}
		args["rate"] = possibleRates[rng.Intn(len(possibleRates))]

		return args
	}
	r.Add(FailureSpec{
		Name:         "Limit Bandwidth",
		GenerateArgs: gen,
	})
}

func registerPartitionNode(r *FailureRegistry) {
	gen := func(rng *rand.Rand) map[string]string {
		return make(map[string]string)
	}
	r.Add(FailureSpec{
		Name:         "Partition Node",
		GenerateArgs: gen,
	})
}
