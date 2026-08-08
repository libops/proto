package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/libops/proto/internal/openapivisibility"
)

func main() {
	inputPath := flag.String("input", "", "complete generated OpenAPI document")
	outputPath := flag.String("output", "", "customer OpenAPI document")
	flag.Parse()
	if *inputPath == "" || *outputPath == "" {
		fmt.Fprintln(os.Stderr, "both -input and -output are required")
		os.Exit(2)
	}

	input, err := os.ReadFile(*inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", *inputPath, err)
		os.Exit(1)
	}
	output, err := openapivisibility.Filter(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "filter %s: %v\n", *inputPath, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*outputPath, output, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", *outputPath, err)
		os.Exit(1)
	}
}
