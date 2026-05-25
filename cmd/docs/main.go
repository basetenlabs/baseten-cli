// Command docs walks the declarative cmd.Root tree and emits a versioned JSON
// description for consumption by docs.baseten.co.
//
// Usage:
//
//	go run ./cmd/docs --cli-version=v0.1.0 --out=docs.json
//	go run ./cmd/docs --cli-version=dev          # writes to stdout
//
// Set SOURCE_DATE_EPOCH (Unix seconds) to pin GeneratedAt for reproducible
// builds; goreleaser sets this automatically.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	cmdpkg "github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/docs"
)

func main() {
	cliVersion := flag.String("cli-version", "dev", "CLI version string to embed in the output (e.g. v0.1.0).")
	outPath := flag.String("out", "-", "Output file path; '-' writes to stdout.")
	flag.Parse()

	generatedAt := time.Now().UTC().Format(time.RFC3339)
	if epoch := os.Getenv("SOURCE_DATE_EPOCH"); epoch != "" {
		secs, err := strconv.ParseInt(epoch, 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid SOURCE_DATE_EPOCH %q: %v\n", epoch, err)
			os.Exit(2)
		}
		generatedAt = time.Unix(secs, 0).UTC().Format(time.RFC3339)
	}

	schema := docs.Walk(*cliVersion, generatedAt, cmdpkg.Root)
	payload, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		os.Exit(1)
	}
	payload = append(payload, '\n')

	var w *os.File = os.Stdout
	if *outPath != "-" {
		f, err := os.Create(*outPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create %s: %v\n", *outPath, err)
			os.Exit(1)
		}
		defer f.Close()
		w = f
	}
	if _, err := w.Write(payload); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
}
