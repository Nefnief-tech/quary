package runtime

import (
	"io/fs"
	"os"
	"path/filepath"

	"quarry.dev/quarry/internal/pathsanitize"
)

// ResolveServerDir validates that path stays inside the server data
// directory. All file operations go through it.
func ResolveServerDir(serversRoot, serverID, path string) (string, error) {
	return pathsanitize.SafeJoin(serversRoot, filepath.Join(serverID, path))
}

// diskUsageLocal walks a directory tree and sums regular file sizes.
func diskUsageLocal(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	if err != nil && os.IsNotExist(err) {
		return 0, nil
	}
	return total, err
}
