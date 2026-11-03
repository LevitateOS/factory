//go:build !linux

package runtime

import "errors"

func nativePrivateDirectory(string) error {
	return errors.New("native backend bootstrap requires supported Linux file admission")
}

func nativePrivateInput(string, int64) ([]byte, error) {
	return nil, errors.New("native backend bootstrap requires supported Linux file admission")
}
