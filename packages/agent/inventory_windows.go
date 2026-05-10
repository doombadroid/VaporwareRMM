//go:build windows

package main

import (
	"encoding/json"
	"os/exec"
	"strconv"
)

// collectSoftware uses PowerShell to read the Uninstall registry hive,
// which is what Windows itself uses for "Apps & Features". Both 32-bit
// (Wow6432Node) and 64-bit hives are read so we don't miss anything on
// 64-bit Windows. winget is not used because it's not present on every
// Windows version we'd run on.
func collectSoftware() []InventorySoftware {
	const script = `
		$paths = @(
			'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*',
			'HKLM:\Software\Wow6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*'
		)
		$apps = Get-ItemProperty -Path $paths -ErrorAction SilentlyContinue |
			Where-Object { $_.DisplayName } |
			ForEach-Object {
				[PSCustomObject]@{
					name = $_.DisplayName
					version = $_.DisplayVersion
					vendor = $_.Publisher
					install_date = $_.InstallDate
				}
			}
		$apps | ConvertTo-Json -Compress
	`
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		reportSoftwareError("registry-uninstall", err)
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	// PowerShell returns either a single object or an array depending on
	// row count. Try array first, fall back to single.
	var rows []map[string]interface{}
	if err := json.Unmarshal(out, &rows); err != nil {
		var single map[string]interface{}
		if err2 := json.Unmarshal(out, &single); err2 != nil {
			reportSoftwareError("registry-parse", err)
			return nil
		}
		rows = []map[string]interface{}{single}
	}
	result := make([]InventorySoftware, 0, len(rows))
	for _, r := range rows {
		name, _ := r["name"].(string)
		if name == "" {
			continue
		}
		version, _ := r["version"].(string)
		vendor, _ := r["vendor"].(string)
		// install_date is YYYYMMDD per registry convention. Convert to
		// unix epoch best-effort; missing or bad values become 0.
		var installEpoch int64
		if v, ok := r["install_date"].(string); ok && len(v) == 8 {
			// parse YYYYMMDD with dummy hour
			y, _ := strconv.Atoi(v[0:4])
			m, _ := strconv.Atoi(v[4:6])
			d, _ := strconv.Atoi(v[6:8])
			if y > 1990 && m >= 1 && m <= 12 && d >= 1 && d <= 31 {
				installEpoch = int64((y-1970)*31536000 + (m-1)*2592000 + (d-1)*86400)
			}
		}
		result = append(result, InventorySoftware{
			Name:        name,
			Version:     version,
			Vendor:      vendor,
			InstallDate: installEpoch,
		})
	}
	return result
}
