//go:build windows

package desktop

import "golang.org/x/sys/windows"

// freeDiskBytes reports the bytes available to the caller on the volume holding
// path.
func freeDiskBytes(path string) (uint64, error) {
	var freeToCaller, total, totalFree uint64
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	if err := windows.GetDiskFreeSpaceEx(p, &freeToCaller, &total, &totalFree); err != nil {
		return 0, err
	}
	return freeToCaller, nil
}
