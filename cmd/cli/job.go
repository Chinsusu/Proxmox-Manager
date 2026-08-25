package main

import (
	"context"
	"flag"
	"fmt"
	"time"
)

// jobDTO khớp components.schemas.Job.
type jobDTO struct {
	ID           string     `json:"id"`
	InstanceID   string     `json:"instance_id"`
	Operation    string     `json:"operation"`
	State        string     `json:"state"`
	Checkpoint   string     `json:"checkpoint"`
	Attempt      int        `json:"attempt"`
	MaxAttempts  int        `json:"max_attempts"`
	ErrorCode    *string    `json:"error_code"`
	ErrorMessage *string    `json:"error_message"`
	CreatedAt    time.Time  `json:"created_at"`
	FinishedAt   *time.Time `json:"finished_at"`
}

// jobEventDTO khớp components.schemas.JobEvent.
type jobEventDTO struct {
	EventID    string    `json:"event_id"`
	Type       string    `json:"type"`
	From       *string   `json:"from"`
	To         *string   `json:"to"`
	OccurredAt time.Time `json:"occurred_at"`
}

func runJobCommand(ctx context.Context, c *apiClient, g globalFlags, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: vmf job <get|retry>")
	}
	action, rest := args[0], args[1:]

	switch action {
	case "get":
		return jobGet(ctx, c, g, rest)
	case "retry":
		return jobRetry(ctx, c, g, rest)
	default:
		return fmt.Errorf("vmf job: unknown action %q", action)
	}
}

func jobGet(ctx context.Context, c *apiClient, g globalFlags, args []string) error {
	fs := flag.NewFlagSet("job get", flag.ExitOnError)
	withEvents := fs.Bool("events", false, "also fetch and print job events")
	if err := fs.Parse(args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) < 1 {
		return fmt.Errorf("usage: vmf job get <id> [--events]")
	}
	id := positional[0]

	var job jobDTO
	if _, err := c.do(ctx, "GET", "/v1/jobs/"+id, "", nil, &job); err != nil {
		return err
	}

	var events listEnvelopeDTO[jobEventDTO]
	if *withEvents {
		if _, err := c.do(ctx, "GET", "/v1/jobs/"+id+"/events", "", nil, &events); err != nil {
			return err
		}
	}

	if g.output == outputJSON {
		if *withEvents {
			printJSON(map[string]any{"job": job, "events": events.Items})
		} else {
			printJSON(job)
		}
		return nil
	}

	printTable([]string{"FIELD", "VALUE"}, [][]string{
		{"id", job.ID}, {"instance_id", job.InstanceID}, {"operation", job.Operation},
		{"state", job.State}, {"checkpoint", job.Checkpoint},
		{"attempt", fmt.Sprintf("%d/%d", job.Attempt, job.MaxAttempts)},
		{"error_code", nilToDash(job.ErrorCode)}, {"error_message", nilToDash(job.ErrorMessage)},
		{"created_at", job.CreatedAt.Format(time.RFC3339)},
	})
	if *withEvents {
		fmt.Println()
		rows := make([][]string, len(events.Items))
		for i, e := range events.Items {
			rows[i] = []string{e.OccurredAt.Format(time.RFC3339), e.Type, nilToDash(e.From), nilToDash(e.To)}
		}
		printTable([]string{"OCCURRED_AT", "TYPE", "FROM", "TO"}, rows)
	}
	return nil
}

func jobRetry(ctx context.Context, c *apiClient, g globalFlags, args []string) error {
	fs := flag.NewFlagSet("job retry", flag.ExitOnError)
	reason := fs.String("reason", "", "reason (required)")
	idemKey := fs.String("idempotency-key", "", "idempotency key (optional, auto-generated if omitted)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) < 1 {
		return fmt.Errorf("usage: vmf job retry <id> --reason <text>")
	}
	if *reason == "" {
		return fmt.Errorf("--reason is required")
	}
	key := *idemKey
	if key == "" {
		key = randomIdempotencyKey("job-retry")
	}

	var result acceptedJobDTO
	if _, err := c.do(ctx, "POST", "/v1/jobs/"+positional[0]+"/retry", key, map[string]any{"reason": *reason}, &result); err != nil {
		return err
	}
	if g.output == outputJSON {
		printJSON(result)
		return nil
	}
	fmt.Printf("job %s retried, state=%s\n", result.JobID, result.State)
	return nil
}
