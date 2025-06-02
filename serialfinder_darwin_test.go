//go:build darwin
// +build darwin

package serialfinder

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Mock commandExecutor for testing
type mockExecutor struct {
	Output     []byte
	Err        error
	CalledName string
	CalledArgs []string
}

func (me *mockExecutor) Execute(name string, arg ...string) ([]byte, error) {
	me.CalledName = name
	me.CalledArgs = arg
	return me.Output, me.Err
}

func TestParseHexValue(t *testing.T) {
	t.Helper()
	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{"valid decimal", "1234", 1234, false},
		{"valid hex with 0x", "0x4D2", 1234, false},
		{"valid hex with 0X", "0X4d2", 1234, false},
		{"valid decimal with comma", "1234,", 1234, false},
		{"valid hex with comma", "0x4D2,", 1234, false},
		{"zero decimal", "0", 0, false},
		{"zero hex", "0x0", 0, false},
		{"large decimal", "1234567890", 1234567890, false},
		{"large hex", "0xABCDEF12", 0xABCDEF12, false},
		{"invalid input - letters", "abc", 0, true},
		{"invalid input - hex letters no prefix", "ABC", 0, true}, // parseHexValue expects decimal if no 0x
		{"empty input", "", 0, true},
		{"only 0x", "0x", 0, true},
		{"hex with invalid chars", "0xGHI", 0, true},
		{"decimal with space", "12 34", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseHexValue(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseHexValue(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("parseHexValue(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseStringValue(t *testing.T) {
	t.Helper()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"quoted string", `"Hello, World!"`, "Hello, World!"},
		{"unquoted string", `MyDevice`, "MyDevice"},
		{"quoted string with comma", `"Test,"`, "Test"},
		{"unquoted string with comma", `Test,`, "Test"}, // TrimSuffix will remove it
		{"empty quoted string", `""`, ""},
		{"empty unquoted string", ``, ""},
		{"string with internal spaces", `"Spaces In Side"`, "Spaces In Side"},
		{"string with leading/trailing spaces in quotes", `"  Spaced  "`, "  Spaced  "},
		{"string with only spaces in quotes", `"   "`, "   "},
		{"already trimmed string", `NoQuotes`, "NoQuotes"},
		{"string is just a quote", `"`, `"`},          // Does not have prefix and suffix of quote
		{"string is two quotes", `""`, ``},             // Has prefix and suffix
		{"string is three quotes", `"""`, `"`},         // Strips first and last
		{"string with internal quotes", `"a"b"c"`, `a"b"c`}, // Strips first and last
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseStringValue(tt.input); got != tt.want {
				t.Errorf("parseStringValue(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

const mockIoregOutputEmpty = `
+-o Root  <class IORegistryEntry, id 0x100000100, retain 21, depth 0>
{
}
`

const mockIoregOutputSingleDevice = `
+-o Root  <class IORegistryEntry, id 0x100000100, retain 21, depth 0>
  +-o IOUSBHostDevice  <class IOUSBHostDevice, id 0x100000432, registered, matched, active, busy 0 (2 ms), retain 14>
  {
    "sessionID" = 1112309454
    "idProduct" = 22332  // PID: 0x573C
    "idVendor" = 1155    // VID: 0x0483
    "kUSBSerialNumberString" = "SERIAL123"
    "USB Product Name" = "Test USB Device"
  }
  +-o AppleUSBHostCompositeDevice  <class AppleUSBHostCompositeDevice, id 0x100000433, busy 0 (0 ms), retain 4>
    +-o AppleUSBHostInterface@0  <class AppleUSBHostInterface, id 0x100000434, busy 0 (0 ms), retain 6>
      +-o IOSerialBSDClient  <class IOSerialBSDClient, id 0x100000438, registered, matched, active, busy 0 (0 ms), retain 7>
        {
          "IOTTYBaseName" = "usbmodem"
          "IOCalloutDevice" = "/dev/cu.usbmodemSERIAL1231"
          "IODialinDevice" = "/dev/tty.usbmodemSERIAL1231"
          "IOTTYDevice" = "usbmodemSERIAL1231"
          "idProduct" = 22332
          "idVendor" = 1155
        }
`
const mockIoregOutputSingleDeviceFTDI = `
+-o Root  <class IORegistryEntry, id 0x100000100, retain 21, depth 0>
  +-o IOUSBDevice  <class IOUSBDevice, id 0x100000432, registered, matched, active, busy 0 (2 ms), retain 14>
  {
    "sessionID" = 1112309454
    "idProduct" = 24577  // PID: 0x6001
    "idVendor" = 1027     // VID: 0x0403
    "USB Serial Number" = "FTDI_SERIAL"
  }
  +-o AppleUSBInterface@0  <class AppleUSBInterface...>
    +-o IOSerialBSDClient  <class IOSerialBSDClient...>
      {
        "IOCalloutDevice" = "/dev/cu.usbserial-FTDI_SERIAL"
      }
`

const mockIoregOutputTwoDevices = mockIoregOutputSingleDevice + `
  +-o IOUSBHostDevice  <class IOUSBHostDevice, id 0x100000555, registered, matched, active, busy 0 (2 ms), retain 14>
  {
    "idProduct" = 8193   // PID: 0x2001
    "idVendor" = 4292    // VID: 0x10C4
    "kUSBSerialNumberString" = "SERIAL_XYZ"
  }
  +-o AppleUSBHostCompositeDevice  <class AppleUSBHostCompositeDevice, id 0x100000556, busy 0 (0 ms), retain 4>
    +-o AppleUSBHostInterface@0  <class AppleUSBHostInterface, id 0x100000557, busy 0 (0 ms), retain 6>
      +-o IOSerialBSDClient  <class IOSerialBSDClient, id 0x100000558, registered, matched, active, busy 0 (0 ms), retain 7>
        {
          "IOCalloutDevice" = "/dev/cu.usbmodemSERIAL_XYZ1"
        }
`
const mockIoregOutputMissingSerial = `
+-o Root  <class IORegistryEntry, id 0x100000100, retain 21, depth 0>
  +-o IOUSBHostDevice  <class IOUSBHostDevice, id 0x100000432, registered, matched, active, busy 0 (2 ms), retain 14>
  {
    "idProduct" = 22332
    "idVendor" = 1155
    // No Serial Number
  }
  +-o AppleUSBHostCompositeDevice  <class AppleUSBHostCompositeDevice, id 0x100000433, busy 0 (0 ms), retain 4>
    +-o AppleUSBHostInterface@0  <class AppleUSBHostInterface, id 0x100000434, busy 0 (0 ms), retain 6>
      +-o IOSerialBSDClient  <class IOSerialBSDClient, id 0x100000438, registered, matched, active, busy 0 (0 ms), retain 7>
        {
          "IOCalloutDevice" = "/dev/cu.usbmodemNOSERIAL1"
        }
`

const mockIoregOutputMissingVID = `
+-o Root  <class IORegistryEntry, id 0x100000100, retain 21, depth 0>
  +-o IOUSBHostDevice  <class IOUSBHostDevice, id 0x100000432, registered, matched, active, busy 0 (2 ms), retain 14>
  {
    "idProduct" = 22332
    // No idVendor
    "kUSBSerialNumberString" = "SERIAL_NOVID"
  }
  +-o AppleUSBHostCompositeDevice  <class AppleUSBHostCompositeDevice, id 0x100000433, busy 0 (0 ms), retain 4>
    +-o AppleUSBHostInterface@0  <class AppleUSBHostInterface, id 0x100000434, busy 0 (0 ms), retain 6>
      +-o IOSerialBSDClient  <class IOSerialBSDClient, id 0x100000438, registered, matched, active, busy 0 (0 ms), retain 7>
        {
          "IOCalloutDevice" = "/dev/cu.usbmodemSERIAL_NOVID1"
        }
`
const mockIoregOutputMissingPID = `
+-o Root  <class IORegistryEntry, id 0x100000100, retain 21, depth 0>
  +-o IOUSBHostDevice  <class IOUSBHostDevice, id 0x100000432, registered, matched, active, busy 0 (2 ms), retain 14>
  {
    "idVendor" = 1155
    // No idProduct
    "kUSBSerialNumberString" = "SERIAL_NOPID"
  }
  +-o AppleUSBHostCompositeDevice  <class AppleUSBHostCompositeDevice, id 0x100000433, busy 0 (0 ms), retain 4>
    +-o AppleUSBHostInterface@0  <class AppleUSBHostInterface, id 0x100000434, busy 0 (0 ms), retain 6>
      +-o IOSerialBSDClient  <class IOSerialBSDClient, id 0x100000438, registered, matched, active, busy 0 (0 ms), retain 7>
        {
          "IOCalloutDevice" = "/dev/cu.usbmodemSERIAL_NOPID1"
        }
`
const mockIoregOutputMissingPort = `
+-o Root  <class IORegistryEntry, id 0x100000100, retain 21, depth 0>
  +-o IOUSBHostDevice  <class IOUSBHostDevice, id 0x100000432, registered, matched, active, busy 0 (2 ms), retain 14>
  {
    "idProduct" = 22332
    "idVendor" = 1155
    "kUSBSerialNumberString" = "SERIAL_NOPORT"
  }
  +-o AppleUSBHostCompositeDevice  <class AppleUSBHostCompositeDevice, id 0x100000433, busy 0 (0 ms), retain 4>
    +-o AppleUSBHostInterface@0  <class AppleUSBHostInterface, id 0x100000434, busy 0 (0 ms), retain 6>
      +-o IOSerialBSDClient  <class IOSerialBSDClient, id 0x100000438, registered, matched, active, busy 0 (0 ms), retain 7>
        {
          // No IOCalloutDevice
        }
`

func TestGetSerialDevicesWithExecutor_NoDevices(t *testing.T) {
	t.Helper()
	executor := &mockExecutor{Output: []byte(mockIoregOutputEmpty)}
	devices, err := getSerialDevicesWithExecutor("", "", executor)
	if err != nil {
		t.Fatalf("getSerialDevicesWithExecutor returned error: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("expected 0 devices, got %d", len(devices))
	}
}

func TestGetSerialDevicesWithExecutor_IoregError(t *testing.T) {
	t.Helper()
	expectedErr := errors.New("ioreg command failed")
	executor := &mockExecutor{Err: expectedErr}
	_, err := getSerialDevicesWithExecutor("", "", executor)
	if err == nil {
		t.Fatalf("expected an error, but got nil")
	}
	if !strings.Contains(err.Error(), expectedErr.Error()) {
		t.Errorf("expected error string '%v' to contain '%v'", err, expectedErr)
	}
}

func TestGetSerialDevicesWithExecutor_SingleDevice_NoFilter(t *testing.T) {
	t.Helper()
	executor := &mockExecutor{Output: []byte(mockIoregOutputSingleDevice)}
	devices, err := getSerialDevicesWithExecutor("", "", executor)
	if err != nil {
		t.Fatalf("getSerialDevicesWithExecutor returned error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}
	expected := SerialDeviceInfo{
		Vid:          "0483", // 1155
		Pid:          "573C", // 22332
		SerialNumber: "SERIAL123",
		Port:         "/dev/cu.usbmodemSERIAL1231",
	}
	if !reflect.DeepEqual(devices[0], expected) {
		t.Errorf("device info mismatch:\ngot  %+v\nwant %+v", devices[0], expected)
	}
}

func TestGetSerialDevicesWithExecutor_SingleDevice_FTDI(t *testing.T) {
	t.Helper()
	executor := &mockExecutor{Output: []byte(mockIoregOutputSingleDeviceFTDI)}
	devices, err := getSerialDevicesWithExecutor("0403", "6001", executor) // VID: 0x0403, PID: 0x6001
	if err != nil {
		t.Fatalf("getSerialDevicesWithExecutor returned error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d: %+v", len(devices), devices)
	}
	expected := SerialDeviceInfo{
		Vid:          "0403",
		Pid:          "6001",
		SerialNumber: "FTDI_SERIAL",
		Port:         "/dev/cu.usbserial-FTDI_SERIAL",
	}
	if !reflect.DeepEqual(devices[0], expected) {
		t.Errorf("device info mismatch:\ngot  %+v\nwant %+v", devices[0], expected)
	}
}


func TestGetSerialDevicesWithExecutor_SingleDevice_WithVIDPIDFilterMatch(t *testing.T) {
	t.Helper()
	executor := &mockExecutor{Output: []byte(mockIoregOutputSingleDevice)}
	// VID: 0x0483, PID: 0x573C
	devices, err := getSerialDevicesWithExecutor("0483", "573C", executor)
	if err != nil {
		t.Fatalf("getSerialDevicesWithExecutor returned error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}
}

func TestGetSerialDevicesWithExecutor_SingleDevice_WithVIDFilterMismatch(t *testing.T) {
	t.Helper()
	executor := &mockExecutor{Output: []byte(mockIoregOutputSingleDevice)}
	devices, err := getSerialDevicesWithExecutor("FFFF", "573C", executor) // Mismatched VID
	if err != nil {
		t.Fatalf("getSerialDevicesWithExecutor returned error: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("expected 0 devices due to VID mismatch, got %d", len(devices))
	}
}

func TestGetSerialDevicesWithExecutor_SingleDevice_WithPIDFilterMismatch(t *testing.T) {
	t.Helper()
	executor := &mockExecutor{Output: []byte(mockIoregOutputSingleDevice)}
	devices, err := getSerialDevicesWithExecutor("0483", "FFFF", executor) // Mismatched PID
	if err != nil {
		t.Fatalf("getSerialDevicesWithExecutor returned error: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("expected 0 devices due to PID mismatch, got %d", len(devices))
	}
}

func TestGetSerialDevicesWithExecutor_VIDPIDCaseInsensitiveFilter(t *testing.T) {
	t.Helper()
	executor := &mockExecutor{Output: []byte(mockIoregOutputSingleDevice)}
	// VID: 0x0483, PID: 0x573C. Device stores them as "0483", "573C".
	// Test with lowercase filter.
	devices, err := getSerialDevicesWithExecutor("0x483", "0x573c", executor)
	if err != nil {
		t.Fatalf("getSerialDevicesWithExecutor returned error: %v", err)
	}
	if len(devices) != 1 {
		t.Errorf("expected 1 device with case-insensitive filter, got %d", len(devices))
	}
}

func TestGetSerialDevicesWithExecutor_MultipleDevices(t *testing.T) {
	t.Helper()
	executor := &mockExecutor{Output: []byte(mockIoregOutputTwoDevices)}
	devices, err := getSerialDevicesWithExecutor("", "", executor)
	if err != nil {
		t.Fatalf("getSerialDevicesWithExecutor returned error: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(devices))
	}

	expected1 := SerialDeviceInfo{
		Vid:          "0483", // 1155
		Pid:          "573C", // 22332
		SerialNumber: "SERIAL123",
		Port:         "/dev/cu.usbmodemSERIAL1231",
	}
	expected2 := SerialDeviceInfo{
		Vid:          "10C4", // 4292
		Pid:          "2001", // 8193
		SerialNumber: "SERIAL_XYZ",
		Port:         "/dev/cu.usbmodemSERIAL_XYZ1",
	}

	// Check if both expected devices are present, order might vary
	found1 := false
	found2 := false
	for _, device := range devices {
		if reflect.DeepEqual(device, expected1) {
			found1 = true
		}
		if reflect.DeepEqual(device, expected2) {
			found2 = true
		}
	}
	if !found1 || !found2 {
		t.Errorf("did not find all expected devices.\nGot: %+v\nExpected to find: %+v and %+v", devices, expected1, expected2)
	}

	// Test with filter matching one
	devices, err = getSerialDevicesWithExecutor("10C4", "2001", executor)
	if err != nil {
		t.Fatalf("getSerialDevicesWithExecutor returned error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected 1 device with filter, got %d", len(devices))
	}
	if !reflect.DeepEqual(devices[0], expected2) {
		t.Errorf("device info mismatch with filter:\ngot  %+v\nwant %+v", devices[0], expected2)
	}
}


func TestGetSerialDevicesWithExecutor_DeviceWithMissingSerialNumber(t *testing.T) {
	t.Helper()
	executor := &mockExecutor{Output: []byte(mockIoregOutputMissingSerial)}
	devices, err := getSerialDevicesWithExecutor("0483", "573C", executor) // VID: 1155->0483, PID: 22332->573C
	if err != nil {
		t.Fatalf("getSerialDevicesWithExecutor returned error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected 1 device even with missing serial, got %d", len(devices))
	}
	if devices[0].SerialNumber != "" {
		t.Errorf("expected empty SerialNumber, got %s", devices[0].SerialNumber)
	}
	if devices[0].Port != "/dev/cu.usbmodemNOSERIAL1" {
		t.Errorf("expected Port '/dev/cu.usbmodemNOSERIAL1', got %s", devices[0].Port)
	}
}

func TestGetSerialDevicesWithExecutor_DeviceWithMissingVID(t *testing.T) {
	t.Helper()
	// The parser skips devices if VID/PID cannot be determined from the USB device block.
	executor := &mockExecutor{Output: []byte(mockIoregOutputMissingVID)}
	devices, err := getSerialDevicesWithExecutor("", "", executor)
	if err != nil {
		t.Fatalf("getSerialDevicesWithExecutor returned error: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("expected 0 devices when VID is missing from USB block, got %d: %+v", len(devices), devices)
	}
}

func TestGetSerialDevicesWithExecutor_DeviceWithMissingPID(t *testing.T) {
	t.Helper()
	// The parser skips devices if VID/PID cannot be determined from the USB device block.
	executor := &mockExecutor{Output: []byte(mockIoregOutputMissingPID)}
	devices, err := getSerialDevicesWithExecutor("", "", executor)
	if err != nil {
		t.Fatalf("getSerialDevicesWithExecutor returned error: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("expected 0 devices when PID is missing from USB block, got %d: %+v", len(devices), devices)
	}
}

func TestGetSerialDevicesWithExecutor_DeviceWithMissingPort(t *testing.T) {
	t.Helper()
	// If IOCalloutDevice is missing, the device won't be added.
	executor := &mockExecutor{Output: []byte(mockIoregOutputMissingPort)}
	devices, err := getSerialDevicesWithExecutor("0483", "573C", executor)
	if err != nil {
		t.Fatalf("getSerialDevicesWithExecutor returned error: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("expected 0 devices when IOCalloutDevice is missing, got %d: %+v", len(devices), devices)
	}
}
