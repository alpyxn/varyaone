package backup

import "golang.org/x/sys/windows"

func freeBytes(path string) (int64, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeToCaller, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &freeToCaller, &total, &free); err != nil {
		return 0, err
	}
	return int64(freeToCaller), nil
}
