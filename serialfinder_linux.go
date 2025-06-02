//go:build linux
// +build linux

package serialfinder

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GetSerialDevices retrieves USB devices on Linux by searching the `/dev/serial/by-id` directory, filtering by VID and PID, and finding the corresponding port
func GetSerialDevices(vid, pid string) ([]SerialDeviceInfo, error) {
	var devices []SerialDeviceInfo

	// Path to the serial devices by ID directory
	serialByIDPath := "/dev/serial/by-id"

	// Read all the symlinks in the directory
	entries, err := os.ReadDir(serialByIDPath)
	if err != nil {
		return nil, err
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
		devicePath, err := filepath.EvalSymlinks(symlinkPath)
		if err != nil {
			continue
		}

		// Find the USB device directory associated with this tty device
		usbDir := findSerialDeviceInfoDir(devicePath)
		if usbDir == "" {
			continue
		}

		// Read the VID and PID
		idVendorBytes, err := os.ReadFile(filepath.Join(usbDir, "idVendor"))
		if err != nil {
			// If we can't read VID, this device is problematic. Return the error.
			return nil, fmt.Errorf("error reading idVendor for %s (from %s): %w", usbDir, symlinkPath, err)
		}
		idVendor := idVendorBytes // Keep original variable name for minimal diff later if needed

		idProductBytes, err := os.ReadFile(filepath.Join(usbDir, "idProduct"))
		if err != nil {
			// If we can't read PID, this device is problematic. Return the error.
			return nil, fmt.Errorf("error reading idProduct for %s (from %s): %w", usbDir, symlinkPath, err)
		}
		idProduct := idProductBytes // Keep original variable name

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
		serialNumberBytes, err := os.ReadFile(filepath.Join(usbDir, "serial"))
		if err != nil {
			// Non-critical if serial is missing, proceed with an empty serial number.
			// No fmt.Printf here, just use empty string.
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

// findSerialDeviceInfoDir returns the directory path of the USB device corresponding to the device path.
// It navigates up from the /sys/class/tty/{ttyName}/device symlink to find the parent USB device
// directory that contains idVendor and idProduct files.
func findSerialDeviceInfoDir(devicePath string) string {
	// Get the full path to the tty device in /sys/class/tty
	sysTTYPath := filepath.Join("/sys/class/tty", filepath.Base(devicePath), "device")

	// Follow the symlink to the actual device directory
	usbDir, err := filepath.EvalSymlinks(sysTTYPath)
	if err != nil {
		return ""
	}

	// Navigate up one or two directories to find the actual USB device directory
	parentDir := filepath.Dir(usbDir)
	if checkForVIDPIDFiles(parentDir) {
		return parentDir
	}

	grandparentDir := filepath.Dir(parentDir)
	if checkForVIDPIDFiles(grandparentDir) {
		return grandparentDir
	}

	return ""
}

// checkForVIDPIDFiles checks if the directory contains idVendor and idProduct files.
// This helps confirm that the directory is indeed a USB device's sysfs entry.
func checkForVIDPIDFiles(dir string) bool {
	_, errVid := os.Stat(filepath.Join(dir, "idVendor"))
	_, errPid := os.Stat(filepath.Join(dir, "idProduct"))
	return errVid == nil && errPid == nil
}
