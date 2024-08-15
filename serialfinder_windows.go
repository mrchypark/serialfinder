//go:build windows
// +build windows

package serialfinder

import (
	"fmt"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// GetSerialDevices retrieves USB devices on Windows, filtering by VID and PID, and finds the corresponding COM port
func GetSerialDevices(vid, pid string) ([]SerialDeviceInfo, error) {
	var devices []SerialDeviceInfo

	// Open the registry key for USB devices
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Enum\USB`, registry.READ)
	if err != nil {
		return nil, err
	}
	defer key.Close()

	// Read the list of subkeys (device IDs)
	deviceIDs, err := key.ReadSubKeyNames(-1)
	if err != nil {
		return nil, err
	}

	// Iterate over each device ID
	for _, deviceID := range deviceIDs {
		// Check if the deviceID contains the specified VID and PID
		if strings.Contains(deviceID, fmt.Sprintf("VID_%s&PID_%s", vid, pid)) {
			deviceKey, err := registry.OpenKey(key, deviceID, registry.READ)
			if err != nil {
				continue
			}
			defer deviceKey.Close()

			// Read the list of subkeys under each device ID (which usually include serial numbers)
			serials, err := deviceKey.ReadSubKeyNames(-1)
			if err != nil {
				continue
			}

			// Iterate over each serial number
			for _, serial := range serials {
				device := iterateSerialsWindows(serial, deviceID, key)
				if device != (SerialDeviceInfo{}) { // Append only if the device is active
					devices = append(devices, device)
				}
			}
		}
	}

	return devices, nil
}

// Helper function to iterate over serials and get the corresponding COM ports on Windows.
func iterateSerialsWindows(serial, deviceID string, key registry.Key) SerialDeviceInfo {
	// Open the `Device Parameters` key to find the COM port
	deviceParamsKeyPath := fmt.Sprintf(`%s\%s\Device Parameters`, deviceID, serial)
	deviceParamsKey, err := registry.OpenKey(key, deviceParamsKeyPath, registry.READ)
	if err != nil {
		return SerialDeviceInfo{}
	}
	defer deviceParamsKey.Close()

	// Read the `PortName` value, which should contain the COM port
	portName, _, err := deviceParamsKey.GetStringValue("PortName")
	if err != nil {
		return SerialDeviceInfo{}
	}

	// Check if the COM port can be opened to determine if the device is active
	isActive := checkCOMPortActiveWindows(portName)
	if !isActive {
		return SerialDeviceInfo{}
	}

	return SerialDeviceInfo{
		SerialNumber: serial,
		Vid:          strings.Split(deviceID, "&")[0][4:],
		Pid:          strings.Split(deviceID, "&")[1][4:],
		Port:         portName,
	}
}

// checkCOMPortActiveWindows tries to open the COM port to check if it is active on Windows
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
