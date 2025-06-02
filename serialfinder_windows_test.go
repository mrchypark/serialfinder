//go:build windows
// +build windows

package serialfinder

import (
	"errors"
	"fmt"
	"reflect"
	"regexp" // For TestVidPidRegex if not already imported by main file for test file
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry" // For registry.Key constants like LOCAL_MACHINE
)

// mockRegistryKey implements the registryKey interface for testing.
type mockRegistryKey struct {
	subKeyNamesToReturn []string
	subKeyNamesError    error
	stringValueToReturn string
	stringTypeToReturn  uint32
	stringValueError    error
	closeError          error
	name                string // For debugging or identification
}

func (mrk *mockRegistryKey) ReadSubKeyNames(n int) ([]string, error) {
	return mrk.subKeyNamesToReturn, mrk.subKeyNamesError
}

func (mrk *mockRegistryKey) GetStringValue(name string) (string, uint32, error) {
	// Could add logic here to return different strings based on 'name' if needed
	return mrk.stringValueToReturn, mrk.stringTypeToReturn, mrk.stringValueError
}

func (mrk *mockRegistryKey) Close() error {
	return mrk.closeError
}

// mockRegistryHandler implements the registryHandler interface for testing.
type mockRegistryHandler struct {
	// mockKeys maps a full path (string) to a mockRegistryKey or an error
	mockKeys     map[string]*mockRegistryKey
	openKeyError map[string]error // Specific error for a path
	genericOpenKeyError error // Generic error if path not in openKeyError
}

func newMockRegistryHandler() *mockRegistryHandler {
	return &mockRegistryHandler{
		mockKeys:     make(map[string]*mockRegistryKey),
		openKeyError: make(map[string]error),
	}
}

func (mrh *mockRegistryHandler) OpenKey(base registry.Key, path string, access uint32) (registryKey, error) {
	// In tests, base is usually registry.LOCAL_MACHINE. We'll use the path as the key for mocks.
	// A real implementation might need to combine base and path for uniqueness if base varies.
	fullPath := path // Assuming path is unique enough for mock map key
	// For more complex scenarios, one might create a unique key from base and path.

	if err, exists := mrh.openKeyError[fullPath]; exists {
		return nil, err
	}
	if mrh.genericOpenKeyError != nil {
		return nil, mrh.genericOpenKeyError
	}

	key, ok := mrh.mockKeys[fullPath]
	if !ok {
		return nil, fmt.Errorf("mockRegistryHandler: unmocked path %s", fullPath) // Or registry.ErrNotExist
	}
	return key, nil
}

// Helper to add a mock key to the handler
func (mrh *mockRegistryHandler) addMockKey(path string, key *mockRegistryKey) {
	mrh.mockKeys[path] = key
	key.name = path // Store path in key for easier debugging if needed
}

// Helper to set an error for a specific OpenKey path
func (mrh *mockRegistryHandler) setOpenKeyError(path string, err error) {
	mrh.openKeyError[path] = err
}


// mockPortChecker is a utility to create a portCheckerFunc for tests.
func mockPortChecker(shouldBeActive bool) portCheckerFunc {
	return func(portName string) bool {
		return shouldBeActive
	}
}

func TestVidPidRegex(t *testing.T) {
	t.Helper()
	tests := []struct {
		name       string
		deviceID   string
		wantVID    string
		wantPID    string
		vidShouldMatch bool
		pidShouldMatch bool
	}{
		{
			name:       "Standard USB VID/PID",
			deviceID:   `USB\VID_1A86&PID_7523\CH340SERIAL`,
			wantVID:    "1A86",
			wantPID:    "7523",
			vidShouldMatch: true,
			pidShouldMatch: true,
		},
		{
			name:       "FTDI Bus VID/PID",
			deviceID:   `FTDIBUS\VID_0403+PID_6001+A50285BI\0000`,
			wantVID:    "0403",
			wantPID:    "6001",
			vidShouldMatch: true,
			pidShouldMatch: true,
		},
		{
			name:       "VID/PID with lowercase hex",
			deviceID:   `USB\VID_abcd&PID_ef01\SERIAL`,
			wantVID:    "abcd",
			wantPID:    "ef01",
			vidShouldMatch: true,
			pidShouldMatch: true,
		},
		{
			name:       "Only VID present",
			deviceID:   `USB\VID_1234\NoPID`,
			wantVID:    "1234",
			wantPID:    "",
			vidShouldMatch: true,
			pidShouldMatch: false,
		},
		{
			name:       "Only PID present (malformed but test regex)",
			deviceID:   `USB\Something&PID_5678\NoVID`,
			wantVID:    "",
			wantPID:    "5678",
			vidShouldMatch: false,
			pidShouldMatch: true,
		},
		{
			name:       "No VID or PID",
			deviceID:   `USB\SomethingElse\AnotherThing`,
			wantVID:    "",
			wantPID:    "",
			vidShouldMatch: false,
			pidShouldMatch: false,
		},
		{
			name:       "Malformed VID (too short)",
			deviceID:   `USB\VID_123&PID_5678\Serial`,
			wantVID:    "",
			wantPID:    "5678",
			vidShouldMatch: false,
			pidShouldMatch: true,
		},
		{
			name:       "Malformed PID (non-hex)",
			deviceID:   `USB\VID_1234&PID_GHIJ\Serial`,
			wantVID:    "1234",
			wantPID:    "",
			vidShouldMatch: true,
			pidShouldMatch: false,
		},
		{
			name:       "Empty Device ID",
			deviceID:   ``,
			wantVID:    "",
			wantPID:    "",
			vidShouldMatch: false,
			pidShouldMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test VID
			vidMatches := vidRegex.FindStringSubmatch(tt.deviceID)
			if tt.vidShouldMatch {
				if len(vidMatches) < 2 {
					t.Errorf("vidRegex did not find VID, but expected one in %q", tt.deviceID)
				} else if vidMatches[1] != tt.wantVID {
					t.Errorf("vidRegex got VID %q, want %q from %q", vidMatches[1], tt.wantVID, tt.deviceID)
				}
			} else {
				if len(vidMatches) > 1 {
					t.Errorf("vidRegex found VID %q, but expected none in %q", vidMatches[1], tt.deviceID)
				}
			}

			// Test PID
			pidMatches := pidRegex.FindStringSubmatch(tt.deviceID)
			if tt.pidShouldMatch {
				if len(pidMatches) < 2 {
					t.Errorf("pidRegex did not find PID, but expected one in %q", tt.deviceID)
				} else if pidMatches[1] != tt.wantPID {
					t.Errorf("pidRegex got PID %q, want %q from %q", pidMatches[1], tt.wantPID, tt.deviceID)
				}
			} else {
				if len(pidMatches) > 1 {
					t.Errorf("pidRegex found PID %q, but expected none in %q", pidMatches[1], tt.deviceID)
				}
			}
		})
	}
}

func TestGetSerialDevicesWithRegistry(t *testing.T) {
	t.Helper()
	const enumUSBPath = `SYSTEM\CurrentControlSet\Enum\USB`

	tests := []struct {
		name        string
		vidFilter   string
		pidFilter   string
		setupMock   func(*mockRegistryHandler)
		portChecker portCheckerFunc
		expected    []SerialDeviceInfo
		wantErr     bool
	}{
		{
			name: "OpenKey for EnumUSB fails",
			setupMock: func(mrh *mockRegistryHandler) {
				mrh.setOpenKeyError(enumUSBPath, errors.New("failed to open Enum\\USB"))
			},
			portChecker: mockPortChecker(true),
			wantErr:     true,
		},
		{
			name: "EnumUSB ReadSubKeyNames fails",
			setupMock: func(mrh *mockRegistryHandler) {
				enumUSBKey := &mockRegistryKey{subKeyNamesError: errors.New("failed to read subkeys")}
				mrh.addMockKey(enumUSBPath, enumUSBKey)
			},
			portChecker: mockPortChecker(true),
			wantErr:     true,
		},
		{
			name: "No device instance IDs",
			setupMock: func(mrh *mockRegistryHandler) {
				enumUSBKey := &mockRegistryKey{subKeyNamesToReturn: []string{}}
				mrh.addMockKey(enumUSBPath, enumUSBKey)
			},
			portChecker: mockPortChecker(true),
			expected:    []SerialDeviceInfo{},
		},
		{
			name:      "Single device, no filter, port active",
			vidFilter: "", pidFilter: "",
			setupMock: func(mrh *mockRegistryHandler) {
				deviceInstanceID := "VID_0403&PID_6001"
				instancePath := enumUSBPath + `\` + deviceInstanceID
				serialKeyName := "SERIAL123"
				deviceParamsPath := instancePath + `\` + serialKeyName + `\Device Parameters`

				mrh.addMockKey(enumUSBPath, &mockRegistryKey{subKeyNamesToReturn: []string{deviceInstanceID}})
				mrh.addMockKey(instancePath, &mockRegistryKey{subKeyNamesToReturn: []string{serialKeyName}})
				mrh.addMockKey(deviceParamsPath, &mockRegistryKey{stringValueToReturn: "COM3"})
			},
			portChecker: mockPortChecker(true),
			expected: []SerialDeviceInfo{
				{Vid: "0403", Pid: "6001", SerialNumber: "SERIAL123", Port: "COM3"},
			},
		},
		{
			name:      "Single device, matches VID/PID filter, port active",
			vidFilter: "0403", pidFilter: "6001",
			setupMock: func(mrh *mockRegistryHandler) {
				deviceInstanceID := "VID_0403&PID_6001"
				instancePath := enumUSBPath + `\` + deviceInstanceID
				serialKeyName := "SERIAL123"
				deviceParamsPath := instancePath + `\` + serialKeyName + `\Device Parameters`

				mrh.addMockKey(enumUSBPath, &mockRegistryKey{subKeyNamesToReturn: []string{deviceInstanceID}})
				mrh.addMockKey(instancePath, &mockRegistryKey{subKeyNamesToReturn: []string{serialKeyName}})
				mrh.addMockKey(deviceParamsPath, &mockRegistryKey{stringValueToReturn: "COM3"})
			},
			portChecker: mockPortChecker(true),
			expected: []SerialDeviceInfo{
				{Vid: "0403", Pid: "6001", SerialNumber: "SERIAL123", Port: "COM3"},
			},
		},
		{
			name:      "Single device, VID filter mismatch",
			vidFilter: "FFFF", pidFilter: "6001",
			setupMock: func(mrh *mockRegistryHandler) {
				deviceInstanceID := "VID_0403&PID_6001"
				mrh.addMockKey(enumUSBPath, &mockRegistryKey{subKeyNamesToReturn: []string{deviceInstanceID}})
				// No need to mock further as it won't be reached
			},
			portChecker: mockPortChecker(true),
			expected:    []SerialDeviceInfo{},
		},
		{
			name:      "Single device, port inactive",
			vidFilter: "0403", pidFilter: "6001",
			setupMock: func(mrh *mockRegistryHandler) {
				deviceInstanceID := "VID_0403&PID_6001"
				instancePath := enumUSBPath + `\` + deviceInstanceID
				serialKeyName := "SERIAL123"
				deviceParamsPath := instancePath + `\` + serialKeyName + `\Device Parameters`

				mrh.addMockKey(enumUSBPath, &mockRegistryKey{subKeyNamesToReturn: []string{deviceInstanceID}})
				mrh.addMockKey(instancePath, &mockRegistryKey{subKeyNamesToReturn: []string{serialKeyName}})
				mrh.addMockKey(deviceParamsPath, &mockRegistryKey{stringValueToReturn: "COM3"})
			},
			portChecker: mockPortChecker(false), // Port is not active
			expected:    []SerialDeviceInfo{},
		},
		{
			name:      "Device missing PortName string value",
			vidFilter: "0403", pidFilter: "6001",
			setupMock: func(mrh *mockRegistryHandler) {
				deviceInstanceID := "VID_0403&PID_6001"
				instancePath := enumUSBPath + `\` + deviceInstanceID
				serialKeyName := "SERIAL123"
				deviceParamsPath := instancePath + `\` + serialKeyName + `\Device Parameters`

				mrh.addMockKey(enumUSBPath, &mockRegistryKey{subKeyNamesToReturn: []string{deviceInstanceID}})
				mrh.addMockKey(instancePath, &mockRegistryKey{subKeyNamesToReturn: []string{serialKeyName}})
				mrh.addMockKey(deviceParamsPath, &mockRegistryKey{stringValueError: errors.New("value not found")})
			},
			portChecker: mockPortChecker(true),
			expected:    []SerialDeviceInfo{},
		},
		{
			name:      "OpenKey error for Device Parameters",
			vidFilter: "0403", pidFilter: "6001",
			setupMock: func(mrh *mockRegistryHandler) {
				deviceInstanceID := "VID_0403&PID_6001"
				instancePath := enumUSBPath + `\` + deviceInstanceID
				serialKeyName := "SERIAL123"
				deviceParamsPath := instancePath + `\` + serialKeyName + `\Device Parameters`

				mrh.addMockKey(enumUSBPath, &mockRegistryKey{subKeyNamesToReturn: []string{deviceInstanceID}})
				mrh.addMockKey(instancePath, &mockRegistryKey{subKeyNamesToReturn: []string{serialKeyName}})
				mrh.setOpenKeyError(deviceParamsPath, errors.New("cannot open device params"))
			},
			portChecker: mockPortChecker(true),
			expected:    []SerialDeviceInfo{},
		},
		{
            name:      "VID/PID filter case insensitivity",
            vidFilter: "0a1b", pidFilter: "0c2d", // Filter with lowercase
            setupMock: func(mrh *mockRegistryHandler) {
                deviceInstanceID := "VID_0A1B&PID_0C2D" // Registry has uppercase
                instancePath := enumUSBPath + `\` + deviceInstanceID
                serialKeyName := "SERIALXYZ"
                deviceParamsPath := instancePath + `\` + serialKeyName + `\Device Parameters`

                mrh.addMockKey(enumUSBPath, &mockRegistryKey{subKeyNamesToReturn: []string{deviceInstanceID}})
                mrh.addMockKey(instancePath, &mockRegistryKey{subKeyNamesToReturn: []string{serialKeyName}})
                mrh.addMockKey(deviceParamsPath, &mockRegistryKey{stringValueToReturn: "COM4"})
            },
            portChecker: mockPortChecker(true),
            expected: []SerialDeviceInfo{
                {Vid: "0A1B", Pid: "0C2D", SerialNumber: "SERIALXYZ", Port: "COM4"},
            },
        },
		{
			name: "Multiple devices, one active, one inactive, one no portname",
			setupMock: func(mrh *mockRegistryHandler) {
				// Device 1: Active
				dev1ID := "VID_AAAA&PID_1111"
				dev1InstancePath := enumUSBPath + `\` + dev1ID
				dev1Serial := "SER_ACTIVE"
				dev1ParamsPath := dev1InstancePath + `\` + dev1Serial + `\Device Parameters`
				mrh.addMockKey(dev1InstancePath, &mockRegistryKey{subKeyNamesToReturn: []string{dev1Serial}})
				mrh.addMockKey(dev1ParamsPath, &mockRegistryKey{stringValueToReturn: "COM10"})

				// Device 2: Inactive port
				dev2ID := "VID_BBBB&PID_2222"
				dev2InstancePath := enumUSBPath + `\` + dev2ID
				dev2Serial := "SER_INACTIVE"
				dev2ParamsPath := dev2InstancePath + `\` + dev2Serial + `\Device Parameters`
				mrh.addMockKey(dev2InstancePath, &mockRegistryKey{subKeyNamesToReturn: []string{dev2Serial}})
				mrh.addMockKey(dev2ParamsPath, &mockRegistryKey{stringValueToReturn: "COM11"})

				// Device 3: No PortName
				dev3ID := "VID_CCCC&PID_3333"
				dev3InstancePath := enumUSBPath + `\` + dev3ID
				dev3Serial := "SER_NOPORT"
				dev3ParamsPath := dev3InstancePath + `\` + dev3Serial + `\Device Parameters`
				mrh.addMockKey(dev3InstancePath, &mockRegistryKey{subKeyNamesToReturn: []string{dev3Serial}})
				mrh.addMockKey(dev3ParamsPath, &mockRegistryKey{stringValueError: errors.New("no portname")})

				mrh.addMockKey(enumUSBPath, &mockRegistryKey{subKeyNamesToReturn: []string{dev1ID, dev2ID, dev3ID}})
			},
			portChecker: func(portName string) bool {
				return portName == "COM10" // Only COM10 is active
			},
			expected: []SerialDeviceInfo{
				{Vid: "AAAA", Pid: "1111", SerialNumber: "SER_ACTIVE", Port: "COM10"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mrh := newMockRegistryHandler()
			tt.setupMock(mrh)

			// Store original checkPortActive and defer its restoration
			originalCheckPortActive := checkPortActive
			checkPortActive = tt.portChecker
			defer func() { checkPortActive = originalCheckPortActive }()


			devices, err := getSerialDevicesWithRegistry(tt.vidFilter, tt.pidFilter, mrh, tt.portChecker)

			if (err != nil) != tt.wantErr {
				t.Fatalf("getSerialDevicesWithRegistry() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr {
				if len(devices) == 0 && len(tt.expected) == 0 {
					// Both are empty, consider it a match.
				} else if !reflect.DeepEqual(devices, tt.expected) {
					t.Errorf("getSerialDevicesWithRegistry() got = %+v, want %+v", devices, tt.expected)
				}
			}
		})
	}
}
