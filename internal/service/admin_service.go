package service

import (
	"viberouter/internal/model"
)

// CallLogService forwards call records to the file logger (JSON Lines).
type CallLogService struct{}

func NewCallLogService() *CallLogService {
	return &CallLogService{}
}

func (s *CallLogService) Log(entry *model.CallLog) {
	if fl := GetFileLogger(); fl != nil {
		fl.Log(entry)
	}
}

// Query returns log rows matching the filter (newest first), for the web UI.
func (s *CallLogService) Query(filter LogFilter) []jsonLine {
	if fl := GetFileLogger(); fl != nil {
		return fl.Query(filter)
	}
	return nil
}
