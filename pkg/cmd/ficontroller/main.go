// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/cockroachdb/cockroach/pkg/ficontroller"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TODO: finish the rest of the CLI tool. Went for an interactive
// tool since maintaining state seems overkill for a tool like this.
// Users of this tool are likely to be running it to conduct one off
// failure injection experiments on adhoc roachprod clusters

var options = map[string]func(context.Context, *ficontroller.ControllerClient){
	uploadPlan: uploadPlanPrompt,
	exit:       exitPrompt,
}

var optionSelect = promptui.Select{
	Label: "Choose an option",
	Items: []string{
		generatePlan,
		uploadPlan,
		updateState,
		startPlan,
		stopPlan,
		exit,
	},
}

func uploadPlanPrompt(ctx context.Context, c *ficontroller.ControllerClient) {
	prompt := promptui.Prompt{
		Label:     "Enter the path to the failure injection plan",
		Templates: templates,
	}
	input, err := prompt.Run()
	if err != nil {
		fmt.Printf("Prompt failed %v\n", err)
		return
	}
	var planBytes []byte
	planBytes, err = os.ReadFile(input)
	if err != nil {
		fmt.Printf("Failed to read file %v\n", err)
		return
	}
	client := *c
	_, err = client.UploadFailureInjectionPlan(ctx, &ficontroller.UploadFailureInjectionPlanRequest{FailurePlan: planBytes})
	if err != nil {
		fmt.Printf("failed to upload failure injection plan: %v\n", err)
	}
}

func exitPrompt(_ context.Context, _ *ficontroller.ControllerClient) {
	os.Exit(0)
}

var templates = &promptui.PromptTemplates{
	Prompt:  "{{ . }} ",
	Valid:   "{{ . | green }} ",
	Invalid: "{{ . | red }} ",
	Success: "{{ . | bold }} ",
}

const (
	generatePlan = "Generate a failure injection plan"
	uploadPlan   = "Upload a failure injection plan"
	updateState  = "Update cluster state"
	startPlan    = "Start a failure injection plan"
	stopPlan     = "Stop a failure injection plan"
	exit         = "Exit"
)

var rootCmd = &cobra.Command{
	Use:   "ficontroller [command] (flags)",
	Short: "standalone failure injection controller tool for conducting failure-injection experiments",
	Long: `standalone failure injection controller tool for conducting failure-injection experiments
`,
}

var (
	config = &ficontroller.ControllerConfig{}
)

var startCmd = &cobra.Command{
	Use:   "start (flags)",
	Short: "start the failure injection controller",
	Long: `start the failure injection controller
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			err := config.Start(ctx)
			if err != nil {
				panic(err)
			}
		}()
		conn, err := grpc.DialContext(ctx, fmt.Sprintf(":%d", config.Port), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return err
		}
		client := ficontroller.NewControllerClient(conn)

		for {
			_, result, _ := optionSelect.Run()
			if prompt, ok := options[result]; !ok {
				fmt.Printf("%s is not implemented yet\n", result)
			} else {
				prompt(ctx, &client)
			}
		}
	},
}

func main() {
	initFlags()
	rootCmd.AddCommand(
		startCmd,
	)

	if err := startCmd.Execute(); err != nil {
		log.Printf("ERROR: %+v", err)
		os.Exit(1)
	}
}

func initFlags() {
	startCmd.Flags().IntVar(&config.Port, "listen-port", 8081, "Port to listen on")
}
