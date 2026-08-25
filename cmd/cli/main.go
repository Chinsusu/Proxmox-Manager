// Command vmf là CLI operator theo Phần X mục 4 (create/get/retry/
// quarantine/rebuild/decommission), gọi vmf-api qua HTTP.
package main

import (
	"context"
	"fmt"
	"os"
)

var (
	version = "dev"
	commit  = "none"
)

// globalFlags là các flag/env dùng chung cho mọi subcommand — theo quy
// ước "Operator → vmf-api: REST/CLI, TLS + JWT/API token" (Phần II
// mục 4.2). VMF_API_URL/VMF_API_TOKEN cho phép dùng CLI trong script mà
// không phải gõ flag mỗi lần.
type globalFlags struct {
	baseURL string
	token   string
	output  outputFormat
}

func parseGlobalFlags() globalFlags {
	g := globalFlags{
		baseURL: envOr("VMF_API_URL", "http://localhost:8080"),
		token:   os.Getenv("VMF_API_TOKEN"),
		output:  outputTable,
	}
	if o := os.Getenv("VMF_OUTPUT"); o == string(outputJSON) {
		g.output = outputJSON
	}
	return g
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	ctx := context.Background()
	g := parseGlobalFlags()
	client := newAPIClient(g.baseURL, g.token)

	var err error
	switch os.Args[1] {
	case "version":
		fmt.Printf("vmf %s (%s)\n", version, commit)
		return
	case "instance":
		err = runInstanceCommand(ctx, client, g, os.Args[2:])
	case "job":
		err = runJobCommand(ctx, client, g, os.Args[2:])
	case "template":
		err = runTemplateCommand(ctx, client, g, os.Args[2:])
	case "segment":
		err = runSegmentCommand(ctx, client, g, os.Args[2:])
	case "finding":
		err = runFindingCommand(ctx, client, g, os.Args[2:])
	default:
		printUsage()
		os.Exit(1)
	}
	if err != nil {
		printErr(err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `vmf - VM Factory operator CLI

Usage:
  vmf version
  vmf instance create --template <id> --segment <id> --egress-policy <id> [--logical-name <name>] [--workload <adapter>] [--idempotency-key <key>]
  vmf instance list
  vmf instance get <id>
  vmf instance evidence <id>
  vmf instance retry <id>
  vmf instance quarantine <id> --reason <text>
  vmf instance rebuild <id>
  vmf instance decommission <id> --reason <text>
  vmf job get <id> [--events]
  vmf job retry <id> --reason <text>
  vmf template list
  vmf template get <id>
  vmf template create --name <n> --version <v> --pve-cluster <id> --pve-node <n> --pve-vmid <n> --checksum <sha>
  vmf template promote <id>
  vmf segment list
  vmf segment create --name <n> --cidr <cidr> --gateway <ip> --bridge <br>
  vmf finding list

Env:
  VMF_API_URL    vmf-api base URL (default http://localhost:8080)
  VMF_API_TOKEN  bearer JWT
  VMF_OUTPUT     table|json (default table)`)
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
