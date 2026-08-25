package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"time"
)

// instanceDTO khớp components.schemas.Instance trong api/openapi.yaml —
// định nghĩa lại ở CLI (không import internal/httpapi, các response type
// ở đó không export) để giải mã JSON từ vmf-api.
type instanceDTO struct {
	ID               string     `json:"id"`
	LogicalName      string     `json:"logical_name"`
	Hostname         string     `json:"hostname"`
	State            string     `json:"state"`
	Generation       int        `json:"generation"`
	TemplateID       string     `json:"template_id"`
	PVENode          *string    `json:"pve_node"`
	VMID             *int       `json:"vmid"`
	IPAddress        *string    `json:"ip_address"`
	NetworkSegmentID *string    `json:"network_segment_id"`
	CurrentJobID     *string    `json:"current_job_id"`
	CreatedAt        time.Time  `json:"created_at"`
	RetiredAt        *time.Time `json:"retired_at"`
}

// validationRunDTO khớp components.schemas.ValidationRun.
type validationRunDTO struct {
	Type           string    `json:"type"`
	Result         string    `json:"result"`
	RulesetVersion string    `json:"ruleset_version"`
	StartedAt      time.Time `json:"started_at"`
}

type acceptedJobDTO struct {
	InstanceID string `json:"instance_id"`
	JobID      string `json:"job_id"`
	State      string `json:"state"`
}

type listEnvelopeDTO[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func runInstanceCommand(ctx context.Context, c *apiClient, g globalFlags, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: vmf instance <create|list|get|evidence|retry|quarantine|rebuild|decommission>")
	}
	action, rest := args[0], args[1:]

	switch action {
	case "create":
		return instanceCreate(ctx, c, g, rest)
	case "list":
		return instanceList(ctx, c, g, rest)
	case "get":
		return instanceGet(ctx, c, g, rest)
	case "evidence":
		return instanceEvidence(ctx, c, g, rest)
	case "retry":
		return instanceAction(ctx, c, g, rest, "retry", false)
	case "quarantine":
		return instanceAction(ctx, c, g, rest, "quarantine", true)
	case "rebuild":
		return instanceAction(ctx, c, g, rest, "rebuild", false)
	case "decommission":
		return instanceAction(ctx, c, g, rest, "decommission", true)
	default:
		return fmt.Errorf("vmf instance: unknown action %q", action)
	}
}

func instanceCreate(ctx context.Context, c *apiClient, g globalFlags, args []string) error {
	fs := flag.NewFlagSet("instance create", flag.ExitOnError)
	template := fs.String("template", "", "template id (required)")
	segment := fs.String("segment", "", "network segment id (required)")
	egressPolicy := fs.String("egress-policy", "", "egress policy id (required)")
	logicalName := fs.String("logical-name", "", "logical name (optional, defaults to generated hostname)")
	workload := fs.String("workload", "", "workload adapter name (optional)")
	idemKey := fs.String("idempotency-key", "", "idempotency key (optional, auto-generated if omitted)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *template == "" || *segment == "" || *egressPolicy == "" {
		return fmt.Errorf("--template, --segment, and --egress-policy are required")
	}
	key := *idemKey
	if key == "" {
		key = randomIdempotencyKey("instance-create")
	}

	body := map[string]any{
		"template_id":        *template,
		"network_segment_id": *segment,
		"egress_policy_id":   *egressPolicy,
	}
	if *logicalName != "" {
		body["logical_name"] = *logicalName
	}
	if *workload != "" {
		body["workload"] = map[string]any{"adapter": *workload}
	}

	var result acceptedJobDTO
	if _, err := c.do(ctx, "POST", "/v1/instances", key, body, &result); err != nil {
		return err
	}
	if g.output == outputJSON {
		printJSON(result)
		return nil
	}
	fmt.Printf("instance %s created, job %s (%s)\n", result.InstanceID, result.JobID, result.State)
	return nil
}

func instanceList(ctx context.Context, c *apiClient, g globalFlags, _ []string) error {
	var result listEnvelopeDTO[instanceDTO]
	if _, err := c.do(ctx, "GET", "/v1/instances", "", nil, &result); err != nil {
		return err
	}
	if g.output == outputJSON {
		printJSON(result)
		return nil
	}
	rows := make([][]string, len(result.Items))
	for i, inst := range result.Items {
		rows[i] = []string{inst.ID, inst.LogicalName, inst.Hostname, inst.State, nilIntToDash(inst.VMID), nilToDash(inst.IPAddress)}
	}
	printTable([]string{"ID", "LOGICAL_NAME", "HOSTNAME", "STATE", "VMID", "IP"}, rows)
	return nil
}

func instanceGet(ctx context.Context, c *apiClient, g globalFlags, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vmf instance get <id>")
	}
	var inst instanceDTO
	if _, err := c.do(ctx, "GET", "/v1/instances/"+args[0], "", nil, &inst); err != nil {
		return err
	}
	if g.output == outputJSON {
		printJSON(inst)
		return nil
	}
	printTable([]string{"FIELD", "VALUE"}, [][]string{
		{"id", inst.ID}, {"logical_name", inst.LogicalName}, {"hostname", inst.Hostname},
		{"state", inst.State}, {"generation", fmt.Sprintf("%d", inst.Generation)},
		{"template_id", inst.TemplateID}, {"pve_node", nilToDash(inst.PVENode)},
		{"vmid", nilIntToDash(inst.VMID)}, {"ip_address", nilToDash(inst.IPAddress)},
		{"current_job_id", nilToDash(inst.CurrentJobID)}, {"created_at", inst.CreatedAt.Format(time.RFC3339)},
	})
	return nil
}

func instanceEvidence(ctx context.Context, c *apiClient, g globalFlags, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vmf instance evidence <id>")
	}
	var result listEnvelopeDTO[validationRunDTO]
	if _, err := c.do(ctx, "GET", "/v1/instances/"+args[0]+"/evidence", "", nil, &result); err != nil {
		return err
	}
	if g.output == outputJSON {
		printJSON(result)
		return nil
	}
	rows := make([][]string, len(result.Items))
	for i, run := range result.Items {
		rows[i] = []string{run.Type, run.Result, run.RulesetVersion, run.StartedAt.Format(time.RFC3339)}
	}
	printTable([]string{"TYPE", "RESULT", "RULESET_VERSION", "STARTED_AT"}, rows)
	return nil
}

// instanceAction xử lý retry/quarantine/rebuild/decommission — reason
// bắt buộc cho quarantine/decommission theo ReasonRequest (Phần II mục 10).
func instanceAction(ctx context.Context, c *apiClient, g globalFlags, args []string, action string, requiresReason bool) error {
	fs := flag.NewFlagSet("instance "+action, flag.ExitOnError)
	reason := fs.String("reason", "", "reason (required for quarantine/decommission)")
	idemKey := fs.String("idempotency-key", "", "idempotency key (optional, auto-generated if omitted)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) < 1 {
		return fmt.Errorf("usage: vmf instance %s <id> [--reason <text>]", action)
	}
	if requiresReason && *reason == "" {
		return fmt.Errorf("--reason is required for %s", action)
	}
	key := *idemKey
	if key == "" {
		key = randomIdempotencyKey("instance-" + action)
	}

	var body any
	if requiresReason {
		body = map[string]any{"reason": *reason}
	}

	var result json.RawMessage
	if _, err := c.do(ctx, "POST", "/v1/instances/"+positional[0]+"/"+action, key, body, &result); err != nil {
		return err
	}
	if g.output == outputJSON {
		fmt.Println(string(result))
		return nil
	}
	fmt.Printf("instance %s: %s accepted\n", positional[0], action)
	return nil
}
