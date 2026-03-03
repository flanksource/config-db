package v1

type ChangeAction string

var (
	Delete ChangeAction = "delete"
	Ignore ChangeAction = "ignore"
	MoveUp ChangeAction = "move-up"
	CopyUp ChangeAction = "copy-up"
	Copy   ChangeAction = "copy"
	Move   ChangeAction = "move"
)

const (
	ChangeTypeDiff              = "diff"
	ChangeTypePermissionAdded   = "PermissionAdded"
	ChangeTypePermissionRemoved = "PermissionRemoved"
)
