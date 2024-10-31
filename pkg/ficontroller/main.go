// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package ficontroller

import (
	"context"

	_ "github.com/lib/pq" // register postgres driver
	"github.com/spf13/cobra"
)

func main() {
	config := &ControllerConfig{}

	var startCmd = &cobra.Command{
		Use:   "failure-controller [command] (flags)",
		Short: "standalone failure-controller tool for conducting failure-injection experiments",
		Long: `standalone failure-controller tool for conducting failure-injection experiments
`,
		Version: "TODO",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			return config.Start(ctx)
		},
	}

	startCmd.Flags().IntVar(&config.port, "listen-port", 8081, "port to listen on")

}
