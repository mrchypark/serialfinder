//go:build linux
// +build linux

package serialfinder

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// mockDirEntry implements os.DirEntry for testing.
type mockDirEntry struct {
	name  string
	isDir bool
	mode  fs.FileMode
}

func (mde *mockDirEntry) Name() string { return mde.name }
func (mde *mockDirEntry) IsDir() bool  { return mde.isDir }
func (mde *mockDirEntry) Type() fs.FileMode {
	if mde.mode != 0 {
		return mde.mode & fs.ModeType // Return only type bits
	}
	if mde.isDir {
		return fs.ModeDir
	}
	return 0 // Regular file
}
func (mde *mockDirEntry) Info() (fs.FileInfo, error) {
	return &mockFileInfo{name: mde.name, isDir: mde.isDir, mode: mde.mode}, nil
}

// mockFileInfo implements os.FileInfo for testing.
type mockFileInfo struct {
	name  string
	isDir bool
	mode  fs.FileMode
	modTime time.Time
	size int64
}

func (mfi *mockFileInfo) Name() string       { return mfi.name }
func (mfi *mockFileInfo) Size() int64        { return mfi.size }
func (mfi *mockFileInfo) Mode() fs.FileMode  { return mfi.mode }
func (mfi *mockFileInfo) ModTime() time.Time { return mfi.modTime }
func (mfi *mockFileInfo) IsDir() bool        { return mfi.isDir }
func (mfi *mockFileInfo) Sys() interface{}   { return nil }

// mockFileSystemReader implements fileSystemReader for testing.
type mockFileSystemReader struct {
	mockFiles       map[string][]byte
	mockDirs        map[string][]os.DirEntry
	mockSymlinks    map[string]string // path -> target
	mockStats       map[string]os.FileInfo
	mockStatErrors  map[string]error // path -> error for Stat

	// Specific errors for methods
	readDirError      error
	evalSymlinksError map[string]error // path -> error
	readFileError     map[string]error // path -> error
}

func newMockFileSystemReader() *mockFileSystemReader {
	return &mockFileSystemReader{
		mockFiles:       make(map[string][]byte),
		mockDirs:        make(map[string][]os.DirEntry),
		mockSymlinks:    make(map[string]string),
		mockStats:       make(map[string]os.FileInfo),
		mockStatErrors:  make(map[string]error),
		evalSymlinksError: make(map[string]error),
		readFileError:   make(map[string]error),
	}
}

func (m *mockFileSystemReader) ReadDir(dirname string) ([]os.DirEntry, error) {
	if m.readDirError != nil {
		return nil, m.readDirError
	}
	entries, ok := m.mockDirs[dirname]
	if !ok {
		return nil, os.ErrNotExist // Default to NotExist if dir not explicitly mocked
	}
	return entries, nil
}

func (m *mockFileSystemReader) EvalSymlinks(path string) (string, error) {
	if err, ok := m.evalSymlinksError[path]; ok && err != nil {
		return "", err
	}
	target, ok := m.mockSymlinks[path]
	if !ok {
		// If not a mocked symlink, behave like EvalSymlinks on a regular file/dir
		// or return a specific error if it should be a symlink that's missing.
		// For simplicity here, if not in map, assume it's not a symlink and return path itself or an error.
		// The actual function expects EvalSymlinks to resolve or fail.
		return "", os.ErrNotExist // Or return path, "", if it's not necessarily an error for it not to be a symlink
	}
	return target, nil
}

func (m *mockFileSystemReader) ReadFile(filename string) ([]byte, error) {
	if err, ok := m.readFileError[filename]; ok && err != nil {
		return nil, err
	}
	content, ok := m.mockFiles[filename]
	if !ok {
		return nil, os.ErrNotExist
	}
	return content, nil
}

func (m *mockFileSystemReader) Stat(name string) (os.FileInfo, error) {
	if err, ok := m.mockStatErrors[name]; ok && err != nil {
		return nil, err
	}
	info, ok := m.mockStats[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return info, nil
}

// Helper to add a mock file
func (m *mockFileSystemReader) addFile(path string, content string) {
	m.mockFiles[path] = []byte(content)
	m.mockStats[path] = &mockFileInfo{name: filepath.Base(path), size: int64(len(content))}
}

// Helper to add a mock symlink
func (m *mockFileSystemReader) addSymlink(path string, target string) {
	m.mockSymlinks[path] = target
	// Stat on a symlink usually returns info about the symlink itself
	m.mockStats[path] = &mockFileInfo{name: filepath.Base(path), mode: fs.ModeSymlink}
}

// Helper to add a mock directory entry for ReadDir
func (m *mockFileSystemReader) addDirEntry(dirPath string, entry os.DirEntry) {
	m.mockDirs[dirPath] = append(m.mockDirs[dirPath], entry)
}

// Helper to set a specific error for Stat
func (m *mockFileSystemReader) setStatError(path string, err error) {
	m.mockStatErrors[path] = err
}

// Helper to set a specific error for ReadFile
func (m *mockFileSystemReader) setReadFileError(path string, err error) {
	if m.readFileError == nil {
		m.readFileError = make(map[string]error)
	}
	m.readFileError[path] = err
}

// Helper to set a specific error for EvalSymlinks
func (m *mockFileSystemReader) setEvalSymlinksError(path string, err error) {
	if m.evalSymlinksError == nil {
		m.evalSymlinksError = make(map[string]error)
	}
	m.evalSymlinksError[path] = err
}


func TestGetSerialDevicesWithReader(t *testing.T) {
	t.Helper()
	const byIDPath = "/dev/serial/by-id"

	tests := []struct {
		name      string
		vidFilter string
		pidFilter string
		setupMock func(*mockFileSystemReader)
		expected  []SerialDeviceInfo
		wantErr   bool
	}{
		{
			name: "No devices in by-id path (empty dir)",
			setupMock: func(mfs *mockFileSystemReader) {
				mfs.mockDirs[byIDPath] = []os.DirEntry{}
			},
			expected: []SerialDeviceInfo{},
		},
		{
			name: "ReadDir for by-id path returns os.ErrNotExist",
			setupMock: func(mfs *mockFileSystemReader) {
				mfs.readDirError = os.ErrNotExist
			},
			expected: []SerialDeviceInfo{}, // Should return empty, not error
		},
		{
			name: "ReadDir for by-id path returns other error",
			setupMock: func(mfs *mockFileSystemReader) {
				mfs.readDirError = errors.New("some ReadDir error")
			},
			wantErr: true,
		},
		{
			name: "Single device, no filter",
			setupMock: func(mfs *mockFileSystemReader) {
				// /dev/serial/by-id entry
				mfs.addDirEntry(byIDPath, &mockDirEntry{name: "usb-MyCorp_MyDevice_SERIAL123-if00-port0", mode: fs.ModeSymlink})
				// Symlink target for by-id entry
				byIDSymlinkPath := filepath.Join(byIDPath, "usb-MyCorp_MyDevice_SERIAL123-if00-port0")
				mfs.addSymlink(byIDSymlinkPath, "../../ttyUSB0") // Target: /dev/ttyUSB0
				// /sys/class/tty/ttyUSB0/device symlink
				sysTTYDeviceLink := "/sys/class/tty/ttyUSB0/device"
				usbDeviceSysfsDir := "/sys/devices/pci0/usb1/1-1" // Assumed parent containing VID/PID
				mfs.addSymlink(sysTTYDeviceLink, filepath.Join(usbDeviceSysfsDir, "1-1:1.0")) // -> /sys/devices/pci0/usb1/1-1/1-1:1.0
				// VID/PID/Serial files
				mfs.addFile(filepath.Join(usbDeviceSysfsDir, "idVendor"), "0403\n")
				mfs.addFile(filepath.Join(usbDeviceSysfsDir, "idProduct"), "6001\n")
				mfs.addFile(filepath.Join(usbDeviceSysfsDir, "serial"), "SERIAL123\n")
			},
			expected: []SerialDeviceInfo{
				{Vid: "0403", Pid: "6001", SerialNumber: "SERIAL123", Port: filepath.Join(byIDPath, "usb-MyCorp_MyDevice_SERIAL123-if00-port0")},
			},
		},
		{
			name:      "Single device, matches VID/PID filter",
			vidFilter: "0403", pidFilter: "6001",
			setupMock: func(mfs *mockFileSystemReader) {
				mfs.addDirEntry(byIDPath, &mockDirEntry{name: "usb-VID0403_PID6001_SERIAL123-if00-port0", mode: fs.ModeSymlink})
				byIDSymlinkPath := filepath.Join(byIDPath, "usb-VID0403_PID6001_SERIAL123-if00-port0")
				mfs.addSymlink(byIDSymlinkPath, "../../ttyUSB0")
				sysTTYDeviceLink := "/sys/class/tty/ttyUSB0/device"
				usbDeviceSysfsDir := "/sys/devices/pci0/usb1/1-1"
				mfs.addSymlink(sysTTYDeviceLink, filepath.Join(usbDeviceSysfsDir, "1-1:1.0"))
				mfs.addFile(filepath.Join(usbDeviceSysfsDir, "idVendor"), "0403")
				mfs.addFile(filepath.Join(usbDeviceSysfsDir, "idProduct"), "6001")
				mfs.addFile(filepath.Join(usbDeviceSysfsDir, "serial"), "SERIAL123")
			},
			expected: []SerialDeviceInfo{
				{Vid: "0403", Pid: "6001", SerialNumber: "SERIAL123", Port: filepath.Join(byIDPath, "usb-VID0403_PID6001_SERIAL123-if00-port0")},
			},
		},
		{
			name:      "Single device, VID filter mismatch",
			vidFilter: "FFFF", pidFilter: "6001",
			setupMock: func(mfs *mockFileSystemReader) {
				mfs.addDirEntry(byIDPath, &mockDirEntry{name: "usb-VID0403_PID6001_SERIAL123-if00-port0", mode: fs.ModeSymlink})
				byIDSymlinkPath := filepath.Join(byIDPath, "usb-VID0403_PID6001_SERIAL123-if00-port0")
				mfs.addSymlink(byIDSymlinkPath, "../../ttyUSB0")
				sysTTYDeviceLink := "/sys/class/tty/ttyUSB0/device"
				usbDeviceSysfsDir := "/sys/devices/pci0/usb1/1-1"
				mfs.addSymlink(sysTTYDeviceLink, filepath.Join(usbDeviceSysfsDir, "1-1:1.0"))
				mfs.addFile(filepath.Join(usbDeviceSysfsDir, "idVendor"), "0403")
				mfs.addFile(filepath.Join(usbDeviceSysfsDir, "idProduct"), "6001")
			},
			expected: []SerialDeviceInfo{},
		},
		{
			name:      "Single device, PID filter mismatch",
			vidFilter: "0403", pidFilter: "FFFF",
			setupMock: func(mfs *mockFileSystemReader) {
				mfs.addDirEntry(byIDPath, &mockDirEntry{name: "usb-VID0403_PID6001_SERIAL123-if00-port0", mode: fs.ModeSymlink})
				byIDSymlinkPath := filepath.Join(byIDPath, "usb-VID0403_PID6001_SERIAL123-if00-port0")
				mfs.addSymlink(byIDSymlinkPath, "../../ttyUSB0")
				sysTTYDeviceLink := "/sys/class/tty/ttyUSB0/device"
				usbDeviceSysfsDir := "/sys/devices/pci0/usb1/1-1"
				mfs.addSymlink(sysTTYDeviceLink, filepath.Join(usbDeviceSysfsDir, "1-1:1.0"))
				mfs.addFile(filepath.Join(usbDeviceSysfsDir, "idVendor"), "0403")
				mfs.addFile(filepath.Join(usbDeviceSysfsDir, "idProduct"), "6001")
			},
			expected: []SerialDeviceInfo{},
		},
		{
			name: "EvalSymlinks error for by-id symlink",
			setupMock: func(mfs *mockFileSystemReader) {
				byIDSymlinkPath := filepath.Join(byIDPath, "usb-SomeDevice-if00")
				mfs.addDirEntry(byIDPath, &mockDirEntry{name: "usb-SomeDevice-if00", mode: fs.ModeSymlink})
				mfs.setEvalSymlinksError(byIDSymlinkPath, errors.New("eval error for by-id link"))
			},
			expected: []SerialDeviceInfo{}, // Skips this device
		},
		{
			name: "ReadFile error for idVendor",
			setupMock: func(mfs *mockFileSystemReader) {
				mfs.addDirEntry(byIDPath, &mockDirEntry{name: "usb-VID0403_PID6001_SERIAL123-if00-port0", mode: fs.ModeSymlink})
				byIDSymlinkPath := filepath.Join(byIDPath, "usb-VID0403_PID6001_SERIAL123-if00-port0")
				mfs.addSymlink(byIDSymlinkPath, "../../ttyUSB0")
				sysTTYDeviceLink := "/sys/class/tty/ttyUSB0/device"
				usbDeviceSysfsDir := "/sys/devices/pci0/usb1/1-1"
				mfs.addSymlink(sysTTYDeviceLink, filepath.Join(usbDeviceSysfsDir, "1-1:1.0"))
				mfs.setReadFileError(filepath.Join(usbDeviceSysfsDir, "idVendor"), errors.New("read idVendor error"))
				// idProduct is still there, but ReadFile for idVendor fails
				mfs.addFile(filepath.Join(usbDeviceSysfsDir, "idProduct"), "6001")
			},
			wantErr: true, // Expect error because reading idVendor is critical
		},
		{
			name: "Device with missing serial file",
			setupMock: func(mfs *mockFileSystemReader) {
				mfs.addDirEntry(byIDPath, &mockDirEntry{name: "usb-VID0403_PID6001_NOSERIAL-if00-port0", mode: fs.ModeSymlink})
				byIDSymlinkPath := filepath.Join(byIDPath, "usb-VID0403_PID6001_NOSERIAL-if00-port0")
				mfs.addSymlink(byIDSymlinkPath, "../../ttyUSB0")
				sysTTYDeviceLink := "/sys/class/tty/ttyUSB0/device"
				usbDeviceSysfsDir := "/sys/devices/pci0/usb1/1-1"
				mfs.addSymlink(sysTTYDeviceLink, filepath.Join(usbDeviceSysfsDir, "1-1:1.0"))
				mfs.addFile(filepath.Join(usbDeviceSysfsDir, "idVendor"), "0403")
				mfs.addFile(filepath.Join(usbDeviceSysfsDir, "idProduct"), "6001")
				mfs.setReadFileError(filepath.Join(usbDeviceSysfsDir, "serial"), os.ErrNotExist) // Serial file does not exist
			},
			expected: []SerialDeviceInfo{
				{Vid: "0403", Pid: "6001", SerialNumber: "", Port: filepath.Join(byIDPath, "usb-VID0403_PID6001_NOSERIAL-if00-port0")},
			},
		},
		{
            name:      "VID/PID filter case insensitivity",
            vidFilter: "0a1b", pidFilter: "0c2d", // Filter with lowercase
            setupMock: func(mfs *mockFileSystemReader) {
                mfs.addDirEntry(byIDPath, &mockDirEntry{name: "usb-VID0A1B_PID0C2D_SERIALXYZ-if00-port0", mode: fs.ModeSymlink})
                byIDSymlinkPath := filepath.Join(byIDPath, "usb-VID0A1B_PID0C2D_SERIALXYZ-if00-port0")
                mfs.addSymlink(byIDSymlinkPath, "../../ttyUSB0")
                sysTTYDeviceLink := "/sys/class/tty/ttyUSB0/device"
                usbDeviceSysfsDir := "/sys/devices/pci0/usb1/1-1"
                mfs.addSymlink(sysTTYDeviceLink, filepath.Join(usbDeviceSysfsDir, "1-1:1.0"))
                mfs.addFile(filepath.Join(usbDeviceSysfsDir, "idVendor"), "0A1B") // Device has uppercase
                mfs.addFile(filepath.Join(usbDeviceSysfsDir, "idProduct"), "0C2D")
                mfs.addFile(filepath.Join(usbDeviceSysfsDir, "serial"), "SERIALXYZ")
            },
            expected: []SerialDeviceInfo{
                {Vid: "0A1B", Pid: "0C2D", SerialNumber: "SERIALXYZ", Port: filepath.Join(byIDPath, "usb-VID0A1B_PID0C2D_SERIALXYZ-if00-port0")},
            },
        },
		{
			name: "Multiple devices, one matching filter",
			vidFilter: "1A86", pidFilter: "7523",
			setupMock: func(mfs *mockFileSystemReader) {
				// Device 1 (matches)
				mfs.addDirEntry(byIDPath, &mockDirEntry{name: "usb-QinHeng_Electronics_CH340_SERIAL_MATCH-if00-port0", mode: fs.ModeSymlink})
				mfs.addSymlink(filepath.Join(byIDPath, "usb-QinHeng_Electronics_CH340_SERIAL_MATCH-if00-port0"), "../../ttyUSB0")
				mfs.addSymlink("/sys/class/tty/ttyUSB0/device", "/sys/devices/pci0/usb1/1-1/1-1:1.0")
				mfs.addFile("/sys/devices/pci0/usb1/1-1/idVendor", "1A86")
				mfs.addFile("/sys/devices/pci0/usb1/1-1/idProduct", "7523")
				mfs.addFile("/sys/devices/pci0/usb1/1-1/serial", "SERIAL_MATCH")

				// Device 2 (does not match)
				mfs.addDirEntry(byIDPath, &mockDirEntry{name: "usb-FTDI_FT232R_USB_UART_SERIAL_NOMATCH-if00-port0", mode: fs.ModeSymlink})
				mfs.addSymlink(filepath.Join(byIDPath, "usb-FTDI_FT232R_USB_UART_SERIAL_NOMATCH-if00-port0"), "../../ttyUSB1")
				mfs.addSymlink("/sys/class/tty/ttyUSB1/device", "/sys/devices/pci0/usb1/1-2/1-2:1.0")
				mfs.addFile("/sys/devices/pci0/usb1/1-2/idVendor", "0403")
				mfs.addFile("/sys/devices/pci0/usb1/1-2/idProduct", "6001")
				mfs.addFile("/sys/devices/pci0/usb1/1-2/serial", "SERIAL_NOMATCH")
			},
			expected: []SerialDeviceInfo{
				{Vid: "1A86", Pid: "7523", SerialNumber: "SERIAL_MATCH", Port: filepath.Join(byIDPath, "usb-QinHeng_Electronics_CH340_SERIAL_MATCH-if00-port0")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mfs := newMockFileSystemReader()
			tt.setupMock(mfs)

			devices, err := getSerialDevicesWithReader(tt.vidFilter, tt.pidFilter, mfs)

			if (err != nil) != tt.wantErr {
				t.Fatalf("getSerialDevicesWithReader() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(devices, tt.expected) {
				// For easier debugging of slice differences
				expectedStr := fmt.Sprintf("%+v", tt.expected)
				gotStr := fmt.Sprintf("%+v", devices)
				// Compare string representations for easier visual diff if DeepEqual fails
				if expectedStr != gotStr {
					t.Errorf("getSerialDevicesWithReader() mismatch:\nExpected: %s\nGot:      %s\n--- Raw Expected ---\n%+v\n--- Raw Got ---\n%+v", expectedStr, gotStr, tt.expected, devices)
				} else { // If string representations match but DeepEqual failed, it might be due to nil vs empty slice
					if len(tt.expected) == 0 && len(devices) == 0 {
						// This is fine, both are effectively "no devices"
					} else {
						t.Errorf("getSerialDevicesWithReader() DeepEqual failed. Expected: %+v, Got: %+v", tt.expected, devices)
					}
				}
			} else if !tt.wantErr && tt.expected == nil && len(devices) == 0 {
                // Special case: if tt.expected is nil and devices is empty slice, it's a match
                // This handles the case where an empty slice is expected, and reflect.DeepEqual(nil, []T{}) is false
            } else if !tt.wantErr && len(tt.expected) == 0 && devices == nil {
                 // Special case: if tt.expected is empty slice and devices is nil, it's a match
            }
		})
	}
}

func TestFindSerialDeviceInfoDirWithReader(t *testing.T) {
	t.Helper()
	// Base path for tty devices, e.g., /dev/ttyUSB0
	const ttyDevicePath = "/dev/ttyUSB0"
	// Path to the 'device' symlink in sysfs for this tty device
	sysTTYDeviceLink := filepath.Join("/sys/class/tty", filepath.Base(ttyDevicePath), "device")

	tests := []struct {
		name      string
		setupMock func(*mockFileSystemReader)
		expected  string // Expected path to the USB device directory, or "" if not found
	}{
		{
			name: "Found in current dir (pointed by 'device' symlink)",
			setupMock: func(mfs *mockFileSystemReader) {
				// /sys/class/tty/ttyUSB0/device -> /sys/devices/pci0/usb1/1-1/1-1:1.0
				mfs.addSymlink(sysTTYDeviceLink, "/sys/devices/pci0/usb1/1-1/1-1:1.0")
				// Mock idVendor/idProduct directly in /sys/devices/pci0/usb1/1-1/1-1:1.0
				mfs.mockStats[filepath.Join("/sys/devices/pci0/usb1/1-1/1-1:1.0", "idVendor")] = &mockFileInfo{}
				mfs.mockStats[filepath.Join("/sys/devices/pci0/usb1/1-1/1-1:1.0", "idProduct")] = &mockFileInfo{}
			},
			expected: "/sys/devices/pci0/usb1/1-1/1-1:1.0",
		},
		{
			name: "Found in parent dir",
			setupMock: func(mfs *mockFileSystemReader) {
				// /sys/class/tty/ttyUSB0/device -> /sys/devices/pci0/usb1/1-1/1-1:1.0
				mfs.addSymlink(sysTTYDeviceLink, "/sys/devices/pci0/usb1/1-1/1-1:1.0")
				// idVendor/idProduct not in 1-1:1.0, but in 1-1
				mfs.setStatError(filepath.Join("/sys/devices/pci0/usb1/1-1/1-1:1.0", "idVendor"), os.ErrNotExist) // So it fails check current
				mfs.setStatError(filepath.Join("/sys/devices/pci0/usb1/1-1/1-1:1.0", "idProduct"), os.ErrNotExist)

				mfs.mockStats[filepath.Join("/sys/devices/pci0/usb1/1-1", "idVendor")] = &mockFileInfo{}
				mfs.mockStats[filepath.Join("/sys/devices/pci0/usb1/1-1", "idProduct")] = &mockFileInfo{}
			},
			expected: "/sys/devices/pci0/usb1/1-1",
		},
		{
			name: "Found in grandparent dir",
			setupMock: func(mfs *mockFileSystemReader) {
				// /sys/class/tty/ttyUSB0/device -> /sys/devices/pci0/usb1/1-1/1-1:1.0/tty/ttyUSB0
				mfs.addSymlink(sysTTYDeviceLink, "/sys/devices/pci0/usb1/1-1/1-1:1.0/tty/ttyUSB0")
				// idVendor/idProduct not in .../ttyUSB0 or .../1-1:1.0, but in .../1-1
				mfs.setStatError(filepath.Join("/sys/devices/pci0/usb1/1-1/1-1:1.0/tty/ttyUSB0", "idVendor"), os.ErrNotExist)
				mfs.setStatError(filepath.Join("/sys/devices/pci0/usb1/1-1/1-1:1.0/tty/ttyUSB0", "idProduct"), os.ErrNotExist)
				mfs.setStatError(filepath.Join("/sys/devices/pci0/usb1/1-1/1-1:1.0", "idVendor"), os.ErrNotExist)
				mfs.setStatError(filepath.Join("/sys/devices/pci0/usb1/1-1/1-1:1.0", "idProduct"), os.ErrNotExist)

				mfs.mockStats[filepath.Join("/sys/devices/pci0/usb1/1-1", "idVendor")] = &mockFileInfo{}
				mfs.mockStats[filepath.Join("/sys/devices/pci0/usb1/1-1", "idProduct")] = &mockFileInfo{}
			},
			expected: "/sys/devices/pci0/usb1/1-1",
		},
		{
			name: "Not found - VID/PID files do not exist in hierarchy",
			setupMock: func(mfs *mockFileSystemReader) {
				mfs.addSymlink(sysTTYDeviceLink, "/sys/devices/pci0/usb1/1-1/1-1:1.0/tty/ttyUSB0")
				mfs.setStatError(filepath.Join("/sys/devices/pci0/usb1/1-1/1-1:1.0/tty/ttyUSB0", "idVendor"), os.ErrNotExist)
				mfs.setStatError(filepath.Join("/sys/devices/pci0/usb1/1-1/1-1:1.0", "idVendor"), os.ErrNotExist)
				mfs.setStatError(filepath.Join("/sys/devices/pci0/usb1/1-1", "idVendor"), os.ErrNotExist)
				// No need to mock idProduct if idVendor is already not found for all relevant paths
			},
			expected: "",
		},
		{
			name: "EvalSymlinks error for sysTTYDeviceLink",
			setupMock: func(mfs *mockFileSystemReader) {
				mfs.setEvalSymlinksError(sysTTYDeviceLink, errors.New("eval symlink failed"))
			},
			expected: "",
		},
		{
			name: "Pathological grandparent (avoid going to . or /)",
			setupMock: func(mfs *mockFileSystemReader) {
				// Sys tty path /sys/class/tty/ttyS0/device -> /sys/devices/platform/serial8250/tty/ttyS0
				// This structure might mean idVendor/idProduct are not found in typical USB-like parent/grandparent.
				localSysTTYDeviceLink := "/sys/class/tty/ttyS0/device"
				targetPath := "/sys/devices/platform/serial8250/tty/ttyS0" // No "usb" like paths here
				mfs.addSymlink(localSysTTYDeviceLink, targetPath)

				// Assume idVendor/idProduct are not found anywhere up this path.
				mfs.setStatError(filepath.Join(targetPath, "idVendor"), os.ErrNotExist)
				mfs.setStatError(filepath.Join(filepath.Dir(targetPath), "idVendor"), os.ErrNotExist)
				mfs.setStatError(filepath.Join(filepath.Dir(filepath.Dir(targetPath)), "idVendor"), os.ErrNotExist)
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mfs := newMockFileSystemReader()
			tt.setupMock(mfs)

			// For the pathological grandparent test, use a different ttyDevicePath
			currentTTYDevicePath := ttyDevicePath
			if tt.name == "Pathological grandparent (avoid going to . or /)" {
				currentTTYDevicePath = "/dev/ttyS0"
			}

			got := findSerialDeviceInfoDirWithReader(currentTTYDevicePath, mfs)
			if got != tt.expected {
				t.Errorf("findSerialDeviceInfoDirWithReader() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestCheckForVIDPIDFilesWithReader(t *testing.T) {
	t.Helper()
	tests := []struct {
		name          string
		setupMock     func(*mockFileSystemReader)
		dirPath       string
		expected      bool
	}{
		{
			name: "VID and PID exist",
			setupMock: func(mfs *mockFileSystemReader) {
				mfs.mockStats[filepath.Join("/sys/test_device", "idVendor")] = &mockFileInfo{name: "idVendor"}
				mfs.mockStats[filepath.Join("/sys/test_device", "idProduct")] = &mockFileInfo{name: "idProduct"}
			},
			dirPath:  "/sys/test_device",
			expected: true,
		},
		{
			name: "idVendor missing",
			setupMock: func(mfs *mockFileSystemReader) {
				mfs.setStatError(filepath.Join("/sys/test_device", "idVendor"), os.ErrNotExist)
				mfs.mockStats[filepath.Join("/sys/test_device", "idProduct")] = &mockFileInfo{name: "idProduct"}
			},
			dirPath:  "/sys/test_device",
			expected: false,
		},
		{
			name: "idProduct missing",
			setupMock: func(mfs *mockFileSystemReader) {
				mfs.mockStats[filepath.Join("/sys/test_device", "idVendor")] = &mockFileInfo{name: "idVendor"}
				mfs.setStatError(filepath.Join("/sys/test_device", "idProduct"), os.ErrNotExist)
			},
			dirPath:  "/sys/test_device",
			expected: false,
		},
		{
			name: "Both idVendor and idProduct missing",
			setupMock: func(mfs *mockFileSystemReader) {
				mfs.setStatError(filepath.Join("/sys/test_device", "idVendor"), os.ErrNotExist)
				mfs.setStatError(filepath.Join("/sys/test_device", "idProduct"), os.ErrNotExist)
			},
			dirPath:  "/sys/test_device",
			expected: false,
		},
		{
			name: "Stat returns other error for idVendor",
			setupMock: func(mfs *mockFileSystemReader) {
				mfs.setStatError(filepath.Join("/sys/test_device", "idVendor"), errors.New("some stat error"))
				mfs.mockStats[filepath.Join("/sys/test_device", "idProduct")] = &mockFileInfo{name: "idProduct"}
			},
			dirPath:  "/sys/test_device",
			expected: false,
		},
		{
			name: "Stat returns other error for idProduct",
			setupMock: func(mfs *mockFileSystemReader) {
				mfs.mockStats[filepath.Join("/sys/test_device", "idVendor")] = &mockFileInfo{name: "idVendor"}
				mfs.setStatError(filepath.Join("/sys/test_device", "idProduct"), errors.New("some stat error"))
			},
			dirPath:  "/sys/test_device",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mfs := newMockFileSystemReader()
			if tt.setupMock != nil {
				tt.setupMock(mfs)
			}
			got := checkForVIDPIDFilesWithReader(tt.dirPath, mfs)
			if got != tt.expected {
				t.Errorf("checkForVIDPIDFilesWithReader() = %v, want %v", got, tt.expected)
			}
		})
	}
}
