//go:build rustdiffgen

package main

/*
#cgo LDFLAGS: ${SRCDIR}/external/diffgen/target/release/libdiffgen.a -ldl
#include "./external/diffgen/libdiffgen.h"
#include <stdlib.h>
*/
import "C"

import (
	"unsafe"

	"github.com/flanksource/config-db/db"
)

func init() {
	db.DiffFunc = func(before, after string) string {
		beforeCString := C.CString(before)
		defer C.free(unsafe.Pointer(beforeCString))

		afterCString := C.CString(after)
		defer C.free(unsafe.Pointer(afterCString))

		diffChar := C.diff(beforeCString, afterCString)
		defer C.free(unsafe.Pointer(diffChar))
		if diffChar == nil {
			return ""
		}

		// prefix is required for UI
		prefix := "--- before\n+++ after\n"
		diff := C.GoString(diffChar)
		return prefix + diff
	}
}
