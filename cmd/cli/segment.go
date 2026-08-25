package main

import (
	"context"
	"flag"
	"fmt"
)

// segmentDTO khớp components.schemas.NetworkSegment.
type segmentDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	CIDR     string `json:"cidr"`
	Gateway  string `json:"gateway"`
	Bridge   string `json:"bridge"`
	State    string `json:"state"`
	Capacity struct {
		Total       int `json:"total"`
		Free        int `json:"free"`
		Reserved    int `json:"reserved"`
		Assigned    int `json:"assigned"`
		Quarantined int `json:"quarantined"`
	} `json:"capacity"`
}

func runSegmentCommand(ctx context.Context, c *apiClient, g globalFlags, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: vmf segment <list|create>")
	}
	action, rest := args[0], args[1:]

	switch action {
	case "list":
		return segmentList(ctx, c, g, rest)
	case "create":
		return segmentCreate(ctx, c, g, rest)
	default:
		return fmt.Errorf("vmf segment: unknown action %q", action)
	}
}

func segmentList(ctx context.Context, c *apiClient, g globalFlags, _ []string) error {
	var result listEnvelopeDTO[segmentDTO]
	if _, err := c.do(ctx, "GET", "/v1/network-segments", "", nil, &result); err != nil {
		return err
	}
	if g.output == outputJSON {
		printJSON(result)
		return nil
	}
	rows := make([][]string, len(result.Items))
	for i, s := range result.Items {
		rows[i] = []string{s.ID, s.Name, s.CIDR, s.Bridge, s.State,
			fmt.Sprintf("%d free / %d total", s.Capacity.Free, s.Capacity.Total)}
	}
	printTable([]string{"ID", "NAME", "CIDR", "BRIDGE", "STATE", "CAPACITY"}, rows)
	return nil
}

func segmentCreate(ctx context.Context, c *apiClient, g globalFlags, args []string) error {
	fs := flag.NewFlagSet("segment create", flag.ExitOnError)
	name := fs.String("name", "", "segment name (required)")
	cidr := fs.String("cidr", "", "IPv4 CIDR (required)")
	gateway := fs.String("gateway", "", "gateway IP (required)")
	bridge := fs.String("bridge", "", "Proxmox bridge (required)")
	idemKey := fs.String("idempotency-key", "", "idempotency key (optional, auto-generated if omitted)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" || *cidr == "" || *gateway == "" || *bridge == "" {
		return fmt.Errorf("--name, --cidr, --gateway, and --bridge are required")
	}
	key := *idemKey
	if key == "" {
		key = randomIdempotencyKey("segment-create")
	}

	body := map[string]any{"name": *name, "cidr": *cidr, "gateway": *gateway, "bridge": *bridge}
	var s segmentDTO
	if _, err := c.do(ctx, "POST", "/v1/network-segments", key, body, &s); err != nil {
		return err
	}
	if g.output == outputJSON {
		printJSON(s)
		return nil
	}
	fmt.Printf("segment %s created (%s)\n", s.ID, s.CIDR)
	return nil
}
