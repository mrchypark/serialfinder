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
		idVendor, err := os.ReadFile(filepath.Join(usbDir, "idVendor"))
		if err != nil {
			fmt.Printf("Error reading idVendor: %v\n", err)
			continue
		}

		idProduct, err := os.ReadFile(filepath.Join(usbDir, "idProduct"))
		if err != nil {
			fmt.Printf("Error reading idProduct: %v\n", err)
			continue
		}

		// Log the VID and PID for debugging
		vidStr := strings.ToUpper(strings.TrimSpace(string(idVendor)))
		pidStr := strings.ToUpper(strings.TrimSpace(string(idProduct)))

		// Check if the VID and PID match the specified values
		if vidStr != "" && vidStr != vid {
			continue
		}
		if pidStr != "" && pidStr != pid {
			continue
		}

		// Read the serial number
		serialNumber, err := os.ReadFile(filepath.Join(usbDir, "serial"))
		if err != nil {
			fmt.Printf("Error reading serial: %v\n", err)
			serialNumber = []byte("")
		}

		// Add the device to the list
		devices = append(devices, SerialDeviceInfo{
			SerialNumber: strings.TrimSpace(string(serialNumber)),
			Vid:          vidStr,
			Pid:          pidStr,
			Port:         symlinkPath,
		})
	}

	return devices, nil
}

// findSerialDeviceInfoDir returns the directory path of the USB device corresponding to the device path
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

// checkForVIDPIDFiles checks if the directory contains idVendor and idProduct files
func checkForVIDPIDFiles(dir string) bool {
	_, errVid := os.Stat(filepath.Join(dir, "idVendor"))
	_, errPid := os.Stat(filepath.Join(dir, "idProduct"))
	return errVid == nil && errPid == nil
}
