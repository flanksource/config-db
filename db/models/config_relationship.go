package models

type ConfigItemRelationship struct {
	ParentID string `gorm:"column:parent_id" json:"parent_id"`
	ChildID  string `gorm:"column:child_id" json:"child_id"`
	Relation string `gorm:"column:relation" json:"relation"`
}
