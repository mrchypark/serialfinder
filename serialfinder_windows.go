//go:build windows
// +build windows

package serialfinder

import (
	"fmt"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
	"regexp"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

var (
	// vidRegex extracts the VID (Vendor ID) from a device ID string.
	// Example: USB\VID_0403&PID_6001\A4001n1A -> 0403
	vidRegex = regexp.MustCompile(`VID_([0-9a-fA-F]{4})`)
	// pidRegex extracts the PID (Product ID) from a device ID string.
	// Example: USB\VID_0403&PID_6001\A4001n1A -> 6001
	pidRegex = regexp.MustCompile(`PID_([0-9a-fA-F]{4})`)
)

// GetSerialDevices retrieves USB devices on Windows, filtering by VID and PID, and finds the corresponding COM port
func GetSerialDevices(vidFilter, pidFilter string) ([]SerialDeviceInfo, error) {
	var devices []SerialDeviceInfo

	targetVidUpper := strings.ToUpper(vidFilter)
	targetPidUpper := strings.ToUpper(pidFilter)

	// Open the registry key for USB devices. This is the typical path for USB-connected devices.
	// Other paths like FTDIBUS might also exist for specific drivers (e.g., FTDI).
	// For this refactor, we'll focus on the common USB path. A more comprehensive solution
	// might check multiple Enum paths.
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Enum\USB`, registry.READ)
	if err != nil {
		// If the main USB enumeration key isn't there, it's a system-level issue or no USB devices.
		return nil, fmt.Errorf("failed to open USB enumeration registry key: %w", err)
	}
	defer key.Close()

	// Read the list of subkeys (representing individual USB device instances)
	deviceInstanceIDs, err := key.ReadSubKeyNames(-1)
	if err != nil {
		return nil, fmt.Errorf("failed to read USB device instance IDs: %w", err)
	}

	// Iterate over each device instance ID
	for _, deviceInstanceID := range deviceInstanceIDs {
		// Extract VID from deviceInstanceID
		vidMatches := vidRegex.FindStringSubmatch(deviceInstanceID)
		var actualVid string
		if len(vidMatches) > 1 {
			actualVid = strings.ToUpper(vidMatches[1])
		} else {
			continue // VID not found in the expected format, skip this device instance
		}

		// Extract PID from deviceInstanceID
		pidMatches := pidRegex.FindStringSubmatch(deviceInstanceID)
		var actualPid string
		if len(pidMatches) > 1 {
			actualPid = strings.ToUpper(pidMatches[1])
		} else {
			continue // PID not found in the expected format, skip this device instance
		}

		// Apply VID filter
		if targetVidUpper != "" && actualVid != targetVidUpper {
			continue
		}

		// Apply PID filter
		if targetPidUpper != "" && actualPid != targetPidUpper {
			continue
		}

		// If filters pass (or were empty), proceed to get more details
		deviceInstanceKey, err := registry.OpenKey(key, deviceInstanceID, registry.READ)
		if err != nil {
			// Failed to open specific device instance key, skip
			continue
		}
		defer deviceInstanceKey.Close() // Defer inside loop: closes each opened subkey

		// The next level of subkeys under the device instance ID often represents
		// the serial number or a unique instance identifier.
		instanceSubKeyNames, err := deviceInstanceKey.ReadSubKeyNames(-1)
		if err != nil {
			// Failed to read subkeys for this instance, skip
			continue
		}

		for _, instanceSubKeyName := range instanceSubKeyNames {
			// This subkey name is often the Serial Number for USB devices.
			// Pass actualVid and actualPid to avoid re-parsing.
			// Note: key refers to the top-level ...\Enum\USB key. We need deviceInstanceKey for sub-properties.
			// However, iterateSerialsWindows constructs the full path from deviceID (deviceInstanceID) and serial (instanceSubKeyName)
			// and uses the top-level key, which is how registry paths work. This seems okay.
			device := iterateSerialsWindows(instanceSubKeyName, deviceInstanceID, actualVid, actualPid, key)
			if device.Port != "" { // A non-empty Port indicates an active and valid device
				devices = append(devices, device)
			}
		}
	}

	return devices, nil
}

// Helper function to iterate over serials and get the corresponding COM ports on Windows.
// actualVid and actualPid are the already extracted and uppercased VID/PID for this device.
func iterateSerialsWindows(serialNumber, deviceInstanceID, vid, pid string, enumUSBKey registry.Key) SerialDeviceInfo {
	// Construct the path to the "Device Parameters" subkey, which contains the PortName (COM port).
	// The path is relative to the enumUSBKey (e.g., SYSTEM\CurrentControlSet\Enum\USB).
	deviceParamsKeyPath := fmt.Sprintf(`%s\%s\Device Parameters`, deviceInstanceID, serialNumber)
	deviceParamsKey, err := registry.OpenKey(enumUSBKey, deviceParamsKeyPath, registry.READ)
	if err != nil {
		return SerialDeviceInfo{} // Cannot open device parameters, so cannot get PortName
	}
	defer deviceParamsKey.Close()

	portName, _, err := deviceParamsKey.GetStringValue("PortName")
	if err != nil {
		return SerialDeviceInfo{} // PortName not found
	}

	// Check if the COM port is actually available/active
	if !checkCOMPortActiveWindows(portName) {
		return SerialDeviceInfo{} // Port not active or accessible
	}

	return SerialDeviceInfo{
		SerialNumber: serialNumber,
		Vid:          vid, // Use pre-extracted VID
		Pid:          pid, // Use pre-extracted PID
		Port:         portName,
	}
}

// checkCOMPortActiveWindows tries to open the COM port to check if it is active on Windows.
// This is a basic check; a port might exist but be in use or misconfigured.
func checkCOMPortActiveWindows(portName string) bool {
	comPort := fmt.Sprintf("\\\\.\\%s", portName)
	handle, err := syscall.CreateFile(
		syscall.StringToUTF16Ptr(comPort),
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0,
		nil,
		syscall.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)

	return true
}
