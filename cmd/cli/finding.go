package main

import (
	"context"
	"fmt"
	"time"
)

// findingDTO khớp components.schemas.Finding.
type findingDTO struct {
	ID         string    `json:"id"`
	Category   string    `json:"category"`
	Severity   string    `json:"severity"`
	Summary    string    `json:"summary"`
	State      string    `json:"state"`
	DetectedAt time.Time `json:"detected_at"`
}

func runFindingCommand(ctx context.Context, c *apiClient, g globalFlags, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: vmf finding <list>")
	}
	action, rest := args[0], args[1:]

	switch action {
	case "list":
		return findingList(ctx, c, g, rest)
	default:
		return fmt.Errorf("vmf finding: unknown action %q", action)
	}
}

func findingList(ctx context.Context, c *apiClient, g globalFlags, _ []string) error {
	var result listEnvelopeDTO[findingDTO]
	if _, err := c.do(ctx, "GET", "/v1/findings", "", nil, &result); err != nil {
		return err
	}
	if g.output == outputJSON {
		printJSON(result)
		return nil
	}
	rows := make([][]string, len(result.Items))
	for i, f := range result.Items {
		rows[i] = []string{f.ID, f.Category, f.Severity, f.State, f.Summary, f.DetectedAt.Format(time.RFC3339)}
	}
	printTable([]string{"ID", "CATEGORY", "SEVERITY", "STATE", "SUMMARY", "DETECTED_AT"}, rows)
	return nil
}
