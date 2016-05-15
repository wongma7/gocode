package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
)

var (
	proc_get_module_file_name = kernel32.NewProc("GetModuleFileNameW")
)

func create_sock_flag(name, desc string) *string {
	return flag.String(name, "tcp", desc)
}

// Full path of the current executable
func get_executable_filename() string {
	b := make([]uint16, syscall.MAX_PATH)
	ret, _, err := syscall.Syscall(proc_get_module_file_name.Addr(), 3,
		0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	if int(ret) == 0 {
		panic(fmt.Sprintf("GetModuleFileNameW : err %d", int(err)))
	}
	return syscall.UTF16ToString(b)
}
