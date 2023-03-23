package db

func GetWorkflowRunCount(workflowID string) (int64, error) {
	var count int64
	err := db.Table("config_changes").
		Where("config_id = (?)", db.Table("config_items").Select("id").Where("? = ANY(external_id)", workflowID)).
		Count(&count).
		Error
	return count, err
}
