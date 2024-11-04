package v1

type ChangeAction string

var (
	Delete ChangeAction = "delete"
	Ignore ChangeAction = "ignore"
)

const ChangeTypeDiff = "diff"
