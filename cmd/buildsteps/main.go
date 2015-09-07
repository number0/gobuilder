package main

import (
	"log"
	"os"

	"github.com/Luzifer/rconfig"
)

var (
	version    = "dev"
	config     = configData{}
	buildSteps = []buildStep{
		StepSetupGPG{},
	}
)

type buildStep interface {
	ShouldExecute() bool
	Run() error
	Name() string
}

func init() {
	if err := rconfig.Parse(&config); err != nil {
		log.Fatalf("Unable to load environment configuration.")
	}
}

func main() {
	log.Printf("Starting to process...")

	for _, step := range buildSteps {
		log.Printf("Starting build-step %s...", step.Name())

		if !step.ShouldExecute() {
			log.Printf("Skipping, requirements are not met.")
			continue
		}

		if err := step.Run(); err != nil {
			switch err {
			default:
				log.Printf("A fatal error ocurred, quitting build: %s", err)
				os.Exit(1)
			}
		}
	}

	log.Printf("Processing finished.")
}
