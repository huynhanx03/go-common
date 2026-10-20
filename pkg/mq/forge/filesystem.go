package forge

import "os"

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, dirPerm); err != nil {
		return err
	}
	return os.Chmod(path, dirPerm)
}

func ensurePrivateFile(file *os.File) error {
	if file == nil {
		return os.ErrInvalid
	}
	return file.Chmod(filePerm)
}
