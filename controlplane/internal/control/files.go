package control

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func openRegular(root, relative string, max uint64) (*os.File, error) {
	if !fs.ValidPath(relative) || strings.Contains(relative, "\\") {
		return nil, errors.New("unsafe evidence path")
	}
	path := root
	for _, part := range strings.Split(relative, "/") {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("evidence symlink forbidden")
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || uint64(info.Size()) > max {
		file.Close()
		return nil, errors.New("nonregular or oversized evidence")
	}
	return file, nil
}

func regularFile(root, relative string, max uint64) ([]byte, error) {
	file, err := openRegular(root, relative, max)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(max)+1))
	if err != nil {
		return nil, err
	}
	if uint64(len(data)) > max {
		return nil, errors.New("evidence exceeds read limit")
	}
	return data, nil
}
