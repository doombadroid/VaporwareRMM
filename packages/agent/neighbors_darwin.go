//go:build darwin

package main

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"time"
)

// collectNeighbors on macOS uses `arp -an` — same parser as the Linux
// arp fallback (BSD-derived format).
func collectNeighbors() []AgentNeighborEntry {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "arp", "-an").Output()
	if err != nil {
		return nil
	}
	res := []AgentNeighborEntry{}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		ipStart := strings.Index(line, "(")
		ipEnd := strings.Index(line, ")")
		if ipStart < 0 || ipEnd <= ipStart {
			continue
		}
		ip := line[ipStart+1 : ipEnd]
		entry := AgentNeighborEntry{IP: ip}
		if at := strings.Index(line, "at "); at >= 0 {
			fields := strings.Fields(line[at+3:])
			if len(fields) >= 1 && fields[0] != "(incomplete)" {
				entry.MAC = fields[0]
			}
			for i, f := range fields {
				if f == "on" && i+1 < len(fields) {
					entry.Iface = fields[i+1]
				}
			}
		}
		if entry.MAC == "" {
			continue
		}
		res = append(res, entry)
	}
	return res
}
