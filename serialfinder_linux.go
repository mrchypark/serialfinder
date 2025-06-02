//go:build linux
// +build linux

package serialfinder

import (
	"fmt"
	"io/fs" // For fs.FileMode
	"os"
	"path/filepath"
	"strings"
)

// fileSystemReader defines an interface for filesystem operations.
// This allows for mocking the filesystem in tests.
type fileSystemReader interface {
	ReadDir(dirname string) ([]os.DirEntry, error)
	EvalSymlinks(path string) (string, error)
	ReadFile(filename string) ([]byte, error)
	Stat(name string) (os.FileInfo, error) // For checkForVIDPIDFiles
}

// defaultFileSystemReader is the default implementation of fileSystemReader using os and filepath.
type defaultFileSystemReader struct{}

func (r *defaultFileSystemReader) ReadDir(dirname string) ([]os.DirEntry, error) {
	return os.ReadDir(dirname)
}
func (r *defaultFileSystemReader) EvalSymlinks(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
func (r *defaultFileSystemReader) ReadFile(filename string) ([]byte, error) {
	return os.ReadFile(filename)
}
func (r *defaultFileSystemReader) Stat(name string) (os.FileInfo, error) {
	return os.Stat(name)
}

// GetSerialDevices is the public function to retrieve USB devices on Linux.
// It uses the default file system reader.
func GetSerialDevices(vid, pid string) ([]SerialDeviceInfo, error) {
	return getSerialDevicesWithReader(vid, pid, &defaultFileSystemReader{})
}

// getSerialDevicesWithReader is the internal implementation that allows using a custom fileSystemReader.
// This is used for testing.
func getSerialDevicesWithReader(vid, pid string, reader fileSystemReader) ([]SerialDeviceInfo, error) {
	var devices []SerialDeviceInfo

	// Path to the serial devices by ID directory
	serialByIDPath := "/dev/serial/by-id"

	// Read all the symlinks in the directory
	entries, err := reader.ReadDir(serialByIDPath)
	if err != nil {
		// If /dev/serial/by-id doesn't exist or is unreadable, it might mean no devices or a permission issue.
		// This is a common scenario if no relevant udev rules created these symlinks.
		if os.IsNotExist(err) || os.IsPermission(err) {
			return devices, nil // Return empty list, not an error
		}
		return nil, fmt.Errorf("error reading %s: %w", serialByIDPath, err)
	}

	// Prepare VID/PID for case-insensitive comparison
	targetVidUpper := strings.ToUpper(vid)
	targetPidUpper := strings.ToUpper(pid)

	// Iterate over each entry in the directory
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Full path to the symbolic link
		symlinkPath := filepath.Join(serialByIDPath, entry.Name())

		// Resolve the symbolic link to get the actual device path
		devicePath, err := reader.EvalSymlinks(symlinkPath)
		if err != nil {
			// Could be a broken symlink, skip it.
			continue
		}

		// Find the USB device directory associated with this tty device
		usbDir := findSerialDeviceInfoDirWithReader(devicePath, reader)
		if usbDir == "" {
			continue
		}

		// Read the VID and PID
		idVendorBytes, err := reader.ReadFile(filepath.Join(usbDir, "idVendor"))
		if err != nil {
			// If we can't read VID, this device is problematic.
			// Depending on desired strictness, could continue or return error.
			// For now, let's be strict as VID/PID are crucial.
			return nil, fmt.Errorf("error reading idVendor for %s (from %s): %w", usbDir, symlinkPath, err)
		}
		idVendor := idVendorBytes

		idProductBytes, err := reader.ReadFile(filepath.Join(usbDir, "idProduct"))
		if err != nil {
			return nil, fmt.Errorf("error reading idProduct for %s (from %s): %w", usbDir, symlinkPath, err)
		}
		idProduct := idProductBytes

		vidStr := strings.ToUpper(strings.TrimSpace(string(idVendor)))
		pidStr := strings.ToUpper(strings.TrimSpace(string(idProduct)))

		// Filter by VID if a VID is provided
		if targetVidUpper != "" && vidStr != targetVidUpper {
			continue
		}
		// Filter by PID if a PID is provided
		if targetPidUpper != "" && pidStr != targetPidUpper {
			continue
		}

		// Read the serial number
		var serialNumberStr string
		serialNumberBytes, err := reader.ReadFile(filepath.Join(usbDir, "serial"))
		if err != nil {
			// Non-critical if serial is missing, proceed with an empty serial number.
			serialNumberStr = ""
		} else {
			serialNumberStr = strings.TrimSpace(string(serialNumberBytes))
		}

		// Add the device to the list
		// Port is the stable /dev/serial/by-id path, which is useful for persistent device naming.
		devices = append(devices, SerialDeviceInfo{
			SerialNumber: serialNumberStr,
			Vid:          vidStr,
			Pid:          pidStr,
			Port:         symlinkPath, // symlinkPath is e.g., /dev/serial/by-id/usb-MyDevice_Serial-if00-port0
		})
	}

	return devices, nil
}

// findSerialDeviceInfoDirWithReader is the testable version of findSerialDeviceInfoDir.
func findSerialDeviceInfoDirWithReader(devicePath string, reader fileSystemReader) string {
	// Get the full path to the tty device in /sys/class/tty
	// devicePath is something like /dev/ttyUSB0 or /dev/ttyACM0
	// We need its base name, e.g., ttyUSB0
	sysTTYPath := filepath.Join("/sys/class/tty", filepath.Base(devicePath), "device")

	// Follow the symlink to the actual device directory in sysfs (e.g., /sys/devices/pci0000:00/0000:00:14.0/usb1/1-1/1-1:1.0)
	usbDeviceSysfsPath, err := reader.EvalSymlinks(sysTTYPath)
	if err != nil {
		return "" // Cannot resolve path to device's sysfs directory
	}

	// The usbDeviceSysfsPath usually points to an interface directory (e.g., /sys/.../1-1:1.0).
	// The actual USB device directory (containing idVendor, idProduct) is typically one or two levels up.
	// Example: /sys/devices/pci0000:00/0000:00:14.0/usb1/1-1  <-- This is what we want
	//          /sys/devices/pci0000:00/0000:00:14.0/usb1/1-1/1-1:1.0
	//          /sys/devices/pci0000:00/0000:00:14.0/usb1/1-1/1-1:1.1
	//
	// Check current directory (usbDeviceSysfsPath could sometimes be the main device dir, though less common for USB serial)
	if checkForVIDPIDFilesWithReader(usbDeviceSysfsPath, reader) {
		return usbDeviceSysfsPath
	}

	// Check parent directory
	parentDir := filepath.Dir(usbDeviceSysfsPath)
	if checkForVIDPIDFilesWithReader(parentDir, reader) {
		return parentDir
	}

	// For some devices, it might be two levels up (e.g. if usbDeviceSysfsPath was .../1-1/1-1.0/tty/ttyUSB0)
	// but the typical structure for /sys/class/tty/{ttyX}/device -> .../usbX/X-Y/X-Y:Z.A
	// has the VID/PID files in .../usbX/X-Y.
	// So checking grandparentDir of usbDeviceSysfsPath (which is parentDir of parentDir)
	grandparentDir := filepath.Dir(parentDir)
	if grandparentDir != parentDir && grandparentDir != "." && grandparentDir != "/" { // Avoid going too high
		if checkForVIDPIDFilesWithReader(grandparentDir, reader) {
			return grandparentDir
		}
	}
	// Further check for cases like /sys/devices/.../usb1/1-1/1-1.0/device/../.. (less direct but possible)
	// The current logic for parentDir and grandparentDir should cover most standard cases where
	// 'device' symlinks to something like '.../1-1:1.0' and VID/PID are in '.../1-1'.

	return "" // Could not find a directory with idVendor/idProduct files
}

// checkForVIDPIDFilesWithReader is the testable version of checkForVIDPIDFiles.
func checkForVIDPIDFilesWithReader(dir string, reader fileSystemReader) bool {
	// Check if idVendor and idProduct files exist in the directory
	_, errVid := reader.Stat(filepath.Join(dir, "idVendor"))
	_, errPid := reader.Stat(filepath.Join(dir, "idProduct"))
	return errVid == nil && errPid == nil
}
