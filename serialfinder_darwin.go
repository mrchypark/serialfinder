//go:build darwin
// +build darwin

package serialfinder

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// GetSerialDevices retrieves USB serial devices on macOS by querying the I/O Registry,
// filtering by VID and PID, and finding the corresponding device path.
func GetSerialDevices(vid, pid string) ([]SerialDeviceInfo, error) {
	var devices []SerialDeviceInfo

	// Use ioreg to get device information in a parseable format
	// -c IOSerialBSDClient: Focus on serial port client drivers
	// -r: Recursive search up the device tree to find parent USB devices
	// -l: Show properties for each device
	cmd := exec.Command("ioreg", "-r", "-c", "IOSerialBSDClient", "-l")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		// Handle case where ioreg might fail or return non-zero if no devices found
		// Check stderr? For now, assume error means failure or no devices.
		// An empty output might just mean no serial devices connected.
		if out.Len() == 0 {
			// No output probably means no serial devices, not necessarily an error
			return devices, nil
		}
		return nil, fmt.Errorf("failed to run ioreg: %v, output: %s", err, out.String())
	}

	// Prepare VID/PID for case-insensitive comparison
	targetVidUpper := strings.ToUpper(vid)
	targetPidUpper := strings.ToUpper(pid)

	scanner := bufio.NewScanner(&out)
	var currentDevice *SerialDeviceInfo
	var inUSBDeviceBlock bool // Flag to track if we are inside a relevant USB device entry

	// Regex to extract key-value pairs like "key" = value
	// Handles strings ("value"), numbers (123), hex numbers (0x123)
	reKeyValue := regexp.MustCompile(`"([^"]+)"\s*=\s*(.*)`)

	for scanner.Scan() {
		line := scanner.Text()

		// Check if we are entering a new device potentially containing USB info
		// Reset state if we leave an indented block associated with a potential USB parent
		// This parsing logic is simplified; a full tree parser would be more robust.
		// We primarily look for IOUSBHostDevice or IOUSBDevice containing VID/PID/Serial,
		// and then find the child IOSerialBSDClient for the port.
		if strings.Contains(line, "<class IOUSB") { // IOUSBHostDevice or IOUSBDevice
			inUSBDeviceBlock = true
			// Prepare a potential device structure, but don't add it yet
			currentDevice = &SerialDeviceInfo{}
		} else if !strings.HasPrefix(strings.TrimSpace(line), "|") && !strings.HasPrefix(strings.TrimSpace(line), "+-o") && !strings.HasPrefix(strings.TrimSpace(line), "{") && !strings.HasPrefix(strings.TrimSpace(line), "}") {
			// If indentation level decreases significantly or line structure changes, assume we left the block
			if !strings.Contains(line, "=") { // Heuristic: Lines without '=' are less likely part of the property block
				inUSBDeviceBlock = false
				currentDevice = nil // Reset current device context
			}
		}

		if currentDevice != nil {
			match := reKeyValue.FindStringSubmatch(strings.TrimSpace(line))
			if len(match) == 3 {
				key := match[1]
				value := strings.TrimSpace(match[2])

				// Extract VID, PID, SerialNumber from the USB device block
				if inUSBDeviceBlock {
					switch key {
					case "idVendor":
						hexVal, err := parseHexValue(value)
						if err == nil {
							currentDevice.Vid = fmt.Sprintf("%04X", hexVal)
						}
					case "idProduct":
						hexVal, err := parseHexValue(value)
						if err == nil {
							currentDevice.Pid = fmt.Sprintf("%04X", hexVal)
						}
					case "USB Serial Number": // Note: Key name can vary slightly (sometimes kUSBSerialNumberString)
						currentDevice.SerialNumber = parseStringValue(value)
					case "kUSBSerialNumberString": // Alternative key name
						if currentDevice.SerialNumber == "" { // Prefer "USB Serial Number" if available
							currentDevice.SerialNumber = parseStringValue(value)
						}
					}
				}

				// Extract Port from the IOSerialBSDClient block (which is a child)
				if key == "IOCalloutDevice" {
					// This property belongs to the IOSerialBSDClient, which should be listed *after*
					// its parent USB device properties in the `ioreg -r` output.
					portPath := parseStringValue(value)
					if portPath != "" && currentDevice.Vid != "" && currentDevice.Pid != "" {
						currentDevice.Port = portPath

						// Check if VID/PID match the filter (if provided)
						vidMatch := (targetVidUpper == "" || currentDevice.Vid == targetVidUpper)
						pidMatch := (targetPidUpper == "" || currentDevice.Pid == targetPidUpper)

						if vidMatch && pidMatch {
							// Found a matching device, add a copy to the list
							devices = append(devices, *currentDevice)
						}
						// Reset for the next potential device block found by ioreg
						// Since IOCalloutDevice is usually the last relevant piece, reset here.
						currentDevice = nil
						inUSBDeviceBlock = false
					}
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning ioreg output: %v", err)
	}

	return devices, nil
}

// parseHexValue converts ioreg number values (like 0x1234 or 1234) to int64
func parseHexValue(value string) (int64, error) {
	value = strings.TrimSpace(value)
	// Remove trailing comma if present (sometimes happens in ioreg output)
	value = strings.TrimSuffix(value, ",")

	// Check if it's already a decimal number
	decVal, errDec := strconv.ParseInt(value, 10, 64)
	if errDec == nil {
		return decVal, nil
	}

	// Try parsing as hex (ioreg usually uses 0x prefix, but let's be flexible)
	if strings.HasPrefix(value, "0x") {
		return strconv.ParseInt(value[2:], 16, 64)
	}
	// Fallback attempt if no prefix but maybe hex? Unlikely needed for VID/PID.
	hexVal, errHex := strconv.ParseInt(value, 16, 64)
	if errHex == nil {
		return hexVal, nil
	}

	// Return the original decimal error if hex also failed
	return 0, errDec
}

// parseStringValue extracts string values like "My String" -> My String
func parseStringValue(value string) string {
	value = strings.TrimSpace(value)
	// Remove trailing comma if present
	value = strings.TrimSuffix(value, ",")
	// Remove surrounding quotes
	if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		return value[1 : len(value)-1]
	}
	return value // Return as-is if not quoted
}
