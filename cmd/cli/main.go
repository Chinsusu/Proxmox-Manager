// Command vmf là CLI operator theo Phần X mục 4 (create/get/retry/
// quarantine/rebuild/decommission). P0-00 chỉ dựng khung subcommand;
// các lệnh thật gọi vmf-api qua HTTP thuộc epic P0-09.
package main

import (
	"fmt"
	"os"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "version":
		fmt.Printf("vmf %s (%s)\n", version, commit)
	case "instance", "job":
		fmt.Fprintf(os.Stderr, "vmf %s: chưa implement, chờ epic P0-09 (API & CLI)\n", os.Args[1])
		os.Exit(1)
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `vmf - VM Factory operator CLI

Usage:
  vmf version
  vmf instance <create|get|retry|quarantine|rebuild|decommission> ...   (P0-09)
  vmf job <get|retry> ...                                               (P0-09)`)
}
