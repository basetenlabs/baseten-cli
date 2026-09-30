// Command cmddocs walks the declarative cmd.Root tree and emits a versioned
// JSON description for consumption by docs.baseten.co. Docs maintainers run it
// from a CLI release checkout; nothing in this repo's release flow runs it.
//
// Usage:
//
//	go run ./internal/cmddocs --cli-version=v0.1.0 --out=docs.json
//	go run ./internal/cmddocs --cli-version=dev          # writes to stdout
//
// Set SOURCE_DATE_EPOCH (Unix seconds) to pin GeneratedAt for reproducible
// output.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	cmdpkg "github.com/basetenlabs/baseten-cli/cmd"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("cmddocs", flag.ContinueOnError)
	flags.SetOutput(stderr)
	cliVersion := flags.String("cli-version", "dev", "CLI version string to embed in the output (e.g. v0.1.0).")
	outPath := flags.String("out", "-", "Output file path; '-' writes to stdout.")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	generatedAt := time.Now().UTC().Format(time.RFC3339)
	if epoch := os.Getenv("SOURCE_DATE_EPOCH"); epoch != "" {
		secs, err := strconv.ParseInt(epoch, 10, 64)
		if err != nil {
			fmt.Fprintf(stderr, "invalid SOURCE_DATE_EPOCH %q: %v\n", epoch, err)
			return 2
		}
		generatedAt = time.Unix(secs, 0).UTC().Format(time.RFC3339)
	}

	schema := Walk(*cliVersion, generatedAt, cmdpkg.Root)
	payload, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "marshal: %v\n", err)
		return 1
	}
	payload = append(payload, '\n')

	if *outPath != "-" {
		if err := os.WriteFile(*outPath, payload, 0666); err != nil {
			fmt.Fprintf(stderr, "write %s: %v\n", *outPath, err)
			return 1
		}
		return 0
	}
	if _, err := stdout.Write(payload); err != nil {
		fmt.Fprintf(stderr, "write: %v\n", err)
		return 1
	}
	return 0
}
