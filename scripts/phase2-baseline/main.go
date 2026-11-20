package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	configPath := flag.String("config", "", "phase2 baseline JSON configuration")
	output := flag.String("output", "", "new exclusive output directory")
	execute := flag.Bool("execute", false, "execute network load; without this flag only validate configuration")
	flag.Parse()
	if *configPath == "" {
		fatal("-config is required")
	}
	cfg, mockCfg, err := LoadConfig(*configPath)
	if err != nil {
		fatal(err.Error())
	}
	if !*execute {
		encoded, _ := json.Marshal(map[string]any{"status": "configuration_valid", "run_id": cfg.RunID, "repeats": cfg.Load.Repeats, "executed": false})
		fmt.Println(string(encoded))
		return
	}
	if *output == "" {
		fatal("-output is required with -execute")
	}
	runner, err := NewRunner(cfg, os.Getenv(cfg.Gateway.APIKeyEnv))
	if err != nil {
		fatal(err.Error())
	}
	defer runner.Close()
	samples, timings, pages, err := runner.Execute()
	if err != nil {
		fatal(err.Error())
	}
	if err := WriteReport(*output, cfg, mockCfg, samples, timings, pages); err != nil {
		fatal(err.Error())
	}
	encoded, _ := json.Marshal(map[string]any{"status": "measurement_written", "report": *output + "/report.json", "raw_samples": *output + "/samples.jsonl", "requests": len(samples), "performance_conclusion": "not_adjudicated"})
	fmt.Println(string(encoded))
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
