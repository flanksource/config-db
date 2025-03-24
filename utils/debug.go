package utils

import (
	"github.com/labstack/echo/v4"
)

var TrackObject = func(name string, obj any) {}
var MemsizeHandler any

var MemsizeScan = func(obj any) uintptr {
	return 0
}

var MemsizeEchoHandler = func(c echo.Context) error {
	return nil
}
