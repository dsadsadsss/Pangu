//go:build windows

package main

import "fmt"

func diskUsage(path string) (total, used uint64, err error) {
	return 0, 0, fmt.Errorf("not implemented on windows")
}
