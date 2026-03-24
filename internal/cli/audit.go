// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
)

func newAuditCmd() *cobra.Command {
	var (
		user      string
		provider  string
		requestID string
		since     string
		until     string
		output    string
	)

	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Query the audit log",
		Example: `  jitsudo audit
  jitsudo audit --user alice@example.com
  jitsudo audit --request req_01J8KZ...
  jitsudo audit --provider aws --since 24h
  jitsudo audit --since 2026-01-01T00:00:00Z --output json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()

			queryInput := &jitsudov1alpha1.QueryAuditInput{
				ActorIdentity: user,
				Provider:      provider,
				RequestId:     requestID,
			}

			if since != "" {
				t, err := parseSinceFlag(since)
				if err != nil {
					return fmt.Errorf("invalid --since: %w", err)
				}
				queryInput.Since = timestamppb.New(t)
			}
			if until != "" {
				t, err := time.Parse(time.RFC3339, until)
				if err != nil {
					return fmt.Errorf("invalid --until (must be RFC3339): %w", err)
				}
				queryInput.Until = timestamppb.New(t)
			}

			resp, err := c.Service().QueryAudit(ctx, queryInput)
			if err != nil {
				return fmt.Errorf("audit query: %w", err)
			}

			events := resp.GetEvents()
			if len(events) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No audit events found.")
				return nil
			}

			switch output {
			case "table":
				return printAuditTable(cmd.OutOrStdout(), events)
			case "json":
				return printAuditJSON(cmd.OutOrStdout(), events)
			case "csv":
				return printAuditCSV(cmd.OutOrStdout(), events)
			default:
				return fmt.Errorf("unknown --output format %q: choose table, json, or csv", output)
			}
		},
	}

	cmd.Flags().StringVar(&user, "user", "", "Filter by actor identity")
	cmd.Flags().StringVar(&provider, "provider", "", "Filter by provider")
	cmd.Flags().StringVar(&requestID, "request", "", "Filter by request ID")
	cmd.Flags().StringVar(&since, "since", "", "Return events after this duration or timestamp (e.g. 24h, 2026-01-01T00:00:00Z)")
	cmd.Flags().StringVar(&until, "until", "", "Return events before this RFC3339 timestamp")
	cmd.Flags().StringVarP(&output, "output", "o", "table", "Output format: table, json, csv")

	return cmd
}

// parseSinceFlag accepts either a Go duration ("24h", "30m") subtracted from
// now, or a full RFC3339 timestamp.
func parseSinceFlag(s string) (time.Time, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().UTC().Add(-d), nil
	}
	return time.Parse(time.RFC3339, s)
}

func printAuditTable(out io.Writer, events []*jitsudov1alpha1.AuditEvent) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIMESTAMP\tACTOR\tACTION\tREQUEST ID\tOUTCOME")
	fmt.Fprintln(w, strings.Repeat("-", 90))
	for _, e := range events {
		ts := e.GetTimestamp().AsTime().UTC().Format(time.RFC3339)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			ts, e.GetActorIdentity(), e.GetAction(), e.GetRequestId(), e.GetOutcome())
	}
	return w.Flush()
}

func printAuditJSON(out io.Writer, events []*jitsudov1alpha1.AuditEvent) error {
	type row struct {
		ID            int64  `json:"id"`
		Timestamp     string `json:"timestamp"`
		Actor         string `json:"actor"`
		Action        string `json:"action"`
		RequestID     string `json:"request_id"`
		Provider      string `json:"provider"`
		ResourceScope string `json:"resource_scope"`
		Outcome       string `json:"outcome"`
	}
	rows := make([]row, 0, len(events))
	for _, e := range events {
		rows = append(rows, row{
			ID:            e.GetId(),
			Timestamp:     e.GetTimestamp().AsTime().UTC().Format(time.RFC3339),
			Actor:         e.GetActorIdentity(),
			Action:        e.GetAction(),
			RequestID:     e.GetRequestId(),
			Provider:      e.GetProvider(),
			ResourceScope: e.GetResourceScope(),
			Outcome:       e.GetOutcome(),
		})
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}

func printAuditCSV(out io.Writer, events []*jitsudov1alpha1.AuditEvent) error {
	fmt.Fprintln(out, "timestamp,actor,action,request_id,provider,outcome")
	for _, e := range events {
		ts := e.GetTimestamp().AsTime().UTC().Format(time.RFC3339)
		fmt.Fprintf(out, "%s,%s,%s,%s,%s,%s\n",
			ts, e.GetActorIdentity(), e.GetAction(), e.GetRequestId(), e.GetProvider(), e.GetOutcome())
	}
	return nil
}
