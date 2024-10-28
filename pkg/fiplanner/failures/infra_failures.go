package failures

import "math/rand"

func registerNodeRestart(r *FailureRegistry) {
	gen := func(rng *rand.Rand) map[string]string {
		args := make(map[string]string)
		if rng.Float64() > 0.5 {
			args["graceful-restart"] = "true"
		}

		if rng.Float64() > 0.5 {
			args["wait-for-replication"] = "true"
		}

		return args
	}
	r.Add(FailureSpec{
		Name:         "Node Restart",
		GenerateArgs: gen,
	})
}
