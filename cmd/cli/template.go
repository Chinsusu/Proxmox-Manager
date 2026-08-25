package main

import (
	"context"
	"flag"
	"fmt"
	"time"
)

// templateDTO khớp components.schemas.Template.
type templateDTO struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Family           string    `json:"family"`
	Version          string    `json:"version"`
	State            string    `json:"state"`
	ValidationStatus string    `json:"validation_status"`
	CreatedAt        time.Time `json:"created_at"`
}

func runTemplateCommand(ctx context.Context, c *apiClient, g globalFlags, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: vmf template <list|get|create|promote>")
	}
	action, rest := args[0], args[1:]

	switch action {
	case "list":
		return templateList(ctx, c, g, rest)
	case "get":
		return templateGet(ctx, c, g, rest)
	case "create":
		return templateCreate(ctx, c, g, rest)
	case "promote":
		return templatePromote(ctx, c, g, rest)
	default:
		return fmt.Errorf("vmf template: unknown action %q", action)
	}
}

func templateList(ctx context.Context, c *apiClient, g globalFlags, _ []string) error {
	var result listEnvelopeDTO[templateDTO]
	if _, err := c.do(ctx, "GET", "/v1/templates", "", nil, &result); err != nil {
		return err
	}
	if g.output == outputJSON {
		printJSON(result)
		return nil
	}
	rows := make([][]string, len(result.Items))
	for i, t := range result.Items {
		rows[i] = []string{t.ID, t.Name, t.Family, t.Version, t.State, t.ValidationStatus}
	}
	printTable([]string{"ID", "NAME", "FAMILY", "VERSION", "STATE", "VALIDATION"}, rows)
	return nil
}

func templateGet(ctx context.Context, c *apiClient, g globalFlags, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vmf template get <id>")
	}
	var t templateDTO
	if _, err := c.do(ctx, "GET", "/v1/templates/"+args[0], "", nil, &t); err != nil {
		return err
	}
	if g.output == outputJSON {
		printJSON(t)
		return nil
	}
	printTable([]string{"FIELD", "VALUE"}, [][]string{
		{"id", t.ID}, {"name", t.Name}, {"family", t.Family}, {"version", t.Version},
		{"state", t.State}, {"validation_status", t.ValidationStatus}, {"created_at", t.CreatedAt.Format(time.RFC3339)},
	})
	return nil
}

func templateCreate(ctx context.Context, c *apiClient, g globalFlags, args []string) error {
	fs := flag.NewFlagSet("template create", flag.ExitOnError)
	name := fs.String("name", "", "template name (required)")
	family := fs.String("family", "", "template family")
	version := fs.String("version", "", "template version (required)")
	pveCluster := fs.String("pve-cluster", "", "Proxmox cluster id (required)")
	pveNode := fs.String("pve-node", "", "Proxmox node (required)")
	pveVMID := fs.Int("pve-vmid", 0, "Proxmox template VMID (required)")
	checksum := fs.String("checksum", "", "source checksum (required)")
	idemKey := fs.String("idempotency-key", "", "idempotency key (optional, auto-generated if omitted)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" || *version == "" || *pveCluster == "" || *pveNode == "" || *pveVMID == 0 || *checksum == "" {
		return fmt.Errorf("--name, --version, --pve-cluster, --pve-node, --pve-vmid, and --checksum are required")
	}
	key := *idemKey
	if key == "" {
		key = randomIdempotencyKey("template-create")
	}

	body := map[string]any{
		"name": *name, "family": *family, "version": *version,
		"pve_cluster_id": *pveCluster, "pve_node": *pveNode, "pve_template_vmid": *pveVMID,
		"source_checksum": *checksum,
	}
	var t templateDTO
	if _, err := c.do(ctx, "POST", "/v1/templates", key, body, &t); err != nil {
		return err
	}
	if g.output == outputJSON {
		printJSON(t)
		return nil
	}
	fmt.Printf("template %s created (state=%s)\n", t.ID, t.State)
	return nil
}

func templatePromote(ctx context.Context, c *apiClient, g globalFlags, args []string) error {
	fs := flag.NewFlagSet("template promote", flag.ExitOnError)
	idemKey := fs.String("idempotency-key", "", "idempotency key (optional, auto-generated if omitted)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) < 1 {
		return fmt.Errorf("usage: vmf template promote <id>")
	}
	key := *idemKey
	if key == "" {
		key = randomIdempotencyKey("template-promote")
	}

	var t templateDTO
	if _, err := c.do(ctx, "POST", "/v1/templates/"+positional[0]+"/promote", key, nil, &t); err != nil {
		return err
	}
	if g.output == outputJSON {
		printJSON(t)
		return nil
	}
	fmt.Printf("template %s promoted to %s\n", t.ID, t.State)
	return nil
}
