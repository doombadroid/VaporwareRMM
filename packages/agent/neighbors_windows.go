//go:build windows

package main

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"time"
)

// collectNeighbors on Windows uses `arp -a`. Output is grouped by
// interface; we ignore the headers and parse the flat rows.
func collectNeighbors() []AgentNeighborEntry {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "arp", "-a").Output()
	if err != nil {
		return nil
	}
	res := []AgentNeighborEntry{}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "Interface:") || strings.HasPrefix(line, "Internet Address") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		// fields: <ip> <mac> <type> — arp uses dashes (00-11-22-...);
		// normalize to colons for the server-side ParseMAC.
		mac := strings.ReplaceAll(fields[1], "-", ":")
		if fields[2] == "invalid" || mac == "ff:ff:ff:ff:ff:ff" {
			continue
		}
		res = append(res, AgentNeighborEntry{IP: fields[0], MAC: mac})
	}
	return res
}
