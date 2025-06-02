//go:build windows
// +build windows

package serialfinder

import (
	"fmt"
	"regexp"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// registryKey is an interface wrapper for registry.Key methods used.
type registryKey interface {
	ReadSubKeyNames(n int) ([]string, error)
	GetStringValue(name string) (string, uint32, error)
	Close() error
}

// defaultRegistryKey wraps a real registry.Key to satisfy the registryKey interface.
type defaultRegistryKey struct {
	registry.Key
}

func (drk *defaultRegistryKey) ReadSubKeyNames(n int) ([]string, error) {
	return drk.Key.ReadSubKeyNames(n)
}

func (drk *defaultRegistryKey) GetStringValue(name string) (string, uint32, error) {
	return drk.Key.GetStringValue(name)
}

func (drk *defaultRegistryKey) Close() error {
	return drk.Key.Close()
}

// registryHandler abstracts registry opening operations.
type registryHandler interface {
	OpenKey(base registry.Key, path string, access uint32) (registryKey, error)
}

// defaultRegistryHandler is the default implementation using the actual registry.
type defaultRegistryHandler struct{}

func (drh *defaultRegistryHandler) OpenKey(base registry.Key, path string, access uint32) (registryKey, error) {
	k, err := registry.OpenKey(base, path, access)
	if err != nil {
		return nil, err
	}
	return &defaultRegistryKey{Key: k}, nil
}

// portCheckerFunc defines the signature for functions that check if a COM port is active.
type portCheckerFunc func(portName string) bool

// checkPortActive is a variable holding the current port checking function.
// This allows it to be replaced during testing.
var checkPortActive = checkCOMPortActiveWindows

var (
	vidRegex = regexp.MustCompile(`VID_([0-9a-fA-F]{4})`)
	pidRegex = regexp.MustCompile(`PID_([0-9a-fA-F]{4})`)
)

// GetSerialDevices is the public function to retrieve USB devices on Windows.
// It uses the default registry handler and port checker.
func GetSerialDevices(vidFilter, pidFilter string) ([]SerialDeviceInfo, error) {
	return getSerialDevicesWithRegistry(vidFilter, pidFilter, &defaultRegistryHandler{}, checkPortActive)
}

// getSerialDevicesWithRegistry is the internal implementation allowing for custom registry handling and port checking.
func getSerialDevicesWithRegistry(vidFilter, pidFilter string, rh registryHandler, portCheck portCheckerFunc) ([]SerialDeviceInfo, error) {
	var devices []SerialDeviceInfo

	targetVidUpper := strings.ToUpper(vidFilter)
	targetPidUpper := strings.ToUpper(pidFilter)

	// The baseKey is effectively registry.LOCAL_MACHINE, but OpenKey in registryHandler takes registry.Key
	// So we need to open the initial Enum\USB key here before passing it to the loop that uses rh.OpenKey for subkeys.
	// This is a bit awkward. A cleaner way might be for registryHandler.OpenKey to handle predefined keys
	// or for the first key to be opened outside and then its subkeys opened via rh.OpenKey.
	// For now, let's open the EnumUSB key directly and then use rh for its children.
	// This means the mock for rh.OpenKey will operate on sub-paths of Enum\USB.

	enumUSBPath := `SYSTEM\CurrentControlSet\Enum\USB`
	enumUSBKeyHandle, err := registry.OpenKey(registry.LOCAL_MACHINE, enumUSBPath, registry.READ)
	if err != nil {
		return nil, fmt.Errorf("failed to open USB enumeration registry key LKM\\%s: %w", enumUSBPath, err)
	}
	// Wrap the initially opened key so its methods (ReadSubKeyNames, Close) are called on the real key.
	// The registryKey interface is primarily for keys *returned by* rh.OpenKey.
	// This is still a bit mixed. Let's assume rh.OpenKey can handle opening the first key too.
	// To do this, rh.OpenKey needs to accept nil or a specific marker for LOCAL_MACHINE.
	// Or, the path passed to rh.OpenKey includes the top-level (e.g. "LKM\\SYSTEM\\...").
	// Let's refine registryHandler to make OpenKey more flexible or add a method for base key.
	// For this iteration, we'll assume rh.OpenKey is for subkeys OF an already opened key.
	// So, the `key` variable below will be the real `registry.Key` for `Enum\USB`.

	// Re-evaluating: The `registryHandler`'s `OpenKey` takes `base registry.Key`.
	// So, for the first call, `base` is `registry.LOCAL_MACHINE`.
	// For subsequent calls, `base` is the key returned by the previous `OpenKey` call (wrapped).
	// This means `defaultRegistryKey` needs to expose its underlying `registry.Key` or
	// `registryHandler.OpenKey` needs to accept `registryKey` as base.
	// Let's make `registryKey` expose its underlying `registry.Key` if it's a `defaultRegistryKey`.

	// Simpler: Let registryHandler.OpenKey take the full path from a known root if base is nil,
	// or path relative to base if base is not nil.
	// For now, the interface is `OpenKey(base registry.Key, path string, access uint32)`.
	// So, the first call: rh.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Enum\USB`, ...)
	// Subsequent calls: rh.OpenKey(parentKey.(actual_type).Key, subPath, ...) -> this is messy.

	// Cleanest approach for interface:
	// registryHandler.OpenTopLevelKey(path string, access uint32) (registryKey, error)
	// registryKey.OpenSubKey(path string, access uint32) (registryKey, error) - this is better.
	//
	// Sticking to current plan for now:
	// Top-level key:
	key, err := rh.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Enum\USB`, registry.READ)
	if err != nil {
		return nil, fmt.Errorf("failed to open USB enumeration registry key: %w", err)
	}
	defer key.Close()

	deviceInstanceIDs, err := key.ReadSubKeyNames(-1)
	if err != nil {
		return nil, fmt.Errorf("failed to read USB device instance IDs: %w", err)
	}

	for _, deviceInstanceID := range deviceInstanceIDs {
		vidMatches := vidRegex.FindStringSubmatch(deviceInstanceID)
		var actualVid string
		if len(vidMatches) > 1 {
			actualVid = strings.ToUpper(vidMatches[1])
		} else {
			continue
		}

		pidMatches := pidRegex.FindStringSubmatch(deviceInstanceID)
		var actualPid string
		if len(pidMatches) > 1 {
			actualPid = strings.ToUpper(pidMatches[1])
		} else {
			continue
		}

		if targetVidUpper != "" && actualVid != targetVidUpper {
			continue
		}
		if targetPidUpper != "" && actualPid != targetPidUpper {
			continue
		}

		// Open the specific device instance key. Base is 'key' (Enum\USB).
		// This assumes 'key' obtained from rh.OpenKey can be used as a 'base' for another rh.OpenKey.
		// This implies that the registryKey interface needs to be usable as a registry.Key for the base argument.
		// This is where the design gets tricky. The `base` in `registry.OpenKey` is a concrete `registry.Key`.
		// The `registryKey` interface would hide this.
		// A simple fix: defaultRegistryKey holds registry.Key, and we cast if needed by defaultRegistryHandler.
		// Or, the handler is responsible for all openings.
		// Let's pass the *path* to the device instance to iterateSerialsWindowsWithRegistry,
		// and it will use rh.OpenKey(registry.LOCAL_MACHINE, fullPathToInstance, ...)

		// Path to device instance: SYSTEM\CurrentControlSet\Enum\USB\<deviceInstanceID>
		fullDeviceInstancePath := fmt.Sprintf(`SYSTEM\CurrentControlSet\Enum\USB\%s`, deviceInstanceID)

		// The subkeys (serial numbers) are read from the deviceInstanceKey itself.
		// So, we need to open deviceInstanceKey first.
		deviceInstanceRegKey, err := rh.OpenKey(registry.LOCAL_MACHINE, fullDeviceInstancePath, registry.READ)
		if err != nil {
			continue
		}
		// Defer needs to be inside the loop for keys opened in loop
		func() {
			defer deviceInstanceRegKey.Close()
			instanceSubKeyNames, err := deviceInstanceRegKey.ReadSubKeyNames(-1)
			if err != nil {
				return // continue outer loop
			}

			for _, instanceSubKeyName := range instanceSubKeyNames {
				// Path to "Device Parameters" key: SYSTEM\CurrentControlSet\Enum\USB\<deviceID>\<serial>\Device Parameters
				deviceParamsPath := fmt.Sprintf(`%s\%s\Device Parameters`, fullDeviceInstancePath, instanceSubKeyName)

				device := iterateSerialsWindowsWithRegistry(
					instanceSubKeyName, deviceInstanceID, actualVid, actualPid,
					deviceParamsPath, rh, portCheck,
				)
				if device.Port != "" {
					devices = append(devices, device)
				}
			}
		}() // Anonymous function for defer scoping
	}
	return devices, nil
}

// iterateSerialsWindowsWithRegistry is the testable helper function.
// deviceParamsRegistryPath is the full path from LOCAL_MACHINE to the "Device Parameters" key.
func iterateSerialsWindowsWithRegistry(
	serialNumber, deviceInstanceID, vid, pid string,
	deviceParamsRegistryPath string,
	rh registryHandler, portCheck portCheckerFunc,
) SerialDeviceInfo {

	deviceParamsKey, err := rh.OpenKey(registry.LOCAL_MACHINE, deviceParamsRegistryPath, registry.READ)
	if err != nil {
		return SerialDeviceInfo{}
	}
	defer deviceParamsKey.Close()

	portName, _, err := deviceParamsKey.GetStringValue("PortName")
	if err != nil {
		return SerialDeviceInfo{}
	}

	if !portCheck(portName) {
		return SerialDeviceInfo{}
	}

	return SerialDeviceInfo{
		SerialNumber: serialNumber,
		Vid:          vid,
		Pid:          pid,
		Port:         portName,
	}
}

// checkCOMPortActiveWindows tries to open the COM port to check if it is active on Windows.
func checkCOMPortActiveWindows(portName string) bool {
	comPort := fmt.Sprintf("\\\\.\\%s", portName)
	handle, err := syscall.CreateFile(
		syscall.StringToUTF16Ptr(comPort),
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	return true
}
