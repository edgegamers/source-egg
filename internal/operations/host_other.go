//go:build !linux

package operations

import (
	"context"
	"errors"
	"os"
)

func diskInfo(string) (DiskInfo, error) {
	return DiskInfo{}, errors.New("disk inspection requires Linux")
}
func processOpenFiles(context.Context, Server) (map[string]bool, error) {
	return nil, errors.New("open-file inspection requires Linux")
}
func inheritOwner(*os.Root, *os.File, string, string) error {
	return errors.New("restore ownership requires Linux")
}
func inheritDirectoryOwner(*os.Root, string) error {
	return errors.New("restore ownership requires Linux")
}
