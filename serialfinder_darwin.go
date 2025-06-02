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
		// If ioreg command itself failed, this is an error.
		// An empty output with an error is still an error.
		// If output is also empty, it might indicate no devices OR a more fundamental issue.
		errMsg := fmt.Sprintf("failed to run ioreg: %v", err)
		if out.Len() > 0 {
			errMsg = fmt.Sprintf("%s, output: %s", errMsg, out.String())
		}
		return nil, fmt.Errorf(errMsg)
	}

	// If ioreg ran successfully but produced no output, it means no serial devices were found.
	if out.Len() == 0 {
		return devices, nil
	}

	// Prepare VID/PID for case-insensitive comparison
	targetVidUpper := strings.ToUpper(vid)
	targetPidUpper := strings.ToUpper(pid)

	scanner := bufio.NewScanner(&out)
	// currentUSBDevice holds properties of the most recently encountered USB device.
	// We assume that an IOSerialBSDClient's properties will follow its parent USB device's properties.
	var currentUSBDevice *SerialDeviceInfo

	// Regex to extract key-value pairs: "key" = value
	reKeyValue := regexp.MustCompile(`"([^"]+)"\s*=\s*(.*)`)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Detect the start of a new device entry in ioreg output.
		// These lines typically start with "+-o" followed by the class name.
		// Example: +-o IOUSBHostDevice  <class IOUSBHostDevice, id 0x10000027f, registered, matched, active, busy 0 (5 ms), retain 19>
		// Or for the serial client: +-o IOSerialBSDClient  <class IOSerialBSDClient, id 0x1000002c6, registered, matched, active, busy 0 (0 ms), retain 7>
		if strings.HasPrefix(line, "+-o") {
			if strings.Contains(line, "IOUSBDevice") || strings.Contains(line, "IOUSBHostDevice") {
				// New USB device encountered, reset currentUSBDevice
				currentUSBDevice = &SerialDeviceInfo{}
			} else if !strings.Contains(line, "IOSerialBSDClient") {
				// If it's another type of device, and not the serial client itself,
				// we might have left the scope of the current USB device.
				// This is a heuristic: if an unrelated device appears, the previous USB context is likely no longer relevant
				// for any subsequent IOSerialBSDClient unless a new USB device is explicitly listed.
				currentUSBDevice = nil
			}
			// If it's an IOSerialBSDClient line, we don't reset currentUSBDevice here,
			// as the following lines will contain its properties, and we need the context
			// of the *parent* USB device.
		}

		match := reKeyValue.FindStringSubmatch(line)
		if len(match) == 3 {
			key := match[1]
			value := strings.TrimSpace(match[2])

			// Populate properties for the current USB device context
			if currentUSBDevice != nil {
				switch key {
				case "idVendor":
					hexVal, err := parseHexValue(value)
					if err == nil {
						currentUSBDevice.Vid = fmt.Sprintf("%04X", hexVal)
					}
				case "idProduct":
					hexVal, err := parseHexValue(value)
					if err == nil {
						currentUSBDevice.Pid = fmt.Sprintf("%04X", hexVal)
					}
				// USB Product Name and Serial Number can also be extracted if needed,
				// but are not strictly part of SerialDeviceInfo struct currently.
				case "USB Serial Number", "kUSBSerialNumberString":
					// Favor "USB Serial Number" but take kUSBSerialNumberString if the other is not present or empty.
					// The check `currentUSBDevice.SerialNumber == ""` handles this implicitly if "USB Serial Number" comes first.
					sn := parseStringValue(value)
					if sn != "" { // Only overwrite if we get a non-empty serial number
						currentUSBDevice.SerialNumber = sn
					}
				}
			}

			// Check for IOCalloutDevice, which indicates the serial port path.
			// This property is part of the IOSerialBSDClient.
			if key == "IOCalloutDevice" {
				// We expect currentUSBDevice to be populated from the parent USB device
				// that appeared earlier in the ioreg output.
				if currentUSBDevice != nil && currentUSBDevice.Vid != "" && currentUSBDevice.Pid != "" {
					portPath := parseStringValue(value)
					if portPath != "" {
						// We have a potential serial device. Check against VID/PID filters.
						// currentUSBDevice.Vid and currentUSBDevice.Pid are already uppercase from fmt.Sprintf("%04X").
						vidMatch := (targetVidUpper == "" || currentUSBDevice.Vid == targetVidUpper)
						pidMatch := (targetPidUpper == "" || currentUSBDevice.Pid == targetPidUpper)

						if vidMatch && pidMatch {
							// Create a new SerialDeviceInfo for the list, copying relevant USB properties.
							device := SerialDeviceInfo{
								Port:         portPath,
								Vid:          currentUSBDevice.Vid,
								Pid:          currentUSBDevice.Pid,
								SerialNumber: currentUSBDevice.SerialNumber,
								// Description could be added here if parsed, e.g., from "USB Product Name"
							}
							devices = append(devices, device)
						}
					}
				}
				// After processing an IOCalloutDevice, the properties of currentUSBDevice have been used
				// or deemed irrelevant. It's not strictly necessary to reset currentUSBDevice here,
				// as a new "+-o IOUSB..." line will do that. However, if multiple IOSerialBSDClient
				// entries were nested under one IOUSBDevice (uncommon for distinct physical ports),
				// not resetting could lead to issues. For typical scenarios, this is okay.
				// For now, let the next "+-o IOUSB..." line handle the reset of currentUSBDevice.
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning ioreg output: %v", err)
	}

	return devices, nil
}

// parseHexValue converts ioreg number values to int64.
// ioreg typically outputs VID/PID as decimal numbers, but can also use "0x" prefix for hex.
func parseHexValue(value string) (int64, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ",") // Remove trailing comma

	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		// Explicitly hex if "0x" prefix is present
		return strconv.ParseInt(value[2:], 16, 64)
	}
	// Otherwise, assume it's a decimal number (standard for ioreg idVendor/idProduct)
	// If it's not a valid decimal, this will return an error.
	return strconv.ParseInt(value, 10, 64)
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
