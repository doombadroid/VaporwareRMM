package handlers

import (
	"strconv"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"

	"github.com/gofiber/fiber/v2"
)

// RegisterAICostRoutes wires the cost-aggregation endpoint used by the
// /admin/ai/cost dashboard. Tenant-scoped except for super_admin (all
// tenants in one chart).
//
// The query buckets cost by day in SQL — we never load raw rows into
// memory because ai_runs grows fast on busy tenants. Day boundaries
// are UTC; the operator can mentally shift if their fleet is in one
// timezone (acceptable for a v1 cost overview).
func RegisterAICostRoutes(api fiber.Router) {
	api.Get("/admin/ai/cost", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		days := 30
		if d := c.Query("days"); d != "" {
			if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 && parsed <= 365 {
				days = parsed
			}
		}
		cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()

		role, _ := c.Locals("user_role").(string)
		tenantClause := ""
		args := []interface{}{cutoff}
		if !auth.IsSuperAdmin(role) {
			tenantClause = " AND tenant_id = ?"
			args = append(args, callerTenantID(c))
		}

		// Per-day aggregate.
		dayRows, err := db.DB.Query(`
			SELECT (created_at / 86400) * 86400 AS day,
				SUM(COALESCE(cost_usd_micros, 0)) AS micros,
				SUM(COALESCE(prompt_tokens, 0) + COALESCE(output_tokens, 0)) AS tokens,
				COUNT(*) AS calls
			  FROM ai_runs
			  WHERE created_at > ?`+tenantClause+`
			  GROUP BY day
			  ORDER BY day ASC
			  LIMIT ?`, append(args, days+1)...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query"})
		}
		defer dayRows.Close()
		type daily struct {
			Day    int64 `json:"day"`
			Micros int64 `json:"cost_usd_micros"`
			Tokens int64 `json:"tokens"`
			Calls  int64 `json:"calls"`
		}
		series := []daily{}
		for dayRows.Next() {
			var d daily
			if err := dayRows.Scan(&d.Day, &d.Micros, &d.Tokens, &d.Calls); err == nil {
				series = append(series, d)
			}
		}
		dayRows.Close()

		// Per-capability totals over the same window.
		capRows, err := db.DB.Query(`
			SELECT COALESCE(capability_id, 'unknown') AS cap,
				SUM(COALESCE(cost_usd_micros, 0)) AS micros,
				SUM(COALESCE(prompt_tokens, 0) + COALESCE(output_tokens, 0)) AS tokens,
				COUNT(*) AS calls
			  FROM ai_runs
			  WHERE created_at > ?`+tenantClause+`
			  GROUP BY cap
			  ORDER BY micros DESC
			  LIMIT 50`, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query"})
		}
		defer capRows.Close()
		type capRow struct {
			Capability string `json:"capability"`
			Micros     int64  `json:"cost_usd_micros"`
			Tokens     int64  `json:"tokens"`
			Calls      int64  `json:"calls"`
		}
		caps := []capRow{}
		for capRows.Next() {
			var r capRow
			if err := capRows.Scan(&r.Capability, &r.Micros, &r.Tokens, &r.Calls); err == nil {
				caps = append(caps, r)
			}
		}

		// Total.
		var totalMicros, totalTokens, totalCalls int64
		for _, d := range series {
			totalMicros += d.Micros
			totalTokens += d.Tokens
			totalCalls += d.Calls
		}

		return c.JSON(fiber.Map{
			"window_days":      days,
			"total_usd_micros": totalMicros,
			"total_tokens":     totalTokens,
			"total_calls":      totalCalls,
			"daily":            series,
			"by_capability":    caps,
		})
	})
}
