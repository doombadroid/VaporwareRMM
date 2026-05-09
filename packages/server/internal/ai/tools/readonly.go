// Package tools registers the AI-callable tools that read project state.
// Stage 2 capabilities (NL fleet search, script gen, ticket summary) wire
// LLMs to these tools so the model can fetch what it needs without us
// stuffing the entire project state into every prompt.
//
// Two invariants every tool here upholds:
//
//  1. Every query is tenant-scoped via ScopeSnapshot.TenantID. The model
//     CANNOT request data from another tenant — args don't carry a
//     tenant_id field, and any field that resembles one would be
//     rejected by PermittedFields.
//  2. Free-text result fields (hostnames, ticket bodies, alert messages)
//     are sanitised before being marshalled back. The model's next call
//     could otherwise treat injected instructions in a hostname as
//     authoritative.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"vaporrmm/server/internal/ai"
	"vaporrmm/server/internal/db"
)

func init() {
	ai.RegisterTool(ai.ToolSpec{
		Name:            "list_devices",
		Description:     "Return a paginated list of devices in the current tenant. Filters: os_class, status (online/offline), tag.",
		PermittedFields: []string{"os_class", "status", "tag", "limit"},
		MinRung:         ai.RungShadow,
		Handler:         listDevicesHandler,
		InputSchema: []byte(`{
			"type":"object",
			"properties":{
				"os_class":{"type":"string","enum":["windows-server","windows-workstation","windows-other","mac","linux-server","linux-workstation","bsd","unknown"]},
				"status":{"type":"string","enum":["online","offline"]},
				"tag":{"type":"string","maxLength":64},
				"limit":{"type":"integer","minimum":1,"maximum":200}
			},
			"additionalProperties":false
		}`),
	})

	ai.RegisterTool(ai.ToolSpec{
		Name:            "list_tickets",
		Description:     "Return a paginated list of tickets in the current tenant. Filters: status, priority, customer_id.",
		PermittedFields: []string{"status", "priority", "customer_id", "limit"},
		MinRung:         ai.RungShadow,
		Handler:         listTicketsHandler,
		InputSchema: []byte(`{
			"type":"object",
			"properties":{
				"status":{"type":"string","enum":["open","in_progress","resolved","closed"]},
				"priority":{"type":"string","enum":["low","medium","high","critical"]},
				"customer_id":{"type":"string","maxLength":64},
				"limit":{"type":"integer","minimum":1,"maximum":200}
			},
			"additionalProperties":false
		}`),
	})

	ai.RegisterTool(ai.ToolSpec{
		Name:            "list_active_clusters",
		Description:     "Return active alert clusters in the current tenant — useful for grouping a new alert against ongoing incidents.",
		PermittedFields: []string{"customer_id", "limit"},
		MinRung:         ai.RungShadow,
		Handler:         listClustersHandler,
		InputSchema: []byte(`{
			"type":"object",
			"properties":{
				"customer_id":{"type":"string","maxLength":64},
				"limit":{"type":"integer","minimum":1,"maximum":50}
			},
			"additionalProperties":false
		}`),
	})
}

// listDevicesHandler returns up to N devices in the caller's tenant. We use
// scope.TenantID — never an args.tenant_id — so the model literally cannot
// reach across tenant boundaries even if it tried.
func listDevicesHandler(ctx context.Context, raw json.RawMessage, scope ai.ScopeSnapshot) (any, error) {
	var args struct {
		OSClass string `json:"os_class"`
		Status  string `json:"status"`
		Tag     string `json:"tag"`
		Limit   int    `json:"limit"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("list_devices: invalid args: %w", err)
		}
	}
	if args.Limit <= 0 || args.Limit > 200 {
		args.Limit = 50
	}

	q := `SELECT id, hostname, os_name, COALESCE(os_class,'unknown'), status, COALESCE(tags,'')
	        FROM devices WHERE tenant_id = ?`
	qArgs := []any{scope.TenantID}
	if args.OSClass != "" {
		q += ` AND os_class = ?`
		qArgs = append(qArgs, args.OSClass)
	}
	if args.Status != "" {
		q += ` AND status = ?`
		qArgs = append(qArgs, args.Status)
	}
	q += ` ORDER BY last_seen DESC LIMIT ?`
	qArgs = append(qArgs, args.Limit)

	rows, err := db.DB.Query(q, qArgs...)
	if err != nil {
		return nil, fmt.Errorf("list_devices: query: %w", err)
	}
	defer rows.Close()
	type devOut struct {
		ID       string   `json:"id"`
		Hostname string   `json:"hostname"`
		OS       string   `json:"os_name"`
		OSClass  string   `json:"os_class"`
		Status   string   `json:"status"`
		Tags     []string `json:"tags,omitempty"`
	}
	out := []devOut{}
	for rows.Next() {
		var d devOut
		var tagCSV string
		if err := rows.Scan(&d.ID, &d.Hostname, &d.OS, &d.OSClass, &d.Status, &tagCSV); err != nil {
			continue
		}
		// Split tags first, then exact-match on the filter — substring
		// matching would let "prod" match "production" or "prod-old", which
		// silently widens the filter behaviour the operator asked for.
		var tagList []string
		if tagCSV != "" {
			for _, t := range strings.Split(tagCSV, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					tagList = append(tagList, t)
				}
			}
		}
		if args.Tag != "" {
			matched := false
			for _, t := range tagList {
				if t == args.Tag {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		// Sanitise the free-text fields the model will see next call.
		d.Hostname = ai.SanitizeFreeText(d.Hostname)
		d.OS = ai.SanitizeFreeText(d.OS)
		for _, t := range tagList {
			d.Tags = append(d.Tags, ai.SanitizeFreeText(t))
		}
		out = append(out, d)
	}
	return map[string]any{"devices": out, "count": len(out)}, nil
}

func listTicketsHandler(ctx context.Context, raw json.RawMessage, scope ai.ScopeSnapshot) (any, error) {
	var args struct {
		Status     string `json:"status"`
		Priority   string `json:"priority"`
		CustomerID string `json:"customer_id"`
		Limit      int    `json:"limit"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("list_tickets: invalid args: %w", err)
		}
	}
	if args.Limit <= 0 || args.Limit > 200 {
		args.Limit = 50
	}

	q := `SELECT id, title, status, priority, COALESCE(customer_id,''), created_at
	        FROM tickets WHERE tenant_id = ?`
	qArgs := []any{scope.TenantID}
	if args.Status != "" {
		q += ` AND status = ?`
		qArgs = append(qArgs, args.Status)
	}
	if args.Priority != "" {
		q += ` AND priority = ?`
		qArgs = append(qArgs, args.Priority)
	}
	if args.CustomerID != "" {
		q += ` AND customer_id = ?`
		qArgs = append(qArgs, args.CustomerID)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	qArgs = append(qArgs, args.Limit)

	rows, err := db.DB.Query(q, qArgs...)
	if err != nil {
		return nil, fmt.Errorf("list_tickets: query: %w", err)
	}
	defer rows.Close()
	type tkt struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		Status     string `json:"status"`
		Priority   string `json:"priority"`
		CustomerID string `json:"customer_id,omitempty"`
		CreatedAt  int64  `json:"created_at"`
	}
	out := []tkt{}
	for rows.Next() {
		var t tkt
		if err := rows.Scan(&t.ID, &t.Title, &t.Status, &t.Priority, &t.CustomerID, &t.CreatedAt); err != nil {
			continue
		}
		t.Title = ai.SanitizeFreeText(t.Title)
		out = append(out, t)
	}
	return map[string]any{"tickets": out, "count": len(out)}, nil
}

func listClustersHandler(ctx context.Context, raw json.RawMessage, scope ai.ScopeSnapshot) (any, error) {
	var args struct {
		CustomerID string `json:"customer_id"`
		Limit      int    `json:"limit"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("list_active_clusters: invalid args: %w", err)
		}
	}
	if args.Limit <= 0 || args.Limit > 50 {
		args.Limit = 20
	}
	q := `SELECT id, COALESCE(name,''), COALESCE(likely_cause,''), count, last_seen
	        FROM ticket_clusters
	       WHERE tenant_id = ? AND status = 'active'`
	qArgs := []any{scope.TenantID}
	if args.CustomerID != "" {
		q += ` AND (customer_id = ? OR customer_id IS NULL)`
		qArgs = append(qArgs, args.CustomerID)
	}
	q += ` ORDER BY last_seen DESC LIMIT ?`
	qArgs = append(qArgs, args.Limit)
	rows, err := db.DB.Query(q, qArgs...)
	if err != nil {
		return nil, fmt.Errorf("list_active_clusters: query: %w", err)
	}
	defer rows.Close()
	type cl struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		LikelyCause string `json:"likely_cause"`
		Count       int    `json:"count"`
		LastSeen    int64  `json:"last_seen"`
	}
	out := []cl{}
	for rows.Next() {
		var c cl
		if err := rows.Scan(&c.ID, &c.Name, &c.LikelyCause, &c.Count, &c.LastSeen); err != nil {
			continue
		}
		c.Name = ai.SanitizeFreeText(c.Name)
		c.LikelyCause = ai.SanitizeFreeText(c.LikelyCause)
		out = append(out, c)
	}
	return map[string]any{"clusters": out, "count": len(out)}, nil
}
