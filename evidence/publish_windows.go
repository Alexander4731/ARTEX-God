package evidence

import "golang.org/x/sys/windows"

// Windows cannot fsync a directory handle. Publish the already-synced body with
// a write-through move so its final name is durable before committing its binding.
func publishBody(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
