package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	configPath := flag.String("config", "", "phase2 baseline JSON configuration")
	output := flag.String("output", "", "new measurement directory, or existing measurement directory with -finalize")
	execute := flag.Bool("execute", false, "execute network load and write immutable measurement artifacts")
	finalize := flag.Bool("finalize", false, "bind real correctness evidence and write the final report without network load")
	flag.Parse()
	if *configPath == "" || *execute && *finalize {
		fatal("-config is required; -execute and -finalize are mutually exclusive")
	}
	cfg, mockCfg, err := LoadConfig(*configPath)
	if err != nil {
		fatal(err.Error())
	}
	if !*execute && !*finalize {
		encoded, _ := json.Marshal(map[string]any{"status": "configuration_valid", "run_id": cfg.RunID, "repeats": cfg.Load.Repeats, "executed": false})
		fmt.Println(string(encoded))
		return
	}
	if *output == "" {
		fatal("-output is required with -execute or -finalize")
	}
	if *finalize {
		if err := FinalizeReport(*output, cfg); err != nil {
			fatal(err.Error())
		}
		encoded, _ := json.Marshal(map[string]any{"status": "report_finalized", "report": *output + "/report.json", "performance_conclusion": "not_adjudicated"})
		fmt.Println(string(encoded))
		return
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
	if err := WriteMeasurement(*output, cfg, mockCfg, samples, timings, pages); err != nil {
		fatal(err.Error())
	}
	encoded, _ := json.Marshal(map[string]any{"status": "measurement_written_correctness_required", "measurement": *output + "/measurement.json", "raw_samples": *output + "/samples.jsonl", "requests": len(samples), "performance_conclusion": "not_adjudicated"})
	fmt.Println(string(encoded))
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
