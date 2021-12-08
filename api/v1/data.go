package v1

type Host interface {
	GetHostname() string
	GetPlatform() string
	GetId() string
	GetIP() string
	GetPatches() []Patch
}

type Patch interface {
	GetName() string
	GetVersion() string
	GetTitle() string
	IsInstalled() bool
	IsMissing() bool
	IsPendingReboot() bool
	IsFailed() bool
}
